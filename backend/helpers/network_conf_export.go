package helpers

import (
	"fmt"

	"kubendt/types"
)

// ExportNetworkConf turns the persisted history of a namespace into the
// configure request that reproduces it: one target per pod that still
// exists, with its operations in the order they were applied. Loading the
// result back is the same as the replay a restart does.
func ExportNetworkConf(namespace string) (types.ConfigureNetworkRequest, error) {
	ops, err := ListDriverOperationsForNamespace(namespace)
	if err != nil {
		return types.ConfigureNetworkRequest{}, err
	}
	nodes, err := GetExistingNodes(namespace)
	if err != nil {
		return types.ConfigureNetworkRequest{}, err
	}
	existing := map[string]bool{}
	for _, n := range nodes {
		for i := 0; i < n.Replicas; i++ {
			existing[fmt.Sprintf("%s-%d", n.Name, i)] = true
		}
	}
	return buildNetworkConf(ops, existing), nil
}

// buildNetworkConf groups operations by pod, first pod first, keeping only
// pods in existing. Options are dropped: everything in the history was
// persisted, and output capture is a request-time choice.
func buildNetworkConf(ops []PersistedDriverOperation, existing map[string]bool) types.ConfigureNetworkRequest {
	req := types.ConfigureNetworkRequest{Targets: []types.PodConfig{}}
	index := map[string]int{}
	for _, op := range ops {
		if !existing[op.PodName] {
			continue
		}
		action := op.Action
		if action.Type == "" {
			action.Type = op.ActionType
		}
		action.Options = nil
		i, ok := index[op.PodName]
		if !ok {
			i = len(req.Targets)
			index[op.PodName] = i
			req.Targets = append(req.Targets, types.PodConfig{Pod: op.PodName, Actions: []types.ActionEntry{}})
		}
		req.Targets[i].Actions = append(req.Targets[i].Actions, action)
	}
	return req
}
