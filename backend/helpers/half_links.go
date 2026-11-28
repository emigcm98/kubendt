package helpers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kubendt/kubeclient"
	"kubendt/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// halfDeclaredEnds finds, for the links of an add, the ends a Topology declares
// while the other end's Topology does not: what is left by a modify that
// stopped halfway, since a link is two entries written one after the other. It
// returns each such pod with the keys of its stale entries. A link with a pod
// on one side only (an uplink) has one entry by design and is never half.
func halfDeclaredEnds(namespace string, links []types.LinkSpec, nodes []types.NodeSpec) (map[string][]string, error) {
	stale := map[string][]string{}
	if len(links) == 0 {
		return stale, nil
	}
	list, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("error listing topologies: %w", err)
	}
	declared := declaredLinkKeys(list.Items)
	for _, link := range links {
		ends := buildPerPodLinkEntries([]types.LinkSpec{link}, nodes)
		if len(ends) != 2 {
			continue
		}
		var present []string
		var pods []string
		for pod := range ends {
			pods = append(pods, pod)
		}
		sort.Strings(pods)
		for _, pod := range pods {
			key := topologyLinkKeyForModify(ends[pod][0])
			if _, ok := declared[pod][key]; ok {
				present = append(present, pod)
			}
		}
		if len(present) == 1 {
			pod := present[0]
			stale[pod] = append(stale[pod], topologyLinkKeyForModify(ends[pod][0]))
		}
	}
	return stale, nil
}

// staleInterfaces is the interface names of the stale entries, by pod. A key
// is the interface, the peer pod and the peer interface.
func staleInterfaces(stale map[string][]string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for pod, keys := range stale {
		out[pod] = map[string]bool{}
		for _, key := range keys {
			intf, _, _ := strings.Cut(key, "|")
			out[pod][intf] = true
		}
	}
	return out
}

// dropLinkEnds takes the stale entries out of their Topologies.
func dropLinkEnds(namespace string, stale map[string][]string) error {
	for pod, keys := range stale {
		drop := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			drop[key] = struct{}{}
		}
		_, err := updateTopology(namespace, pod, func(obj *unstructured.Unstructured) (bool, error) {
			existing, _, _ := unstructured.NestedSlice(obj.Object, "spec", "links")
			kept := make([]interface{}, 0, len(existing))
			changed := false
			for _, item := range existing {
				if linkMap, ok := item.(map[string]interface{}); ok {
					if _, gone := drop[topologyLinkKeyForModify(linkMap)]; gone {
						changed = true
						continue
					}
				}
				kept = append(kept, item)
			}
			if !changed {
				return false, nil
			}
			return true, unstructured.SetNestedSlice(obj.Object, kept, "spec", "links")
		})
		if err != nil {
			return fmt.Errorf("error dropping the stale end of a link from Topology %s: %w", pod, err)
		}
	}
	return nil
}
