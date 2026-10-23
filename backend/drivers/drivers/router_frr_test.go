package drivers

import (
	"reflect"
	"testing"
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
	cmd, failure, ok := d.CapabilityPrecondition("OSPFCapable")
	if !ok || !reflect.DeepEqual(cmd, []string{"test", "-S", "/var/run/frr/ospfd.vty"}) || failure == "" {
		t.Errorf("OSPF precondition: %v %q %v", cmd, failure, ok)
	}
	if _, _, ok := d.CapabilityPrecondition("L3Capable"); ok {
		t.Errorf("L3 needs no daemon")
	}
}
