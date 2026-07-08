package helpers

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"kubendt/kubeclient"
	"kubendt/types"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ApplyAddToExistingTopology applies CRD changes for the add phase (new
// node StatefulSets, new link CRDs, peer-side link upserts). It does NOT
// restart QEMU peers, wait for new pods, or replay driver ops, those are
// handled by the modify handler in a single unified phase after all
// modify phases have published their changes (see ModifyNetwork).
//
// Returns the list of new indexed pod names (so the handler can wait for
// them alongside restarted peers), the list of touched peer pods (existing
// pods whose Topology CRDs were augmented with new links), and any
// non-fatal warnings (e.g. mount file missing in the namespace file
// manager so the ConfigMap was skipped).
func ApplyAddToExistingTopology(namespace string, request types.DeployRequest, existingNodes []types.NodeSpec) ([]string, []string, []types.Warning, error) {
	warnings := make([]types.Warning, 0)
	existingSet := make(map[string]struct{}, len(existingNodes))
	for _, n := range existingNodes {
		existingSet[n.Name] = struct{}{}
	}

	newNodeSet := make(map[string]struct{}, len(request.Nodes))
	for i, node := range request.Nodes {
		if node.Name == "" {
			return nil, nil, nil, fmt.Errorf("VALIDATION:There are nodes without name")
		}
		if _, exists := existingSet[node.Name]; exists {
			return nil, nil, nil, fmt.Errorf("VALIDATION:Node '%s' already exists in namespace", node.Name)
		}
		if _, exists := newNodeSet[node.Name]; exists {
			return nil, nil, nil, fmt.Errorf("VALIDATION:Duplicate node in add: '%s'", node.Name)
		}
		newNodeSet[node.Name] = struct{}{}

		typ := node.Type
		if typ != "host" && typ != "switch" && typ != "router" {
			return nil, nil, nil, fmt.Errorf("VALIDATION:Node '%s' has invalid type '%s' (expected: host|switch|router)", node.Name, node.Type)
		}

		switch {
		case node.Replicas == 0:
			request.Nodes[i].Replicas = 1
		case node.Replicas < 0:
			return nil, nil, nil, fmt.Errorf("VALIDATION:Node '%s' has invalid replicas count (%d), must be >= 1", node.Name, node.Replicas)
		case node.Replicas > types.MaxReplicas:
			request.Nodes[i].Replicas = types.MaxReplicas
		}
	}

	if len(request.Nodes) > 0 {
		if err := ResolveDriversForNodes(request.Nodes); err != nil {
			return nil, nil, nil, fmt.Errorf("VALIDATION:%s", err.Error())
		}
	}

	allNodes := make([]types.NodeSpec, 0, len(existingNodes)+len(request.Nodes))
	allNodes = append(allNodes, existingNodes...)
	allNodes = append(allNodes, request.Nodes...)

	if err := ValidateLinksForModify(request.Links, allNodes); err != nil {
		return nil, nil, nil, fmt.Errorf("VALIDATION:%s", err.Error())
	}

	if err := ValidateLinkInterfaceNamesForDrivers(request.Links, allNodes); err != nil {
		return nil, nil, nil, fmt.Errorf("VALIDATION:%s", err.Error())
	}

	if err := ValidateInterfaceConflicts(namespace, request.Links, allNodes); err != nil {
		return nil, nil, nil, fmt.Errorf("VALIDATION:%s", err.Error())
	}

	preparedLinks, err := PrepareUniqueLinkUIDs(namespace, request.Links)
	if err != nil {
		return nil, nil, nil, err
	}
	request.Links = preparedLinks

	// Pre-pass: any mount declaring sensitive=true upgrades the file flag in
	// namespace_file_meta. JSON can only mark sensitive, never unmark (see
	// MountSpec.Sensitive).
	for _, node := range request.Nodes {
		for _, mount := range node.Mounts {
			if mount.Sensitive {
				if err := SetFileSensitive(namespace, mount.File, true); err != nil {
					log.Printf("⚠️ Could not mark file %q as sensitive: %v", mount.File, err)
				}
			}
		}
	}

	validMountsMap := make(map[string][]types.MountSpec)
	for _, node := range request.Nodes {
		for _, mount := range node.Mounts {
			_, _, err := CreateMountResourceForFile(namespace, mount.File)
			if err != nil {
				if strings.Contains(err.Error(), "error reading file") {
					log.Printf("⚠️ File %s does not exist. Skipping ConfigMap creation: %v", mount.File, err)
					warnings = append(warnings, types.Warning{
						Node:   node.Name,
						Kind:   "mount_file_missing",
						File:   mount.File,
						Detail: fmt.Sprintf("File %q not found in namespace file manager. Mount skipped, the pod will start without it.", mount.File),
					})
					continue
				}
				return nil, nil, nil, fmt.Errorf("error creating ConfigMap for file %s on node %s: %w", mount.File, node.Name, err)
			}
			validMountsMap[node.Name] = append(validMountsMap[node.Name], mount)
		}
	}

	// Topology CRDs BEFORE StatefulSets, Meshnet's CNI ADD needs them to
	// exist when the pod sandbox is created, otherwise interfaces fall through
	// to a post-hoc reconciler run.
	for _, node := range request.Nodes {
		if err := CreateTopologyObject(namespace, node, request.Links, allNodes); err != nil {
			return nil, nil, nil, fmt.Errorf("error creating topology for %s: %w", node.Name, err)
		}
	}

	// Peer-side CRDs first too, both ends of every link must be declared
	// before the new pod's CNI ADD fires.
	touchedPods, err := upsertAppendLinksInTopologies(namespace, request.Links, allNodes)
	if err != nil {
		return nil, nil, nil, err
	}

	// Pre-inject peer skip entries. Without them the new pod's CNI ADD waits
	// for a peer veth that never comes (peer doesn't re-fire ADD) and we
	// fall back to a ~40s reconciliation restart per pair. Writing
	// {link_uid, new_pod_name} into peer.status.skipped beforehand tells
	// meshnet "the new pod will create the veth" so the link lands in one pass.
	for _, node := range request.Nodes {
		for i := 0; i < node.Replicas; i++ {
			podName := fmt.Sprintf("%s-%d", node.Name, i)
			if err := injectPeerSkipEntries(namespace, podName); err != nil {
				log.Printf("⚠️ Could not pre-inject peer skip entries for %s: %v (continuing)", podName, err)
			}
		}
	}

	// Republish the per-pod expected-iface-count ConfigMap BEFORE creating
	// the new StatefulSets so kubelet, when it creates each new pod's
	// sandbox, already mounts the file for that pod. Without this the new
	// pod's entrypoint reads an empty /etc/kubendt/iface-counts/<pod> dir
	// and falls back to the fixed 5s sleep, which races with meshnet CNI
	// ADD and can leave QEMU launched with zero TAPs.
	if len(request.Nodes) > 0 {
		if err := UpdateIfaceCountsConfigMap(namespace, allNodes); err != nil {
			log.Printf("⚠️ add: could not update iface counts ConfigMap pre-create: %v (new pods may fall back to fixed sleep)", err)
		}
	}

	for _, node := range request.Nodes {
		mounts := validMountsMap[node.Name]
		err := CreateNetworkStatefulSet(namespace, node, mounts)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("error creating StatefulSet %s: %w", node.Name, err)
		}
	}

	// Build the indexed pod-name list for the new nodes so the handler can
	// wait for them alongside any restarted QEMU peers. We do not wait here
	//, see ModifyNetwork for the unified wait/restart/replay phase.
	newPodNames := make([]string, 0)
	for _, node := range request.Nodes {
		replicas := node.Replicas
		if replicas <= 0 {
			replicas = 1
		}
		for i := 0; i < replicas; i++ {
			newPodNames = append(newPodNames, fmt.Sprintf("%s-%d", node.Name, i))
		}
	}

	return newPodNames, touchedPods, warnings, nil
}

func ApplyDeleteOnExistingTopology(namespace string, deleteNodes []string, deleteLinks []types.LinkSpec, existingNodes []types.NodeSpec) ([]string, []string, error) {
	if len(deleteNodes) == 0 && len(deleteLinks) == 0 {
		return []string{}, []string{}, nil
	}

	nodeByName := make(map[string]types.NodeSpec, len(existingNodes))
	for _, n := range existingNodes {
		nodeByName[n.Name] = n
	}

	toDeleteNodes := make(map[string]struct{})
	for _, nodeName := range deleteNodes {
		nodeName = strings.TrimSpace(nodeName)
		if nodeName == "" {
			return nil, nil, fmt.Errorf("VALIDATION:delete.nodes contains empty name")
		}
		if _, _, ok := SplitIndexedPodName(nodeName); ok {
			return nil, nil, fmt.Errorf("VALIDATION:delete.nodes must use node name (e.g.: 'host3'), not indexed pod ('%s')", nodeName)
		}
		if _, exists := nodeByName[nodeName]; !exists {
			return nil, nil, fmt.Errorf("VALIDATION:node '%s' does not exist and cannot be deleted", nodeName)
		}
		toDeleteNodes[nodeName] = struct{}{}
	}

	if err := ValidateLinksForModify(deleteLinks, existingNodes); err != nil {
		return nil, nil, fmt.Errorf("VALIDATION:%s", err.Error())
	}

	if len(deleteLinks) > 0 {
		if err := ValidateDeleteLinksExist(namespace, deleteLinks, existingNodes); err != nil {
			return nil, nil, fmt.Errorf("VALIDATION:%s", err.Error())
		}
	}

	restartedSet := make(map[string]struct{})

	if len(deleteLinks) > 0 {
		touched, err := RemoveSpecificLinksFromTopologies(namespace, deleteLinks, existingNodes)
		if err != nil {
			return nil, nil, err
		}
		for _, pod := range touched {
			restartedSet[pod] = struct{}{}
		}
	}

	deletedPods := make(map[string]struct{})
	for nodeName := range toDeleteNodes {
		node := nodeByName[nodeName]
		for i := 0; i < node.Replicas; i++ {
			deletedPods[fmt.Sprintf("%s-%d", nodeName, i)] = struct{}{}
		}
	}

	if len(deletedPods) > 0 {
		touched, err := RemoveLinksReferencingDeletedPods(namespace, deletedPods)
		if err != nil {
			return nil, nil, err
		}
		for _, pod := range touched {
			restartedSet[pod] = struct{}{}
		}
	}

	deletedNodeNames := make([]string, 0, len(toDeleteNodes))
	for nodeName := range toDeleteNodes {
		node := nodeByName[nodeName]
		node.Name = nodeName
		if err := DeleteNodeStatefulSetAndTopologies(namespace, node); err != nil {
			return nil, nil, err
		}

		deletedNodeNames = append(deletedNodeNames, nodeName)
	}

	for podName := range deletedPods {
		if err := DeleteDriverOperationHistoryForPod(namespace, podName); err != nil {
			return nil, nil, fmt.Errorf("could not cleanup driver operation history for deleted pod %s: %w", podName, err)
		}
	}

	// Touched peers (pods whose Topology CRDs were modified by the link/node
	// deletions) need to be restarted so meshnet redraws the wiring; the
	// modify handler does that in a single unified phase after all modify
	// phases complete. We just declare them here.
	touchedPeers := make([]string, 0, len(restartedSet))
	for pod := range restartedSet {
		if _, isDeleted := deletedPods[pod]; isDeleted {
			continue
		}
		touchedPeers = append(touchedPeers, pod)
	}
	sort.Strings(touchedPeers)

	sort.Strings(deletedNodeNames)
	return touchedPeers, deletedNodeNames, nil
}

func GetExistingNodes(namespace string) ([]types.NodeSpec, error) {
	stsList, err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	nodes := make([]types.NodeSpec, 0, len(stsList.Items))
	for _, sts := range stsList.Items {
		name := sts.Spec.Template.Labels["app"]
		if name == "" {
			name = sts.Name
		}

		replicas := 1
		if sts.Spec.Replicas != nil {
			replicas = int(*sts.Spec.Replicas)
		}

		nodes = append(nodes, types.NodeSpec{
			Name:     name,
			Replicas: replicas,
			Type:     sts.Spec.Template.Labels["kubendt/type"],
			Driver:   sts.Spec.Template.Labels["kubendt/driver"],
			Qemu:     sts.Spec.Template.Labels["kubendt/qemu"] == "true" || sts.Spec.Template.Labels["kubendt/runtime"] == "qemu",
		})
	}

	return nodes, nil
}

// ValidateInterfaceConflicts rejects new links whose interfaces are already
// occupied. It checks each incoming link against:
//   - the namespace's existing Topology CRDs (state already on the cluster),
//   - earlier links in the SAME request (in-payload duplicates and chains
//     that would oversubscribe the same iface).
//
// The "external" side is not tracked as a conflict source: multiple pods
// can legitimately attach to the same host iface (broadcast domain).
func ValidateInterfaceConflicts(namespace string, links []types.LinkSpec, allNodes []types.NodeSpec) error {
	podSet := make(map[string]struct{})
	for _, link := range links {
		if link.Node != "external" {
			podSet[ResolvePodNameFromLink(link.Node, allNodes)] = struct{}{}
		}
		if link.PeerNode != "external" {
			podSet[ResolvePodNameFromLink(link.PeerNode, allNodes)] = struct{}{}
		}
	}

	// Seed podUsed with the interfaces already in use according to each
	// pod's current Topology CRD. podUsed[pod][intf] = struct{}{} means the
	// iface is claimed (by existing state or by an earlier link in this
	// request once we get there).
	podUsed := make(map[string]map[string]struct{})
	for podName := range podSet {
		obj, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil {
			// Pod has no Topology yet, no existing claims.
			continue
		}
		specLinks, _, _ := unstructured.NestedSlice(obj.Object, "spec", "links")
		used := make(map[string]struct{}, len(specLinks))
		for _, item := range specLinks {
			lm, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if intf, ok := lm["local_intf"].(string); ok && intf != "" {
				used[intf] = struct{}{}
			}
		}
		podUsed[podName] = used
	}

	// Helper to claim an iface for a pod (creates the inner map if needed).
	claim := func(pod, intf string) {
		if _, ok := podUsed[pod]; !ok {
			podUsed[pod] = make(map[string]struct{})
		}
		podUsed[pod][intf] = struct{}{}
	}

	// Walk links in order, validating against everything claimed so far
	// (existing CRDs + earlier links in this same payload), then claim the
	// new ifaces for subsequent iterations.
	for i, link := range links {
		if link.Node != "external" {
			pod := ResolvePodNameFromLink(link.Node, allNodes)
			if used, ok := podUsed[pod]; ok {
				if _, conflict := used[link.LocalIntf]; conflict {
					return fmt.Errorf("link[%d]: interface '%s' is already in use on pod '%s'", i, link.LocalIntf, pod)
				}
			}
			claim(pod, link.LocalIntf)
		}
		if link.PeerNode != "external" {
			pod := ResolvePodNameFromLink(link.PeerNode, allNodes)
			if used, ok := podUsed[pod]; ok {
				if _, conflict := used[link.PeerIntf]; conflict {
					return fmt.Errorf("link[%d]: interface '%s' is already in use on pod '%s'", i, link.PeerIntf, pod)
				}
			}
			claim(pod, link.PeerIntf)
		}
	}

	return nil
}

// ValidateLinkName checks the optional link name field.
// Rules: max 16 characters, no leading/trailing whitespace, printable ASCII only (no control
// characters, no slashes or other chars that could confuse annotation storage or display).
func ValidateLinkName(name string, linkIndex int) error {
	if name == "" {
		return nil // optional, absence is fine
	}
	if len(name) > 16 {
		return fmt.Errorf("link[%d].name %q: must be 16 characters or fewer (got %d)", linkIndex, name, len(name))
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("link[%d].name %q: must not have leading or trailing whitespace", linkIndex, name)
	}
	for _, r := range name {
		if r < 0x20 || r > 0x7E {
			return fmt.Errorf("link[%d].name %q: only printable ASCII characters are allowed", linkIndex, name)
		}
		if r == '/' || r == '"' || r == '\\' {
			return fmt.Errorf("link[%d].name %q: character %q is not allowed", linkIndex, name, r)
		}
	}
	return nil
}

func ValidateLinksForModify(links []types.LinkSpec, allNodes []types.NodeSpec) error {
	replicasByNode := make(map[string]int, len(allNodes))
	for _, n := range allNodes {
		replicasByNode[n.Name] = n.Replicas
	}

	for i, link := range links {
		if strings.TrimSpace(link.Node) == "" || strings.TrimSpace(link.PeerNode) == "" {
			return fmt.Errorf("link[%d]: 'node' and 'peerNode' are required", i)
		}

		// Both endpoints cannot be "external", at least one must be a real pod
		if link.Node == externalNodeName && link.PeerNode == externalNodeName {
			return fmt.Errorf("link[%d]: both 'node' and 'peerNode' cannot be '%s'; at least one must be a real pod", i, externalNodeName)
		}

		if err := ValidateLinkEndpoint(link.Node, replicasByNode, i, "node"); err != nil {
			return err
		}
		if err := ValidateLinkEndpoint(link.PeerNode, replicasByNode, i, "peerNode"); err != nil {
			return err
		}

		if err := ValidateLinkName(link.Name, i); err != nil {
			return err
		}
	}

	return nil
}

func ValidateLinkEndpoint(endpoint string, replicasByNode map[string]int, linkIndex int, field string) error {
	if endpoint == "external" {
		return nil
	}

	if base, idx, ok := SplitIndexedPodName(endpoint); ok {
		replicas, exists := replicasByNode[base]
		if !exists {
			return fmt.Errorf("link[%d].%s: node '%s' does not exist", linkIndex, field, endpoint)
		}
		if idx < 0 || idx >= replicas {
			return fmt.Errorf("link[%d].%s: pod '%s' is out of replicas range (%d)", linkIndex, field, endpoint, replicas)
		}
		return nil
	}

	replicas, exists := replicasByNode[endpoint]
	if !exists {
		return fmt.Errorf("link[%d].%s: node '%s' does not exist", linkIndex, field, endpoint)
	}
	if replicas > 1 {
		return fmt.Errorf("link[%d].%s: '%s' has %d replicas, you must specify a concrete pod (e.g.: %s-0)", linkIndex, field, endpoint, replicas, endpoint)
	}

	return nil
}

func SplitIndexedPodName(name string) (string, int, bool) {
	lastDash := strings.LastIndex(name, "-")
	if lastDash <= 0 || lastDash == len(name)-1 {
		return "", 0, false
	}

	idx, err := strconv.Atoi(name[lastDash+1:])
	if err != nil {
		return "", 0, false
	}

	return name[:lastDash], idx, true
}

func ValidateDeleteLinksExist(namespace string, links []types.LinkSpec, allNodes []types.NodeSpec) error {
	perPod := buildPerPodLinkEntries(links, allNodes)

	for podName, entries := range perPod {
		obj, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("topology does not exist for pod '%s'", podName)
			}
			return fmt.Errorf("error fetching Topology %s: %w", podName, err)
		}

		existingLinks, found, err := unstructured.NestedSlice(obj.Object, "spec", "links")
		if err != nil {
			return fmt.Errorf("error reading links from Topology %s: %w", podName, err)
		}
		if !found {
			existingLinks = []interface{}{}
		}

		existingKeys := make(map[string]struct{}, len(existingLinks))
		for _, item := range existingLinks {
			linkMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			existingKeys[topologyLinkKeyForModify(linkMap)] = struct{}{}
		}

		for _, entry := range entries {
			if _, ok := existingKeys[topologyLinkKeyForModify(entry)]; !ok {
				return fmt.Errorf("link to delete does not exist in pod '%s'", podName)
			}
		}
	}

	return nil
}

func RemoveSpecificLinksFromTopologies(namespace string, links []types.LinkSpec, allNodes []types.NodeSpec) ([]string, error) {
	perPod := buildPerPodLinkEntries(links, allNodes)
	touched := make([]string, 0, len(perPod))

	for podName, entries := range perPod {
		obj, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("error fetching Topology %s: %w", podName, err)
		}

		existingLinks, found, err := unstructured.NestedSlice(obj.Object, "spec", "links")
		if err != nil {
			return nil, fmt.Errorf("error reading links from Topology %s: %w", podName, err)
		}
		if !found {
			existingLinks = []interface{}{}
		}

		deleteKeys := make(map[string]struct{}, len(entries))
		for _, entry := range entries {
			deleteKeys[topologyLinkKeyForModify(entry)] = struct{}{}
		}

		newLinks := make([]interface{}, 0, len(existingLinks))
		changed := false
		for _, item := range existingLinks {
			linkMap, ok := item.(map[string]interface{})
			if !ok {
				newLinks = append(newLinks, item)
				continue
			}
			if _, remove := deleteKeys[topologyLinkKeyForModify(linkMap)]; remove {
				changed = true
				continue
			}
			newLinks = append(newLinks, item)
		}

		if !changed {
			continue
		}

		if err := unstructured.SetNestedSlice(obj.Object, newLinks, "spec", "links"); err != nil {
			return nil, fmt.Errorf("error updating links in %s: %w", podName, err)
		}
		if _, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Update(context.TODO(), obj, metav1.UpdateOptions{}); err != nil {
			return nil, fmt.Errorf("error updating Topology %s: %w", podName, err)
		}

		touched = append(touched, podName)
	}

	return touched, nil
}

func RemoveLinksReferencingDeletedPods(namespace string, deletedPods map[string]struct{}) ([]string, error) {
	topologyList, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("error listing topologies: %w", err)
	}

	touched := make([]string, 0)

	for _, item := range topologyList.Items {
		podName := item.GetName()
		if _, deleted := deletedPods[podName]; deleted {
			continue
		}

		existingLinks, found, err := unstructured.NestedSlice(item.Object, "spec", "links")
		if err != nil {
			return nil, fmt.Errorf("error reading links from Topology %s: %w", podName, err)
		}
		if !found {
			continue
		}

		newLinks := make([]interface{}, 0, len(existingLinks))
		changed := false
		for _, linkData := range existingLinks {
			linkMap, ok := linkData.(map[string]interface{})
			if !ok {
				newLinks = append(newLinks, linkData)
				continue
			}
			peerPod := fmt.Sprintf("%v", linkMap["peer_pod"])
			if _, remove := deletedPods[peerPod]; remove {
				changed = true
				continue
			}
			newLinks = append(newLinks, linkData)
		}

		if !changed {
			continue
		}

		if err := unstructured.SetNestedSlice(item.Object, newLinks, "spec", "links"); err != nil {
			return nil, fmt.Errorf("error updating Topology %s links: %w", podName, err)
		}
		if _, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Update(context.TODO(), &item, metav1.UpdateOptions{}); err != nil {
			return nil, fmt.Errorf("error updating Topology %s: %w", podName, err)
		}

		touched = append(touched, podName)
	}

	return touched, nil
}

func BuildLinksFromTopologyCRDs(namespace string) ([]types.LinkSpec, error) {
	topologyList, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("error listing topologies: %w", err)
	}

	links := make([]types.LinkSpec, 0)
	seen := make(map[string]struct{})

	for _, item := range topologyList.Items {
		podName := item.GetName()
		specLinks, found, err := unstructured.NestedSlice(item.Object, "spec", "links")
		if err != nil {
			return nil, fmt.Errorf("error reading links from Topology %s: %w", podName, err)
		}
		if !found {
			continue
		}

		for _, linkData := range specLinks {
			linkMap, ok := linkData.(map[string]interface{})
			if !ok {
				continue
			}

			peerPod := fmt.Sprintf("%v", linkMap["peer_pod"])
			localIntf := fmt.Sprintf("%v", linkMap["local_intf"])
			peerIntf := fmt.Sprintf("%v", linkMap["peer_intf"])

			left := podName + "/" + localIntf
			right := peerPod + "/" + peerIntf
			key := left + "<->" + right
			if right < left {
				key = right + "<->" + left
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}

			link := types.LinkSpec{
				Node:      podName,
				LocalIntf: localIntf,
				PeerNode:  fromCRDPeerPod(peerPod), // translate "localhost" → "external" for API layer
				PeerIntf:  peerIntf,
			}

			if localIP, ok := linkMap["local_ip"].(string); ok {
				link.LocalIP = localIP
			}
			if peerIP, ok := linkMap["peer_ip"].(string); ok {
				link.PeerIP = peerIP
			}

			switch uidVal := linkMap["uid"].(type) {
			case int64:
				u := int(uidVal)
				link.UID = &u
			case int:
				u := uidVal
				link.UID = &u
			case float64:
				u := int(uidVal)
				link.UID = &u
			}

			links = append(links, link)
		}
	}

	return links, nil
}

func DeleteNodeStatefulSetAndTopologies(namespace string, node types.NodeSpec) error {
	ctx := context.TODO()

	// Inspect the StatefulSet BEFORE deleting it so we can recover the
	// names of ConfigMaps / Secrets it references (env + mount-file).
	// Without this any per-node env ConfigMap or orphaned mount resource
	// would linger in the namespace forever.
	var envCMs []string
	var mountCMs []string
	var mountSecrets []string
	if sts, err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).Get(ctx, node.Name, metav1.GetOptions{}); err == nil {
		for _, container := range sts.Spec.Template.Spec.Containers {
			for _, envFrom := range container.EnvFrom {
				if envFrom.ConfigMapRef != nil && envFrom.ConfigMapRef.Name != "" {
					envCMs = append(envCMs, envFrom.ConfigMapRef.Name)
				}
			}
		}
		for _, volume := range sts.Spec.Template.Spec.Volumes {
			if volume.ConfigMap != nil && volume.ConfigMap.Name != "" {
				mountCMs = append(mountCMs, volume.ConfigMap.Name)
			}
			if volume.Secret != nil && volume.Secret.SecretName != "" {
				mountSecrets = append(mountSecrets, volume.Secret.SecretName)
			}
		}
	}

	if err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).Delete(ctx, node.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("error deleting StatefulSet %s: %w", node.Name, err)
	}

	for i := 0; i < node.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", node.Name, i)
		err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("error deleting Topology %s: %w", podName, err)
		}
	}

	// Env ConfigMaps are per-node, never shared. Safe to delete unconditionally.
	for _, name := range envCMs {
		if err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			log.Printf("⚠️ delete-node: could not delete env ConfigMap %s: %v", name, err)
		}
	}

	// Mount resources (ConfigMaps or Secrets) are shared across every pod
	// that mounts the same file in the namespace. We can only delete one
	// if no other live StatefulSet still references it. Rebuild the
	// in-use sets from the remaining StatefulSets and skip names still
	// in use.
	if len(mountCMs) > 0 || len(mountSecrets) > 0 {
		cmInUse := make(map[string]struct{})
		secretInUse := make(map[string]struct{})
		if remaining, err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{}); err == nil {
			for _, sts := range remaining.Items {
				for _, volume := range sts.Spec.Template.Spec.Volumes {
					if volume.ConfigMap != nil && volume.ConfigMap.Name != "" {
						cmInUse[volume.ConfigMap.Name] = struct{}{}
					}
					if volume.Secret != nil && volume.Secret.SecretName != "" {
						secretInUse[volume.Secret.SecretName] = struct{}{}
					}
				}
			}
		}
		// Iface-counts ConfigMap is also managed by us but lives on a
		// different lifecycle (rebuilt on every topology mutation). Never
		// GC it here.
		for _, name := range mountCMs {
			if name == IfaceCountsConfigMapName {
				continue
			}
			if _, stillUsed := cmInUse[name]; stillUsed {
				continue
			}
			if err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				log.Printf("⚠️ delete-node: could not delete mount ConfigMap %s: %v", name, err)
			}
		}
		for _, name := range mountSecrets {
			if _, stillUsed := secretInUse[name]; stillUsed {
				continue
			}
			if err := kubeclient.Clientset.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				log.Printf("⚠️ delete-node: could not delete mount Secret %s: %v", name, err)
			}
		}
	}

	return nil
}

func upsertAppendLinksInTopologies(namespace string, links []types.LinkSpec, allNodes []types.NodeSpec) ([]string, error) {
	if len(links) == 0 {
		return []string{}, nil
	}

	links = SanitizeLinksForQemuNodes(links, allNodes)

	podLinks := make(map[string][]map[string]interface{})
	peerLabelByPod := make(map[string]string)

	for _, link := range links {
		resolvedNode := ResolvePodNameFromLink(link.Node, allNodes)
		resolvedPeer := ResolvePodNameFromLink(link.PeerNode, allNodes)

		uid := 0
		if link.UID != nil {
			uid = *link.UID
		} else {
			uid = rand.Intn(9_000_000) + 1_000_000
		}

		if resolvedNode != "external" {
			podLinks[resolvedNode] = append(podLinks[resolvedNode], map[string]interface{}{
				"local_intf": link.LocalIntf,
				"local_ip":   link.LocalIP,
				"peer_pod":   toCRDPeerPod(resolvedPeer), // controller expects "localhost" for external
				"peer_intf":  link.PeerIntf,
				"peer_ip":    link.PeerIP,
				"uid":        int64(uid),
			})

			// peerLabel: store on the real-pod side regardless of which end is external
			if link.PeerLabel != "" && (link.PeerNode == externalNodeName || link.Node == externalNodeName) {
				peerLabelByPod[resolvedNode] = link.PeerLabel
			}
		}

		if resolvedPeer != "external" {
			podLinks[resolvedPeer] = append(podLinks[resolvedPeer], map[string]interface{}{
				"local_intf": link.PeerIntf,
				"local_ip":   link.PeerIP,
				"peer_pod":   toCRDPeerPod(resolvedNode), // controller expects "localhost" for external
				"peer_intf":  link.LocalIntf,
				"peer_ip":    link.LocalIP,
				"uid":        int64(uid),
			})
			// peerLabel when node is the external side (peer is the real pod)
			if link.PeerLabel != "" && link.Node == externalNodeName {
				peerLabelByPod[resolvedPeer] = link.PeerLabel
			}
		}
	}

	touched := make([]string, 0, len(podLinks))

	for podName, addedLinks := range podLinks {
		if podName == "external" {
			continue
		}

		obj, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("error fetching Topology %s: %w", podName, err)
			}

			annotations := map[string]string{}
			if peerLabel, ok := peerLabelByPod[podName]; ok && peerLabel != "" {
				annotations["kubendt/peerlabel"] = peerLabel
			}

			topology := &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "networkop.co.uk/v1beta1",
				"kind":       "Topology",
				"metadata": map[string]interface{}{
					"name":        podName,
					"namespace":   namespace,
					"annotations": annotations,
				},
				"spec": map[string]interface{}{
					"links": addedLinks,
				},
			}}

			if _, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Create(context.TODO(), topology, metav1.CreateOptions{}); err != nil {
				return nil, fmt.Errorf("error creating Topology %s: %w", podName, err)
			}
			touched = append(touched, podName)
			continue
		}

		existingLinks, found, err := unstructured.NestedSlice(obj.Object, "spec", "links")
		if err != nil {
			return nil, fmt.Errorf("error reading existing links from %s: %w", podName, err)
		}
		if !found {
			existingLinks = []interface{}{}
		}

		existingKeys := make(map[string]struct{}, len(existingLinks))
		for _, item := range existingLinks {
			linkMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			existingKeys[topologyLinkKey(linkMap)] = struct{}{}
		}

		changed := false
		for _, link := range addedLinks {
			key := topologyLinkKey(link)
			if _, exists := existingKeys[key]; exists {
				continue
			}
			existingLinks = append(existingLinks, link)
			existingKeys[key] = struct{}{}
			changed = true
		}

		if !changed {
			continue
		}

		if err := unstructured.SetNestedSlice(obj.Object, existingLinks, "spec", "links"); err != nil {
			return nil, fmt.Errorf("error updating links in %s: %w", podName, err)
		}

		if peerLabel, ok := peerLabelByPod[podName]; ok && peerLabel != "" {
			annotations := obj.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations["kubendt/peerlabel"] = peerLabel
			obj.SetAnnotations(annotations)
		}

		if _, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Update(context.TODO(), obj, metav1.UpdateOptions{}); err != nil {
			return nil, fmt.Errorf("error updating Topology %s: %w", podName, err)
		}

		touched = append(touched, podName)
	}

	return touched, nil
}

func topologyLinkKey(link map[string]interface{}) string {
	localIntf := fmt.Sprintf("%v", link["local_intf"])
	localIP := fmt.Sprintf("%v", link["local_ip"])
	peerPod := fmt.Sprintf("%v", link["peer_pod"])
	peerIntf := fmt.Sprintf("%v", link["peer_intf"])
	peerIP := fmt.Sprintf("%v", link["peer_ip"])
	return strings.Join([]string{localIntf, localIP, peerPod, peerIntf, peerIP}, "|")
}

func buildPerPodLinkEntries(links []types.LinkSpec, allNodes []types.NodeSpec) map[string][]map[string]interface{} {
	links = SanitizeLinksForQemuNodes(links, allNodes)

	perPod := make(map[string][]map[string]interface{})
	for _, link := range links {
		resolvedNode := ResolvePodNameFromLink(link.Node, allNodes)
		resolvedPeer := ResolvePodNameFromLink(link.PeerNode, allNodes)

		if resolvedNode != "external" {
			perPod[resolvedNode] = append(perPod[resolvedNode], map[string]interface{}{
				"local_intf": link.LocalIntf,
				"local_ip":   link.LocalIP,
				"peer_pod":   toCRDPeerPod(resolvedPeer),
				"peer_intf":  link.PeerIntf,
				"peer_ip":    link.PeerIP,
			})
		}

		if resolvedPeer != "external" {
			perPod[resolvedPeer] = append(perPod[resolvedPeer], map[string]interface{}{
				"local_intf": link.PeerIntf,
				"local_ip":   link.PeerIP,
				"peer_pod":   toCRDPeerPod(resolvedNode),
				"peer_intf":  link.LocalIntf,
				"peer_ip":    link.LocalIP,
			})
		}
	}
	return perPod
}

func topologyLinkKeyForModify(link map[string]interface{}) string {
	localIntf := fmt.Sprintf("%v", link["local_intf"])
	peerPod := fmt.Sprintf("%v", link["peer_pod"])
	peerIntf := fmt.Sprintf("%v", link["peer_intf"])
	return localIntf + "|" + peerPod + "|" + peerIntf
}

// FilterLinksByPods narrows links to a conservative scope around impacted pods.
// It includes links that touch impacted pods and one-hop neighbor links.
func FilterLinksByPods(links []types.LinkSpec, pods []string) []types.LinkSpec {
	if len(links) == 0 || len(pods) == 0 {
		return links
	}

	scope := make(map[string]struct{}, len(pods))
	for _, p := range pods {
		if p == "" || p == "external" {
			continue
		}
		scope[p] = struct{}{}
	}

	if len(scope) == 0 {
		return links
	}

	// First pass: include links touching initial scope and expand with neighbors.
	for _, l := range links {
		a := l.Node
		b := l.PeerNode
		_, inA := scope[a]
		_, inB := scope[b]
		if inA || inB {
			if a != "" && a != "external" {
				scope[a] = struct{}{}
			}
			if b != "" && b != "external" {
				scope[b] = struct{}{}
			}
		}
	}

	filtered := make([]types.LinkSpec, 0, len(links))
	for _, l := range links {
		a := l.Node
		b := l.PeerNode
		_, inA := scope[a]
		_, inB := scope[b]
		if inA || inB {
			filtered = append(filtered, l)
		}
	}

	if len(filtered) == 0 {
		return links
	}

	return filtered
}
