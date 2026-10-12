package helpers

import (
	"fmt"
	"log"
	"strings"

	"kubendt/kubeclient"
	"kubendt/types"
)

// Link attributes are the shaping a topology declares on a link and the
// platform imposes after wiring it: a netem with the delay, jitter, loss and
// bandwidth, symmetric on both ends. They are kept in their own table so
// GET /network/get-network can echo them, and applied as ordinary operations
// of each end, so a restart replays them.

// LinkAttrs are the declared properties of one link.
type LinkAttrs struct {
	Delay     string
	Jitter    string
	Loss      string
	Bandwidth string
}

func attrsOf(l types.LinkSpec) LinkAttrs {
	return LinkAttrs{Delay: l.Delay, Jitter: l.Jitter, Loss: l.Loss, Bandwidth: l.Bandwidth}
}

func (a LinkAttrs) empty() bool {
	return a.Delay == "" && a.Jitter == "" && a.Loss == "" && a.Bandwidth == ""
}

// shaping is the netem the delay, jitter, loss and bandwidth make.
func (a LinkAttrs) shaping() *types.TCParamEntry {
	return &types.TCParamEntry{Qdisc: "netem", Delay: a.Delay, Jitter: a.Jitter, Loss: a.Loss, Rate: a.Bandwidth}
}

// ValidateLinkAttributes checks the shaping declared on links with the rules
// of a shaping request, before anything is created.
func ValidateLinkAttributes(links []types.LinkSpec) error {
	for i, l := range links {
		a := attrsOf(l)
		if a.empty() {
			continue
		}
		if _, _, err := buildTCParams(a.shaping()); err != nil {
			msg := strings.ReplaceAll(err.Error(), "tcparams.rate", "bandwidth")
			msg = strings.ReplaceAll(msg, "tcparams.", "")
			return fmt.Errorf("link[%d]: %s", i, msg)
		}
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
		if a.empty() {
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
		if _, err := DB.Exec(`INSERT OR REPLACE INTO link_attributes (cluster_id, namespace, node, intf, peer_node, peer_intf, delay, jitter, loss, bandwidth)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			clusterID, namespace, node, l.LocalIntf, peer, l.PeerIntf, a.Delay, a.Jitter, a.Loss, a.Bandwidth); err != nil {
			return fmt.Errorf("error saving link attributes for %s:%s: %w", node, l.LocalIntf, err)
		}
	}
	return nil
}

// LoadLinkAttributes returns the declared attributes keyed by "pod/intf" for
// both ends of every link, so a lookup works whichever end a reader has.
func LoadLinkAttributes(namespace string) (map[string]LinkAttrs, error) {
	out := map[string]LinkAttrs{}
	if DB == nil {
		return out, nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return nil, err
	}
	rows, err := DB.Query(`SELECT node, intf, peer_node, peer_intf, delay, jitter, loss, bandwidth FROM link_attributes WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var node, intf, peer, peerIntf string
		var a LinkAttrs
		if err := rows.Scan(&node, &intf, &peer, &peerIntf, &a.Delay, &a.Jitter, &a.Loss, &a.Bandwidth); err != nil {
			return nil, err
		}
		out[node+"/"+intf] = a
		if peer != externalNodeName {
			out[peer+"/"+peerIntf] = a
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
// from the cluster, looked up by either of its ends.
func FillLinkAttributes(link *types.LinkSpec, podName, peerPod string, attrs map[string]LinkAttrs) {
	a, ok := attrs[podName+"/"+link.LocalIntf]
	if !ok && peerPod != externalNodeName {
		a, ok = attrs[peerPod+"/"+link.PeerIntf]
	}
	if !ok {
		return
	}
	link.Delay, link.Jitter, link.Loss, link.Bandwidth = a.Delay, a.Jitter, a.Loss, a.Bandwidth
}
