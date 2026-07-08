package helpers

import (
	"fmt"
	"log"
	"sort"
	"sync"

	"kubendt/types"
)

// RewireQemuPeersAfterRestart refreshes the in-pod TC redirect wiring on
// every QEMU pod that is a direct peer of one of the pods we just restarted
// (but that was not itself restarted). It is the safety net for the rare
// cases where meshnet had to recreate the peer's veth/vxlan device with a
// new ifindex, at that point the peer's pre-existing TC rules silently
// drop traffic in one direction because their `mirred` target points at
// the dead ifindex.
//
// The refresh just exec's `/usr/local/bin/kubendt-rewire-tc` inside the
// peer pod. That script is planted by the QEMU image's entrypoint at boot
// and knows the eth↔tap mapping it set up. It deletes the old TC filters
// (idempotent if absent) and re-installs them against the current ifindex.
// If the script isn't present (older image or non-QEMU pod), we log and
// move on, this function is best-effort defence in depth, not load-bearing.
//
// restartedPods is the set we just restarted. allNodes is the cluster's
// current node list (used to identify which pods are QEMU and to expand
// peer references).
func RewireQemuPeersAfterRestart(namespace string, restartedPods []string, allNodes []types.NodeSpec) error {
	if len(restartedPods) == 0 {
		return nil
	}

	isQemuByBase := make(map[string]bool, len(allNodes))
	for _, n := range allNodes {
		isQemuByBase[n.Name] = n.Qemu
	}
	isQemu := func(podName string) bool {
		base, _, ok := SplitIndexedPodName(podName)
		if !ok {
			base = podName
		}
		return isQemuByBase[base]
	}

	restartedSet := make(map[string]struct{}, len(restartedPods))
	for _, p := range restartedPods {
		restartedSet[p] = struct{}{}
	}

	allLinks, err := BuildLinksFromTopologyCRDs(namespace)
	if err != nil {
		return fmt.Errorf("could not read topology CRDs for rewire pass: %w", err)
	}

	// Collect distinct QEMU peer pods that border a restarted pod but were
	// not themselves restarted. Those are the pods whose TC rules may now
	// point at a dead ifindex.
	targets := make(map[string]struct{})
	for _, l := range allLinks {
		var restarted, peer string
		switch {
		case isInSet(restartedSet, l.Node) && !isInSet(restartedSet, l.PeerNode):
			restarted, peer = l.Node, l.PeerNode
		case isInSet(restartedSet, l.PeerNode) && !isInSet(restartedSet, l.Node):
			restarted, peer = l.PeerNode, l.Node
		default:
			continue
		}
		_ = restarted
		if peer == "" || peer == externalNodeName {
			continue
		}
		if !isQemu(peer) {
			continue
		}
		targets[peer] = struct{}{}
	}

	if len(targets) == 0 {
		return nil
	}

	targetList := make([]string, 0, len(targets))
	for p := range targets {
		targetList = append(targetList, p)
	}
	sort.Strings(targetList)
	log.Printf("ℹ️ Post-restart QEMU rewire: refreshing TC on %d peer(s): %v", len(targetList), targetList)

	// Run all rewires in parallel; each is a single kubectl exec.
	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := make([]string, 0)
	for _, pod := range targetList {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			stdout, stderr, execErr := ExecInPod(namespace, p, []string{"/usr/local/bin/kubendt-rewire-tc"})
			if execErr != nil {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("%s: %v (stderr=%q)", p, execErr, stderr))
				mu.Unlock()
				log.Printf("⚠️ Post-restart QEMU rewire: %s failed: %v (stderr=%q)", p, execErr, stderr)
				return
			}
			if stdout != "" {
				log.Printf("ℹ️ Post-restart QEMU rewire: %s output: %s", p, stdout)
			} else {
				log.Printf("ℹ️ Post-restart QEMU rewire: %s OK", p)
			}
		}(pod)
	}
	wg.Wait()

	if len(failures) > 0 {
		return fmt.Errorf("rewire failed on %d pod(s): %v", len(failures), failures)
	}
	return nil
}

func isInSet(s map[string]struct{}, key string) bool {
	_, ok := s[key]
	return ok
}
