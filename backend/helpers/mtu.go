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

	"kubendt/capabilities"
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
	// The extra addresses a VM guest boots with, "eth1=a,b;eth2=c".
	LinkAddressesEnv = "KUBENDT_LINK_ADDRESSES"
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

// MTUPass is what one pass did, for the operation's response and its log:
// the MTU it settled on, the links it covered, the interfaces whose MTU it
// changed and the declared extra addresses it wrote.
type MTUPass struct {
	MTU       int             `json:"mtu"`
	Links     int             `json:"links"`
	Changed   int             `json:"changed"`
	Addresses int             `json:"addresses"`
	Warnings  []types.Warning `json:"-"`
}

// mtuEnd is one pod interface that terminates a link.
type mtuEnd struct {
	Pod, Iface   string
	Worker       string
	Peer, PeerIf string
	Cross        bool     // the peer runs on another worker, so this is a VXLAN end
	Fresh        bool     // created after this backend started, so its MTU is the kernel's own
	Want         int      // the MTU the topology declares for this link, 0 for the platform's
	Addrs        []string // the addresses the topology declares on this end beyond Meshnet's
}

// targetOf is the MTU an end gets: the link's own when declared, else the
// namespace value.
func targetOf(e mtuEnd, eff int) int {
	if e.Want > 0 {
		return e.Want
	}
	return eff
}

// refusedEnd is a VXLAN end the kernel would not raise to the value.
type refusedEnd struct {
	mtuEnd
	Current int
}

// ApplyPlatformMTU sets the MTU on every end of every link of the namespace,
// pod side and guest side, and is run after each wiring: deploy, modify and
// restart. Every link gets the platform value unless the topology declares
// its own. It reads what the pods have (or takes the heal pass's reads, per
// pod the interfaces with their MTU, for the pods it has), learns the VXLAN
// ceiling of each worker from the fresh devices, settles the value for the
// whole namespace and changes only what differs. A value someone asked for
// that an end cannot carry, the operator's or a link's, is an error; the
// default that does not fit is lowered with a warning. With only set, just
// the links touching those pods are passed (a configure that changed one).
// The same write puts the extra addresses a link declares on each end,
// which Meshnet does not set: idempotent, so they are written every pass,
// except those the pod's history removed.
func ApplyPlatformMTU(namespace string, known map[string]map[string]int, only map[string]bool) (MTUPass, error) {
	target, explicit, err := PlatformLinkMTU()
	if err != nil {
		return MTUPass{}, err
	}
	links, err := BuildLinksFromTopologyCRDs(namespace)
	if err != nil {
		return MTUPass{}, fmt.Errorf("mtu pass: %w", err)
	}
	attrs, err := LoadLinkAttributes(namespace)
	if err != nil {
		log.Printf("⚠️ MTU pass: could not load the link attributes of %s, links get the platform value: %v", namespace, err)
		attrs = map[string]LinkAttrs{}
	}
	podList, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return MTUPass{}, fmt.Errorf("mtu pass: listing pods: %w", err)
	}
	pods := map[string]*v1.Pod{}
	for i := range podList.Items {
		pods[podList.Items[i].Name] = &podList.Items[i]
	}

	ends, nLinks := linkEnds(links, pods, attrs, only)
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
	changed, addressed := 0, 0
	for round := 1; round <= 2; round++ {
		n, nAddr, refused, setWarnings := setLinkMTUs(namespace, eff, ends, current, pods)
		changed += n
		addressed += nAddr
		warnings = append(warnings, setWarnings...)
		if len(refused) == 0 {
			break
		}
		for _, r := range refused {
			if r.Want > 0 {
				return MTUPass{}, fmt.Errorf("link %s:%s ↔ %s:%s asks for mtu %d: worker %s refused it over VXLAN, the device carries %d",
					r.Pod, r.Iface, r.Peer, r.PeerIf, r.Want, r.Worker, r.Current)
			}
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

	log.Printf("📏 MTU pass in %s: %d links at %d, %d interface(s) changed, %d address(es) written, %d warning(s)", namespace, nLinks, eff, changed, addressed, len(warnings))
	return MTUPass{MTU: eff, Links: nLinks, Changed: changed, Addresses: addressed, Warnings: warnings}, nil
}

// linkEnds turns the namespace's links into pod interfaces, dropping host
// uplinks (the pod end is a macvlan on the host NIC, not Meshnet's), links
// whose pods are not running and, with only set, links touching none of
// those pods. Both ends of a link are fresh when either pod is: Meshnet
// rebuilds the facing end along with the new pod. A link's declared MTU and
// extra addresses come from the attributes, keyed by either end.
func linkEnds(links []types.LinkSpec, pods map[string]*v1.Pod, attrs map[string]LinkAttrs, only map[string]bool) ([]mtuEnd, int) {
	var ends []mtuEnd
	n := 0
	for _, l := range links {
		if l.Node == externalNodeName || l.PeerNode == externalNodeName {
			continue
		}
		if only != nil && !only[l.Node] && !only[l.PeerNode] {
			continue
		}
		a, b := pods[l.Node], pods[l.PeerNode]
		if !podRunning(a) || !podRunning(b) {
			continue
		}
		cross := a.Spec.NodeName != b.Spec.NodeName
		fresh := a.CreationTimestamp.After(processStart) || b.CreationTimestamp.After(processStart)
		la, ok := attrs[l.Node+"/"+l.LocalIntf]
		if !ok {
			la = attrs[l.PeerNode+"/"+l.PeerIntf].swapped()
		}
		n++
		ends = append(ends,
			mtuEnd{Pod: l.Node, Iface: l.LocalIntf, Worker: a.Spec.NodeName, Peer: l.PeerNode, PeerIf: l.PeerIntf, Cross: cross, Fresh: fresh, Want: la.MTU, Addrs: la.LocalAddrs},
			mtuEnd{Pod: l.PeerNode, Iface: l.PeerIntf, Worker: b.Spec.NodeName, Peer: l.Node, PeerIf: l.LocalIntf, Cross: cross, Fresh: fresh, Want: la.MTU, Addrs: la.PeerAddrs},
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
// The ceiling of a VXLAN end is its worker's; a veth end has none that
// matters. A link with its own MTU is checked against the ceiling on its own
// and never lowers the namespace value.
func settleMTU(target int, explicit bool, ends []mtuEnd) (int, []types.Warning, error) {
	eff := target
	var warnings []types.Warning
	seen := map[string]bool{}
	for _, e := range ends {
		if !e.Cross {
			continue
		}
		c := WorkerVXLANCeiling(e.Worker)
		if e.Want > 0 {
			if c > 0 && e.Want > c {
				return 0, nil, fmt.Errorf("link %s:%s ↔ %s:%s asks for mtu %d but worker %s carries at most %d over VXLAN",
					e.Pod, e.Iface, e.Peer, e.PeerIf, e.Want, e.Worker, c)
			}
			continue
		}
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

// setItem is one write of the per-pod script: the MTU of an interface, or
// an address to put on it.
type setItem struct {
	Name string
	MTU  int
	Addr string
}

// key names the item in the script's FAIL lines.
func (it setItem) key() string {
	if it.Addr != "" {
		return it.Name + " " + it.Addr
	}
	return it.Name
}

// setScript writes each item on its own, so one refusal does not stop the
// rest and names itself on stdout.
func setScript(items []setItem) string {
	parts := make([]string, len(items))
	for i, it := range items {
		if it.Addr != "" {
			parts[i] = "ip addr replace " + it.Addr + " dev " + it.Name + " 2>/dev/null || echo FAIL " + it.key()
		} else {
			parts[i] = "ip link set dev " + it.Name + " mtu " + strconv.Itoa(it.MTU) + " 2>/dev/null || echo FAIL " + it.Name
		}
	}
	return strings.Join(parts, "; ")
}

// parseSetFailures returns the keys of the items a setScript could not write.
func parseSetFailures(out string) map[string]bool {
	failed := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "FAIL" {
			failed[strings.Join(f[1:], " ")] = true
		}
	}
	return failed
}

// removedAddresses lists the "iface cidr" pairs the pod's history removes:
// a declared address the user took off with remove_ip stays off whichever
// runs first, the pass or the replay. A later set_ip of it puts it back.
func removedAddresses(namespace, pod string) map[string]bool {
	if DB == nil {
		return map[string]bool{}
	}
	ops, err := ListDriverOperationsForPod(namespace, pod)
	if err != nil {
		log.Printf("⚠️ MTU pass: could not read the history of %s/%s: %v", namespace, pod, err)
		return map[string]bool{}
	}
	return addressesRemovedBy(ops)
}

func addressesRemovedBy(ops []PersistedDriverOperation) map[string]bool {
	removed := map[string]bool{}
	for _, op := range ops {
		t := op.Action.Type
		if t == "" {
			t = op.ActionType
		}
		key := op.Action.Iface + " " + op.Action.CIDR
		switch t {
		case "remove_ip":
			removed[key] = true
		case "set_ip", "replace_ip":
			delete(removed, key)
		}
	}
	return removed
}

// setLinkMTUs writes eff on every end that does not have it and the extra
// addresses on every end that declares them: one exec per pod for its
// interfaces (and taps on a VM pod), then one commit in the guest when its
// driver reaches it. Returns how many interfaces changed, how many addresses
// were written and the VXLAN ends the kernel refused, which sit at the most
// their worker carries.
func setLinkMTUs(namespace string, eff int, ends []mtuEnd, current map[string]map[string]int, pods map[string]*v1.Pod) (int, int, []refusedEnd, []types.Warning) {
	changed, addressed := 0, 0
	var refused []refusedEnd
	var warnings []types.Warning
	var mu sync.Mutex
	forEachPod(endsByPod(ends), func(podName string, podEnds []mtuEnd) {
		pod := pods[podName]
		guest := IsGuestVM(pod.Labels)
		have := current[podName]
		removed := removedAddresses(namespace, podName)
		var todo []setItem
		byName := map[string]mtuEnd{}
		for _, e := range podEnds {
			byName[e.Iface] = e
			want := targetOf(e, eff)
			if have[e.Iface] != want {
				todo = append(todo, setItem{Name: e.Iface, MTU: want})
			}
			if guest {
				// The guest owns the addresses of a VM pod, set below.
				if tap := tapFor(e.Iface); tap != "" {
					if mtu, ok := have[tap]; ok && mtu != want {
						todo = append(todo, setItem{Name: tap, MTU: want})
					}
				}
				continue
			}
			for _, addr := range e.Addrs {
				if !removed[e.Iface+" "+addr] {
					todo = append(todo, setItem{Name: e.Iface, Addr: addr})
				}
			}
		}
		var podWarnings []types.Warning
		var podRefused []refusedEnd
		n, nAddr := 0, 0
		if len(todo) > 0 {
			sort.Slice(todo, func(i, j int) bool { return todo[i].key() < todo[j].key() })
			keys := make([]string, len(todo))
			for i, it := range todo {
				keys[i] = it.key()
			}
			stdout, stderr, err := ExecInPod(namespace, podName, []string{"sh", "-c", setScript(todo)})
			if err != nil {
				podWarnings = append(podWarnings, types.Warning{
					Node:   podName,
					Kind:   "link_mtu_not_set",
					Detail: fmt.Sprintf("could not write %s: %v %s", strings.Join(keys, ", "), err, strings.TrimSpace(stderr)),
				})
			} else {
				failed := parseSetFailures(stdout)
				for _, it := range todo {
					if !failed[it.key()] {
						if it.Addr != "" {
							nAddr++
						} else {
							n++
						}
						continue
					}
					if it.Addr != "" {
						podWarnings = append(podWarnings, types.Warning{
							Node:   podName,
							Kind:   "link_address_not_set",
							Detail: fmt.Sprintf("could not set %s on %s", it.Addr, it.Name),
						})
						continue
					}
					e, isEnd := byName[it.Name]
					if isEnd && e.Cross && have[it.Name] > 0 {
						podRefused = append(podRefused, refusedEnd{mtuEnd: e, Current: have[it.Name]})
						continue
					}
					podWarnings = append(podWarnings, types.Warning{
						Node:   podName,
						Kind:   "link_mtu_not_set",
						Detail: fmt.Sprintf("could not set the MTU on %s", it.Name),
					})
				}
			}
		}
		if guest && len(podRefused) == 0 {
			g, w := setGuestLinks(namespace, pod, podEnds, eff, removed)
			nAddr += g
			podWarnings = append(podWarnings, w...)
		}
		mu.Lock()
		changed += n
		addressed += nAddr
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
	return changed, addressed, refused, warnings
}

// setGuestLinks commits inside a VM guest what its link interfaces lack, in
// one commit: the MTU, unless the guest already reports it (a read is a
// fraction of a commit, and a guest booted with the value, the VyOS image
// takes it from its env, costs nothing), and the declared extra addresses it
// does not show. Returns how many addresses it committed.
func setGuestLinks(namespace string, pod *v1.Pod, ends []mtuEnd, eff int, removed map[string]bool) (int, []types.Warning) {
	warn := func(kind, detail string) types.Warning {
		return types.Warning{Node: pod.Name, Kind: kind, Detail: detail}
	}
	drv, err := GetDriverForListedPod(pod)
	if err != nil {
		return 0, []types.Warning{warn("link_mtu_not_set", "guest: "+err.Error())}
	}
	var entries []map[string]string
	inspected := false
	if inspector, ok := drv.(drivers_meta.EffectiveInterfaceInspector); ok {
		if entries, err = inspector.GetEffectiveInterfaces(namespace, pod.Name); err == nil {
			inspected = true
		}
	}
	var lines []string
	var warnings []types.Warning
	if !inspected || guestNeedsMTU(entries, ends, eff) {
		if setter, ok := drv.(drivers_meta.GuestMTUSetter); ok {
			for _, e := range ends {
				for _, cmd := range setter.GuestMTUCommands(e.Iface, targetOf(e, eff)) {
					lines = append(lines, strings.Join(cmd, " "))
				}
			}
		} else {
			warnings = append(warnings, warn("link_mtu_not_set", fmt.Sprintf("guest MTU: driver %s cannot set the MTU inside its guest", pod.Labels["kubendt/driver"])))
		}
	}
	missing := guestMissingAddrs(entries, inspected, ends, removed)
	nAddr := 0
	if len(missing) > 0 {
		if l3, ok := drv.(capabilities.L3Capable); ok {
			for _, m := range missing {
				for _, cmd := range l3.SetIP(m.Name, m.Addr) {
					lines = append(lines, strings.Join(cmd, " "))
				}
				nAddr++
			}
		} else {
			warnings = append(warnings, warn("link_address_not_set", fmt.Sprintf("guest addresses: driver %s has no L3 support", pod.Labels["kubendt/driver"])))
		}
	}
	if len(lines) == 0 {
		return 0, warnings
	}
	exec, _, err := executor.ResolveForDriver(drv)
	if err != nil {
		return 0, append(warnings, warn("link_mtu_not_set", "guest: "+err.Error()))
	}
	if err := exec.ExecCommand(pod.Name, namespace, executor.NewArgsCommand(lines)); err != nil {
		return 0, append(warnings, warn("link_mtu_not_set", fmt.Sprintf("guest commit: %v", err)))
	}
	return nAddr, warnings
}

// guestMissingAddrs is every declared extra address the guest does not show
// on its interface, all of them when the guest could not be read, minus the
// ones the history removed.
func guestMissingAddrs(entries []map[string]string, inspected bool, ends []mtuEnd, removed map[string]bool) []setItem {
	have := map[string]map[string]bool{}
	for _, e := range entries {
		set := map[string]bool{}
		for _, a := range strings.Split(e["addresses"], ",") {
			if a != "" {
				set[a] = true
			}
		}
		have[e["interface"]] = set
	}
	var missing []setItem
	for _, e := range ends {
		for _, addr := range e.Addrs {
			if removed[e.Iface+" "+addr] || (inspected && have[e.Iface][addr]) {
				continue
			}
			missing = append(missing, setItem{Name: e.Iface, Addr: addr})
		}
	}
	return missing
}

// guestNeedsMTU says whether any link interface of the guest reports an MTU
// other than its target; an interface the guest does not report counts as
// needing it.
func guestNeedsMTU(entries []map[string]string, ends []mtuEnd, eff int) bool {
	have := map[string]int{}
	for _, e := range entries {
		if n, err := strconv.Atoi(e["mtu"]); err == nil {
			have[e["interface"]] = n
		}
	}
	for _, e := range ends {
		if have[e.Iface] != targetOf(e, eff) {
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
