package helpers

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"kubendt/kubeclient"
	"kubendt/types"
)

// Link attributes are the properties a topology declares on a link and the
// platform imposes after wiring it: a netem with the delay, jitter, loss and
// bandwidth, the MTU, and the addresses beyond the first on each end. They
// are kept in their own table so GET /network/get-network can echo them. The
// shaping is applied as ordinary operations of each end, so a restart
// replays it; the MTU and the extra addresses are applied by the MTU pass,
// which runs after every wiring anyway (Meshnet wires one address per end).

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

// Extra addresses travel as a JSON array in a text column, "" for none.
func encodeAddrs(addrs []string) string {
	if len(addrs) == 0 {
		return ""
	}
	raw, _ := json.Marshal(addrs)
	return string(raw)
}

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

	var warnings []types.Warning
	plans := PlanActions(namespace, podName, driver, nil, actions)
	outcomes, _ := RunPlannedActions(namespace, podName, plans, RunOptions{})
	for i, o := range outcomes {
		if o.Err != nil {
			warnings = append(warnings, warn(plans[i].Action, o.Err))
			continue
		}
		if err := SaveDriverOperation(namespace, podName, driverType, plans[i].Action); err != nil {
			warnings = append(warnings, warn(plans[i].Action, fmt.Errorf("applied but not persisted: %w", err)))
		}
	}
	for _, w := range warnings {
		log.Printf("⚠️ Link attributes: %s: %s", w.Node, w.Detail)
	}
	return warnings
}

// Storage. One row per declared link, keyed by the end the topology named.

func SaveLinkAttributes(namespace string, links []types.LinkSpec, nodes []types.NodeSpec) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	for _, l := range links {
		a := attrsOf(l)
		node, peer := ResolvePodNameFromLink(l.Node, nodes), l.PeerNode
		if peer != externalNodeName {
			peer = ResolvePodNameFromLink(l.PeerNode, nodes)
		}
		if a.empty() {
			if _, err := DB.Exec(`DELETE FROM link_attributes WHERE cluster_id = ? AND namespace = ? AND node = ? AND intf = ?`, clusterID, namespace, node, l.LocalIntf); err != nil {
				return err
			}
			continue
		}
		if _, err := DB.Exec(`INSERT OR REPLACE INTO link_attributes (cluster_id, namespace, node, intf, peer_node, peer_intf, delay, jitter, loss, bandwidth, mtu, local_addresses, peer_addresses)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			clusterID, namespace, node, l.LocalIntf, peer, l.PeerIntf, a.Delay, a.Jitter, a.Loss, a.Bandwidth, a.MTU, encodeAddrs(a.LocalAddrs), encodeAddrs(a.PeerAddrs)); err != nil {
			return fmt.Errorf("error saving link attributes for %s:%s: %w", node, l.LocalIntf, err)
		}
	}
	return nil
}

// LoadLinkAttributes returns the declared attributes keyed by "pod/intf" for
// both ends of every link, each seen from that end, so a lookup works
// whichever end a reader has.
func LoadLinkAttributes(namespace string) (map[string]LinkAttrs, error) {
	out := map[string]LinkAttrs{}
	if DB == nil {
		return out, nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return nil, err
	}
	rows, err := DB.Query(`SELECT node, intf, peer_node, peer_intf, delay, jitter, loss, bandwidth, mtu, local_addresses, peer_addresses FROM link_attributes WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var node, intf, peer, peerIntf, localAddrs, peerAddrs string
		var a LinkAttrs
		if err := rows.Scan(&node, &intf, &peer, &peerIntf, &a.Delay, &a.Jitter, &a.Loss, &a.Bandwidth, &a.MTU, &localAddrs, &peerAddrs); err != nil {
			return nil, err
		}
		a.LocalAddrs, a.PeerAddrs = decodeAddrs(localAddrs), decodeAddrs(peerAddrs)
		out[node+"/"+intf] = a
		if peer != externalNodeName {
			out[peer+"/"+peerIntf] = a.swapped()
		}
	}
	return out, rows.Err()
}

// DeleteLinkAttributesForLinks drops the rows of links removed by a modify,
// whichever end named them.
func DeleteLinkAttributesForLinks(namespace string, links []types.LinkSpec, nodes []types.NodeSpec) error {
	if DB == nil {
		return nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	for _, l := range links {
		node, peer := ResolvePodNameFromLink(l.Node, nodes), l.PeerNode
		if peer != externalNodeName {
			peer = ResolvePodNameFromLink(l.PeerNode, nodes)
		}
		if _, err := DB.Exec(`DELETE FROM link_attributes WHERE cluster_id = ? AND namespace = ? AND ((node = ? AND intf = ?) OR (node = ? AND intf = ?))`,
			clusterID, namespace, node, l.LocalIntf, peer, l.PeerIntf); err != nil {
			return err
		}
	}
	return nil
}

// DeleteLinkAttributesForPods drops the rows of every link a deleted pod was on.
func DeleteLinkAttributesForPods(namespace string, pods map[string]struct{}) error {
	if DB == nil {
		return nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	for pod := range pods {
		if _, err := DB.Exec(`DELETE FROM link_attributes WHERE cluster_id = ? AND namespace = ? AND (node = ? OR peer_node = ?)`, clusterID, namespace, pod, pod); err != nil {
			return err
		}
	}
	return nil
}

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
// section of a configure request, on the row of whichever end the topology
// named, so the next MTU pass and every restart apply it. 0 clears it.
func SetLinkMTU(namespace, pod, iface, peerPod, peerIntf string, mtu int) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	res, err := DB.Exec(`UPDATE link_attributes SET mtu = ? WHERE cluster_id = ? AND namespace = ? AND ((node = ? AND intf = ?) OR (node = ? AND intf = ?))`,
		mtu, clusterID, namespace, pod, iface, peerPod, peerIntf)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 || mtu == 0 {
		return nil
	}
	_, err = DB.Exec(`INSERT INTO link_attributes (cluster_id, namespace, node, intf, peer_node, peer_intf, mtu) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		clusterID, namespace, pod, iface, peerPod, peerIntf, mtu)
	return err
}
