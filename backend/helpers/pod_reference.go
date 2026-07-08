package helpers

import (
	"context"
	"fmt"

	"kubendt/kubeclient"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResolvePodReference accepts either an exact pod name (e.g. web-server-0)
// or a base node name (e.g. web-server) when that node has exactly one pod.
func ResolvePodReference(namespace, podRef string) (string, error) {
	if podRef == "" {
		return "", fmt.Errorf("pod reference cannot be empty")
	}

	if _, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), podRef, metav1.GetOptions{}); err == nil {
		return podRef, nil
	}

	if _, _, indexed := SplitIndexedPodName(podRef); indexed {
		return "", fmt.Errorf("pod '%s' not found in namespace '%s'", podRef, namespace)
	}

	// All StatefulSet pods created by KubeNDT carry app=<node name>.
	podList, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", podRef),
	})
	if err != nil {
		return "", fmt.Errorf("error resolving pod '%s': %w", podRef, err)
	}

	if len(podList.Items) == 1 {
		return podList.Items[0].Name, nil
	}

	if len(podList.Items) > 1 {
		return "", fmt.Errorf("pod reference '%s' is ambiguous (%d replicas); use an explicit pod name like '%s-0'", podRef, len(podList.Items), podRef)
	}

	return "", fmt.Errorf("pod '%s' not found in namespace '%s'", podRef, namespace)
}
