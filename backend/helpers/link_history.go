package helpers

import (
	"fmt"
	"log"
	"net/netip"
	"sort"
	"strings"

	"kubendt/types"
)

// RemovedEnds is what a pod lost with the links taken from it: the interfaces
// and the subnets of the addresses they carried.
type RemovedEnds struct {
	Ifaces  map[string]bool
	Subnets []netip.Prefix
}

func subnetsOf(addrs types.Addrs) []netip.Prefix {
	var out []netip.Prefix
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(strings.TrimSpace(a)); err == nil {
			out = append(out, p.Masked())
		}
	}
	return out
}

// KeptSubnetsByPod is the subnets each pod still has on ends that came through
// the change as they were: an end of the same link, with the same uid. An end of
// a link put back under the same name is a new interface, so what it carries now
// is not what a route that went with the old one can lean on.
func KeptSubnetsByPod(before, after []types.LinkSpec) map[string][]netip.Prefix {
	type key struct {
		pod, intf string
		uid       int
	}
	uidOf := func(l types.LinkSpec) int {
		if l.UID != nil {
			return *l.UID
		}
		return 0
	}
	was := map[key]bool{}
	for _, l := range before {
		was[key{l.Node, l.LocalIntf, uidOf(l)}] = true
		was[key{l.PeerNode, l.PeerIntf, uidOf(l)}] = true
	}
	out := map[string][]netip.Prefix{}
	for _, l := range after {
		if l.Node != "" && l.Node != externalNodeName && was[key{l.Node, l.LocalIntf, uidOf(l)}] {
			out[l.Node] = append(out[l.Node], subnetsOf(l.LocalIP)...)
		}
		if l.PeerNode != "" && l.PeerNode != externalNodeName && was[key{l.PeerNode, l.PeerIntf, uidOf(l)}] {
			out[l.PeerNode] = append(out[l.PeerNode], subnetsOf(l.PeerIP)...)
		}
	}
	return out
}

// RemovedLinkEnds lists what a change took away, by pod: the ends of a link in
// before that are not in after. An end counts with its link's uid, so a link
// taken out and put back under the same name is one removed, as the daemon
// makes its interface again from nothing.
func RemovedLinkEnds(before, after []types.LinkSpec) map[string]*RemovedEnds {
	type end struct {
		pod, intf string
		uid       int
		addrs     types.Addrs
	}
	ends := func(l types.LinkSpec) []end {
		uid := 0
		if l.UID != nil {
			uid = *l.UID
		}
		var out []end
		for _, e := range []end{{l.Node, l.LocalIntf, uid, l.LocalIP}, {l.PeerNode, l.PeerIntf, uid, l.PeerIP}} {
			if e.pod != "" && e.pod != externalNodeName && e.intf != "" {
				out = append(out, e)
			}
		}
		return out
	}
	type key struct {
		pod, intf string
		uid       int
	}
	kept := map[key]bool{}
	for _, l := range after {
		for _, e := range ends(l) {
			kept[key{e.pod, e.intf, e.uid}] = true
		}
	}
	removed := map[string]*RemovedEnds{}
	for _, l := range before {
		for _, e := range ends(l) {
			if kept[key{e.pod, e.intf, e.uid}] {
				continue
			}
			if removed[e.pod] == nil {
				removed[e.pod] = &RemovedEnds{Ifaces: map[string]bool{}}
			}
			removed[e.pod].Ifaces[e.intf] = true
			removed[e.pod].Subnets = append(removed[e.pod].Subnets, subnetsOf(e.addrs)...)
		}
	}
	return removed
}

// routeLost says whether a route operation went with the interfaces: its
// gateway sat on a subnet the pod lost and none it still has. The kernel drops
// a route with its interface and a replay of it would fail, but a route
// through an address of the link attributes, which this does not see, may be
// kept by mistake, never dropped by mistake.
func routeLost(action types.ActionEntry, gone *RemovedEnds, remaining []netip.Prefix) bool {
	if !routeActions[action.Type] || action.Gateway == "" {
		return false
	}
	gw, err := netip.ParseAddr(strings.TrimSpace(action.Gateway))
	if err != nil {
		return false
	}
	in := func(prefixes []netip.Prefix) bool {
		for _, p := range prefixes {
			if p.Contains(gw) {
				return true
			}
		}
		return false
	}
	return in(gone.Subnets) && !in(remaining)
}

// Forgotten is one operation a pod lost with its links: dropped whole, or kept
// without the interfaces in Cut.
type Forgotten struct {
	Action types.ActionEntry
	Cut    []string
}

func (f Forgotten) String() string {
	a := f.Action
	name := a.Type
	if a.Bridge != "" {
		name += " " + a.Bridge
	}
	switch {
	case len(f.Cut) > 0:
		return strings.Join(f.Cut, ", ") + " in " + name
	case a.Iface != "":
		return name + " on " + a.Iface
	case a.Gateway != "" && a.DstCIDR != "":
		return name + " " + a.DstCIDR + " via " + a.Gateway
	case a.Gateway != "":
		return name + " via " + a.Gateway
	}
	return name
}

// forgottenShown caps how many operations a warning names.
const forgottenShown = 4

func forgottenDetail(list []Forgotten) string {
	names := make([]string, 0, forgottenShown)
	for _, f := range list {
		if len(names) == forgottenShown {
			break
		}
		names = append(names, f.String())
	}
	text := strings.Join(names, ", ")
	if more := len(list) - len(names); more > 0 {
		text += fmt.Sprintf(" and %d more", more)
	}
	return "Went with the removed link: " + text + ". Configure it again if still wanted."
}

// forgets says what a removal does to one operation: nothing, drop it, or cut
// the interfaces that went from its list.
func forgets(action types.ActionEntry, gone *RemovedEnds, remaining []netip.Prefix) (drop bool, cut []string) {
	if (action.Iface != "" && gone.Ifaces[action.Iface]) || routeLost(action, gone, remaining) {
		return true, nil
	}
	for _, i := range action.Ifaces {
		if gone.Ifaces[i] {
			cut = append(cut, i)
		}
	}
	return len(cut) > 0 && len(cut) == len(action.Ifaces), cut
}

// ForgetRemoved takes what is gone out of the history: an operation on one of
// the interfaces (iface) is dropped, a list of them (ifaces) is cut to what is
// left and dropped when nothing is, and a route that went with them is dropped.
// The rest keeps its place and its stamp. A pod that is recreated drops these
// when it replays and they fail; one that keeps running, as on the fork's path,
// has to be told, or the history would call applied what the kernel lost with
// the interface and a configure would be refused as a duplicate. It returns
// what it took out, in the history's order.
func (h *PodHistory) ForgetRemoved(gone *RemovedEnds, remaining []netip.Prefix) []Forgotten {
	var forgotten []Forgotten
	for _, op := range h.ops {
		action := opAction(op)
		drop, cut := forgets(action, gone, remaining)
		if drop {
			forgotten = append(forgotten, Forgotten{Action: action})
		} else if len(cut) > 0 {
			forgotten = append(forgotten, Forgotten{Action: action, Cut: cut})
		}
	}
	if len(forgotten) == 0 {
		return nil
	}
	h.change(func(ops []PersistedDriverOperation) []PersistedDriverOperation {
		out := make([]PersistedDriverOperation, 0, len(ops))
		for _, op := range ops {
			action := opAction(op)
			drop, cut := forgets(action, gone, remaining)
			if drop {
				continue
			}
			if len(cut) > 0 {
				left := make([]string, 0, len(action.Ifaces)-len(cut))
				for _, i := range action.Ifaces {
					if !gone.Ifaces[i] {
						left = append(left, i)
					}
				}
				op.Action.Type = action.Type
				op.Action.Ifaces = left
			}
			out = append(out, op)
		}
		return out
	})
	return forgotten
}

// ForgetRemovedInterfaces applies ForgetRemoved to each pod that lost links,
// with the subnets the pod keeps, and returns one warning per pod naming what
// went. A failure is logged: the link is gone either way.
func ForgetRemovedInterfaces(namespace string, removed map[string]*RemovedEnds, remaining map[string][]netip.Prefix) []types.Warning {
	pods := make([]string, 0, len(removed))
	for pod := range removed {
		pods = append(pods, pod)
	}
	sort.Strings(pods)
	var warnings []types.Warning
	for _, pod := range pods {
		h, err := OpenPodHistory(namespace, pod)
		if err != nil {
			log.Printf("⚠️ Could not open the history of %s/%s to forget its removed interfaces: %v", namespace, pod, err)
			continue
		}
		forgotten := h.ForgetRemoved(removed[pod], remaining[pod])
		if len(forgotten) == 0 {
			continue
		}
		if err := h.Commit(); err != nil {
			log.Printf("⚠️ Could not write the history of %s/%s after removing its interfaces: %v", namespace, pod, err)
			continue
		}
		warnings = append(warnings, types.Warning{Node: pod, Kind: "operations_forgotten", Detail: forgottenDetail(forgotten)})
		log.Printf("🧹 Forgot %d persisted operation(s) of %s/%s on what the removed links carried", len(forgotten), namespace, pod)
	}
	return warnings
}
