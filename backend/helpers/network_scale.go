package helpers

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"kubendt/kubeclient"
	"kubendt/types"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScalePlan: one scale op normalized against cluster state.
// CurrentReplicas comes from the live StatefulSet; NewReplicas is the absolute
// target from the user.
type ScalePlan struct {
	Node            types.NodeSpec
	CurrentReplicas int
	NewReplicas     int
}

// Direction returns -1 for scale-down, +1 for scale-up, 0 for no-op.
func (p ScalePlan) Direction() int {
	switch {
	case p.NewReplicas < p.CurrentReplicas:
		return -1
	case p.NewReplicas > p.CurrentReplicas:
		return 1
	default:
		return 0
	}
}

// BuildScalePlans validates the scale section vs existing nodes and the
// request's add/delete, rejecting:
//   - replicas < 1 (use delete.nodes for 0)
//   - unknown node, indexed pod name ("host-0"), or node also in add/delete
//
// No-op entries (NewReplicas == CurrentReplicas) are dropped.
func BuildScalePlans(
	scale []types.ScaleSpec,
	existingNodes []types.NodeSpec,
	addNodeNames map[string]struct{},
	deleteNodeNames map[string]struct{},
) ([]ScalePlan, error) {
	if len(scale) == 0 {
		return nil, nil
	}

	byName := make(map[string]types.NodeSpec, len(existingNodes))
	for _, n := range existingNodes {
		byName[n.Name] = n
	}

	seen := make(map[string]struct{}, len(scale))
	plans := make([]ScalePlan, 0, len(scale))

	for i, entry := range scale {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			return nil, fmt.Errorf("VALIDATION:scale[%d]: missing 'name'", i)
		}
		if _, _, ok := SplitIndexedPodName(name); ok {
			return nil, fmt.Errorf("VALIDATION:scale[%d]: 'name' must be a node base name (e.g. 'host'), not an indexed pod ('%s')", i, name)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("VALIDATION:scale: duplicate entry for node '%s'", name)
		}
		seen[name] = struct{}{}

		existing, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("VALIDATION:scale: node '%s' does not exist in the namespace", name)
		}
		if _, conflict := deleteNodeNames[name]; conflict {
			return nil, fmt.Errorf("VALIDATION:scale: node '%s' is also in delete.nodes; use one or the other", name)
		}
		if _, conflict := addNodeNames[name]; conflict {
			return nil, fmt.Errorf("VALIDATION:scale: node '%s' is also in add.nodes; a node cannot be both newly created and scaled in the same request", name)
		}

		if entry.Replicas < 1 {
			return nil, fmt.Errorf("VALIDATION:scale: node '%s' replicas must be >= 1 (to remove the node entirely use delete.nodes)", name)
		}
		if entry.Replicas > types.MaxReplicas {
			return nil, fmt.Errorf("VALIDATION:scale: node '%s' replicas %d exceeds the maximum (%d)", name, entry.Replicas, types.MaxReplicas)
		}

		current := existing.Replicas
		if current == 0 {
			current = 1
		}
		if entry.Replicas == current {
			log.Printf("ℹ️ Scale: node '%s' already at %d replicas. No-op, dropping entry", name, current)
			continue
		}

		plans = append(plans, ScalePlan{
			Node:            existing,
			CurrentReplicas: current,
			NewReplicas:     entry.Replicas,
		})
	}

	return plans, nil
}

// SplitScalePlans separates plans by direction for the two-phase apply.
func SplitScalePlans(plans []ScalePlan) (downs, ups []ScalePlan) {
	for _, p := range plans {
		switch p.Direction() {
		case -1:
			downs = append(downs, p)
		case 1:
			ups = append(ups, p)
		}
	}
	return downs, ups
}

// ApplyEffectiveReplicasToNodes returns a copy of allNodes with scaled nodes'
// Replicas overridden to NewReplicas, for downstream validators that need
// the post-scale view.
func ApplyEffectiveReplicasToNodes(allNodes []types.NodeSpec, plans []ScalePlan) []types.NodeSpec {
	if len(plans) == 0 {
		return allNodes
	}
	override := make(map[string]int, len(plans))
	for _, p := range plans {
		override[p.Node.Name] = p.NewReplicas
	}
	out := make([]types.NodeSpec, len(allNodes))
	copy(out, allNodes)
	for i := range out {
		if newRep, ok := override[out[i].Name]; ok {
			out[i].Replicas = newRep
		}
	}
	return out
}

// ApplyScaleDowns processes each plan: strip peer-side link entries, delete
// orphan Topology CRDs, patch StatefulSet Spec.Replicas, clear driver
// history for orphans. Returns the touched peer pods so the caller can
// restart+replay them (Meshnet only reacts to CNI ADD/DEL, not to spec
// edits, so without restart inter-node VXLAN interfaces stay dangling).
func ApplyScaleDowns(namespace string, plans []ScalePlan) (touchedPeers []string, orphanPods []string, err error) {
	if len(plans) == 0 {
		return nil, nil, nil
	}

	touchedSet := make(map[string]struct{})
	orphanSet := make(map[string]struct{})

	for _, plan := range plans {
		// Pods that will be removed: indices in [new, current).
		toRemove := make(map[string]struct{}, plan.CurrentReplicas-plan.NewReplicas)
		for i := plan.NewReplicas; i < plan.CurrentReplicas; i++ {
			toRemove[fmt.Sprintf("%s-%d", plan.Node.Name, i)] = struct{}{}
		}

		// 1) Strip references from peer CRDs.
		touched, err := RemoveLinksReferencingDeletedPods(namespace, toRemove)
		if err != nil {
			return nil, nil, fmt.Errorf("scale-down: error removing links for '%s' replicas: %w", plan.Node.Name, err)
		}
		for _, p := range touched {
			if _, isOrphan := toRemove[p]; isOrphan {
				continue
			}
			touchedSet[p] = struct{}{}
		}

		// 2) Delete the orphan pods' Topology CRDs.
		for pod := range toRemove {
			if err := kubeclient.DynamicClient.Resource(TopologyGVR).
				Namespace(namespace).
				Delete(context.TODO(), pod, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return nil, nil, fmt.Errorf("scale-down: error deleting topology %s: %w", pod, err)
			}
		}

		// 3) Patch StatefulSet replicas. K8s scales the StatefulSet down,
		// terminating pods from the highest ordinal first.
		if err := patchStatefulSetReplicas(namespace, plan.Node.Name, plan.NewReplicas); err != nil {
			return nil, nil, fmt.Errorf("scale-down: %w", err)
		}

		// 4) Driver operation history cleanup for each orphan pod.
		for pod := range toRemove {
			if err := DeleteDriverOperationHistoryForPod(namespace, pod); err != nil {
				return nil, nil, fmt.Errorf("scale-down: could not clean driver history for %s: %w", pod, err)
			}
			orphanSet[pod] = struct{}{}
		}

		log.Printf("⬇️  Scaled '%s' %d → %d (removed %d pods)", plan.Node.Name, plan.CurrentReplicas, plan.NewReplicas, plan.CurrentReplicas-plan.NewReplicas)
	}

	touchedPeers = make([]string, 0, len(touchedSet))
	for p := range touchedSet {
		touchedPeers = append(touchedPeers, p)
	}
	sort.Strings(touchedPeers)

	orphanPods = make([]string, 0, len(orphanSet))
	for p := range orphanSet {
		orphanPods = append(orphanPods, p)
	}
	sort.Strings(orphanPods)

	// Peers need to be restarted so Meshnet runs CNI DEL/ADD and tears down
	// inter-node VXLAN interfaces (intra-node veths self-clean when the
	// orphan's netns dies, but VXLAN doesn't). The modify handler does the
	// actual restart in a single unified phase after all modify phases
	// complete.
	return touchedPeers, orphanPods, nil
}

// ApplyScaleUps materializes new pod ordinals: writes Topology CRDs, patches
// replicas, waits for Ready, then upserts the same links into peer CRDs.
// Caller must (1) pre-stamp UIDs via PrepareUniqueLinkUIDs and (2) filter
// the returned consumedUIDs out of request.Add.Links before ApplyAdd, or the
// conflict-validator rejects them as "already in use".
func ApplyScaleUps(
	namespace string,
	plans []ScalePlan,
	preparedAddLinks []types.LinkSpec,
	allNodesAfter []types.NodeSpec,
) (newPods []string, touchedPeers []string, consumedUIDs map[int]struct{}, err error) {
	if len(plans) == 0 {
		return nil, nil, nil, nil
	}

	created := make([]string, 0)
	allTouchedPeers := make(map[string]struct{})
	consumedUIDs = make(map[int]struct{})

	for _, plan := range plans {
		// New pod ordinals: [current, new).
		newPodNames := make([]string, 0, plan.NewReplicas-plan.CurrentReplicas)
		for i := plan.CurrentReplicas; i < plan.NewReplicas; i++ {
			newPodNames = append(newPodNames, fmt.Sprintf("%s-%d", plan.Node.Name, i))
		}

		// Filter the add-links to those that involve one of the new pods.
		// Anything that does not touch a new pod will flow through the
		// normal add phase later.
		newPodSet := make(map[string]struct{}, len(newPodNames))
		for _, p := range newPodNames {
			newPodSet[p] = struct{}{}
		}

		relevantLinks := make([]types.LinkSpec, 0, len(preparedAddLinks))
		for _, link := range preparedAddLinks {
			lhs := ResolvePodNameFromLink(link.Node, allNodesAfter)
			rhs := ResolvePodNameFromLink(link.PeerNode, allNodesAfter)
			if _, hit := newPodSet[lhs]; hit {
				relevantLinks = append(relevantLinks, link)
				if link.UID != nil {
					consumedUIDs[*link.UID] = struct{}{}
				}
				continue
			}
			if _, hit := newPodSet[rhs]; hit {
				relevantLinks = append(relevantLinks, link)
				if link.UID != nil {
					consumedUIDs[*link.UID] = struct{}{}
				}
			}
		}

		// 1) Create Topology CRDs for the new pods.
		for _, pod := range newPodNames {
			if err := CreateTopologyForPod(namespace, pod, relevantLinks, allNodesAfter); err != nil {
				return nil, nil, nil, fmt.Errorf("scale-up: error creating topology for %s: %w", pod, err)
			}
		}

		// 2) Update the peer-side Topology CRDs BEFORE patching the
		// StatefulSet, so both sides of every new link are declared.
		if len(relevantLinks) > 0 {
			touched, err := upsertAppendLinksInTopologies(namespace, relevantLinks, allNodesAfter)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("scale-up: error updating peer-side topology CRDs: %w", err)
			}
			for _, p := range touched {
				if _, isNew := newPodSet[p]; isNew {
					continue
				}
				allTouchedPeers[p] = struct{}{}
			}
		}

		// 3) Pre-inject peer skip entries so each new replica's first CNI ADD
		// creates its veth(s) on the spot. See ApplyAddToExistingTopology.
		for _, podName := range newPodNames {
			if err := injectPeerSkipEntries(namespace, podName); err != nil {
				log.Printf("⚠️ Could not pre-inject peer skip entries for %s: %v (continuing)", podName, err)
			}
		}

		// 4) Republish the per-pod expected-iface-count ConfigMap BEFORE the
		// StatefulSet scale-up so kubelet, when it creates the new pod's
		// sandbox, already mounts the file for that pod. Without this the new
		// pod's entrypoint reads an empty /etc/kubendt/iface-counts/<pod> dir
		// and falls back to the fixed 5s sleep, which races with meshnet CNI
		// ADD and leaves QEMU launched with zero TAPs.
		if err := UpdateIfaceCountsConfigMap(namespace, allNodesAfter); err != nil {
			log.Printf("⚠️ scale-up: could not update iface counts ConfigMap pre-scale: %v (new pod may fall back to fixed sleep)", err)
		}

		// 5) Patch StatefulSet replicas. K8s creates the new ordinals. We do
		// NOT wait for the new pods here, the modify handler waits once at
		// the end, in parallel with any QEMU peer restarts that other
		// modify phases declare.
		if err := patchStatefulSetReplicas(namespace, plan.Node.Name, plan.NewReplicas); err != nil {
			return nil, nil, nil, fmt.Errorf("scale-up: %w", err)
		}

		created = append(created, newPodNames...)
		log.Printf("⬆️  Scaled '%s' %d → %d (added %d pods, wait deferred to unified phase)", plan.Node.Name, plan.CurrentReplicas, plan.NewReplicas, plan.NewReplicas-plan.CurrentReplicas)
	}

	if len(allTouchedPeers) > 0 {
		filtered := make([]string, 0, len(allTouchedPeers))
		for p := range allTouchedPeers {
			filtered = append(filtered, p)
		}
		sort.Strings(filtered)
		touchedPeers = filtered
	}

	// QEMU peers freeze their NIC list at launch, so a topology update alone
	// isn't visible to the running guest, they need a restart. We declare
	// touchedPeers here and let the modify handler do the restart in one
	// unified phase after all modify phases publish their changes. That gives
	// us one parallel restart round instead of one-per-phase, and dedupes
	// peers that several phases touch.
	sort.Strings(created)
	return created, touchedPeers, consumedUIDs, nil
}

// patchStatefulSetReplicas updates the StatefulSet's Spec.Replicas to the
// requested count. Uses a read-then-update cycle rather than the Scale
// subresource to keep this dependency-free (no extra client imports).
func patchStatefulSetReplicas(namespace, stsName string, replicas int) error {
	sts, err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).Get(context.TODO(), stsName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not read StatefulSet %s: %w", stsName, err)
	}
	r := int32(replicas)
	sts.Spec.Replicas = &r
	if _, err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).Update(context.TODO(), sts, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("could not patch StatefulSet %s replicas to %d: %w", stsName, replicas, err)
	}
	return nil
}
