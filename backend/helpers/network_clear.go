package helpers

import (
	"context"
	"fmt"
	"log"
	"time"

	"kubendt/kubeclient"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RollbackNamespaceTopology tears down the resources a failed deploy created so
// the namespace is left clean, keeping saved positions and uploaded files so the
// user can fix the input and re-import. Best-effort: it logs and continues past
// individual cleanup errors.
func RollbackNamespaceTopology(namespace string) {
	if err := ClearNamespaceTopologyResources(namespace, false, false); err != nil {
		log.Printf("⚠️ Rollback incomplete for '%s': %v", namespace, err)
	}
	if _, err := SyncNamespaceTopologyState(namespace); err != nil {
		log.Printf("⚠️ Could not refresh topology state during rollback for '%s': %v", namespace, err)
	}
	if err := DeleteNamespaceLinkUIDRegistry(namespace); err != nil {
		log.Printf("⚠️ Could not clean link UID registry during rollback for '%s': %v", namespace, err)
	}
	if err := DeleteNamespaceDriverOperationHistory(namespace); err != nil {
		log.Printf("⚠️ Could not clean driver history during rollback for '%s': %v", namespace, err)
	}
	if err := DeleteIfaceCountsConfigMap(namespace); err != nil {
		log.Printf("⚠️ Could not delete iface counts ConfigMap during rollback for '%s': %v", namespace, err)
	}
}

// ClearNamespaceTopologyResources removes all topology-related Kubernetes resources for the namespace.
// deletePositions: if true, also removes saved node positions from the database.
// deleteFiles: if true, removes all files stored in the namespace file-manager directory.
func ClearNamespaceTopologyResources(namespace string, deletePositions, deleteFiles bool) error {
	ctx := context.TODO()

	stsList, err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing StatefulSets: %w", err)
	}

	configMapsToDelete := make(map[string]struct{})
	secretsToDelete := make(map[string]struct{})
	deletedStatefulSets := make(map[string]struct{})
	for _, sts := range stsList.Items {
		deletedStatefulSets[sts.Name] = struct{}{}
		for _, container := range sts.Spec.Template.Spec.Containers {
			for _, envFrom := range container.EnvFrom {
				if envFrom.ConfigMapRef != nil && envFrom.ConfigMapRef.Name != "" {
					configMapsToDelete[envFrom.ConfigMapRef.Name] = struct{}{}
				}
			}
		}

		for _, volume := range sts.Spec.Template.Spec.Volumes {
			if volume.ConfigMap != nil && volume.ConfigMap.Name != "" {
				configMapsToDelete[volume.ConfigMap.Name] = struct{}{}
			}
			if volume.Secret != nil && volume.Secret.SecretName != "" {
				secretsToDelete[volume.Secret.SecretName] = struct{}{}
			}
		}
	}

	for _, sts := range stsList.Items {
		if err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).Delete(ctx, sts.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("error deleting StatefulSet %s: %w", sts.Name, err)
		}
	}

	topologyList, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing Topology resources: %w", err)
	}

	deletedTopologies := make(map[string]struct{})
	for _, topology := range topologyList.Items {
		deletedTopologies[topology.GetName()] = struct{}{}
	}

	for _, topology := range topologyList.Items {
		if err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Delete(ctx, topology.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("error deleting Topology %s: %w", topology.GetName(), err)
		}
	}

	if err := waitForTopologyResourcesDeletion(ctx, namespace, deletedStatefulSets, deletedTopologies, 120*time.Second); err != nil {
		return err
	}

	for configMapName := range configMapsToDelete {
		if err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, configMapName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("error deleting ConfigMap %s: %w", configMapName, err)
		}
	}
	for secretName := range secretsToDelete {
		if err := kubeclient.Clientset.CoreV1().Secrets(namespace).Delete(ctx, secretName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("error deleting Secret %s: %w", secretName, err)
		}
	}

	if deletePositions {
		if err := DeletePositionsByNamespace(namespace); err != nil {
			return fmt.Errorf("error deleting stored positions for namespace %s: %w", namespace, err)
		}
	}

	if deleteFiles {
		if err := DeleteNamespaceFilesAll(namespace); err != nil {
			return fmt.Errorf("error deleting files for namespace %s: %w", namespace, err)
		}
	}

	if err := DeleteNamespaceLinkUIDRegistry(namespace); err != nil {
		return fmt.Errorf("error deleting link UID registry for namespace %s: %w", namespace, err)
	}

	if err := SetNamespaceHasTopology(namespace, false); err != nil {
		return fmt.Errorf("error resetting topology state: %w", err)
	}

	return nil
}

func waitForTopologyResourcesDeletion(ctx context.Context, namespace string, deletedStatefulSets, deletedTopologies map[string]struct{}, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for {
		statefulSetsGone, err := trackedStatefulSetsDeleted(ctx, namespace, deletedStatefulSets)
		if err != nil {
			return err
		}

		podsGone, err := trackedPodsDeleted(ctx, namespace, deletedStatefulSets)
		if err != nil {
			return err
		}

		topologiesGone, err := trackedTopologiesDeleted(ctx, namespace, deletedTopologies)
		if err != nil {
			return err
		}

		if statefulSetsGone && podsGone && topologiesGone {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for topology resources in namespace %s to terminate", namespace)
		}

		time.Sleep(1 * time.Second)
	}
}

func trackedStatefulSetsDeleted(ctx context.Context, namespace string, tracked map[string]struct{}) (bool, error) {
	if len(tracked) == 0 {
		return true, nil
	}

	stsList, err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("error listing StatefulSets while waiting for clear-topology: %w", err)
	}

	for _, sts := range stsList.Items {
		if _, ok := tracked[sts.Name]; ok {
			return false, nil
		}
	}

	return true, nil
}

func trackedPodsDeleted(ctx context.Context, namespace string, trackedStatefulSets map[string]struct{}) (bool, error) {
	if len(trackedStatefulSets) == 0 {
		return true, nil
	}

	podList, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("error listing Pods while waiting for clear-topology: %w", err)
	}

	for _, pod := range podList.Items {
		if podBelongsToTrackedStatefulSet(pod, trackedStatefulSets) {
			return false, nil
		}
	}

	return true, nil
}

func trackedTopologiesDeleted(ctx context.Context, namespace string, tracked map[string]struct{}) (bool, error) {
	if len(tracked) == 0 {
		return true, nil
	}

	topologyList, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("error listing Topologies while waiting for clear-topology: %w", err)
	}

	for _, topology := range topologyList.Items {
		if _, ok := tracked[topology.GetName()]; ok {
			return false, nil
		}
	}

	return true, nil
}

func podBelongsToTrackedStatefulSet(pod v1.Pod, trackedStatefulSets map[string]struct{}) bool {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "StatefulSet" {
			if _, ok := trackedStatefulSets[owner.Name]; ok {
				return true
			}
		}
	}

	if appName := pod.Labels["app"]; appName != "" {
		if _, ok := trackedStatefulSets[appName]; ok {
			return true
		}
	}

	return false
}
