package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"kubendt/kubeclient"
	"kubendt/types"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Link attributes are the properties a topology declares on a link and the
// platform imposes after wiring it: a netem with the delay, jitter, loss and
// bandwidth, the MTU, and the addresses beyond the first on each end. They
// live on the Topology object of the pod the topology named, so they die
// with it and any backend sees them, and GET /network/get-network echoes
// them. The shaping is applied as ordinary operations of each end, so a
// restart replays it; the MTU and the extra addresses are applied by the MTU
// pass, which runs after every wiring anyway (Meshnet wires one address per
// end).

// LinkAttrs are the declared properties of one link. LocalAddrs are the
// extra addresses of the end the lookup key names, PeerAddrs the other
// end's.
type LinkAttrs struct {
	Delay      string
	Jitter     string
	Loss       string
	Bandwidth  string
	MTU        int
	LocalAddrs []string
	PeerAddrs  []string
}

func attrsOf(l types.LinkSpec) LinkAttrs {
	return LinkAttrs{Delay: l.Delay, Jitter: l.Jitter, Loss: l.Loss, Bandwidth: l.Bandwidth, MTU: l.MTU,
		LocalAddrs: l.LocalIP.Extra(), PeerAddrs: l.PeerIP.Extra()}
}

func (a LinkAttrs) empty() bool {
	return !a.hasShaping() && a.MTU == 0 && len(a.LocalAddrs) == 0 && len(a.PeerAddrs) == 0
}

// swapped is the same link seen from its other end.
func (a LinkAttrs) swapped() LinkAttrs {
	a.LocalAddrs, a.PeerAddrs = a.PeerAddrs, a.LocalAddrs
	return a
}

// decodeAddrs reads the JSON array an older backend kept in a text column,
// "" for none.
func decodeAddrs(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		log.Printf("⚠️ Link attributes: unreadable address list %q", raw)
		return nil
	}
	return out
}

func (a LinkAttrs) hasShaping() bool {
	return a.Delay != "" || a.Jitter != "" || a.Loss != "" || a.Bandwidth != ""
}

// shaping is the netem the delay, jitter, loss and bandwidth make.
func (a LinkAttrs) shaping() *types.TCParamEntry {
	return &types.TCParamEntry{Qdisc: "netem", Delay: a.Delay, Jitter: a.Jitter, Loss: a.Loss, Rate: a.Bandwidth}
}

// ValidateLinkAttributes checks the shaping declared on links with the rules
// of a shaping request, and the MTU, before anything is created.
func ValidateLinkAttributes(links []types.LinkSpec) error {
	for i, l := range links {
		a := attrsOf(l)
		if a.hasShaping() {
			if _, _, err := buildTCParams(a.shaping()); err != nil {
				msg := strings.ReplaceAll(err.Error(), "tcparams.rate", "bandwidth")
				msg = strings.ReplaceAll(msg, "tcparams.", "")
				return fmt.Errorf("link[%d]: %s", i, msg)
			}
		}
		if err := ValidateLinkMTU(a.MTU, append(append([]string{}, l.LocalIP...), l.PeerIP...)); err != nil {
			return fmt.Errorf("link[%d]: %w", i, err)
		}
	}
	return nil
}

// ValidateLinkMTU checks an MTU asked for one link: 0 means the platform's
// value; otherwise within the kernel's range, at least 1280 when any address
// of either end is IPv6, and never above what the operator said the cluster
// carries, since the link would just not work.
func ValidateLinkMTU(mtu int, addrs []string) error {
	if mtu == 0 {
		return nil
	}
	if mtu < MinLinkMTU || mtu > MaxLinkMTU {
		return fmt.Errorf("mtu %d is not between %d and %d", mtu, MinLinkMTU, MaxLinkMTU)
	}
	for _, a := range addrs {
		if ipFamily(a) == 6 && mtu < 1280 {
			return fmt.Errorf("mtu %d is below 1280, the least IPv6 needs", mtu)
		}
	}
	platform, _, err := PlatformLinkMTU()
	if err != nil {
		return err
	}
	if mtu > platform {
		return fmt.Errorf("mtu %d is above %s (%d)", mtu, LinkMTUEnv, platform)
	}
	return nil
}

// LinkAttributeActions turns the declared attributes into per-pod actions:
// the netem on both ends, on the node's end only when the peer is the
// external network. Pods come out in link order.
func LinkAttributeActions(links []types.LinkSpec, nodes []types.NodeSpec) []types.PodConfig {
	var targets []types.PodConfig
	add := func(pod string, action types.ActionEntry) {
		for i := range targets {
			if targets[i].Pod == pod {
				targets[i].Actions = append(targets[i].Actions, action)
				return
			}
		}
		targets = append(targets, types.PodConfig{Pod: pod, Actions: []types.ActionEntry{action}})
	}
	for _, l := range links {
		a := attrsOf(l)
		if !a.hasShaping() {
			continue
		}
		ends := []struct{ pod, iface string }{{ResolvePodNameFromLink(l.Node, nodes), l.LocalIntf}}
		if l.PeerNode != externalNodeName {
			ends = append(ends, struct{ pod, iface string }{ResolvePodNameFromLink(l.PeerNode, nodes), l.PeerIntf})
		}
		for _, e := range ends {
			add(e.pod, types.ActionEntry{Type: "add_qdisc", Iface: e.iface, TCParams: a.shaping()})
		}
	}
	return targets
}

// ApplyLinkAttributes applies the declared attributes once the links are
// wired. A failure is a warning on the response, the topology is up anyway.
func ApplyLinkAttributes(namespace string, links []types.LinkSpec, nodes []types.NodeSpec) []types.Warning {
	var warnings []types.Warning
	for _, t := range LinkAttributeActions(links, nodes) {
		warnings = append(warnings, ApplyPodActions(namespace, t.Pod, t.Actions)...)
	}
	return warnings
}

// ApplyPodActions runs configure actions on one pod outside the configure
// endpoint and persists each one that succeeds, so a restart replays it.
// Returns one warning per action that could not be applied.
func ApplyPodActions(namespace, podName string, actions []types.ActionEntry) []types.Warning {
	warn := func(action types.ActionEntry, err error) types.Warning {
		return types.Warning{Node: podName, Kind: "link_attribute_failed", Detail: fmt.Sprintf("%s on %s: %v", action.Type, action.Iface, err)}
	}
	driver, err := GetDriverForPod(namespace, podName)
	if err != nil {
		var out []types.Warning
		for _, a := range actions {
			out = append(out, warn(a, err))
		}
		return out
	}
	driverType := fmt.Sprintf("%T", driver)

	history, err := OpenPodHistory(namespace, podName)
	if err != nil {
		var out []types.Warning
		for _, a := range actions {
			out = append(out, warn(a, err))
		}
		return out
	}

	var warnings []types.Warning
	var saved []types.ActionEntry
	plans := PlanActions(namespace, podName, driver, nil, actions)
	outcomes, _ := RunPlannedActions(namespace, podName, plans, RunOptions{})
	for i, o := range outcomes {
		if o.Err != nil {
			warnings = append(warnings, warn(plans[i].Action, o.Err))
			continue
		}
		history.Save(driverType, plans[i].Action)
		saved = append(saved, plans[i].Action)
	}
	if err := history.Commit(); err != nil {
		for _, a := range saved {
			warnings = append(warnings, warn(a, fmt.Errorf("applied but not persisted: %w", err)))
		}
	}
	for _, w := range warnings {
		log.Printf("⚠️ Link attributes: %s: %s", w.Node, w.Detail)
	}
	return warnings
}

// Storage. Each pod's Topology object carries, in one annotation, the
// attributes of the links the topology named from its side, keyed by
// interface. The rows an older backend left in SQLite are read once into the
// annotations and dropped.

const LinkAttrsAnnotation = "kubendt/link-attrs"

type linkAttrEntry struct {
	Peer       string   `json:"peer"`
	PeerIntf   string   `json:"peer_intf"`
	Delay      string   `json:"delay,omitempty"`
	Jitter     string   `json:"jitter,omitempty"`
	Loss       string   `json:"loss,omitempty"`
	Bandwidth  string   `json:"bandwidth,omitempty"`
	MTU        int      `json:"mtu,omitempty"`
	LocalAddrs []string `json:"addrs,omitempty"`
	PeerAddrs  []string `json:"peer_addrs,omitempty"`
}

func (e linkAttrEntry) attrs() LinkAttrs {
	return LinkAttrs{Delay: e.Delay, Jitter: e.Jitter, Loss: e.Loss, Bandwidth: e.Bandwidth, MTU: e.MTU, LocalAddrs: e.LocalAddrs, PeerAddrs: e.PeerAddrs}
}

func entryOf(a LinkAttrs, peer, peerIntf string) linkAttrEntry {
	return linkAttrEntry{Peer: peer, PeerIntf: peerIntf, Delay: a.Delay, Jitter: a.Jitter, Loss: a.Loss, Bandwidth: a.Bandwidth, MTU: a.MTU, LocalAddrs: a.LocalAddrs, PeerAddrs: a.PeerAddrs}
}

func linkAttrEntries(obj *unstructured.Unstructured) map[string]linkAttrEntry {
	entries := map[string]linkAttrEntry{}
	raw := obj.GetAnnotations()[LinkAttrsAnnotation]
	if raw == "" {
		return entries
	}
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		log.Printf("⚠️ Link attributes: unreadable annotation on %s/%s, taken as none: %v", obj.GetNamespace(), obj.GetName(), err)
		return map[string]linkAttrEntry{}
	}
	return entries
}

func setLinkAttrEntries(obj *unstructured.Unstructured, entries map[string]linkAttrEntry) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	if len(entries) == 0 {
		delete(annotations, LinkAttrsAnnotation)
	} else {
		raw, _ := json.Marshal(entries)
		annotations[LinkAttrsAnnotation] = string(raw)
	}
	obj.SetAnnotations(annotations)
}

// updateLinkAttrs rewrites a pod's entries under its Topology's
// resourceVersion, retrying when something else wrote in between. A pod
// without a Topology has no links to carry attributes.
func updateLinkAttrs(namespace, pod string, mutate func(map[string]linkAttrEntry) bool) error {
	for attempt := 0; attempt < 3; attempt++ {
		obj, err := topologyObjects(namespace).Get(context.TODO(), pod, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("could not read the Topology of %s/%s: %w", namespace, pod, err)
		}
		entries := linkAttrEntries(obj)
		if !mutate(entries) {
			return nil
		}
		setLinkAttrEntries(obj, entries)
		if _, err := topologyObjects(namespace).Update(context.TODO(), obj, metav1.UpdateOptions{}); err == nil {
			return nil
		} else if !apierrors.IsConflict(err) {
			return fmt.Errorf("could not write the link attributes of %s/%s: %w", namespace, pod, err)
		}
	}
	return fmt.Errorf("the Topology of %s/%s kept changing while its link attributes were written", namespace, pod)
}

func peerPodOf(l types.LinkSpec, nodes []types.NodeSpec) string {
	if l.PeerNode == externalNodeName {
		return externalNodeName
	}
	return ResolvePodNameFromLink(l.PeerNode, nodes)
}

func SaveLinkAttributes(namespace string, links []types.LinkSpec, nodes []types.NodeSpec) error {
	if err := migrateLinkAttributes(namespace); err != nil {
		return err
	}
	byPod := map[string][]types.LinkSpec{}
	for _, l := range links {
		node := ResolvePodNameFromLink(l.Node, nodes)
		byPod[node] = append(byPod[node], l)
	}
	for pod, podLinks := range byPod {
		err := updateLinkAttrs(namespace, pod, func(entries map[string]linkAttrEntry) bool {
			for _, l := range podLinks {
				if a := attrsOf(l); a.empty() {
					delete(entries, l.LocalIntf)
				} else {
					entries[l.LocalIntf] = entryOf(a, peerPodOf(l, nodes), l.PeerIntf)
				}
			}
			return true
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// LoadLinkAttributes returns the declared attributes keyed by "pod/intf" for
// both ends of every link, each seen from that end, so a lookup works
// whichever end a reader has.
func LoadLinkAttributes(namespace string) (map[string]LinkAttrs, error) {
	if err := migrateLinkAttributes(namespace); err != nil {
		return nil, err
	}
	list, err := topologyObjects(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not list the Topology objects of %s: %w", namespace, err)
	}
	out := map[string]LinkAttrs{}
	for i := range list.Items {
		pod := list.Items[i].GetName()
		for intf, e := range linkAttrEntries(&list.Items[i]) {
			a := e.attrs()
			out[pod+"/"+intf] = a
			if e.Peer != externalNodeName && e.Peer != "" {
				out[e.Peer+"/"+e.PeerIntf] = a.swapped()
			}
		}
	}
	return out, nil
}

// DeleteLinkAttributesForLinks drops the entries of links removed by a
// modify, whichever end named them.
func DeleteLinkAttributesForLinks(namespace string, links []types.LinkSpec, nodes []types.NodeSpec) error {
	if err := migrateLinkAttributes(namespace); err != nil {
		return err
	}
	drop := map[string]map[string]bool{}
	for _, l := range links {
		node, peer := ResolvePodNameFromLink(l.Node, nodes), peerPodOf(l, nodes)
		if drop[node] == nil {
			drop[node] = map[string]bool{}
		}
		drop[node][l.LocalIntf] = true
		if peer != externalNodeName {
			if drop[peer] == nil {
				drop[peer] = map[string]bool{}
			}
			drop[peer][l.PeerIntf] = true
		}
	}
	for pod, intfs := range drop {
		err := updateLinkAttrs(namespace, pod, func(entries map[string]linkAttrEntry) bool {
			changed := false
			for intf := range intfs {
				if _, ok := entries[intf]; ok {
					delete(entries, intf)
					changed = true
				}
			}
			return changed
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteLinkAttributesForPods drops, on the pods that stay, the entries of
// links that went to a deleted pod; the deleted pods' own entries go with
// their Topology objects.
func DeleteLinkAttributesForPods(namespace string, pods map[string]struct{}) error {
	if err := migrateLinkAttributes(namespace); err != nil {
		return err
	}
	list, err := topologyObjects(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("could not list the Topology objects of %s: %w", namespace, err)
	}
	for i := range list.Items {
		pod := list.Items[i].GetName()
		if _, gone := pods[pod]; gone {
			continue
		}
		err := updateLinkAttrs(namespace, pod, func(entries map[string]linkAttrEntry) bool {
			changed := false
			for intf, e := range entries {
				if _, gone := pods[e.Peer]; gone {
					delete(entries, intf)
					changed = true
				}
			}
			return changed
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteNamespaceLinkAttributes clears what an older backend left in SQLite
// for the namespace; the entries themselves die with the Topology objects.
func DeleteNamespaceLinkAttributes(namespace string) error {
	if DB == nil {
		return nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	_, err = DB.Exec(`DELETE FROM link_attributes WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace)
	return err
}

// FillLinkAttributes copies the declared attributes onto a link read back
// from the cluster (one address per end, Meshnet's), looked up by either of
// its ends.
func FillLinkAttributes(link *types.LinkSpec, podName, peerPod string, attrs map[string]LinkAttrs) {
	a, ok := attrs[podName+"/"+link.LocalIntf]
	if !ok && peerPod != externalNodeName {
		if a, ok = attrs[peerPod+"/"+link.PeerIntf]; ok {
			a = a.swapped()
		}
	}
	if !ok {
		return
	}
	link.Delay, link.Jitter, link.Loss, link.Bandwidth, link.MTU = a.Delay, a.Jitter, a.Loss, a.Bandwidth, a.MTU
	link.LocalIP = append(append(types.Addrs{}, link.LocalIP...), a.LocalAddrs...)
	link.PeerIP = append(append(types.Addrs{}, link.PeerIP...), a.PeerAddrs...)
}

// SetLinkMTU records the MTU asked for a running link through the links
// section of a configure request, on the entry of whichever end the topology
// named, so the next MTU pass and every restart apply it. 0 clears it.
func SetLinkMTU(namespace, pod, iface, peerPod, peerIntf string, mtu int) error {
	if err := migrateLinkAttributes(namespace); err != nil {
		return err
	}
	set := func(entries map[string]linkAttrEntry, intf string) bool {
		e, ok := entries[intf]
		if !ok {
			return false
		}
		e.MTU = mtu
		if e.attrs().empty() {
			delete(entries, intf)
		} else {
			entries[intf] = e
		}
		return true
	}
	found := false
	err := updateLinkAttrs(namespace, pod, func(entries map[string]linkAttrEntry) bool {
		found = set(entries, iface)
		return found
	})
	if err != nil || found {
		return err
	}
	err = updateLinkAttrs(namespace, peerPod, func(entries map[string]linkAttrEntry) bool {
		found = set(entries, peerIntf)
		return found
	})
	if err != nil || found || mtu == 0 {
		return err
	}
	return updateLinkAttrs(namespace, pod, func(entries map[string]linkAttrEntry) bool {
		entries[iface] = linkAttrEntry{Peer: peerPod, PeerIntf: peerIntf, MTU: mtu}
		return true
	})
}

// migrateLinkAttributes moves the rows an older backend kept for the
// namespace onto its Topology objects and drops them, the first time any of
// them is touched. A namespace with no rows costs one query.
func migrateLinkAttributes(namespace string) error {
	if DB == nil {
		return nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	rows, err := DB.Query(`SELECT node, intf, peer_node, peer_intf, delay, jitter, loss, bandwidth, mtu, local_addresses, peer_addresses FROM link_attributes WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace)
	if err != nil {
		return err
	}
	defer rows.Close()
	byPod := map[string]map[string]linkAttrEntry{}
	for rows.Next() {
		var node, intf, localAddrs, peerAddrs string
		var e linkAttrEntry
		if err := rows.Scan(&node, &intf, &e.Peer, &e.PeerIntf, &e.Delay, &e.Jitter, &e.Loss, &e.Bandwidth, &e.MTU, &localAddrs, &peerAddrs); err != nil {
			return err
		}
		e.LocalAddrs, e.PeerAddrs = decodeAddrs(localAddrs), decodeAddrs(peerAddrs)
		if byPod[node] == nil {
			byPod[node] = map[string]linkAttrEntry{}
		}
		byPod[node][intf] = e
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(byPod) == 0 {
		return nil
	}
	for pod, entries := range byPod {
		err := updateLinkAttrs(namespace, pod, func(current map[string]linkAttrEntry) bool {
			for intf, e := range entries {
				if _, ok := current[intf]; !ok {
					current[intf] = e
				}
			}
			return true
		})
		if err != nil {
			return fmt.Errorf("could not move the link attributes of %s to its Topology: %w", pod, err)
		}
	}
	if _, err := DB.Exec(`DELETE FROM link_attributes WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace); err != nil {
		return err
	}
	log.Printf("ℹ️ Link attributes of namespace '%s' moved from the database to its Topology objects", namespace)
	return nil
}
