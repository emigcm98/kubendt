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

func TestLinkAttrsAddresses(t *testing.T) {
	l := types.LinkSpec{Node: "a", LocalIntf: "eth1", PeerNode: "b", PeerIntf: "eth1",
		LocalIP: types.Addrs{"10.0.0.1/30", "fd00::1/64"}, PeerIP: types.Addrs{"10.0.0.2/30", "fd00::2/64", "10.0.0.9/30"}}
	a := attrsOf(l)
	if a.empty() || !reflect.DeepEqual(a.LocalAddrs, []string{"fd00::1/64"}) || !reflect.DeepEqual(a.PeerAddrs, []string{"fd00::2/64", "10.0.0.9/30"}) {
		t.Fatalf("attrs: %+v", a)
	}
	if attrsOf(types.LinkSpec{LocalIP: types.Addrs{"10.0.0.1/30"}}).empty() != true {
		t.Fatal("one address per end is nothing to keep")
	}
	if encodeAddrs(nil) != "" || !reflect.DeepEqual(decodeAddrs(encodeAddrs(a.PeerAddrs)), a.PeerAddrs) || decodeAddrs("nonsense") != nil {
		t.Fatal("address columns do not round trip")
	}

	// A link read back from the cluster has one address per end; the fill
	// restores the rest from either end's key, from that end's view.
	fromA := map[string]LinkAttrs{"a/eth1": a}
	fromB := map[string]LinkAttrs{"b/eth1": a.swapped()}
	for name, attrs := range map[string]map[string]LinkAttrs{"by a": fromA, "by b": fromB} {
		link := types.LinkSpec{Node: "a", LocalIntf: "eth1", PeerNode: "b", PeerIntf: "eth1", LocalIP: types.Addrs{"10.0.0.1/30"}, PeerIP: types.Addrs{"10.0.0.2/30"}}
		FillLinkAttributes(&link, "a", "b", attrs)
		if !reflect.DeepEqual(link.LocalIP, l.LocalIP) || !reflect.DeepEqual(link.PeerIP, l.PeerIP) {
			t.Errorf("%s: filled %v / %v", name, link.LocalIP, link.PeerIP)
		}
	}
}

func TestValidateLinkMTU(t *testing.T) {
	t.Setenv(LinkMTUEnv, "")
	cases := []struct {
		name    string
		mtu     int
		local   string
		peer    string
		wantErr string
	}{
		{"unset", 0, "10.0.0.1/24", "10.0.0.2/24", ""},
		{"fits", 1400, "10.0.0.1/24", "10.0.0.2/24", ""},
		{"the platform value", 1450, "", "", ""},
		{"above the variable", 1500, "", "", "above KUBENDT_LINK_MTU (1450)"},
		{"below the kernel", 60, "", "", "not between 68 and 65535"},
		{"ipv6 floor", 1200, "fd00::1/64", "fd00::2/64", "below 1280"},
		{"ipv4 may go low", 1200, "10.0.0.1/24", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLinkMTU(tc.mtu, []string{tc.local, tc.peer})
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("want %q, got %v", tc.wantErr, err)
			}
		})
	}
	t.Setenv(LinkMTUEnv, "8950")
	if err := ValidateLinkMTU(9000, nil); err == nil || !strings.Contains(err.Error(), "(8950)") {
		t.Fatalf("the ceiling follows the variable: %v", err)
	}
	t.Setenv(LinkMTUEnv, "")
	if err := ValidateLinkAttributes([]types.LinkSpec{{Node: "a", LocalIntf: "eth1", PeerNode: "b", PeerIntf: "eth1", MTU: 9001}}); err == nil || !strings.Contains(err.Error(), "link[0]") {
		t.Fatalf("the link index is named: %v", err)
	}
}
