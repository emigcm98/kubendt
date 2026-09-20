package helpers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kubendt/kubeclient"
	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// ValidateNodePlacement checks every nodeName and nodeSelector in the request
// against the cluster before anything is created. Kubernetes itself would not
// complain: a pod with an unknown nodeName or an unsatisfiable selector just
// sits Pending until the readiness wait gives up, so the mistake is caught
// here with a message that names the fix. One Nodes list per request.
func ValidateNodePlacement(specs []types.NodeSpec) error {
	constrained := false
	for _, n := range specs {
		if n.NodeName != "" || len(n.NodeSelector) > 0 {
			constrained = true
			break
		}
	}
	if !constrained {
		return nil
	}
	list, err := kubeclient.Clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("could not list cluster nodes to validate placement: %w", err)
	}
	return checkPlacement(specs, list.Items)
}

// checkPlacement is the cluster-independent half of ValidateNodePlacement.
func checkPlacement(specs []types.NodeSpec, clusterNodes []v1.Node) error {
	byName := make(map[string]*v1.Node, len(clusterNodes))
	names := make([]string, 0, len(clusterNodes))
	for i := range clusterNodes {
		byName[clusterNodes[i].Name] = &clusterNodes[i]
		names = append(names, clusterNodes[i].Name)
	}
	sort.Strings(names)

	for _, spec := range specs {
		selector := labels.SelectorFromSet(labels.Set(spec.NodeSelector))

		if spec.NodeName != "" {
			target, ok := byName[spec.NodeName]
			if !ok {
				return fmt.Errorf("node '%s': nodeName '%s' does not exist in the cluster (nodes: %s)", spec.Name, spec.NodeName, strings.Join(names, ", "))
			}
			if len(spec.NodeSelector) > 0 && !selector.Matches(labels.Set(target.Labels)) {
				return fmt.Errorf("node '%s': cluster node '%s' does not carry the labels in nodeSelector %s", spec.Name, spec.NodeName, selector.String())
			}
			continue
		}

		if len(spec.NodeSelector) > 0 {
			matched := false
			for i := range clusterNodes {
				if nodeIsReady(&clusterNodes[i]) && selector.Matches(labels.Set(clusterNodes[i].Labels)) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("node '%s': no Ready cluster node matches nodeSelector %s (label one with 'kubectl label node <name> %s' or drop the selector)", spec.Name, selector.String(), selector.String())
			}
		}
	}
	return nil
}

func nodeIsReady(node *v1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == v1.NodeReady {
			return c.Status == v1.ConditionTrue
		}
	}
	return false
}
