package helpers

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"kubendt/types"
)

func TestRemovedLinkEnds(t *testing.T) {
	uid := func(n int) *int { return &n }
	link := func(a, ai, aip, b, bi, bip string, u int) types.LinkSpec {
		return types.LinkSpec{Node: a, LocalIntf: ai, LocalIP: types.OneAddr(aip), PeerNode: b, PeerIntf: bi, PeerIP: types.OneAddr(bip), UID: uid(u)}
	}
	before := []types.LinkSpec{
		link("h-0", "eth1", "10.0.1.10/24", "sw-0", "eth1", "", 1),
		link("h-1", "eth1", "10.0.2.10/24", "sw-0", "eth2", "", 2),
		link("r-0", "eth3", "10.0.3.1/24", "external", "ens18", "", 3),
	}
	after := []types.LinkSpec{before[0]}
	got := RemovedLinkEnds(before, after)
	if len(got) != 3 || !got["h-1"].Ifaces["eth1"] || !got["sw-0"].Ifaces["eth2"] || !got["r-0"].Ifaces["eth3"] {
		t.Fatalf("got %v", got)
	}
	if want := []netip.Prefix{netip.MustParsePrefix("10.0.2.0/24")}; !reflect.DeepEqual(got["h-1"].Subnets, want) {
		t.Errorf("h-1 lost the subnets %v, want %v", got["h-1"].Subnets, want)
	}
	// Put back under the same names it is a new link, so the old ends are gone.
	again := []types.LinkSpec{before[0], link("h-1", "eth1", "10.0.2.10/24", "sw-0", "eth2", "", 9)}
	got = RemovedLinkEnds(before[:2], again)
	if !got["h-1"].Ifaces["eth1"] || !got["sw-0"].Ifaces["eth2"] || got["h-0"] != nil {
		t.Errorf("a link replaced by a new uid should count as removed, got %v", got)
	}
	if got := RemovedLinkEnds(before, before); len(got) != 0 {
		t.Errorf("nothing changed, got %v", got)
	}
	subnets := KeptSubnetsByPod(before, after)
	if !reflect.DeepEqual(subnets["h-0"], []netip.Prefix{netip.MustParsePrefix("10.0.1.0/24")}) || len(subnets["sw-0"]) != 0 {
		t.Errorf("subnets kept: %v", subnets)
	}
	// A link put back with a new uid keeps nothing: its subnet is that of a new interface.
	if got := KeptSubnetsByPod(before[:2], again); len(got["h-1"]) != 0 || len(got["h-0"]) != 1 {
		t.Errorf("the replaced link's subnets counted as kept: %v", got)
	}
}

func TestForgetRemoved(t *testing.T) {
	op := func(id int64, typ string, a types.ActionEntry) PersistedDriverOperation {
		a.Type = typ
		return PersistedDriverOperation{ID: id, ActionType: typ, Action: a, ExecutedAt: "2026-11-27T10:00:00Z"}
	}
	h := &PodHistory{ops: []PersistedDriverOperation{
		op(1, "setup_bridge", types.ActionEntry{Bridge: "br0", Ifaces: []string{"eth1", "eth2"}}),
		op(2, "replace_ip", types.ActionEntry{Iface: "eth2", CIDR: "10.0.2.1/24"}),
		op(3, "set_default_route", types.ActionEntry{Gateway: "10.0.1.254"}),
		op(4, "setup_bridge", types.ActionEntry{Bridge: "br1", Ifaces: []string{"eth2"}}),
		op(5, "replace_ip", types.ActionEntry{Iface: "eth1", CIDR: "10.0.1.1/24"}),
	}}
	gone := &RemovedEnds{Ifaces: map[string]bool{"eth2": true}}
	forgotten := h.ForgetRemoved(gone, nil)
	var said []string
	for _, f := range forgotten {
		said = append(said, f.String())
	}
	if want := []string{"eth2 in setup_bridge br0", "replace_ip on eth2", "setup_bridge br1"}; !reflect.DeepEqual(said, want) {
		t.Errorf("forgot %v, want %v: the bridge list cut, the address and the lone bridge dropped", said, want)
	}
	if got, want := forgottenDetail(forgotten), "Went with the removed link: eth2 in setup_bridge br0, replace_ip on eth2, setup_bridge br1. Configure it again if still wanted."; got != want {
		t.Errorf("detail %q, want %q", got, want)
	}
	var ids []int64
	for _, o := range h.Operations() {
		ids = append(ids, o.ID)
	}
	if !reflect.DeepEqual(ids, []int64{1, 3, 5}) {
		t.Errorf("kept %v, want 1, 3, 5 in their order", ids)
	}
	if got := h.Operations()[0].Action.Ifaces; !reflect.DeepEqual(got, []string{"eth1"}) {
		t.Errorf("the bridge keeps %v, want only eth1", got)
	}
	if h.Operations()[0].ExecutedAt != "2026-11-27T10:00:00Z" {
		t.Error("a cut operation keeps its stamp")
	}
	// The route has no interface of its own and no subnet was lost: it stays.
	if len(h.ForgetRemoved(gone, nil)) != 0 {
		t.Error("nothing left to forget, yet something changed")
	}
}

func TestForgetRemovedRoutes(t *testing.T) {
	route := func(id int64, typ, gw string) PersistedDriverOperation {
		return PersistedDriverOperation{ID: id, ActionType: typ, Action: types.ActionEntry{Type: typ, Gateway: gw}}
	}
	ops := func() *PodHistory {
		return &PodHistory{ops: []PersistedDriverOperation{
			route(1, "set_default_route", "192.168.1.1"),
			route(2, "add_static_route", "10.9.0.1"),
			route(3, "set_default_route", "fd00:a::1"),
			route(4, "set_default_route", "not-an-address"),
		}}
	}
	gone := &RemovedEnds{Ifaces: map[string]bool{"eth1": true}, Subnets: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("fd00:a::/64")}}

	// The host lost its only link: both default routes that sat on it go, the
	// one on a subnet it never lost and the unreadable one stay.
	h := ops()
	forgotten := h.ForgetRemoved(gone, nil)
	if len(forgotten) != 2 || forgotten[0].String() != "set_default_route via 192.168.1.1" {
		t.Errorf("forgot %v, want the IPv4 and the IPv6 default route", forgotten)
	}
	var ids []int64
	for _, o := range h.Operations() {
		ids = append(ids, o.ID)
	}
	if !reflect.DeepEqual(ids, []int64{2, 4}) {
		t.Errorf("kept %v, want 2 and 4", ids)
	}

	// Still a link on the same subnet: the route may well be there through it.
	h = ops()
	if n := len(h.ForgetRemoved(gone, []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")})); n != 1 {
		t.Errorf("forgot %d, want only the IPv6 one, the IPv4 gateway is still reachable", n)
	}
}

func TestForgottenDetailCaps(t *testing.T) {
	var list []Forgotten
	for _, i := range []string{"eth1", "eth2", "eth3", "eth4", "eth5", "eth6"} {
		list = append(list, Forgotten{Action: types.ActionEntry{Type: "replace_ip", Iface: i}})
	}
	list = append(list, Forgotten{Action: types.ActionEntry{Type: "add_static_route", DstCIDR: "10.0.0.0/8", Gateway: "10.1.0.1"}})
	got := forgottenDetail(list)
	if !strings.Contains(got, "replace_ip on eth4 and 3 more.") || strings.Contains(got, "eth5") {
		t.Errorf("detail not capped at %d: %q", forgottenShown, got)
	}
	if s := list[6].String(); s != "add_static_route 10.0.0.0/8 via 10.1.0.1" {
		t.Errorf("a static route reads %q", s)
	}
}
