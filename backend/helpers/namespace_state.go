package helpers

import (
	"context"
	"fmt"

	"kubendt/kubeclient"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
)

// Whether a namespace holds a topology is read from the cluster, the
// StatefulSets and Topology objects a deploy creates, so every backend sees
// the same answer and nothing has to be kept in step with it.
var (
	statefulSets = func(namespace string) appsv1client.StatefulSetInterface {
		return kubeclient.Clientset.AppsV1().StatefulSets(namespace)
	}
	topologyObjects = func(namespace string) dynamic.ResourceInterface {
		return kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace)
	}
)

func NamespaceHasTopology(namespace string) (bool, error) {
	sts, err := statefulSets(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("could not list the StatefulSets of %s: %w", namespace, err)
	}
	if len(sts.Items) > 0 {
		return true, nil
	}
	tops, err := topologyObjects(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("could not list the Topology objects of %s: %w", namespace, err)
	}
	return len(tops.Items) > 0, nil
}
