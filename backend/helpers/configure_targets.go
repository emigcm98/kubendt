package helpers

import "kubendt/types"

// MergeTargetsByPod folds the targets that name the same pod into one, in
// request order. A pod can appear twice by hand or through a link entry that
// lands on a pod already in targets, and two targets for one pod would be
// two jobs run at once, with their actions persisted in whichever order the
// goroutines reached the database. A reference that does not resolve keeps
// its own target, so it fails where it did before.
func MergeTargetsByPod(namespace string, targets []types.PodConfig) []types.PodConfig {
	return mergeTargets(targets, func(ref string) string {
		if name, err := ResolvePodReference(namespace, ref); err == nil {
			return name
		}
		return ref
	})
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
