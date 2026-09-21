package helpers

import (
	"strings"
	"testing"

	"kubendt/types"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func topologyObject(pod string, intfs ...string) unstructured.Unstructured {
	links := make([]interface{}, 0, len(intfs))
	for _, i := range intfs {
		links = append(links, map[string]interface{}{"local_intf": i, "peer_pod": "x", "peer_intf": "eth9"})
	}
	return unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": pod},
		"spec":     map[string]interface{}{"links": links},
	}}
}

func TestClaimedInterfacesKeepsOnlyRequestedPods(t *testing.T) {
	items := []unstructured.Unstructured{
		topologyObject("r-0", "eth1", "eth2"),
		topologyObject("h-0", "eth1"),
		topologyObject("sw-0"),
	}
	podSet := map[string]struct{}{"r-0": {}, "sw-0": {}, "missing-0": {}}

	got := claimedInterfaces(items, podSet)

	if len(got) != 2 {
		t.Fatalf("want claims for r-0 and sw-0 only, got %v", got)
	}
	if _, ok := got["r-0"]["eth2"]; !ok || len(got["r-0"]) != 2 {
		t.Errorf("r-0 claims = %v, want eth1 and eth2", got["r-0"])
	}
	if len(got["sw-0"]) != 0 {
		t.Errorf("sw-0 has no links, claims = %v", got["sw-0"])
	}
	if _, ok := got["h-0"]; ok {
		t.Errorf("h-0 was not in the request and must not be seeded")
	}
}

func TestCheckInterfaceConflicts(t *testing.T) {
	nodes := []types.NodeSpec{{Name: "r", Type: "router", Replicas: 1}, {Name: "h", Type: "host", Replicas: 2}}
	link := func(a, ai, b, bi string) types.LinkSpec {
		return types.LinkSpec{Node: a, LocalIntf: ai, PeerNode: b, PeerIntf: bi}
	}
	existing := func() map[string]map[string]struct{} {
		return map[string]map[string]struct{}{"r-0": {"eth1": {}}}
	}

	cases := []struct {
		name    string
		links   []types.LinkSpec
		wantErr string
	}{
		{"free interfaces pass", []types.LinkSpec{link("r", "eth2", "h-0", "eth1"), link("r", "eth3", "h-1", "eth1")}, ""},
		{"claimed by existing topology", []types.LinkSpec{link("r", "eth1", "h-0", "eth1")}, "link[0]: interface 'eth1' is already in use on pod 'r-0'"},
		{"duplicate inside the payload", []types.LinkSpec{link("r", "eth2", "h-0", "eth1"), link("h-1", "eth1", "r", "eth2")}, "link[1]: interface 'eth2' is already in use on pod 'r-0'"},
		{"external side is never a conflict", []types.LinkSpec{link("h-0", "eth1", "external", "ens3"), link("h-1", "eth1", "external", "ens3")}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkInterfaceConflicts(tc.links, nodes, existing())
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("got %v, want %q", err, tc.wantErr)
			}
		})
	}
}
