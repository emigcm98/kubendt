package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"strings"
	"time"

	drivers_meta "kubendt/drivers/meta"
	drivers_registry "kubendt/drivers/registry"
	"kubendt/kubeclient"
	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var indexedPodRe = regexp.MustCompile(`^(.+)-(\d+)$`)

// externalNodeName is the API-facing identifier for external uplinks.
// At the CRD boundary it's translated to "localhost" (meshnet's required
// literal). Always compare against this constant, never against "localhost".
const externalNodeName = "external"

// crdExternalPeerPod is meshnet's required literal for external uplinks.
// Do not change without updating the controller as well.
const crdExternalPeerPod = "localhost"

// ToCRDPeerPod maps the API "external" identifier to the CRD "localhost".
func ToCRDPeerPod(name string) string {
	if name == externalNodeName {
		return crdExternalPeerPod
	}
	return name
}

func toCRDPeerPod(name string) string { return ToCRDPeerPod(name) }

// FromCRDPeerPod is the inverse of ToCRDPeerPod.
func FromCRDPeerPod(name string) string {
	if name == crdExternalPeerPod {
		return externalNodeName
	}
	return name
}

func fromCRDPeerPod(name string) string { return FromCRDPeerPod(name) }

// isIndexedPodName matches "<base>-<digits>". Note: names with hyphens
// but non-digit tails (e.g. "dmz-sw") are NOT indexed pods.
func isIndexedPodName(name string) bool {
	return indexedPodRe.MatchString(name)
}

// ResolvePodNameFromLink resolves a topology node reference to its real pod
// name. Direct match wins over indexed match, "router-1" as a single-replica
// node is NOT the second replica of "router". Inverting the order silently
// breaks meshnet wiring.
func ResolvePodNameFromLink(name string, nodes []types.NodeSpec) string {
	if name == "" {
		return name
	}
	if name == "external" {
		return name
	}

	for _, n := range nodes {
		if n.Name == name {
			if n.Replicas > 1 {
				log.Printf("⚠️ Ambiguous link reference: '%s' has %d replicas; expected '%s-N'. Defaulting to '%s-0'.",
					name, n.Replicas, name, name)
			}
			return name + "-0"
		}
	}

	if isIndexedPodName(name) {
		if base, _, ok := SplitIndexedPodName(name); ok {
			for _, n := range nodes {
				if n.Name == base {
					return name
				}
			}
		}
	}

	return name
}

// ResolveRealPodName is a legacy alias kept for backwards compatibility.
func ResolveRealPodName(name string, nodes []types.NodeSpec) string {
	return ResolvePodNameFromLink(name, nodes)
}

// SanitizeLinksForQemuNodes is a hook for per-driver link rewrites.
// QEMU nodes currently need none, so it's a passthrough.
func SanitizeLinksForQemuNodes(links []types.LinkSpec, allNodes []types.NodeSpec) []types.LinkSpec {
	return links
}

// ConvertToNodeNameIfSingleReplica collapses "<base>-0" back to "<base>"
// when base has exactly 1 replica; otherwise returns podName unchanged.
func ConvertToNodeNameIfSingleReplica(podName string, replicaCount map[string]int) string {
	m := indexedPodRe.FindStringSubmatch(podName)
	if m == nil {
		return podName
	}

	baseName := m[1]
	if count, ok := replicaCount[baseName]; ok && count == 1 {
		return baseName
	}
	return podName
}

func CreateTopologyObject(namespace string, node types.NodeSpec, links []types.LinkSpec, allNodes []types.NodeSpec) error {
	links = SanitizeLinksForQemuNodes(links, allNodes)

	resolvedUIDs := resolveLinkUIDs(links)

	for i := 0; i < node.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", node.Name, i)
		if err := createTopologyForPod(namespace, podName, links, resolvedUIDs, allNodes); err != nil {
			return err
		}
	}

	return nil
}

// CreateTopologyForPod materializes the Topology CRD for a single pod
// (e.g. during scale-up). Caller must pass UID-consistent links across
// replicas, use PrepareUniqueLinkUIDs.
func CreateTopologyForPod(namespace, podName string, links []types.LinkSpec, allNodes []types.NodeSpec) error {
	links = SanitizeLinksForQemuNodes(links, allNodes)
	resolvedUIDs := resolveLinkUIDs(links)
	return createTopologyForPod(namespace, podName, links, resolvedUIDs, allNodes)
}

// resolveLinkUIDs returns UIDs aligned with links: explicit UIDs are kept,
// missing ones are randomized.
func resolveLinkUIDs(links []types.LinkSpec) []int {
	resolvedUIDs := make([]int, len(links))
	for idx, link := range links {
		if link.UID != nil {
			resolvedUIDs[idx] = *link.UID
			continue
		}
		resolvedUIDs[idx] = rand.Intn(9_000_000) + 1_000_000
	}
	return resolvedUIDs
}

// createTopologyForPod builds and POSTs the Topology CRD for one pod.
// Shared by the per-replica loop and the single-pod (scale-up) path.
func createTopologyForPod(namespace, podName string, links []types.LinkSpec, resolvedUIDs []int, allNodes []types.NodeSpec) error {
	var peerLabel string
	linkNames := make(map[string]string) // uid (string) → link name
	var podLinks []map[string]interface{}

	for linkIdx, link := range links {
		resolvedNode := ResolvePodNameFromLink(link.Node, allNodes)
		resolvedPeer := ResolvePodNameFromLink(link.PeerNode, allNodes)

		// Skip links this pod doesn't participate in.
		if resolvedNode != podName && resolvedPeer != podName {
			continue
		}

		localIntf := link.LocalIntf
		localIP := link.LocalIP
		peerIntf := link.PeerIntf
		peerIP := link.PeerIP
		peerPod := resolvedPeer

		// Swap so "local_*" always refers to podName, no matter which side it sits on.
		if resolvedPeer == podName {
			localIntf, peerIntf = peerIntf, localIntf
			localIP, peerIP = peerIP, localIP
			peerPod = resolvedNode
		}

		uid := resolvedUIDs[linkIdx]

		// External uplinks carry their label through to the participating pod.
		if link.PeerLabel != "" && (link.PeerNode == externalNodeName || link.Node == externalNodeName) {
			peerLabel = link.PeerLabel
		}

		if link.Name != "" {
			linkNames[fmt.Sprintf("%d", uid)] = link.Name
		}

		log.Printf("🔗 Link for %s => %s(%s) <-> %s(%s)", podName, localIntf, localIP, peerPod, peerIP)

		podLinks = append(podLinks, map[string]interface{}{
			"local_intf": localIntf,
			"local_ip":   localIP,
			"peer_pod":   toCRDPeerPod(peerPod),
			"peer_intf":  peerIntf,
			"peer_ip":    peerIP,
			"uid":        int64(uid),
		})
	}

	annotations := map[string]string{}
	if peerLabel != "" {
		annotations["kubendt/peerlabel"] = peerLabel
	}
	if len(linkNames) > 0 {
		if namesJSON, err := json.Marshal(linkNames); err == nil {
			annotations["kubendt/linknames"] = string(namesJSON)
		}
	}

	metadata := map[string]interface{}{
		"name":      podName,
		"namespace": namespace,
	}

	if len(annotations) > 0 {
		metadata["annotations"] = annotations
	}

	topology := map[string]interface{}{
		"apiVersion": "networkop.co.uk/v1beta1",
		"kind":       "Topology",
		"metadata":   metadata,
		"spec": map[string]interface{}{
			"links": podLinks,
		},
	}

	topologyJSON, _ := json.Marshal(topology)
	err := kubeclient.Clientset.RESTClient().
		Post().
		AbsPath("/apis/networkop.co.uk/v1beta1/namespaces/" + namespace + "/topologies").
		Body(topologyJSON).
		Do(context.TODO()).
		Error()
	if err != nil {
		return fmt.Errorf("error creating topology for %s: %w", podName, err)
	}

	log.Printf("✅ Topology created for %s", podName)
	return nil
}

// fatalImageWaitReasons are container "Waiting" reasons that will never
// resolve on their own: a bad or unreachable image. Detecting them lets us fail
// a deploy in seconds with a precise message instead of stalling for the full
// readiness timeout. CrashLoopBackOff is deliberately excluded: some network-OS
// pods legitimately restart while booting.
var fatalImageWaitReasons = map[string]bool{
	"ErrImagePull":        true,
	"ImagePullBackOff":    true,
	"InvalidImageName":    true,
	"ErrInvalidImageName": true,
}

// fatalPodImageError reports whether any (init) container of the pod is stuck on
// an unrecoverable image error, returning the reason and the kubelet's message.
func fatalPodImageError(pod *v1.Pod) (reason, detail string, bad bool) {
	statuses := append([]v1.ContainerStatus{}, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)
	for _, cs := range statuses {
		if cs.State.Waiting != nil && fatalImageWaitReasons[cs.State.Waiting.Reason] {
			if msg := cs.State.Waiting.Message; msg != "" {
				detail = ": " + msg
			}
			return cs.State.Waiting.Reason, detail, true
		}
	}
	return "", "", false
}

// WaitForPodsReady waits until all pods for all nodes are Running and Ready.
func WaitForPodsReady(namespace string, nodes []types.NodeSpec) error {
	timeout := time.After(180 * time.Second)
	ticker := time.Tick(5 * time.Second)

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for pods to become Ready")

		case <-ticker:
			var readyStats []string
			var notReadyStats []string
			allReady := true

			for _, node := range nodes {
				labelSelector := fmt.Sprintf("app=%s", node.Name)
				podList, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
					LabelSelector: labelSelector,
				})
				if err != nil {
					log.Printf("❌ Error listing pods for %s: %v", node.Name, err)
					allReady = false
					break
				}

				expected := node.Replicas
				runningReady := 0

				for _, pod := range podList.Items {
					// Fail fast on unrecoverable image errors instead of
					// waiting out the whole timeout.
					if reason, detail, bad := fatalPodImageError(&pod); bad {
						return fmt.Errorf("node '%s': pod '%s' cannot start due to an image error (%s)%s", node.Name, pod.Name, reason, detail)
					}
					if pod.Status.Phase == v1.PodRunning {
						for _, cond := range pod.Status.Conditions {
							if cond.Type == v1.PodReady && cond.Status == v1.ConditionTrue {
								runningReady++
								break
							}
						}
					}
				}

				stat := fmt.Sprintf("'%s' %d/%d", node.Name, runningReady, expected)
				if runningReady < expected {
					notReadyStats = append(notReadyStats, stat+" ⏳")
					allReady = false
				} else {
					readyStats = append(readyStats, stat+" ✅")
				}
			}

			allStats := append(readyStats, notReadyStats...)
			log.Printf("📊 Nodes status: %s", strings.Join(allStats, ", "))

			if allReady {
				log.Println("✅ All pods are Running and Ready.")
				return nil
			}
		}
	}
}

func WaitForPodsReadyByName(namespace string, podNames []string) error {
	timeout := time.After(180 * time.Second)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for pods to be Ready: %v", podNames)

		case <-ticker.C:
			allReady := true
			var readyStats []string
			var notReadyStats []string

			for _, podName := range podNames {
				pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					notReadyStats = append(notReadyStats, fmt.Sprintf("'%s' (recreating) ⏳", podName))
					allReady = false
					continue
				}
				if err != nil {
					log.Printf("❌ Error getting pod %s: %v", podName, err)
					notReadyStats = append(notReadyStats, fmt.Sprintf("'%s' (error) ⏳", podName))
					allReady = false
					continue
				}

				if r, detail, bad := fatalPodImageError(pod); bad {
					return fmt.Errorf("pod '%s' cannot start due to an image error (%s)%s", podName, r, detail)
				}

				ok, reason := isPodReady(pod)
				if ok {
					readyStats = append(readyStats, fmt.Sprintf("'%s' ✅", podName))
				} else {
					notReadyStats = append(notReadyStats, fmt.Sprintf("'%s' (%s) ⏳", podName, reason))
					allReady = false
				}
			}

			allStats := append(readyStats, notReadyStats...)
			log.Printf("📊 Pods status: %s", strings.Join(allStats, ", "))

			if allReady {
				log.Printf("✅ All restarted pods are Running/Ready: %v", podNames)
				return nil
			}
		}
	}
}

// validIfaceRe: 1-15 chars, no '/', ':' or whitespace (Linux IFNAMSIZ rules).
var validIfaceRe = regexp.MustCompile(`^[^\s:/]{1,15}$`)

// ValidateLinuxInterfaceName rejects names Linux can't accept as interface
// labels (>15 chars, '/', ':', whitespace, empty).
func ValidateLinuxInterfaceName(name string) error {
	if name == "" {
		return fmt.Errorf("interface name must not be empty")
	}
	if len(name) > 15 {
		return fmt.Errorf("interface name exceeds 15 characters (Linux IFNAMSIZ limit): %q", name)
	}
	if strings.ContainsAny(name, "/: \t\n") {
		return fmt.Errorf("interface name contains invalid characters ('/', ':', whitespace): %q", name)
	}
	if !validIfaceRe.MatchString(name) {
		return fmt.Errorf("interface name %q is not valid for Linux", name)
	}
	return nil
}

// driverInterfaceConstraintsFor looks up a driver by name and returns its
// declared InterfaceNameConstraints (if any). Drivers that don't implement
// InterfaceNameConstrainer impose no extra constraint beyond Linux rules.
func driverInterfaceConstraintsFor(driverName string) (drivers_meta.InterfaceNameConstraints, bool) {
	if driverName == "" {
		return drivers_meta.InterfaceNameConstraints{}, false
	}
	inst, err := drivers_registry.NewByName(driverName)
	if err != nil {
		return drivers_meta.InterfaceNameConstraints{}, false
	}
	c, ok := inst.(drivers_meta.InterfaceNameConstrainer)
	if !ok {
		return drivers_meta.InterfaceNameConstraints{}, false
	}
	return c.InterfaceNameConstraints(), true
}

// ValidateInterfaceNameForDriver applies Linux kernel rules first, then the
// driver-specific constraints (pattern + reserved names) if the driver
// declares any. driverName may be empty (no driver-specific check).
func ValidateInterfaceNameForDriver(name, driverName string) error {
	if err := ValidateLinuxInterfaceName(name); err != nil {
		return err
	}
	// Platform-reserved interfaces, regardless of driver: eth0 is the primary
	// CNI/management interface present on every pod, and lo is loopback.
	if name == "eth0" || name == "lo" {
		return fmt.Errorf("name %q is reserved (primary CNI/management or loopback interface)", name)
	}
	c, ok := driverInterfaceConstraintsFor(driverName)
	if !ok {
		return nil
	}
	if c.Pattern != nil && !c.Pattern.MatchString(name) {
		return fmt.Errorf("name %q does not match pattern %s", name, c.PatternHuman)
	}
	for _, r := range c.Reserved {
		if name == r {
			return fmt.Errorf("name %q is reserved by driver %s", name, driverName)
		}
	}
	return nil
}

// resolveDriverForEndpoint maps a link endpoint (e.g. "router-0" or "router")
// to its driver name using the supplied node list. Returns "" for the
// reserved "external" endpoint.
func resolveDriverForEndpoint(endpoint string, driverByNode map[string]string) string {
	if endpoint == externalNodeName {
		return ""
	}
	if drv, ok := driverByNode[endpoint]; ok {
		return drv
	}
	if base, _, ok := SplitIndexedPodName(endpoint); ok {
		return driverByNode[base]
	}
	return ""
}

// ValidateLinkInterfaceNamesForDrivers checks each link's localIntf against
// the driver of `node`, and peerIntf against the driver of `peerNode`. The
// "external" endpoint is exempt from driver-specific rules.
// Nodes must already have their Driver field resolved.
func ValidateLinkInterfaceNamesForDrivers(links []types.LinkSpec, nodes []types.NodeSpec) error {
	driverByNode := make(map[string]string, len(nodes))
	for _, n := range nodes {
		driverByNode[n.Name] = n.Driver
	}

	for i, link := range links {
		localDrv := resolveDriverForEndpoint(link.Node, driverByNode)
		if err := ValidateInterfaceNameForDriver(link.LocalIntf, localDrv); err != nil {
			return fmt.Errorf("link[%d].localIntf on node %q: %s", i, link.Node, err)
		}

		peerDrv := resolveDriverForEndpoint(link.PeerNode, driverByNode)
		if err := ValidateInterfaceNameForDriver(link.PeerIntf, peerDrv); err != nil {
			return fmt.Errorf("link[%d].peerIntf on node %q: %s", i, link.PeerNode, err)
		}
	}
	return nil
}

// extractExternalSide returns the host iface and the user-supplied peerLabel
// for a link that has "external" on either side. The returned bool tells the
// caller whether external is involved at all. peerLabel is trimmed; an empty
// peerLabel means the link did not pin the iface to any label (the link will
// inherit whatever label the iface already carries in the namespace, or none).
func extractExternalSide(link types.LinkSpec) (peerIntf string, peerLabel string, isExternal bool) {
	switch {
	case link.PeerNode == externalNodeName:
		return strings.TrimSpace(link.PeerIntf), strings.TrimSpace(link.PeerLabel), true
	case link.Node == externalNodeName:
		return strings.TrimSpace(link.LocalIntf), strings.TrimSpace(link.PeerLabel), true
	default:
		return "", "", false
	}
}

// buildNamespaceExternalLabelMaps reads every Topology CRD in the namespace
// and reconstructs the (peerIntf -> peerLabel) and (peerLabel -> peerIntf)
// mappings already in effect. The peerLabel is stored as a per-pod
// annotation (kubendt/peerlabel) and applies to every external link of that
// pod, so we walk spec.links of each pod and pair every "peer_pod=localhost"
// entry's peer_intf with the pod's annotation value.
//
// If pre-existing state already contained a conflict (last-write-wins on
// either map key) we keep whichever value we read last; the validator only
// blocks NEW writes that conflict with the current view, it does not try to
// retroactively fix history.
func buildNamespaceExternalLabelMaps(namespace string) (labelByIntf map[string]string, intfByLabel map[string]string, err error) {
	labelByIntf = make(map[string]string)
	intfByLabel = make(map[string]string)

	topologies, listErr := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	if listErr != nil {
		return nil, nil, fmt.Errorf("could not list topologies for external-label validation: %w", listErr)
	}

	for _, topo := range topologies.Items {
		label := strings.TrimSpace(topo.GetAnnotations()["kubendt/peerlabel"])
		if label == "" {
			continue
		}
		spec, found, _ := unstructured.NestedSlice(topo.Object, "spec", "links")
		if !found {
			continue
		}
		for _, item := range spec {
			linkMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			peerPod, _ := linkMap["peer_pod"].(string)
			if peerPod != "localhost" {
				continue
			}
			peerIntf, _ := linkMap["peer_intf"].(string)
			peerIntf = strings.TrimSpace(peerIntf)
			if peerIntf == "" {
				continue
			}
			labelByIntf[peerIntf] = label
			intfByLabel[label] = peerIntf
		}
	}
	return labelByIntf, intfByLabel, nil
}

// ValidateExternalLabelConsistency enforces a 1:1 mapping between peerLabel
// and peerIntf within a namespace for links touching "external":
//   - A given peerIntf must always carry the same peerLabel.
//   - A given peerLabel must always refer to the same peerIntf.
//
// Links that omit peerLabel are accepted: they will inherit the label that
// the iface already carries (or get a default at write time). Links that
// don't involve "external" are ignored.
//
// The validation considers both the live state of the namespace AND the
// incoming links together, so it catches conflicts that span existing state
// vs new payload and conflicts within the same request.
func ValidateExternalLabelConsistency(namespace string, links []types.LinkSpec) error {
	labelByIntf, intfByLabel, err := buildNamespaceExternalLabelMaps(namespace)
	if err != nil {
		return err
	}

	for i, link := range links {
		peerIntf, peerLabel, isExternal := extractExternalSide(link)
		if !isExternal || peerIntf == "" || peerLabel == "" {
			continue
		}

		if existingLabel, ok := labelByIntf[peerIntf]; ok && existingLabel != peerLabel {
			return fmt.Errorf(
				"link[%d].peerLabel: peerIntf %q is already labelled %q in this namespace; cannot relabel it to %q. "+
					"If these are intentionally different networks (e.g. workers have heterogeneous uplinks on %s), include the distinction in the label itself (for example %q and %q)",
				i, peerIntf, existingLabel, peerLabel, peerIntf,
				existingLabel+" (worker-1)", peerLabel+" (worker-2)",
			)
		}
		if existingIntf, ok := intfByLabel[peerLabel]; ok && existingIntf != peerIntf {
			return fmt.Errorf(
				"link[%d].peerIntf: peerLabel %q is already bound to peerIntf %q in this namespace; cannot reuse it with peerIntf %q. "+
					"If these are intentionally different networks, use distinct labels for each one",
				i, peerLabel, existingIntf, peerIntf,
			)
		}

		labelByIntf[peerIntf] = peerLabel
		intfByLabel[peerLabel] = peerIntf
	}
	return nil
}
