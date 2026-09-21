package helpers

import (
	"sort"

	"kubendt/types"
)

// EndpointsToRestartForNewLinks picks the pods to recreate so that Meshnet wires
// links added between pods that already exist. Meshnet only creates a link on a
// CNI ADD, so a link between two running pods never appears on its own; one of
// the two has to be recreated. A link with a new pod on either side needs
// nothing, that pod's first CNI ADD creates both ends, and a link whose endpoint
// is already going to be restarted is covered by that restart. When both ends
// are candidates, the heal pass's rule picks the cheaper one, and one restart is
// reused for every link it touches.
func EndpointsToRestartForNewLinks(links []types.LinkSpec, nodes []types.NodeSpec, newPods, restarting map[string]struct{}) []string {
	podType := buildPodTypeMap(nodes)
	chosen := make(map[string]struct{})
	covered := func(pod string) bool {
		_, r := restarting[pod]
		_, c := chosen[pod]
		return r || c
	}
	for _, l := range links {
		a := ResolvePodNameFromLink(l.Node, nodes)
		b := ResolvePodNameFromLink(l.PeerNode, nodes)
		if a == "" || b == "" {
			continue
		}
		if _, isNew := newPods[a]; isNew {
			continue
		}
		if _, isNew := newPods[b]; isNew {
			continue
		}
		if covered(a) || covered(b) {
			continue
		}
		switch {
		case a == "external" && b == "external":
			continue
		case a == "external":
			chosen[b] = struct{}{}
		case b == "external":
			chosen[a] = struct{}{}
		default:
			chosen[chooseRestartEndpoint(a, b, []string{l.LocalIntf}, []string{l.PeerIntf}, nil, podType)] = struct{}{}
		}
	}
	out := make([]string, 0, len(chosen))
	for p := range chosen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
