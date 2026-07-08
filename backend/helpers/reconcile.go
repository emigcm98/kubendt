package helpers

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"kubendt/kubeclient"
	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type desiredLink struct {
	PodA string
	IfA  string
	PodB string
	IfB  string
}

type linkIssue struct {
	PodA       string
	PodB       string
	MissingA   []string // missing ifaces on PodA (from IfA only, but kept as slice for logging/extensibility)
	MissingB   []string
	ReasonA    []string // same length as MissingA
	ReasonB    []string
	DisplayKey string // stable string for sorting/logging
}

func ReconcileMissingInterfaces(namespace string, nodes []types.NodeSpec, links []types.LinkSpec, maxRounds int) error {
	if maxRounds <= 0 {
		maxRounds = 2
	}

	podType := buildPodTypeMap(nodes)
	desired := buildDesiredResolvedLinks(nodes, links)

	if len(desired) == 0 {
		log.Printf("ℹ️ Reconciliation: no links to validate.")
		return nil
	}

	for round := 1; round <= maxRounds; round++ {
		log.Printf("🔁 Reconciliation %d/%d: validating expected interfaces (by links, restart 1 endpoint per link)...", round, maxRounds)

		// 1) Collect missing per pod (count) + per link (pretty logs)
		missingCountByPod := make(map[string]int)
		issues := collectLinkIssues(namespace, desired, missingCountByPod)

		if len(issues) == 0 {
			log.Printf("✅ Reconciliation: all expected interfaces are present.")
			return nil
		}

		// 2) Pretty logs grouped by pair
		logLinkIssuesPretty(issues)

		// 3) Decide restart targets: ONE endpoint per broken link
		toRestartSet := make(map[string]struct{})
		restartWhy := make(map[string][]string)

		for _, is := range issues {
			missA := len(is.MissingA) > 0
			missB := len(is.MissingB) > 0

			// If only ONE side is missing, use a progressive strategy:
			// round 1 -> restart only the missing side (avoid restart cascades)
			// round >=2 -> restart both endpoints (force clear of meshnet skip state)
			if (missA && !missB) || (missB && !missA) {
				var parts []string
				if missA {
					parts = append(parts, fmt.Sprintf("%s:%s", is.PodA, strings.Join(is.MissingA, ",")))
				}
				if missB {
					parts = append(parts, fmt.Sprintf("%s:%s", is.PodB, strings.Join(is.MissingB, ",")))
				}

				if round <= 1 {
					if missA && is.PodA != "external" {
						toRestartSet[is.PodA] = struct{}{}
						restartWhy[is.PodA] = append(restartWhy[is.PodA],
							fmt.Sprintf("%s ↔ %s | missing [%s] | round=%d restart only missing side",
								is.PodA, is.PodB, strings.Join(parts, " ; "), round))
					}
					if missB && is.PodB != "external" {
						toRestartSet[is.PodB] = struct{}{}
						restartWhy[is.PodB] = append(restartWhy[is.PodB],
							fmt.Sprintf("%s ↔ %s | missing [%s] | round=%d restart only missing side",
								is.PodA, is.PodB, strings.Join(parts, " ; "), round))
					}
				} else {
					if is.PodA != "external" {
						toRestartSet[is.PodA] = struct{}{}
					}
					if is.PodB != "external" {
						toRestartSet[is.PodB] = struct{}{}
					}
					for _, pod := range []string{is.PodA, is.PodB} {
						if pod != "external" {
							restartWhy[pod] = append(restartWhy[pod],
								fmt.Sprintf("%s ↔ %s | missing [%s] | round=%d restart both (unilateral skip persists)",
									is.PodA, is.PodB, strings.Join(parts, " ; "), round))
						}
					}
				}
				continue
			}

			// If BOTH sides are missing, choose one intelligently
			chosen := chooseRestartEndpoint(
				is.PodA, is.PodB,
				is.MissingA, is.MissingB,
				missingCountByPod,
				podType,
			)

			if chosen == "" || chosen == "external" {
				continue
			}
			toRestartSet[chosen] = struct{}{}

			// Build compact reason string for this link
			var parts []string
			if len(is.MissingA) > 0 {
				parts = append(parts, fmt.Sprintf("%s:%s", is.PodA, strings.Join(is.MissingA, ",")))
			}
			if len(is.MissingB) > 0 {
				parts = append(parts, fmt.Sprintf("%s:%s", is.PodB, strings.Join(is.MissingB, ",")))
			}

			restartWhy[chosen] = append(restartWhy[chosen],
				fmt.Sprintf("%s ↔ %s | missing [%s] | chosen=%s (both broken)",
					is.PodA, is.PodB, strings.Join(parts, " ; "), chosen))
		}

		if len(toRestartSet) == 0 {
			log.Printf("⏳ Reconciliation: there are broken links but no stable pods to restart in this round.")
			sleep := time.Duration(2*round) * time.Second
			log.Printf("⏳ Reconciliation: waiting %s before retrying...", sleep)
			time.Sleep(sleep)
			continue
		}

		// Deterministic order for restart list
		toRestart := make([]string, 0, len(toRestartSet))
		for p := range toRestartSet {
			toRestart = append(toRestart, p)
		}
		sort.Strings(toRestart)

		log.Printf("🧯 Reconciliation: restarting %d pods (due to broken links; on unilateral failures both endpoints are restarted): %v", len(toRestart), toRestart)

		for _, pod := range toRestart {
			log.Printf("💥 Reconciliation: restarting pod=%s type=%s missing_total=%d", pod, podTypeOrUnknown(podType, pod), missingCountByPod[pod])
			for _, why := range restartWhy[pod] {
				log.Printf("   • %s", why)
			}
			if err := RestartPod(namespace, pod); err != nil {
				log.Printf("❌ Reconciliation: failed restarting pod=%s: %v", pod, err)
			}
		}

		if err := WaitForPodsReadyByName(namespace, toRestart); err != nil {
			log.Printf("❌ Reconciliation: error waiting for Ready after restarts: %v", err)
		}

		if err := ReplayDriverOperationsForPods(namespace, toRestart); err != nil {
			log.Printf("❌ Reconciliation: failed replaying persisted driver operations after restarts: %v", err)
		}

		if round == maxRounds {
			log.Printf("🔎 Reconciliation: final validation after restarts (ronda %d/%d)...", round, maxRounds)

			// Re-check issues with a fresh counter map
			finalMissingCount := make(map[string]int)
			finalIssues := collectLinkIssues(namespace, desired, finalMissingCount)

			if len(finalIssues) == 0 {
				log.Printf("✅ Reconciliation: OK after restarts. No missing interfaces.")
				return nil
			}

			log.Printf("⚠️ Reconciliation: issues still remain after restarts.")
			logLinkIssuesPretty(finalIssues)
			return fmt.Errorf("reconciliation: interfaces are still missing after %d rounds", maxRounds)
		}

		sleep := time.Duration(2*round) * time.Second
		log.Printf("⏳ Reconciliation: waiting %s before next validation...", sleep)
		time.Sleep(sleep)
	}

	return fmt.Errorf("reconciliation: interfaces are still missing after %d rounds", maxRounds)
}

/* --------------------------- issue collection --------------------------- */

func collectLinkIssues(namespace string, desired []desiredLink, missingCountByPod map[string]int) []linkIssue {
	// Deterministic processing order (stable display keys and bucket indices)
	sort.Slice(desired, func(i, j int) bool {
		a := desired[i].PodA + "/" + desired[i].IfA + "<->" + desired[i].PodB + "/" + desired[i].IfB
		b := desired[j].PodA + "/" + desired[j].IfA + "<->" + desired[j].PodB + "/" + desired[j].IfB
		return a < b
	})

	type epCheck struct {
		linkIdx int
		side    int // 0=A, 1=B
		pod     string
		iface   string
	}
	type epResult struct {
		linkIdx int
		side    int
		pod     string
		iface   string
		ok      bool
		reason  string
	}

	// Pre-fetch all pods once to avoid per-endpoint API Get calls (which cause client-side throttling)
	podCache := make(map[string]*v1.Pod)
	if podList, listErr := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{}); listErr == nil {
		for i := range podList.Items {
			p := &podList.Items[i]
			podCache[p.Name] = p
		}
	}

	var checks []epCheck
	for i, dl := range desired {
		if dl.PodA != "external" && dl.IfA != "" {
			checks = append(checks, epCheck{i, 0, dl.PodA, dl.IfA})
		}
		if dl.PodB != "external" && dl.IfB != "" {
			checks = append(checks, epCheck{i, 1, dl.PodB, dl.IfB})
		}
	}

	const maxWorkers = 20
	resultsCh := make(chan epResult, len(checks))
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for _, chk := range checks {
		wg.Add(1)
		sem <- struct{}{}
		go func(c epCheck) {
			defer wg.Done()
			defer func() { <-sem }()
			ok, reason := podHasInterfaceWithCachedPod(namespace, c.pod, podCache[c.pod], c.iface, 3)
			resultsCh <- epResult{c.linkIdx, c.side, c.pod, c.iface, ok, reason}
		}(chk)
	}
	wg.Wait()
	close(resultsCh)

	type linkBucket struct {
		missA []string
		reaA  []string
		missB []string
		reaB  []string
	}
	buckets := make([]linkBucket, len(desired))

	for r := range resultsCh {
		if r.ok {
			continue
		}
		if !shouldCountAsMissing(r.reason) {
			log.Printf("ℹ️ Reconciliation: skipping %s/%s (reason: %s)", r.pod, r.iface, r.reason)
			continue
		}
		missingCountByPod[r.pod]++
		if r.side == 0 {
			buckets[r.linkIdx].missA = append(buckets[r.linkIdx].missA, r.iface)
			buckets[r.linkIdx].reaA = append(buckets[r.linkIdx].reaA, r.reason)
		} else {
			buckets[r.linkIdx].missB = append(buckets[r.linkIdx].missB, r.iface)
			buckets[r.linkIdx].reaB = append(buckets[r.linkIdx].reaB, r.reason)
		}
	}

	issues := make([]linkIssue, 0)
	for i, b := range buckets {
		if len(b.missA) == 0 && len(b.missB) == 0 {
			continue
		}
		dl := desired[i]
		left, right := dl.PodA, dl.PodB
		if left > right {
			left, right = right, left
		}
		issues = append(issues, linkIssue{
			PodA:       dl.PodA,
			PodB:       dl.PodB,
			MissingA:   b.missA,
			MissingB:   b.missB,
			ReasonA:    b.reaA,
			ReasonB:    b.reaB,
			DisplayKey: fmt.Sprintf("%s<->%s", left, right),
		})
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].DisplayKey == issues[j].DisplayKey {
			ai := issues[i].PodA + "<->" + issues[i].PodB
			aj := issues[j].PodA + "<->" + issues[j].PodB
			return ai < aj
		}
		return issues[i].DisplayKey < issues[j].DisplayKey
	})

	return issues
}

func logLinkIssuesPretty(issues []linkIssue) {
	log.Printf("🧩 Reconciliation: links with issues (grouped by pairs):")

	// Group by unordered pair
	group := make(map[string][]linkIssue)
	keys := make([]string, 0)
	for _, is := range issues {
		if _, ok := group[is.DisplayKey]; !ok {
			keys = append(keys, is.DisplayKey)
		}
		group[is.DisplayKey] = append(group[is.DisplayKey], is)
	}
	sort.Strings(keys)

	for _, k := range keys {
		items := group[k]
		// Print pair headline using the stable key
		log.Printf("  🔗 %s", k)

		// Within the pair, we can have multiple "links" (different ifnames)
		for _, is := range items {
			// Build nice side strings
			aStr := is.PodA
			if len(is.MissingA) > 0 {
				aStr = fmt.Sprintf("%s falta[%s]", is.PodA, joinIfaceReasons(is.MissingA, is.ReasonA))
			}
			bStr := is.PodB
			if len(is.MissingB) > 0 {
				bStr = fmt.Sprintf("%s falta[%s]", is.PodB, joinIfaceReasons(is.MissingB, is.ReasonB))
			}
			log.Printf("     • %s  ↔  %s", aStr, bStr)
		}
	}
}

func joinIfaceReasons(ifaces []string, reasons []string) string {
	parts := make([]string, 0, len(ifaces))
	for i := range ifaces {
		r := ""
		if i < len(reasons) {
			r = reasons[i]
		}
		if r != "" {
			parts = append(parts, fmt.Sprintf("%s(%s)", ifaces[i], r))
		} else {
			parts = append(parts, ifaces[i])
		}
	}
	return strings.Join(parts, ",")
}

/* ------------------------- restart decision logic ------------------------- */

// Priority:
// 1) host > router > switch (independent of #missing interfaces)
// 2) if tie: deterministic lexicographic
func chooseRestartEndpoint(podA, podB string, missingA, missingB []string, missingCountByPod map[string]int, podType map[string]string) string {
	if podA == "external" && podB != "external" {
		return podB
	}
	if podB == "external" && podA != "external" {
		return podA
	}

	missA := len(missingA) > 0
	missB := len(missingB) > 0

	// If only one side is missing, restart that side.
	if missA && !missB {
		return podA
	}
	if missB && !missA {
		return podB
	}

	// If both sides are missing, prefer restarting switch endpoint when present.
	if missA && missB {
		ta := podTypeOrUnknown(podType, podA)
		tb := podTypeOrUnknown(podType, podB)
		if ta == "switch" && tb != "switch" {
			return podA
		}
		if tb == "switch" && ta != "switch" {
			return podB
		}

		// Otherwise prefer the endpoint with more missing interfaces globally.
		ca := missingCountByPod[podA]
		cb := missingCountByPod[podB]
		if ca > cb {
			return podA
		}
		if cb > ca {
			return podB
		}
	}

	// Fallback: type-based deterministic choice.
	ta := podTypeOrUnknown(podType, podA)
	tb := podTypeOrUnknown(podType, podB)
	ra := typeRank(ta)
	rb := typeRank(tb)

	if ra < rb {
		return podA
	}
	if rb < ra {
		return podB
	}

	// tie -> stable fallback
	if podA <= podB {
		return podA
	}
	return podB
}

/* ------------------------------ existing helpers ------------------------------ */

func buildDesiredResolvedLinks(nodes []types.NodeSpec, links []types.LinkSpec) []desiredLink {
	out := make([]desiredLink, 0, len(links))
	for _, l := range links {
		a := ResolvePodNameFromLink(l.Node, nodes)
		b := ResolvePodNameFromLink(l.PeerNode, nodes)
		out = append(out, desiredLink{
			PodA: a,
			IfA:  l.LocalIntf,
			PodB: b,
			IfB:  l.PeerIntf,
		})
	}
	return out
}

func buildPodTypeMap(nodes []types.NodeSpec) map[string]string {
	m := make(map[string]string)
	for _, n := range nodes {
		rep := n.Replicas
		if rep <= 0 {
			rep = 1
		}
		for i := 0; i < rep; i++ {
			pod := fmt.Sprintf("%s-%d", n.Name, i)
			m[pod] = n.Type
		}
	}
	return m
}

func podTypeOrUnknown(m map[string]string, pod string) string {
	if t, ok := m[pod]; ok {
		return t
	}
	return "unknown"
}

func typeRank(t string) int {
	switch t {
	case "host":
		return 1
	case "router":
		return 2
	case "switch":
		return 3
	default:
		return 9
	}
}

func shouldCountAsMissing(reason string) bool {
	switch reason {
	case "iface-missing", "exec-error", "pod-not-ready":
		return true
	default:
		return false
	}
}

// podHasInterfaceWithCachedPod runs "ip link show <iface>" against a
// pre-fetched pod (avoids per-pod API Gets that throttle under concurrency).
// Falls back to a Get if pod is nil. Returns one of: present, iface-missing,
// exec-error, pod-not-ready, pod-terminating, pod-not-found.
func podHasInterfaceWithCachedPod(namespace, podName string, pod *v1.Pod, iface string, tries int) (bool, string) {
	if pod == nil {
		fetched, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil {
			return false, "pod-not-found"
		}
		pod = fetched
	}
	if pod.DeletionTimestamp != nil {
		return false, "pod-terminating"
	}
	if !isPodReadyFast(pod) {
		return false, "pod-not-ready"
	}

	for i := 0; i < tries; i++ {
		stdout, stderr, err := ExecInPod(namespace, podName, []string{"ip", "link", "show", iface})
		if err == nil && strings.TrimSpace(stdout) != "" {
			return true, "present"
		}

		combined := strings.ToLower(stdout + "\n" + stderr)
		if strings.Contains(combined, "does not exist") || strings.Contains(combined, "cannot find device") {
			return false, "iface-missing"
		}

		if err != nil {
			time.Sleep(time.Duration(150*(i+1)) * time.Millisecond)
			continue
		}

		return false, "iface-missing"
	}

	return false, "exec-error"
}

func isPodReadyFast(pod *v1.Pod) bool {
	if pod.Status.Phase != v1.PodRunning {
		return false
	}
	readyCond := false
	for _, c := range pod.Status.Conditions {
		if c.Type == v1.PodReady && c.Status == v1.ConditionTrue {
			readyCond = true
			break
		}
	}
	if !readyCond {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			return false
		}
	}
	return true
}
