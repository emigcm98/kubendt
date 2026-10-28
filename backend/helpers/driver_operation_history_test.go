package helpers

import (
	"kubendt/types"
	"testing"
	"time"
)

func TestParseExecutedAt(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want time.Time
	}{
		{"2026-07-29T17:22:59.067761927Z", true, time.Date(2026, 7, 29, 17, 22, 59, 67761927, time.UTC)},
		{"2026-09-20T21:29:14Z", true, time.Date(2026, 9, 20, 21, 29, 14, 0, time.UTC)},
		{"2026-01-05 10:11:12", true, time.Date(2026, 1, 5, 10, 11, 12, 0, time.UTC)},
		{"not a date", false, time.Time{}},
	}
	for _, tc := range cases {
		got, ok := parseExecutedAt(tc.in)
		if ok != tc.ok || (ok && !got.Equal(tc.want)) {
			t.Fatalf("parseExecutedAt(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// Every pair meets on the same state and key, and the undo keeps or cancels
// as declared: kept where the platform or the cluster would put the state
// back by itself, cancelled where only the history ever set it.
func TestStatePairs(t *testing.T) {
	type pair struct {
		set, clear   types.ActionEntry
		state        string
		undoPersists bool
	}
	pairs := []pair{
		{types.ActionEntry{Type: "link_up", Iface: "eth1"}, types.ActionEntry{Type: "link_down", Iface: "eth1"}, "link", true},
		{types.ActionEntry{Type: "set_ip", Iface: "eth1", CIDR: "10.0.0.9/24"}, types.ActionEntry{Type: "remove_ip", Iface: "eth1", CIDR: "10.0.0.9/24"}, "address", true},
		{types.ActionEntry{Type: "replace_ip", Iface: "eth1", CIDR: "10.0.0.9/24"}, types.ActionEntry{Type: "remove_ip", Iface: "eth1", CIDR: "10.0.0.9/24"}, "address", true},
		{types.ActionEntry{Type: "set_default_route", Gateway: "10.0.0.1"}, types.ActionEntry{Type: "remove_default_route"}, "default_route", true},
		{types.ActionEntry{Type: "set_default_route", Gateway: "fd00::1"}, types.ActionEntry{Type: "remove_default_route", Family: "6"}, "default_route", true},
		{types.ActionEntry{Type: "add_static_route", DstCIDR: "10.1.0.0/16", Gateway: "10.0.0.254"}, types.ActionEntry{Type: "remove_static_route", DstCIDR: "10.1.0.0/16", Gateway: "10.0.0.254"}, "static_route", false},
		{types.ActionEntry{Type: "add_dns_nameserver", DNSServer: "8.8.8.8"}, types.ActionEntry{Type: "remove_dns_nameserver", DNSServer: "8.8.8.8"}, "dns_nameserver", false},
		{types.ActionEntry{Type: "add_dns_search", DNSDomain: "lab"}, types.ActionEntry{Type: "remove_dns_search", DNSDomain: "lab"}, "dns_search", false},
		{types.ActionEntry{Type: "setup_bridge", Bridge: "br0"}, types.ActionEntry{Type: "teardown_bridge", Bridge: "br0"}, "bridge", false},
		{types.ActionEntry{Type: "add_interface_to_bridge", Iface: "eth1", Bridge: "br0"}, types.ActionEntry{Type: "remove_interface_from_bridge", Iface: "eth1", Bridge: "br0"}, "bridge_member", false},
		{types.ActionEntry{Type: "enable_snat", Iface: "eth0"}, types.ActionEntry{Type: "disable_snat", Iface: "eth0"}, "snat", false},
		{types.ActionEntry{Type: "enable_dnat", Iface: "eth4", Protocol: "tcp", ExternalPort: 80, InternalIP: "10.0.4.10", InternalPort: 80}, types.ActionEntry{Type: "disable_dnat", Iface: "eth4", Protocol: "tcp", ExternalPort: 80, InternalIP: "10.0.4.10", InternalPort: 80}, "dnat", false},
		{types.ActionEntry{Type: "ospf_add_network", CIDR: "10.0.0.0/30", OSPFArea: "0"}, types.ActionEntry{Type: "ospf_remove_network", CIDR: "10.0.0.0/30", OSPFArea: "0"}, "ospf_network", false},
		{types.ActionEntry{Type: "ospf_set_router_id", RouterID: "1.1.1.1"}, types.ActionEntry{Type: "ospf_remove_router_id"}, "ospf_router_id", false},
		{types.ActionEntry{Type: "ospf_passive_default"}, types.ActionEntry{Type: "ospf_remove_passive_default"}, "ospf_passive_default", false},
		{types.ActionEntry{Type: "ospf_no_passive", Iface: "eth1"}, types.ActionEntry{Type: "ospf_remove_no_passive", Iface: "eth1"}, "ospf_no_passive", false},
		{types.ActionEntry{Type: "ospf_originate_default"}, types.ActionEntry{Type: "ospf_remove_originate_default"}, "ospf_originate_default", false},
		{types.ActionEntry{Type: "ospf_mtu_ignore", Iface: "eth1"}, types.ActionEntry{Type: "ospf_remove_mtu_ignore", Iface: "eth1"}, "ospf_mtu_ignore", false},
		{types.ActionEntry{Type: "add_qdisc", Iface: "eth1"}, types.ActionEntry{Type: "del_qdisc", Iface: "eth1"}, "qdisc", false},
		{types.ActionEntry{Type: "bgp_set_router", AS: 65001}, types.ActionEntry{Type: "bgp_remove_router"}, "bgp_router", false},
		{types.ActionEntry{Type: "bgp_add_neighbor", Neighbor: "10.0.0.2", RemoteAS: 65002}, types.ActionEntry{Type: "bgp_remove_neighbor", Neighbor: "10.0.0.2"}, "bgp_neighbor", false},
		{types.ActionEntry{Type: "bgp_add_network", CIDR: "10.1.0.0/24"}, types.ActionEntry{Type: "bgp_remove_network", CIDR: "10.1.0.0/24"}, "bgp_network", false},
		{types.ActionEntry{Type: "bgp_redistribute", Redistribute: "connected", Family: "6"}, types.ActionEntry{Type: "bgp_remove_redistribute", Redistribute: "connected", Family: "6"}, "bgp_redistribute", false},
		{types.ActionEntry{Type: "isis_set_router", NET: "49.0001.0000.0000.0001.00"}, types.ActionEntry{Type: "isis_remove_router"}, "isis_router", false},
		{types.ActionEntry{Type: "isis_add_interface", Iface: "eth1", Family: "6"}, types.ActionEntry{Type: "isis_remove_interface", Iface: "eth1"}, "isis_interface", false},
		{types.ActionEntry{Type: "isis_passive_interface", Iface: "eth1"}, types.ActionEntry{Type: "isis_remove_passive_interface", Iface: "eth1"}, "isis_passive", false},
		{types.ActionEntry{Type: "isis_set_metric", Iface: "eth1", Cost: 50}, types.ActionEntry{Type: "isis_remove_metric", Iface: "eth1"}, "isis_metric", false},
		{types.ActionEntry{Type: "isis_set_network_type", Iface: "eth1", NetworkType: "point-to-point"}, types.ActionEntry{Type: "isis_remove_network_type", Iface: "eth1"}, "isis_network_type", false},
		{types.ActionEntry{Type: "isis_redistribute", Redistribute: "connected", Level: "level-1"}, types.ActionEntry{Type: "isis_remove_redistribute", Redistribute: "connected", Level: "level-1"}, "isis_redistribute", false},
		{types.ActionEntry{Type: "nat64_enable", Pool: "172.22.19.0/24"}, types.ActionEntry{Type: "nat64_disable"}, "nat64", false},
	}
	for _, p := range pairs {
		s1, k1, c1, u1 := stateOf(p.set)
		s2, k2, c2, u2 := stateOf(p.clear)
		if s1 != p.state || s2 != p.state || k1 != k2 {
			t.Errorf("%s / %s: states %q %q keys %q %q", p.set.Type, p.clear.Type, s1, s2, k1, k2)
		}
		if c1 || !c2 || u1 || u2 != p.undoPersists {
			t.Errorf("%s / %s: flags set(%v,%v) clear(%v,%v), want undoPersists=%v", p.set.Type, p.clear.Type, c1, u1, c2, u2, p.undoPersists)
		}
	}
	for _, free := range []string{"custom", "nope"} {
		if s, _, _, _ := stateOf(types.ActionEntry{Type: free}); s != "" {
			t.Errorf("%s should carry no state, got %q", free, s)
		}
	}
	if _, k, _, _ := stateOf(types.ActionEntry{Type: "remove_default_route"}); k != "4" {
		t.Errorf("a default route removal without family is IPv4, got key %q", k)
	}
	if _, k1, _, _ := stateOf(types.ActionEntry{Type: "set_ip", Iface: "eth1", CIDR: "a"}); k1 == stateKeyOf("set_ip", "eth2", "a") {
		t.Error("the same address on another interface is another state")
	}
}

func stateKeyOf(typ, iface, cidr string) string {
	_, k, _, _ := stateOf(types.ActionEntry{Type: typ, Iface: iface, CIDR: cidr})
	return k
}

func TestRowsOnStateAndSuperseded(t *testing.T) {
	op := func(id int64, typ, iface, cidr, at string) PersistedDriverOperation {
		return PersistedDriverOperation{ID: id, ActionType: typ, Action: types.ActionEntry{Type: typ, Iface: iface, CIDR: cidr}, ExecutedAt: at}
	}
	ops := []PersistedDriverOperation{
		op(1, "set_ip", "eth2", "10.0.1.9/24", "2026-10-21T11:36:55.100Z"),
		op(2, "remove_ip", "eth2", "10.0.1.9/24", "2026-10-21T11:36:55.200Z"),
		op(3, "remove_ip", "eth2", "10.0.1.77/24", "2026-10-21T11:36:55.300Z"),
		op(4, "link_down", "eth2", "", "2026-10-21T11:36:55.400Z"),
		{ID: 5, ActionType: "set_ip", Action: types.ActionEntry{Iface: "eth3", CIDR: "10.0.1.9/24"}, ExecutedAt: "2026-10-21T11:36:55.500Z"}, // an old row without a type
	}
	ids := func(rows []PersistedDriverOperation) []int64 {
		var out []int64
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	if got := ids(rowsOnState(ops, "address", "eth2 10.0.1.9/24")); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("rows on the address: %v", got)
	}
	if got := ids(rowsOnState(ops, "address", "eth3 10.0.1.9/24")); len(got) != 1 || got[0] != 5 {
		t.Errorf("a row without a type still has its state: %v", got)
	}
	if got := rowsOnState(ops, "link", "eth1"); len(got) != 0 {
		t.Errorf("another interface's link state: %v", ids(got))
	}

	setJSON := `{"type":"set_ip","iface":"eth2","cidr":"10.0.1.9/24"}`
	setAt, _ := parseExecutedAt("2026-10-21T11:36:55.100Z")
	if !supersededAfter(ops, "address", "eth2 10.0.1.9/24", setJSON, setAt) {
		t.Error("the remove after the set supersedes it")
	}
	removeJSON := `{"type":"remove_ip","iface":"eth2","cidr":"10.0.1.9/24"}`
	removeAt, _ := parseExecutedAt("2026-10-21T11:36:55.200Z")
	if supersededAfter(ops, "address", "eth2 10.0.1.9/24", removeJSON, removeAt) {
		t.Error("nothing touches the address after its remove")
	}
	if supersededAfter(ops, "address", "eth2 10.0.1.50/24", setJSON, setAt) {
		t.Error("another address does not count")
	}
}

// Stopping BGP takes its neighbours, networks and redistributions with it,
// whatever their keys, and nothing else.
func TestRowsOnStatesClearedWith(t *testing.T) {
	ops := []PersistedDriverOperation{
		{ID: 1, ActionType: "bgp_set_router", Action: types.ActionEntry{Type: "bgp_set_router", AS: 65002}},
		{ID: 2, ActionType: "bgp_add_neighbor", Action: types.ActionEntry{Type: "bgp_add_neighbor", Neighbor: "10.0.0.1", RemoteAS: 65001}},
		{ID: 3, ActionType: "bgp_add_neighbor", Action: types.ActionEntry{Type: "bgp_add_neighbor", Neighbor: "fd00::1", RemoteAS: 65003}},
		{ID: 4, ActionType: "bgp_redistribute", Action: types.ActionEntry{Type: "bgp_redistribute", Redistribute: "connected"}},
		{ID: 5, ActionType: "bgp_add_network", Action: types.ActionEntry{Type: "bgp_add_network", CIDR: "10.2.0.0/24"}},
		{ID: 6, ActionType: "set_ip", Action: types.ActionEntry{Type: "set_ip", Iface: "eth1", CIDR: "10.0.0.2/30"}},
		{ID: 7, ActionType: "ospf_add_network", Action: types.ActionEntry{Type: "ospf_add_network", CIDR: "10.2.0.0/24", OSPFArea: "0"}},
	}
	states, keyed := clearedWith(types.ActionEntry{Type: "bgp_remove_router"})
	if len(states) != 3 || keyed {
		t.Fatalf("bgp_remove_router clears %v keyed=%v", states, keyed)
	}
	ids := func(rows []PersistedDriverOperation) []int64 {
		var out []int64
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	if got := ids(rowsOnStates(ops, states, "", false)); len(got) != 4 || got[0] != 2 || got[1] != 3 || got[2] != 4 || got[3] != 5 {
		t.Errorf("rows taken down with BGP: %v", got)
	}
	states, keyed = clearedWith(types.ActionEntry{Type: "bgp_remove_neighbor", Neighbor: "10.0.0.1"})
	if rows := rowsOnStates(ops, states, "10.0.0.1", keyed); len(rows) != 0 {
		t.Errorf("removing one neighbour clears nothing else, got %d rows", len(rows))
	}

	// An interface leaving IS-IS takes its own passive flag and metric, not
	// another interface's.
	isis := []PersistedDriverOperation{
		{ID: 1, ActionType: "isis_add_interface", Action: types.ActionEntry{Type: "isis_add_interface", Iface: "eth1"}},
		{ID: 2, ActionType: "isis_passive_interface", Action: types.ActionEntry{Type: "isis_passive_interface", Iface: "eth1"}},
		{ID: 3, ActionType: "isis_set_metric", Action: types.ActionEntry{Type: "isis_set_metric", Iface: "eth1", Cost: 50}},
		{ID: 4, ActionType: "isis_passive_interface", Action: types.ActionEntry{Type: "isis_passive_interface", Iface: "eth2"}},
		{ID: 5, ActionType: "isis_redistribute", Action: types.ActionEntry{Type: "isis_redistribute", Redistribute: "connected"}},
	}
	states, keyed = clearedWith(types.ActionEntry{Type: "isis_remove_interface", Iface: "eth1"})
	if got := ids(rowsOnStates(isis, states, "eth1", keyed)); !keyed || len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Errorf("rows taken down with eth1 leaving IS-IS: %v keyed=%v", got, keyed)
	}
	states, keyed = clearedWith(types.ActionEntry{Type: "isis_remove_router"})
	if got := ids(rowsOnStates(isis, states, "", keyed)); keyed || len(got) != 5 {
		t.Errorf("rows taken down with IS-IS stopping: %v keyed=%v", got, keyed)
	}
}
