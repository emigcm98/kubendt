package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"kubendt/kubeclient"
	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	k8stypes "k8s.io/apimachinery/pkg/types"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

const (
	PlacementPack   = "pack"
	PlacementSpread = "spread"
	// The operator's default when a topology names no placement.
	PlacementEnv = "KUBENDT_PLACEMENT"
	// Kept on the namespace so a modify's add follows the deploy and the
	// export carries it.
	PlacementAnnotation = "kubendt/placement"
)

// ResolvePlacement is the placement a deploy uses: the request's word, else
// the operator's default, else none. Anything else is an error that names
// where the word came from.
func ResolvePlacement(requested string) (string, error) {
	if p, ok := placementWord(requested); !ok {
		return "", fmt.Errorf("placement %q is not one of %s or %s", requested, PlacementPack, PlacementSpread)
	} else if p != "" {
		return p, nil
	}
	env := os.Getenv(PlacementEnv)
	p, ok := placementWord(env)
	if !ok {
		return "", fmt.Errorf("%s=%q is not one of %s or %s", PlacementEnv, env, PlacementPack, PlacementSpread)
	}
	return p, nil
}

func placementWord(raw string) (string, bool) {
	p := strings.ToLower(strings.TrimSpace(raw))
	switch p {
	case "", PlacementPack, PlacementSpread:
		return p, true
	}
	return "", false
}

// Swapped by the tests for client-go's fake.
var placementNamespaces = func() corev1.NamespaceInterface { return kubeclient.Clientset.CoreV1().Namespaces() }

// SetNamespacePlacement writes the placement on the namespace, or removes it
// when empty.
func SetNamespacePlacement(namespace, placement string) error {
	var value any
	if placement != "" {
		value = placement
	}
	patch, err := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{PlacementAnnotation: value}}})
	if err != nil {
		return err
	}
	_, err = placementNamespaces().Patch(context.TODO(), namespace, k8stypes.MergePatchType, patch, metav1.PatchOptions{})
	if placement == "" && apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// NamespacePlacement is the placement the namespace was deployed with, empty
// when none or when the namespace cannot be read.
func NamespacePlacement(namespace string) string {
	ns, err := placementNamespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	p, _ := placementWord(ns.Annotations[PlacementAnnotation])
	return p
}

// placementAffinity is a placement as the scheduler takes it: a preference,
// weight 100, toward or away from the namespace's own pods on one worker. A
// preference, so a lab that does not fit still deploys.
func placementAffinity(placement string) *v1.Affinity {
	term := []v1.WeightedPodAffinityTerm{{
		Weight: 100,
		PodAffinityTerm: v1.PodAffinityTerm{
			LabelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "kubendt/type", Operator: metav1.LabelSelectorOpExists}},
			},
			TopologyKey: "kubernetes.io/hostname",
		},
	}}
	switch placement {
	case PlacementPack:
		return &v1.Affinity{PodAffinity: &v1.PodAffinity{PreferredDuringSchedulingIgnoredDuringExecution: term}}
	case PlacementSpread:
		return &v1.Affinity{PodAntiAffinity: &v1.PodAntiAffinity{PreferredDuringSchedulingIgnoredDuringExecution: term}}
	}
	return nil
}

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
