package helpers

import (
	"testing"

	"kubendt/types"
)

func TestActionTouchesIface(t *testing.T) {
	recreated := map[string]bool{"eth3": true}
	cases := []struct {
		name   string
		action types.ActionEntry
		want   bool
	}{
		{"direct iface", types.ActionEntry{Type: "set_ip", Iface: "eth3"}, true},
		{"other iface", types.ActionEntry{Type: "set_ip", Iface: "eth1"}, false},
		{"bridge member", types.ActionEntry{Type: "setup_bridge", Bridge: "br0", Ifaces: []string{"eth1", "eth2", "eth3"}}, true},
		{"bridge without it", types.ActionEntry{Type: "setup_bridge", Bridge: "br0", Ifaces: []string{"eth1", "eth2"}}, false},
		{"no interface at all", types.ActionEntry{Type: "ospf_set_router_id", RouterID: "1.1.1.1"}, false},
		{"default route dies with the interface", types.ActionEntry{Type: "set_default_route", Gateway: "10.0.2.1"}, true},
		{"static route too", types.ActionEntry{Type: "add_static_route", DstCIDR: "10.0.0.0/8", Gateway: "10.0.2.1"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := actionTouchesIface(tc.action, recreated); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
