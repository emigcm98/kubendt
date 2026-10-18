package helpers

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	drivers_meta "kubendt/drivers/meta"
	"kubendt/executor"
	"kubendt/kubeclient"
	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The platform owns the MTU of every link. Meshnet creates a veth at 1500 and
// a VXLAN at the worker NIC's MTU minus 50, and a bridge drops a frame larger
// than its egress port without ICMP, so one value for the whole namespace is
// what keeps a lab independent of where its pods landed.
const (
	DefaultLinkMTU = 1450
	MinLinkMTU     = 68
	MaxLinkMTU     = 65535
	LinkMTUEnv     = "KUBENDT_LINK_MTU"
)

// PlatformLinkMTU returns the MTU every link gets and whether the operator
// set it. An unusable value is an error, not a fallback: it is a cluster
// setting and a silent default would hide the typo.
func PlatformLinkMTU() (mtu int, explicit bool, err error) {
	return parsePlatformLinkMTU(os.Getenv(LinkMTUEnv))
}

func parsePlatformLinkMTU(raw string) (int, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultLinkMTU, false, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < MinLinkMTU || n > MaxLinkMTU {
		return 0, true, fmt.Errorf("%s=%q is not an MTU between %d and %d", LinkMTUEnv, raw, MinLinkMTU, MaxLinkMTU)
	}
	return n, true, nil
}

// A VXLAN device is created at the most its worker carries and the kernel
// reports no ceiling afterwards, only refuses to go above it. So the ceiling
// is read off devices created after this backend started (a pass may have
// lowered older ones), and when memory has none the kernel's refusal at set
// time is the answer.
var (
	processStart  = time.Now()
	ceilingMu     sync.Mutex
	workerCeiling = map[string]int{}
	// The value the last pass left on each namespace, for GET /network/links.
	namespaceMTU = map[string]int{}
)

func recordWorkerCeiling(worker string, mtu int) {
	if worker == "" || mtu <= 0 {
		return
	}
	ceilingMu.Lock()
	defer ceilingMu.Unlock()
	if mtu > workerCeiling[worker] {
		workerCeiling[worker] = mtu
	}
}

// WorkerVXLANCeiling is the largest MTU a fresh VXLAN end has been seen with
// on the worker, 0 while none has been wired there since the backend started.
func WorkerVXLANCeiling(worker string) int {
	ceilingMu.Lock()
	defer ceilingMu.Unlock()
	return workerCeiling[worker]
}

// NamespaceLinkMTU is the MTU the last pass set in the namespace, 0 before
// the first one.
func NamespaceLinkMTU(namespace string) int {
	ceilingMu.Lock()
	defer ceilingMu.Unlock()
	return namespaceMTU[namespace]
}

// MTUPass is what one pass did, for the operation's response and its log.
type MTUPass struct {
	MTU      int             `json:"mtu"`
	Links    int             `json:"links"`
	Changed  int             `json:"changed"`
	Warnings []types.Warning `json:"-"`
}

// mtuEnd is one pod interface that terminates a link.
type mtuEnd struct {
	Pod, Iface   string
	Worker       string
	Peer, PeerIf string
	Cross        bool // the peer runs on another worker, so this is a VXLAN end
	Fresh        bool // created after this backend started, so its MTU is the kernel's own
}

// refusedEnd is a VXLAN end the kernel would not raise to the value.
type refusedEnd struct {
	mtuEnd
	Current int
}

// ApplyPlatformMTU sets the platform MTU on every end of every link of the
// namespace, pod side and guest side, and is run after each wiring: deploy,
// modify and restart. It reads what the pods have (or takes the heal pass's
// reads, per pod the interfaces with their MTU, for the pods it has), learns
// the VXLAN ceiling of each worker from the fresh devices, settles the value
// for the whole namespace and changes only what differs. An operator value
// that no end can carry is an error; the default that does not fit is lowered
// with a warning.
func ApplyPlatformMTU(namespace string, known map[string]map[string]int) (MTUPass, error) {
	target, explicit, err := PlatformLinkMTU()
	if err != nil {
		return MTUPass{}, err
	}
	links, err := BuildLinksFromTopologyCRDs(namespace)
	if err != nil {
		return MTUPass{}, fmt.Errorf("mtu pass: %w", err)
	}
	podList, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return MTUPass{}, fmt.Errorf("mtu pass: listing pods: %w", err)
	}
	pods := map[string]*v1.Pod{}
	for i := range podList.Items {
		pods[podList.Items[i].Name] = &podList.Items[i]
	}

	ends, nLinks := linkEnds(links, pods)
	if len(ends) == 0 {
		return MTUPass{MTU: target, Warnings: []types.Warning{}}, nil
	}

	// One read per pod, in parallel, of every interface the pod has, skipping
	// the pods the caller already read.
	current := readPodMTUs(namespace, endsByPod(ends), known)

	for _, e := range ends {
		if e.Cross && e.Fresh {
			recordWorkerCeiling(e.Worker, current[e.Pod][e.Iface])
		}
	}

	eff, warnings, err := settleMTU(target, explicit, ends)
	if err != nil {
		return MTUPass{}, err
	}

	// A second round only happens when the kernel refused the value on a
	// VXLAN end memory knew nothing about: the value is lowered to what that
	// end carries and every link is written again so they all agree.
	changed := 0
	for round := 1; round <= 2; round++ {
		n, refused, setWarnings := setLinkMTUs(namespace, eff, ends, current, pods)
		changed += n
		warnings = append(warnings, setWarnings...)
		if len(refused) == 0 {
			break
		}
		if explicit {
			r := refused[0]
			return MTUPass{}, fmt.Errorf("%s=%d does not fit link %s:%s ↔ %s:%s: worker %s refused it over VXLAN, the device carries %d",
				LinkMTUEnv, eff, r.Pod, r.Iface, r.Peer, r.PeerIf, r.Worker, r.Current)
		}
		low := eff
		for _, r := range refused {
			recordWorkerCeiling(r.Worker, r.Current)
			if r.Current < low {
				low = r.Current
			}
		}
		warnings = append(warnings, types.Warning{
			Kind:   "link_mtu_lowered",
			Detail: fmt.Sprintf("platform MTU lowered from %d to %d: a VXLAN end on worker %s refused %d (set %s to choose the value)", eff, low, refused[0].Worker, eff, LinkMTUEnv),
		})
		eff = low
	}
	if warnings == nil {
		warnings = []types.Warning{}
	}

	ceilingMu.Lock()
	namespaceMTU[namespace] = eff
	ceilingMu.Unlock()

	log.Printf("📏 MTU pass in %s: %d links at %d, %d interface(s) changed, %d warning(s)", namespace, nLinks, eff, changed, len(warnings))
	return MTUPass{MTU: eff, Links: nLinks, Changed: changed, Warnings: warnings}, nil
}

// linkEnds turns the namespace's links into pod interfaces, dropping host
// uplinks (the pod end is a macvlan on the host NIC, not Meshnet's) and
// links whose pods are not running. Both ends of a link are fresh when
// either pod is: Meshnet rebuilds the facing end along with the new pod.
func linkEnds(links []types.LinkSpec, pods map[string]*v1.Pod) ([]mtuEnd, int) {
	var ends []mtuEnd
	n := 0
	for _, l := range links {
		if l.Node == externalNodeName || l.PeerNode == externalNodeName {
			continue
		}
		a, b := pods[l.Node], pods[l.PeerNode]
		if !podRunning(a) || !podRunning(b) {
			continue
		}
		cross := a.Spec.NodeName != b.Spec.NodeName
		fresh := a.CreationTimestamp.After(processStart) || b.CreationTimestamp.After(processStart)
		n++
		ends = append(ends,
			mtuEnd{Pod: l.Node, Iface: l.LocalIntf, Worker: a.Spec.NodeName, Peer: l.PeerNode, PeerIf: l.PeerIntf, Cross: cross, Fresh: fresh},
			mtuEnd{Pod: l.PeerNode, Iface: l.PeerIntf, Worker: b.Spec.NodeName, Peer: l.Node, PeerIf: l.LocalIntf, Cross: cross, Fresh: fresh},
		)
	}
	return ends, n
}

func podRunning(pod *v1.Pod) bool {
	return pod != nil && pod.Status.Phase == v1.PodRunning && pod.DeletionTimestamp == nil
}

func endsByPod(ends []mtuEnd) map[string][]mtuEnd {
	byPod := map[string][]mtuEnd{}
	for _, e := range ends {
		byPod[e.Pod] = append(byPod[e.Pod], e)
	}
	return byPod
}

// settleMTU picks the value the whole namespace gets from what memory knows.
// The ceiling of a VXLAN end is its worker's; a veth end has none that matters.
func settleMTU(target int, explicit bool, ends []mtuEnd) (int, []types.Warning, error) {
	eff := target
	var warnings []types.Warning
	seen := map[string]bool{}
	for _, e := range ends {
		if !e.Cross {
			continue
		}
		c := WorkerVXLANCeiling(e.Worker)
		if c == 0 || eff <= c {
			continue
		}
		if explicit {
			return 0, nil, fmt.Errorf("%s=%d does not fit link %s:%s ↔ %s:%s: worker %s carries at most %d over VXLAN",
				LinkMTUEnv, target, e.Pod, e.Iface, e.Peer, e.PeerIf, e.Worker, c)
		}
		if !seen[e.Worker] {
			seen[e.Worker] = true
			warnings = append(warnings, types.Warning{
				Kind:   "link_mtu_lowered",
				Detail: fmt.Sprintf("platform MTU lowered from %d to %d: worker %s carries at most %d over VXLAN (set %s to choose the value)", eff, c, e.Worker, c, LinkMTUEnv),
			})
		}
		eff = c
	}
	return eff, warnings, nil
}

// readPodMTUs returns, per pod, the MTU of every interface it has.
func readPodMTUs(namespace string, byPod map[string][]mtuEnd, known map[string]map[string]int) map[string]map[string]int {
	out := map[string]map[string]int{}
	var mu sync.Mutex
	forEachPod(byPod, func(pod string, ends []mtuEnd) {
		mtus, ok := known[pod]
		if !ok {
			mtus = podLinkMTUs(namespace, pod, ends)
		}
		mu.Lock()
		out[pod] = mtus
		mu.Unlock()
	})
	return out
}

// podLinkMTUs reads the pod's interfaces in one exec: ip -o link show, or the
// sysfs values of the link interfaces when that output does not parse.
func podLinkMTUs(namespace, pod string, ends []mtuEnd) map[string]int {
	stdout, _, err := ExecInPod(namespace, pod, []string{"ip", "-o", "link", "show"})
	if err == nil {
		if mtus := parseLinkMTUs(stdout); len(mtus) > 0 {
			return mtus
		}
	}
	names := make([]string, 0, len(ends)*2)
	for _, e := range ends {
		names = append(names, e.Iface)
		if tap := tapFor(e.Iface); tap != "" {
			names = append(names, tap)
		}
	}
	script := "for i in " + strings.Join(names, " ") + "; do [ -r /sys/class/net/$i/mtu ] && echo \"$i $(cat /sys/class/net/$i/mtu)\"; done"
	stdout, _, err = ExecInPod(namespace, pod, []string{"sh", "-c", script})
	if err != nil {
		log.Printf("⚠️ MTU pass: could not read the interfaces of %s/%s: %v", namespace, pod, err)
		return map[string]int{}
	}
	return parseSysfsMTUs(stdout)
}

// parseLinkMTUs reads "ip -o link show" into interface name to MTU.
func parseLinkMTUs(out string) map[string]int {
	res := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || !strings.HasSuffix(f[0], ":") {
			continue
		}
		name := strings.TrimSuffix(f[1], ":")
		if i := strings.Index(name, "@"); i >= 0 {
			name = name[:i]
		}
		for i := 2; i+1 < len(f); i++ {
			if f[i] == "mtu" {
				if n, err := strconv.Atoi(f[i+1]); err == nil {
					res[name] = n
				}
				break
			}
		}
	}
	return res
}

// parseSysfsMTUs reads "name mtu" lines.
func parseSysfsMTUs(out string) map[string]int {
	res := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		if n, err := strconv.Atoi(f[1]); err == nil {
			res[f[0]] = n
		}
	}
	return res
}

// tapFor names the tap a VM pod's entrypoint pairs with a pod interface
// (eth2 and tap2), "" for a name that has no tap.
func tapFor(iface string) string {
	if !strings.HasPrefix(iface, "eth") || len(iface) == len("eth") {
		return ""
	}
	return "tap" + strings.TrimPrefix(iface, "eth")
}

// setScript writes the MTU on each interface on its own, so one refusal does
// not stop the rest and names itself on stdout.
func setScript(names []string, mtu int) string {
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = "ip link set dev " + name + " mtu " + strconv.Itoa(mtu) + " 2>/dev/null || echo FAIL " + name
	}
	return strings.Join(parts, "; ")
}

// parseSetFailures returns the interfaces a setScript could not set.
func parseSetFailures(out string) map[string]bool {
	failed := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "FAIL" {
			failed[f[1]] = true
		}
	}
	return failed
}

// setLinkMTUs writes eff on every end that does not have it: one exec per
// pod for its interfaces (and taps on a VM pod), then one commit in the guest
// when its driver reaches it. Returns how many interfaces changed and the
// VXLAN ends the kernel refused, which sit at the most their worker carries.
func setLinkMTUs(namespace string, eff int, ends []mtuEnd, current map[string]map[string]int, pods map[string]*v1.Pod) (int, []refusedEnd, []types.Warning) {
	changed := 0
	var refused []refusedEnd
	var warnings []types.Warning
	var mu sync.Mutex
	forEachPod(endsByPod(ends), func(podName string, podEnds []mtuEnd) {
		pod := pods[podName]
		guest := IsGuestVM(pod.Labels)
		have := current[podName]
		var todo []string
		byName := map[string]mtuEnd{}
		for _, e := range podEnds {
			byName[e.Iface] = e
			if have[e.Iface] != eff {
				todo = append(todo, e.Iface)
			}
			if guest {
				if tap := tapFor(e.Iface); tap != "" {
					if mtu, ok := have[tap]; ok && mtu != eff {
						todo = append(todo, tap)
					}
				}
			}
		}
		var podWarnings []types.Warning
		var podRefused []refusedEnd
		n := 0
		if len(todo) > 0 {
			sort.Strings(todo)
			stdout, stderr, err := ExecInPod(namespace, podName, []string{"sh", "-c", setScript(todo, eff)})
			if err != nil {
				podWarnings = append(podWarnings, types.Warning{
					Node:   podName,
					Kind:   "link_mtu_not_set",
					Detail: fmt.Sprintf("could not set MTU %d on %s: %v %s", eff, strings.Join(todo, ", "), err, strings.TrimSpace(stderr)),
				})
			} else {
				failed := parseSetFailures(stdout)
				n = len(todo) - len(failed)
				for name := range failed {
					e, isEnd := byName[name]
					if isEnd && e.Cross && have[name] > 0 {
						podRefused = append(podRefused, refusedEnd{mtuEnd: e, Current: have[name]})
						continue
					}
					podWarnings = append(podWarnings, types.Warning{
						Node:   podName,
						Kind:   "link_mtu_not_set",
						Detail: fmt.Sprintf("could not set MTU %d on %s", eff, name),
					})
				}
			}
		}
		if guest && len(podRefused) == 0 {
			if w := setGuestMTU(namespace, pod, podEnds, eff); w != nil {
				podWarnings = append(podWarnings, *w)
			}
		}
		mu.Lock()
		changed += n
		refused = append(refused, podRefused...)
		warnings = append(warnings, podWarnings...)
		mu.Unlock()
	})
	sort.Slice(refused, func(i, j int) bool {
		if refused[i].Pod != refused[j].Pod {
			return refused[i].Pod < refused[j].Pod
		}
		return refused[i].Iface < refused[j].Iface
	})
	return changed, refused, warnings
}

// setGuestMTU commits the MTU of the pod's link interfaces inside its guest,
// unless the guest already reports it: a read is a fraction of a commit, and
// a guest booted with the value (the VyOS image takes it from its env) or set
// by an earlier pass costs nothing.
func setGuestMTU(namespace string, pod *v1.Pod, ends []mtuEnd, eff int) *types.Warning {
	drv, err := GetDriverForListedPod(pod)
	if err != nil {
		return &types.Warning{Node: pod.Name, Kind: "link_mtu_not_set", Detail: "guest MTU: " + err.Error()}
	}
	setter, ok := drv.(drivers_meta.GuestMTUSetter)
	if !ok {
		return &types.Warning{Node: pod.Name, Kind: "link_mtu_not_set", Detail: fmt.Sprintf("guest MTU: driver %s cannot set the MTU inside its guest", pod.Labels["kubendt/driver"])}
	}
	if inspector, ok := drv.(drivers_meta.EffectiveInterfaceInspector); ok {
		if entries, err := inspector.GetEffectiveInterfaces(namespace, pod.Name); err == nil && !guestNeedsMTU(entries, ends, eff) {
			return nil
		}
	}
	exec, _, err := executor.ResolveForDriver(drv)
	if err != nil {
		return &types.Warning{Node: pod.Name, Kind: "link_mtu_not_set", Detail: "guest MTU: " + err.Error()}
	}
	var lines []string
	for _, e := range ends {
		for _, cmd := range setter.GuestMTUCommands(e.Iface, eff) {
			lines = append(lines, strings.Join(cmd, " "))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	if err := exec.ExecCommand(pod.Name, namespace, executor.NewArgsCommand(lines)); err != nil {
		return &types.Warning{Node: pod.Name, Kind: "link_mtu_not_set", Detail: fmt.Sprintf("guest MTU %d: %v", eff, err)}
	}
	return nil
}

// guestNeedsMTU says whether any link interface of the guest reports an MTU
// other than eff; an interface the guest does not report counts as needing it.
func guestNeedsMTU(entries []map[string]string, ends []mtuEnd, eff int) bool {
	have := map[string]int{}
	for _, e := range entries {
		if n, err := strconv.Atoi(e["mtu"]); err == nil {
			have[e["interface"]] = n
		}
	}
	for _, e := range ends {
		if have[e.Iface] != eff {
			return true
		}
	}
	return false
}

// forEachPod runs fn for every pod with a bounded number in flight.
func forEachPod(byPod map[string][]mtuEnd, fn func(pod string, ends []mtuEnd)) {
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for pod, ends := range byPod {
		wg.Add(1)
		sem <- struct{}{}
		go func(pod string, ends []mtuEnd) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(pod, ends)
		}(pod, ends)
	}
	wg.Wait()
}
