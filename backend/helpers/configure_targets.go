package helpers

import (
	"context"

	"kubendt/kubeclient"
	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MergeTargetsByPod folds targets that name the same pod ("router1" and
// "router1-0", or a links entry on a pod already listed) into one, in request
// order, so they run as one job. One pod listing resolves every reference.
func MergeTargetsByPod(namespace string, targets []types.PodConfig) []types.PodConfig {
	if len(targets) < 2 {
		return targets
	}
	list, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return mergeTargets(targets, func(ref string) string { return ref })
	}
	return mergeTargets(targets, podResolver(list.Items))
}

// podResolver is ResolvePodReference over a listing: exact name, or the base
// node name when it has one pod, anything else left as written.
func podResolver(pods []v1.Pod) func(string) string {
	exact := map[string]bool{}
	byNode := map[string][]string{}
	for _, p := range pods {
		exact[p.Name] = true
		if node := p.Labels["app"]; node != "" {
			byNode[node] = append(byNode[node], p.Name)
		}
	}
	return func(ref string) string {
		if exact[ref] {
			return ref
		}
		if _, _, indexed := SplitIndexedPodName(ref); indexed {
			return ref
		}
		if names := byNode[ref]; len(names) == 1 {
			return names[0]
		}
		return ref
	}
}

func mergeTargets(targets []types.PodConfig, resolve func(string) string) []types.PodConfig {
	out := make([]types.PodConfig, 0, len(targets))
	index := map[string]int{}
	for _, t := range targets {
		key := resolve(t.Pod)
		if i, ok := index[key]; ok {
			out[i].Actions = append(out[i].Actions, t.Actions...)
			continue
		}
		index[key] = len(out)
		out = append(out, types.PodConfig{Pod: t.Pod, Actions: append([]types.ActionEntry{}, t.Actions...)})
	}
	return out
}
