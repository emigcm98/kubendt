package helpers

import (
	"context"
	"fmt"

	"kubendt/kubeclient"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	allStatefulSets = func() appsv1client.StatefulSetInterface {
		return kubeclient.Clientset.AppsV1().StatefulSets(metav1.NamespaceAll)
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

// LabSize is what a namespace's lab holds: its nodes counted as the replicas
// declared, and its links once each.
type LabSize struct {
	HasTopology bool
	Pods        int
	Links       int
}

// LabSizes reads every namespace's lab from two cluster-wide lists, so the
// namespace list costs the same however many namespaces there are.
func LabSizes(ctx context.Context) (map[string]*LabSize, error) {
	sizes := map[string]*LabSize{}
	of := func(ns string) *LabSize {
		if sizes[ns] == nil {
			sizes[ns] = &LabSize{}
		}
		return sizes[ns]
	}
	sts, err := allStatefulSets().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not list the StatefulSets: %w", err)
	}
	for _, s := range sts.Items {
		size := of(s.Namespace)
		size.HasTopology = true
		if s.Spec.Replicas != nil {
			size.Pods += int(*s.Spec.Replicas)
		} else {
			size.Pods++
		}
	}
	tops, err := allTopologies().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not list the Topology objects: %w", err)
	}
	byNamespace := map[string][]unstructured.Unstructured{}
	for _, item := range tops.Items {
		byNamespace[item.GetNamespace()] = append(byNamespace[item.GetNamespace()], item)
	}
	for ns, items := range byNamespace {
		size := of(ns)
		size.HasTopology = true
		size.Links = countTopologyLinks(items)
	}
	return sizes, nil
}

func countTopologyLinks(items []unstructured.Unstructured) int {
	seen := map[string]bool{}
	for _, item := range items {
		links, _, _ := unstructured.NestedSlice(item.Object, "spec", "links")
		for _, l := range links {
			m, ok := l.(map[string]interface{})
			if !ok {
				continue
			}
			seen[linkKeyBothEnds(item.GetName(), fmt.Sprint(m["local_intf"]), fmt.Sprint(m["peer_pod"]), fmt.Sprint(m["peer_intf"]))] = true
		}
	}
	return len(seen)
}
