package helpers

import (
	"fmt"

	"kubendt/types"
)

// ExpandLinkShaping turns the links section of a configure request into
// per-pod actions. The peer end of each link comes from the Topology CRDs,
// so a link is named by one end and the shaping lands on the interface of
// each end, where history and replay already work per interface.
func ExpandLinkShaping(namespace string, links []types.LinkShaping) ([]types.PodConfig, error) {
	if len(links) == 0 {
		return nil, nil
	}
	topo, err := BuildLinksFromTopologyCRDs(namespace)
	if err != nil {
		return nil, err
	}
	return expandLinkShaping(links, topo, func(ref string) (string, error) {
		return ResolvePodReference(namespace, ref)
	})
}

// expandLinkShaping is the pure part: topo is the namespace's links with pod
// names on both ends ("external" for a host uplink), resolve maps a pod
// reference to a pod name.
func expandLinkShaping(links []types.LinkShaping, topo []types.LinkSpec, resolve func(string) (string, error)) ([]types.PodConfig, error) {
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

	for i, l := range links {
		where := fmt.Sprintf("links[%d]", i)
		if l.Node == "" || l.LocalIntf == "" {
			return nil, fmt.Errorf("%s: node and localIntf are required", where)
		}
		pod, err := resolve(l.Node)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		peerPod, peerIntf, found := linkPeer(topo, pod, l.LocalIntf)
		if !found {
			return nil, fmt.Errorf("%s: no link on %s:%s in the topology", where, pod, l.LocalIntf)
		}
		if l.PeerNode != "" {
			want := l.PeerNode
			if want != externalNodeName {
				if want, err = resolve(l.PeerNode); err != nil {
					return nil, fmt.Errorf("%s: %w", where, err)
				}
			}
			if want != peerPod {
				return nil, fmt.Errorf("%s: %s:%s is linked to %s, not to %s", where, pod, l.LocalIntf, peerPod, l.PeerNode)
			}
		}
		if l.PeerIntf != "" && l.PeerIntf != peerIntf {
			return nil, fmt.Errorf("%s: %s:%s is linked to %s:%s, not to %s", where, pod, l.LocalIntf, peerPod, peerIntf, l.PeerIntf)
		}
		peerIsPod := peerPod != externalNodeName

		switch {
		case l.Remove:
			if l.Shaping != nil || l.Reverse != nil || l.Symmetric {
				return nil, fmt.Errorf("%s: remove takes no shaping", where)
			}
			add(pod, types.ActionEntry{Type: "del_qdisc", Iface: l.LocalIntf})
			if peerIsPod {
				add(peerPod, types.ActionEntry{Type: "del_qdisc", Iface: peerIntf})
			}
		case l.Shaping == nil && l.Reverse == nil:
			// An entry may carry only the link's MTU, recorded apart by RecordLinkMTUs.
			if l.MTU == nil {
				return nil, fmt.Errorf("%s: needs shaping, reverse, remove or mtu", where)
			}
		default:
			if (l.Symmetric || l.Reverse != nil) && !peerIsPod {
				return nil, fmt.Errorf("%s: the peer of %s:%s is the external network and cannot be shaped, drop symmetric and reverse", where, pod, l.LocalIntf)
			}
			if l.Symmetric && l.Reverse != nil {
				return nil, fmt.Errorf("%s: symmetric and reverse are exclusive", where)
			}
			if l.Shaping != nil {
				add(pod, types.ActionEntry{Type: "add_qdisc", Iface: l.LocalIntf, TCParams: l.Shaping})
			}
			switch {
			case l.Reverse != nil:
				add(peerPod, types.ActionEntry{Type: "add_qdisc", Iface: peerIntf, TCParams: l.Reverse})
			case l.Symmetric:
				add(peerPod, types.ActionEntry{Type: "add_qdisc", Iface: peerIntf, TCParams: l.Shaping})
			}
		}
	}
	return targets, nil
}

// RecordLinkMTUs keeps the MTU a configure request asks for its links as a
// property of each link, so the MTU pass and every restart apply it, and
// returns the pods whose links changed so the pass can run on just those.
func RecordLinkMTUs(namespace string, links []types.LinkShaping) (map[string]bool, error) {
	pods := map[string]bool{}
	var topo []types.LinkSpec
	for i, l := range links {
		if l.MTU == nil {
			continue
		}
		where := fmt.Sprintf("links[%d]", i)
		if topo == nil {
			var err error
			if topo, err = BuildLinksFromTopologyCRDs(namespace); err != nil {
				return nil, err
			}
		}
		pod, err := ResolvePodReference(namespace, l.Node)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		peerPod, peerIntf, found := linkPeer(topo, pod, l.LocalIntf)
		if !found {
			return nil, fmt.Errorf("%s: no link on %s:%s in the topology", where, pod, l.LocalIntf)
		}
		if peerPod == externalNodeName {
			return nil, fmt.Errorf("%s: %s:%s goes to the external network, whose MTU is the host's", where, pod, l.LocalIntf)
		}
		localIP, peerIP := linkIPs(topo, pod, l.LocalIntf)
		if err := ValidateLinkMTU(*l.MTU, localIP, peerIP); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if err := SetLinkMTU(namespace, pod, l.LocalIntf, peerPod, peerIntf, *l.MTU); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		pods[pod], pods[peerPod] = true, true
	}
	return pods, nil
}

// linkIPs returns the addresses of both ends of the link pod:iface terminates.
func linkIPs(topo []types.LinkSpec, pod, iface string) (string, string) {
	for _, t := range topo {
		if t.Node == pod && t.LocalIntf == iface {
			return t.LocalIP, t.PeerIP
		}
		if t.PeerNode == pod && t.PeerIntf == iface {
			return t.PeerIP, t.LocalIP
		}
	}
	return "", ""
}

// linkPeer finds the other end of pod:iface in the topology, whichever side
// the CRD listed it on.
func linkPeer(topo []types.LinkSpec, pod, iface string) (peerPod, peerIntf string, found bool) {
	for _, t := range topo {
		if t.Node == pod && t.LocalIntf == iface {
			return t.PeerNode, t.PeerIntf, true
		}
		if t.PeerNode == pod && t.PeerIntf == iface {
			return t.Node, t.LocalIntf, true
		}
	}
	return "", "", false
}
