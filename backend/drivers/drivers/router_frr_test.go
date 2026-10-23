package drivers

import (
	"reflect"
	"testing"

	"kubendt/types"
)

func TestFRRRoutesGoThroughStaticd(t *testing.T) {
	var d FRRRouterDriver
	drop4 := "{ ! ip -o route show default dev eth0 | grep -q . || ip route del default dev eth0; }"
	drop6 := "{ ! ip -6 -o route show default dev eth0 | grep -q . || ip -6 route del default dev eth0; }"
	clear4 := `vtysh -c 'show running-config' | grep '^ip route 0\.0\.0\.0/0 ' | while read -r l; do vtysh -c 'configure terminal' -c "no $l"; done`
	clear6 := `vtysh -c 'show running-config' | grep '^ipv6 route ::/0 ' | while read -r l; do vtysh -c 'configure terminal' -c "no $l"; done`
	clearNet := `vtysh -c 'show running-config' | grep '^ip route 10\.1\.0\.0/16 ' | while read -r l; do vtysh -c 'configure terminal' -c "no $l"; done`
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"default v4", d.SetDefaultRoute("10.0.0.1"), [][]string{{"sh", "-c",
			drop4 + " && { vtysh -c 'show running-config' | grep -qxF 'ip route 0.0.0.0/0 10.0.0.1' || { " + clear4 + " && vtysh -c 'configure terminal' -c 'ip route 0.0.0.0/0 10.0.0.1'; }; }"}}},
		{"default v6", d.SetDefaultRoute("fd00:1::2"), [][]string{{"sh", "-c",
			drop6 + " && { vtysh -c 'show running-config' | grep -qxF 'ipv6 route ::/0 fd00:1::2' || { " + clear6 + " && vtysh -c 'configure terminal' -c 'ipv6 route ::/0 fd00:1::2'; }; }"}}},
		{"remove default v4", d.RemoveDefaultRoute(), [][]string{{"sh", "-c", drop4 + " && " + clear4}}},
		{"remove default v6", d.RemoveDefaultRoute6(), [][]string{{"sh", "-c", drop6 + " && " + clear6}}},
		{"static with device", d.AddStaticRoute("10.1.0.0/16", "10.0.0.254", "eth2"), [][]string{{"sh", "-c",
			"{ vtysh -c 'show running-config' | grep -qxF 'ip route 10.1.0.0/16 10.0.0.254 eth2' || { " + clearNet + " && vtysh -c 'configure terminal' -c 'ip route 10.1.0.0/16 10.0.0.254 eth2'; }; }"}}},
		{"remove static v6", d.RemoveStaticRoute("fd00:99::/64", "fd00:1::2", ""), [][]string{{"vtysh", "-c", "configure terminal", "-c", "no ipv6 route fd00:99::/64 fd00:1::2"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %q\n want %q", c.name, c.got, c.want)
		}
	}
}

func TestFRRCapabilityPrecondition(t *testing.T) {
	var d FRRRouterDriver
	cmd, failure, ok := d.CapabilityPrecondition("OSPFCapable", types.ActionEntry{})
	if !ok || !reflect.DeepEqual(cmd, []string{"test", "-S", "/var/run/frr/ospfd.vty"}) || failure == "" {
		t.Errorf("OSPF precondition: %v %q %v", cmd, failure, ok)
	}
	if _, _, ok := d.CapabilityPrecondition("L3Capable", types.ActionEntry{}); ok {
		t.Errorf("L3 needs no daemon")
	}
	for _, c := range []struct {
		a    types.ActionEntry
		want string
	}{
		{types.ActionEntry{CIDR: "10.0.0.0/24"}, "/var/run/frr/ripd.vty"},
		{types.ActionEntry{CIDR: "fd00::/64"}, "/var/run/frr/ripngd.vty"},
		{types.ActionEntry{Iface: "eth1", Family: "6"}, "/var/run/frr/ripngd.vty"},
		{types.ActionEntry{Iface: "eth1"}, "/var/run/frr/ripd.vty"},
	} {
		cmd, _, ok := d.CapabilityPrecondition("RIPCapable", c.a)
		if !ok || cmd[2] != c.want {
			t.Errorf("RIP precondition for %+v: %v", c.a, cmd)
		}
	}
}

func TestFRRRIPFamilies(t *testing.T) {
	var d FRRRouterDriver
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"network v4", d.RIPAddNetwork("10.0.0.0/24", false), [][]string{{"vtysh", "-c", "configure terminal", "-c", "router rip", "-c", "network 10.0.0.0/24"}}},
		{"network v6", d.RIPAddNetwork("fd00::/64", true), [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ripng", "-c", "network fd00::/64"}}},
		{"redistribute ospf v6", d.RIPRedistribute("ospf", true), [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ripng", "-c", "redistribute ospf6"}}},
		{"timers", d.RIPSetTimers(10, 60, 40, false), [][]string{{"vtysh", "-c", "configure terminal", "-c", "router rip", "-c", "timers basic 10 60 40"}}},
		{"no timers v6", d.RIPRemoveTimers(true), [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ripng", "-c", "no timers basic"}}},
		{"ospf redistribute rip", d.OSPFRedistribute("rip"), [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "redistribute rip"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}

func TestFRROSPF6(t *testing.T) {
	var d FRRRouterDriver
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"router id", d.OSPF6SetRouterID("10.0.0.1"), [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf6", "-c", "ospf6 router-id 10.0.0.1"}}},
		{"interface area", d.OSPF6AddInterface("eth2", "0"), [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface eth2", "-c", "ipv6 ospf6 area 0"}}},
		{"passive", d.OSPF6PassiveInterface("eth1"), [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface eth1", "-c", "ipv6 ospf6 passive"}}},
		{"redistribute", d.OSPF6Redistribute("ripng"), [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf6", "-c", "redistribute ripng"}}},
		{"no originate", d.OSPF6RemoveOriginateDefault(), [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf6", "-c", "no default-information originate"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
	cmd, _, ok := d.CapabilityPrecondition("OSPF6Capable", types.ActionEntry{})
	if !ok || cmd[2] != "/var/run/frr/ospf6d.vty" {
		t.Errorf("OSPFv3 precondition: %v %v", cmd, ok)
	}
}
