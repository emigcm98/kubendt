package helpers

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"kubendt/kubeclient"
	"kubendt/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	if decodeAddrs("") != nil || !reflect.DeepEqual(decodeAddrs(`["fd00::2/64","10.0.0.9/30"]`), a.PeerAddrs) || decodeAddrs("nonsense") != nil {
		t.Fatal("the address column of an older database does not read back")
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

// Two pods wired to each other, and one of them to the outside.
func fakeLinkAttrsTopologies(t *testing.T) {
	t.Helper()
	fakeTopologies(t,
		topologyCR("lab", "a-0",
			map[string]interface{}{"uid": int64(1000001), "local_intf": "eth1", "peer_pod": "b-0", "peer_intf": "eth1"},
			map[string]interface{}{"uid": int64(1000002), "local_intf": "eth2", "peer_pod": "localhost", "peer_intf": "eth0"}),
		topologyCR("lab", "b-0",
			map[string]interface{}{"uid": int64(1000001), "local_intf": "eth1", "peer_pod": "a-0", "peer_intf": "eth1"}),
	)
	prev := DB
	DB = nil
	t.Cleanup(func() { DB = prev })
}

var attrsNodes = []types.NodeSpec{{Name: "a", Replicas: 1}, {Name: "b", Replicas: 1}}

func TestLinkAttributesLiveOnTheTopologyObjects(t *testing.T) {
	fakeLinkAttrsTopologies(t)
	links := []types.LinkSpec{
		{Node: "a", LocalIntf: "eth1", PeerNode: "b", PeerIntf: "eth1", Delay: "10ms", MTU: 1400,
			LocalIP: types.Addrs{"10.0.0.1/24", "fd00::1/64"}, PeerIP: types.Addrs{"10.0.0.2/24", "fd00::2/64"}},
		{Node: "a", LocalIntf: "eth2", PeerNode: "external", PeerIntf: "eth0", Bandwidth: "10mbit"},
	}
	if err := SaveLinkAttributes("lab", links, attrsNodes); err != nil {
		t.Fatal(err)
	}
	obj, err := topologyObjects("lab").Get(context.TODO(), "a-0", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	entries := linkAttrEntries(obj)
	if entries["eth1"].Peer != "b-0" || entries["eth1"].MTU != 1400 || entries["eth1"].Delay != "10ms" || entries["eth2"].Bandwidth != "10mbit" || entries["eth2"].Peer != "external" {
		t.Fatalf("the annotation should carry both links from a's side: %+v", entries)
	}
	if !reflect.DeepEqual(entries["eth1"].LocalAddrs, []string{"fd00::1/64"}) || !reflect.DeepEqual(entries["eth1"].PeerAddrs, []string{"fd00::2/64"}) {
		t.Errorf("only the addresses beyond the first are kept: %+v", entries["eth1"])
	}
	attrs, err := LoadLinkAttributes("lab")
	if err != nil {
		t.Fatal(err)
	}
	if attrs["a-0/eth1"].MTU != 1400 || attrs["b-0/eth1"].MTU != 1400 || !reflect.DeepEqual(attrs["b-0/eth1"].LocalAddrs, []string{"fd00::2/64"}) {
		t.Fatalf("a load answers both ends, each from its side: %+v", attrs)
	}
	if _, ok := attrs["external/eth0"]; ok {
		t.Error("the outside has no end to look up")
	}

	// The MTU set on the peer's side lands on the entry a's side keeps.
	if err := SetLinkMTU("lab", "b-0", "eth1", "a-0", "eth1", 1300); err != nil {
		t.Fatal(err)
	}
	attrs, _ = LoadLinkAttributes("lab")
	if attrs["a-0/eth1"].MTU != 1300 || attrs["b-0/eth1"].MTU != 1300 {
		t.Fatalf("the MTU should follow whichever end set it: %+v", attrs)
	}
	// A link nobody declared attributes on gets an entry when an MTU is set,
	// and loses it when the MTU is cleared.
	if err := SetLinkMTU("lab", "b-0", "eth9", "a-0", "eth9", 1200); err != nil {
		t.Fatal(err)
	}
	if attrs, _ = LoadLinkAttributes("lab"); attrs["b-0/eth9"].MTU != 1200 {
		t.Fatalf("a fresh entry on the end that asked: %+v", attrs)
	}
	if err := SetLinkMTU("lab", "b-0", "eth9", "a-0", "eth9", 0); err != nil {
		t.Fatal(err)
	}
	if attrs, _ = LoadLinkAttributes("lab"); attrs["b-0/eth9"].MTU != 0 {
		t.Fatalf("cleared: %+v", attrs)
	}

	// A deleted link takes its entry, whichever end named it.
	if err := DeleteLinkAttributesForLinks("lab", []types.LinkSpec{{Node: "b", LocalIntf: "eth1", PeerNode: "a", PeerIntf: "eth1"}}, attrsNodes); err != nil {
		t.Fatal(err)
	}
	attrs, _ = LoadLinkAttributes("lab")
	if _, ok := attrs["a-0/eth1"]; ok {
		t.Errorf("the entry of a deleted link should go: %+v", attrs)
	}
	if attrs["a-0/eth2"].Bandwidth != "10mbit" {
		t.Errorf("the other link stays: %+v", attrs)
	}
}

func TestLinkAttributesOfDeletedPodsLeaveTheirPeers(t *testing.T) {
	fakeLinkAttrsTopologies(t)
	links := []types.LinkSpec{{Node: "a", LocalIntf: "eth1", PeerNode: "b", PeerIntf: "eth1", Loss: "1%"}}
	if err := SaveLinkAttributes("lab", links, attrsNodes); err != nil {
		t.Fatal(err)
	}
	if err := DeleteLinkAttributesForPods("lab", map[string]struct{}{"b-0": {}}); err != nil {
		t.Fatal(err)
	}
	obj, _ := topologyObjects("lab").Get(context.TODO(), "a-0", metav1.GetOptions{})
	if _, still := obj.GetAnnotations()[LinkAttrsAnnotation]; still {
		t.Errorf("a's entry toward the deleted pod should go, and with it the annotation: %v", obj.GetAnnotations())
	}
}

func TestLinkAttributesMigrateFromTheDatabase(t *testing.T) {
	fakeLinkAttrsTopologies(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Skip("no sqlite:", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE link_attributes (cluster_id TEXT, namespace TEXT, node TEXT, intf TEXT, peer_node TEXT, peer_intf TEXT, delay TEXT DEFAULT '', jitter TEXT DEFAULT '', loss TEXT DEFAULT '', bandwidth TEXT DEFAULT '', mtu INTEGER DEFAULT 0, local_addresses TEXT DEFAULT '', peer_addresses TEXT DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO link_attributes (cluster_id, namespace, node, intf, peer_node, peer_intf, delay, mtu, local_addresses) VALUES ('c1', 'lab', 'b-0', 'eth1', 'a-0', 'eth1', '5ms', 1450, '["fd00::2/64"]')`); err != nil {
		t.Fatal(err)
	}
	DB = db
	kubeclient.SetClusterIDForTest("c1")
	t.Cleanup(func() { kubeclient.SetClusterIDForTest("") })

	attrs, err := LoadLinkAttributes("lab")
	if err != nil {
		t.Fatal(err)
	}
	if attrs["b-0/eth1"].Delay != "5ms" || attrs["a-0/eth1"].MTU != 1450 || !reflect.DeepEqual(attrs["a-0/eth1"].PeerAddrs, []string{"fd00::2/64"}) {
		t.Fatalf("the rows should answer through the objects: %+v", attrs)
	}
	obj, _ := topologyObjects("lab").Get(context.TODO(), "b-0", metav1.GetOptions{})
	if linkAttrEntries(obj)["eth1"].Delay != "5ms" {
		t.Errorf("the row should now be an entry on b's Topology: %v", obj.GetAnnotations())
	}
	var left int
	if err := db.QueryRow(`SELECT count(*) FROM link_attributes`).Scan(&left); err != nil || left != 0 {
		t.Errorf("the rows should be gone once moved, %d left (%v)", left, err)
	}
}
