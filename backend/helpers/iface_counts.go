package helpers

import (
	"context"
	"fmt"
	"strconv"

	"kubendt/kubeclient"
	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// IfaceCountsConfigMapName is the well-known name of the kubendt-internal
// ConfigMap that holds the per-pod expected dataplane interface counts. It
// is mounted read-only at /etc/kubendt/iface-counts/ inside every QEMU pod
// (see CreateNetworkStatefulSet). The QEMU entrypoint reads the file whose
// name matches the pod's own hostname (StatefulSet guarantees hostname ==
// pod name) and waits until pod-side veths reach that count before
// snapshotting and launching QEMU.
//
// Why a separate ConfigMap from the user-facing file-manager ones:
//   - This one is kubendt's own internal state, users never touch it.
//   - The naming + labels (kubendt/internal=true, managed-by=kubendt-backend)
//     make it trivially filterable so it doesn't visually collide with
//     namespace file-manager ConfigMaps in kubectl listings.
//   - Pods mount it with Optional=true so a missing ConfigMap doesn't block
//     them; the entrypoint falls back to a fixed sleep in that case.
const IfaceCountsConfigMapName = "kubendt-internal-iface-counts"

// UpdateIfaceCountsConfigMap rebuilds the per-pod expected-iface-count
// ConfigMap from the current set of Topology CRDs in the namespace. Only
// QEMU-flagged pods are recorded (non-QEMU pods don't need a snapshot
// wait, their data interfaces are picked up dynamically by user-space
// at runtime).
//
// `nodes` carries the QEMU flag per node-base-name. Callers MUST pass the
// authoritative node list because StatefulSets may not exist yet at call
// time (deploy publishes this ConfigMap before creating StatefulSets so
// kubelet sees the data when it mounts the volume into new pods).
//
// Called after every topology mutation (deploy, modify-add, modify-delete,
// modify-scale-up, modify-scale-down) by the corresponding handler.
// Idempotent: if nothing changed, the resulting Data map is the same and
// the Update call is a no-op.
func UpdateIfaceCountsConfigMap(namespace string, nodes []types.NodeSpec) error {
	topologies, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("could not list topologies: %w", err)
	}

	isQemuByBase := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		isQemuByBase[n.Name] = n.Qemu
	}

	data := make(map[string]string)
	for _, item := range topologies.Items {
		podName := item.GetName()
		base, _, ok := SplitIndexedPodName(podName)
		if !ok {
			base = podName
		}
		if !isQemuByBase[base] {
			continue
		}
		linkCount, err := readLinkCountFromTopology(&item)
		if err != nil {
			// Skip pods with malformed CRDs rather than failing the whole
			// update; the entrypoint will fall back to its fixed sleep.
			continue
		}
		data[podName] = strconv.Itoa(linkCount)
	}

	desired := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      IfaceCountsConfigMapName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "kubendt-backend",
				"kubendt/internal":             "true",
			},
		},
		Data: data,
	}

	existing, err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Get(context.TODO(), IfaceCountsConfigMapName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Create(context.TODO(), desired, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("could not create %s ConfigMap: %w", IfaceCountsConfigMapName, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not read %s ConfigMap: %w", IfaceCountsConfigMapName, err)
	}

	existing.Labels = desired.Labels
	existing.Data = desired.Data
	if _, err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Update(context.TODO(), existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("could not update %s ConfigMap: %w", IfaceCountsConfigMapName, err)
	}
	return nil
}

// DeleteIfaceCountsConfigMap removes the per-pod expected-iface-count
// ConfigMap. Called on ClearTopology and DeleteNamespace flows so it
// doesn't linger around as orphan state when the topology goes away.
// Safe to call even if the ConfigMap doesn't exist.
func DeleteIfaceCountsConfigMap(namespace string) error {
	err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Delete(context.TODO(), IfaceCountsConfigMapName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("could not delete %s ConfigMap: %w", IfaceCountsConfigMapName, err)
	}
	return nil
}

// readLinkCountFromTopology returns len(spec.links) for a Topology CRD,
// tolerating missing fields by treating them as zero links.
func readLinkCountFromTopology(item *unstructured.Unstructured) (int, error) {
	spec, ok := item.Object["spec"].(map[string]interface{})
	if !ok {
		return 0, nil
	}
	links, ok := spec["links"].([]interface{})
	if !ok {
		return 0, nil
	}
	return len(links), nil
}
