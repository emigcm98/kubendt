package helpers

import (
	"reflect"
	"strings"
	"testing"

	"kubendt/types"
)

func TestValidateLinkAttributes(t *testing.T) {
	cases := []struct {
		name    string
		link    types.LinkSpec
		wantErr string
	}{
		{"nothing declared", types.LinkSpec{Node: "a", LocalIntf: "eth1"}, ""},
		{"delay and bandwidth", types.LinkSpec{Delay: "10ms", Bandwidth: "50mbit"}, ""},
		{"full", types.LinkSpec{Delay: "10ms", Jitter: "2ms", Loss: "0.5%", Bandwidth: "50mbit"}, ""},
		{"bad delay", types.LinkSpec{Delay: "fast"}, `link[0]: delay "fast" is not a time`},
		{"jitter without delay", types.LinkSpec{Jitter: "2ms"}, "link[0]: jitter requires a delay"},
		{"bad bandwidth", types.LinkSpec{Bandwidth: "0mbit"}, "link[0]: bandwidth must be greater than zero"},
		{"loss out of range", types.LinkSpec{Loss: "150%"}, "link[0]: loss"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLinkAttributes([]types.LinkSpec{tc.link})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

// Declared attributes become a symmetric netem on both ends, resolved to pod
// names, and the node's end only toward the external network.
func TestLinkAttributeActions(t *testing.T) {
	nodes := []types.NodeSpec{{Name: "room-router", Replicas: 1}, {Name: "dist-sw", Replicas: 1}, {Name: "edge-router", Replicas: 1}, {Name: "user", Replicas: 4}}
	links := []types.LinkSpec{
		{Node: "dist-sw", LocalIntf: "eth3", PeerNode: "room-router", PeerIntf: "eth1", Delay: "10ms", Bandwidth: "50mbit"},
		{Node: "edge-router", LocalIntf: "eth1", PeerNode: "external", PeerIntf: "ens18", Loss: "1%"},
		{Node: "room-router", LocalIntf: "eth2", PeerNode: "user-2", PeerIntf: "eth1"},
	}
	got := LinkAttributeActions(links, nodes)
	netem := &types.TCParamEntry{Qdisc: "netem", Delay: "10ms", Rate: "50mbit"}
	want := []types.PodConfig{
		{Pod: "dist-sw-0", Actions: []types.ActionEntry{{Type: "add_qdisc", Iface: "eth3", TCParams: netem}}},
		{Pod: "room-router-0", Actions: []types.ActionEntry{{Type: "add_qdisc", Iface: "eth1", TCParams: netem}}},
		{Pod: "edge-router-0", Actions: []types.ActionEntry{{Type: "add_qdisc", Iface: "eth1", TCParams: &types.TCParamEntry{Qdisc: "netem", Loss: "1%"}}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("actions = %+v, want %+v", got, want)
	}
}

func TestFillLinkAttributes(t *testing.T) {
	attrs := map[string]LinkAttrs{
		"dist-sw-0/eth3":     {Delay: "10ms", Bandwidth: "50mbit"},
		"room-router-0/eth1": {Delay: "10ms", Bandwidth: "50mbit"},
	}
	// Read back from the peer's Topology, the link is named from the other end.
	link := types.LinkSpec{Node: "room-router", LocalIntf: "eth1", PeerNode: "dist-sw", PeerIntf: "eth3"}
	FillLinkAttributes(&link, "room-router-0", "dist-sw-0", attrs)
	if link.Delay != "10ms" || link.Bandwidth != "50mbit" {
		t.Errorf("filled = %+v", link)
	}
	other := types.LinkSpec{Node: "user-0", LocalIntf: "eth1", PeerNode: "room-sw-0", PeerIntf: "eth2"}
	FillLinkAttributes(&other, "user-0", "room-sw-0", attrs)
	if other.Delay != "" {
		t.Errorf("a link without attributes must stay empty, got %+v", other)
	}
}
