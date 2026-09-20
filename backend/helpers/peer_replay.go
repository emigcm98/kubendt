package helpers

import (
	"context"
	"log"
	"sort"

	"kubendt/executor"
	"kubendt/kubeclient"
	"kubendt/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PeerReplayStats summarizes the re-apply of neighbour state after pods were
// recreated. QemuRewired counts guest-VM neighbours whose TC redirect between
// the recreated interface and its tap was refreshed instead of replayed, their
// configuration lives inside the guest.
type PeerReplayStats struct {
	Peers       int `json:"peers"`
	Reapplied   int `json:"reapplied"`
	Failed      int `json:"failed"`
	QemuRewired int `json:"qemu_rewired"`
}

// routeActions are re-applied on a neighbour even though they name no
// interface: the kernel drops every route through an interface when that
// interface disappears, and Meshnet only restores the CRD addresses.
var routeActions = map[string]bool{
	"set_default_route": true,
	"add_static_route":  true,
}

// ReapplyPeerInterfaceState re-runs, on every neighbour of the recreated pods,
// the persisted operations that touch the interface facing a recreated pod.
//
// Meshnet recreates that interface together with the pod: a veth dies with the
// old netns and the VXLAN device is removed and re-added on the next CNI ADD.
// Whatever a driver had put on it is gone, bridge membership, VLANs, qdiscs,
// addresses not declared in the Topology CRD, while the neighbour's own history
// still lists those operations as applied. Replaying the recreated pod alone
// leaves the link half configured, which is what a switch losing a bridge port
// after a router restart looks like.
//
// Only operations naming one of the recreated interfaces are re-run, in their
// original order, and nothing is pruned from history on failure: "File exists"
// is the expected outcome when Meshnet already restored an address from the CRD.
func ReapplyPeerInterfaceState(namespace string, recreated []string) PeerReplayStats {
	var stats PeerReplayStats
	if len(recreated) == 0 {
		return stats
	}
	recreatedSet := make(map[string]struct{}, len(recreated))
	for _, p := range recreated {
		recreatedSet[p] = struct{}{}
	}

	links, err := BuildLinksFromTopologyCRDs(namespace)
	if err != nil {
		log.Printf("⚠️ Peer replay: could not read links in %s: %v", namespace, err)
		return stats
	}
	ifacesByPeer := map[string]map[string]bool{}
	note := func(peer, iface string) {
		if peer == "" || peer == externalNodeName || iface == "" {
			return
		}
		if _, ok := recreatedSet[peer]; ok {
			return // gets a full replay of its own
		}
		if ifacesByPeer[peer] == nil {
			ifacesByPeer[peer] = map[string]bool{}
		}
		ifacesByPeer[peer][iface] = true
	}
	for _, l := range links {
		if _, ok := recreatedSet[l.Node]; ok {
			note(l.PeerNode, l.PeerIntf)
		}
		if _, ok := recreatedSet[l.PeerNode]; ok {
			note(l.Node, l.LocalIntf)
		}
	}
	if len(ifacesByPeer) == 0 {
		return stats
	}

	qemuPods := map[string]bool{}
	if podList, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{}); err == nil {
		for _, p := range podList.Items {
			if p.Labels["kubendt/runtime"] == "qemu" || p.Labels["kubendt/qemu"] == "true" {
				qemuPods[p.Name] = true
			}
		}
	}

	peers := make([]string, 0, len(ifacesByPeer))
	for p := range ifacesByPeer {
		peers = append(peers, p)
	}
	sort.Strings(peers)

	qemuPeers := 0
	for _, peer := range peers {
		if qemuPods[peer] {
			qemuPeers++
			continue
		}
		reapplied, failed := reapplyOpsOnIfaces(namespace, peer, ifacesByPeer[peer])
		if reapplied+failed > 0 {
			stats.Peers++
		}
		stats.Reapplied += reapplied
		stats.Failed += failed
	}

	// A guest VM keeps its configuration inside the guest, but the TC
	// redirect between the recreated pod interface and the guest's tap still
	// points at the dead ifindex until it is rewired.
	if qemuPeers > 0 {
		nodes, err := GetExistingNodes(namespace)
		if err != nil {
			log.Printf("⚠️ Peer replay: could not list nodes for the QEMU rewire: %v", err)
		} else if err := RewireQemuPeersAfterRestart(namespace, recreated, nodes); err != nil {
			log.Printf("⚠️ Peer replay: QEMU rewire: %v", err)
		} else {
			stats.QemuRewired = qemuPeers
		}
	}
	if stats.Peers > 0 || qemuPeers > 0 {
		log.Printf("🔁 Peer replay after recreating %v: %d peer(s), %d op(s) re-applied, %d failed, %d QEMU peer(s) rewired", recreated, stats.Peers, stats.Reapplied, stats.Failed, stats.QemuRewired)
	}
	return stats
}

// reapplyOpsOnIfaces re-executes the persisted operations of podName that
// reference any of ifaces. Failures are logged, never pruned.
func reapplyOpsOnIfaces(namespace, podName string, ifaces map[string]bool) (reapplied, failed int) {
	ops, err := ListDriverOperationsForPod(namespace, podName)
	if err != nil || len(ops) == 0 {
		return 0, 0
	}
	driver, err := GetDriverForPod(namespace, podName)
	if err != nil {
		log.Printf("⚠️ Peer replay: cannot resolve driver for %s/%s: %v", namespace, podName, err)
		return 0, 0
	}
	driverExec, driverExecName, err := executor.ResolveForDriver(driver)
	if err != nil {
		log.Printf("⚠️ Peer replay: cannot resolve executor for %s/%s: %v", namespace, podName, err)
		return 0, 0
	}

	for _, op := range ops {
		action := op.Action
		if action.Type == "" {
			action.Type = op.ActionType
		}
		if !actionTouchesIface(action, ifaces) || !ResolveActionFlags(action).Persist {
			continue
		}
		execName, commands, err := ResolveDriverExecutionPlanForPod(namespace, podName, driver, action)
		if err != nil || commands == nil {
			log.Printf("⚠️ Peer replay: op id=%d (%s) on %s/%s not re-applied: %v", op.ID, action.Type, namespace, podName, err)
			failed++
			continue
		}
		execInst := driverExec
		if execName == "" {
			execName = driverExecName
		} else if execName != driverExecName {
			override, getErr := executor.Get(execName)
			if getErr != nil {
				log.Printf("⚠️ Peer replay: op id=%d executor %q unavailable on %s/%s: %v", op.ID, execName, namespace, podName, getErr)
				failed++
				continue
			}
			execInst = override
		}
		if executor.BatchableExecutors[execName] {
			// Guest-VM transports keep their own config inside the guest.
			continue
		}
		ok := true
		for _, cmd := range executor.CommandsFromLegacyForExecutor(commands, execName) {
			if execErr := execInst.ExecCommand(podName, namespace, cmd); execErr != nil {
				log.Printf("⚠️ Peer replay: op id=%d (%s) on %s/%s failed (kept in history): %v", op.ID, action.Type, namespace, podName, execErr)
				ok = false
				break
			}
		}
		if ok {
			reapplied++
			log.Printf("✅ Peer replay: re-applied op id=%d (%s) on %s/%s", op.ID, action.Type, namespace, podName)
		} else {
			failed++
		}
	}
	return reapplied, failed
}

// actionTouchesIface says whether an action's effect lives on one of the given
// interfaces: it names it (iface), lists it as a member (ifaces, e.g.
// setup_bridge), or installs a route the kernel drops with the interface.
func actionTouchesIface(action types.ActionEntry, ifaces map[string]bool) bool {
	if ifaces[action.Iface] || routeActions[action.Type] {
		return true
	}
	for _, i := range action.Ifaces {
		if ifaces[i] {
			return true
		}
	}
	return false
}
