package helpers

import (
	"reflect"
	"testing"

	"kubendt/types"
)

func TestEndpointsToRestartForNewLinks(t *testing.T) {
	nodes := []types.NodeSpec{
		{Name: "h", Type: "host", Replicas: 3},
		{Name: "r", Type: "router", Replicas: 1},
		{Name: "sw", Type: "switch", Replicas: 1},
	}
	link := func(a, ai, b, bi string) types.LinkSpec {
		return types.LinkSpec{Node: a, LocalIntf: ai, PeerNode: b, PeerIntf: bi}
	}
	none := map[string]struct{}{}

	cases := []struct {
		name       string
		links      []types.LinkSpec
		newPods    map[string]struct{}
		restarting map[string]struct{}
		want       []string
	}{
		{"two existing hosts: one of them", []types.LinkSpec{link("h-0", "eth1", "h-1", "eth1")}, none, none, []string{"h-0"}},
		{"host and router: the host", []types.LinkSpec{link("r-0", "eth2", "h-2", "eth1")}, none, none, []string{"h-2"}},
		{"switch endpoint preferred", []types.LinkSpec{link("h-0", "eth1", "sw-0", "eth3")}, none, none, []string{"sw-0"}},
		{"new pod on one side: nothing", []types.LinkSpec{link("h-2", "eth1", "r-0", "eth3")}, map[string]struct{}{"h-2": {}}, none, nil},
		{"endpoint already restarting covers it", []types.LinkSpec{link("h-0", "eth1", "r-0", "eth3")}, none, map[string]struct{}{"r-0": {}}, nil},
		{"external peer: the pod", []types.LinkSpec{link("r-0", "eth4", "external", "ens18")}, none, none, []string{"r-0"}},
		{"base name resolves to the first replica", []types.LinkSpec{link("r", "eth2", "sw", "eth1")}, none, none, []string{"sw-0"}},
		{"one restart shared by two links", []types.LinkSpec{link("h-0", "eth1", "h-1", "eth1"), link("h-0", "eth2", "h-2", "eth1")}, none, none, []string{"h-0"}},
	}
	for _, c := range cases {
		got := EndpointsToRestartForNewLinks(c.links, nodes, c.newPods, c.restarting)
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
