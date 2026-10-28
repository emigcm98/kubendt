package drivers

import (
	"kubendt/actions"
	"reflect"
	"strings"
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
	if cmd, _, ok := d.CapabilityPrecondition("BGPCapable", types.ActionEntry{}); !ok || cmd[2] != "/var/run/frr/bgpd.vty" {
		t.Errorf("BGP precondition: %v", cmd)
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

func TestFRROSPFInterface(t *testing.T) {
	var d FRRRouterDriver
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"cost", d.OSPFSetCost("eth2", 100), [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface eth2", "-c", "ip ospf cost 100"}}},
		{"priority", d.OSPFSetPriority("eth1", 255), [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface eth1", "-c", "ip ospf priority 255"}}},
		{"timers", d.OSPFSetTimers("eth2", 5, 20), [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface eth2", "-c", "ip ospf hello-interval 5", "-c", "ip ospf dead-interval 20"}}},
		{"no timers", d.OSPFRemoveTimers("eth2"), [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface eth2", "-c", "no ip ospf hello-interval", "-c", "no ip ospf dead-interval"}}},
		{"network type", d.OSPFSetNetworkType("eth2", "point-to-point"), [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface eth2", "-c", "ip ospf network point-to-point"}}},
		{"no network type", d.OSPFRemoveNetworkType("eth2"), [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface eth2", "-c", "no ip ospf network"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}

func TestFRROSPFAreasAndAuth(t *testing.T) {
	var d FRRRouterDriver
	ospf := func(line string) [][]string {
		return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", line}}
	}
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"stub", d.OSPFSetAreaType("3", "stub"), ospf("area 3 stub")},
		{"totally stub", d.OSPFSetAreaType("2", "totally-stub"), [][]string{{"sh", "-c", "vtysh -c 'configure terminal' -c 'router ospf' -c 'no area 2 stub' >/dev/null 2>&1; vtysh -c 'configure terminal' -c 'router ospf' -c 'area 2 stub no-summary'"}}},
		{"totally nssa", d.OSPFSetAreaType("4", "totally-nssa"), [][]string{{"sh", "-c", "vtysh -c 'configure terminal' -c 'router ospf' -c 'no area 4 nssa' >/dev/null 2>&1; vtysh -c 'configure terminal' -c 'router ospf' -c 'area 4 nssa no-summary'"}}},
		{"no totally stub", d.OSPFRemoveAreaType("2", "totally-stub"), ospf("no area 2 stub")},
		{"no nssa", d.OSPFRemoveAreaType("4", "nssa"), ospf("no area 4 nssa")},
		{"range", d.OSPFAddAreaRange("2", "172.22.16.64/26"), ospf("area 2 range 172.22.16.127/26")},
		{"no range", d.OSPFRemoveAreaRange("2", "172.22.16.64/26"), ospf("no area 2 range 172.22.16.127/26")},
		{"plain auth", d.OSPFSetAuthentication("eth1", "plain", "pass", 1), [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface eth1", "-c", "ip ospf authentication", "-c", "ip ospf authentication-key pass"}}},
		{"md5 auth", d.OSPFSetAuthentication("eth1", "md5", "s3cret", 2), [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface eth1", "-c", "ip ospf authentication message-digest", "-c", "ip ospf message-digest-key 2 md5 s3cret"}}},
		{"no md5 auth", d.OSPFRemoveAuthentication("eth1", "md5", 2), [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface eth1", "-c", "no ip ospf authentication", "-c", "no ip ospf message-digest-key 2"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}

func TestFRRRangePrefix(t *testing.T) {
	cases := map[string]string{"172.22.16.64/26": "172.22.16.127/26", "10.0.16.0/26": "10.0.16.63/26", "172.22.16.0/22": "172.22.19.255/22", "10.0.0.0/8": "10.255.255.255/8", "nonsense": "nonsense"}
	for in, want := range cases {
		if got := frrRangePrefix(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestFRRStartupCommands(t *testing.T) {
	cmd := FRRRouterDriver{}.StartupCommands()
	if len(cmd) != 3 || cmd[0] != "sh" || cmd[1] != "-c" {
		t.Fatalf("startup command: %v", cmd)
	}
	for _, want := range []string{"s/^ospfd=no/ospfd=yes/", "s/^ospf6d=no/ospf6d=yes/", "s/^ripd=no/ripd=yes/", "s/^ripngd=no/ripngd=yes/", "s/^bgpd=no/bgpd=yes/", "touch /etc/frr/frr.conf /etc/frr/vtysh.conf", "exec /sbin/tini -- /usr/lib/frr/docker-start"} {
		if !strings.Contains(cmd[2], want) {
			t.Errorf("startup command lacks %q: %s", want, cmd[2])
		}
	}
	if strings.Contains(cmd[2], "apk") {
		t.Errorf("the default must not install packages: %s", cmd[2])
	}
}

func TestOSPFInterfaceRanges(t *testing.T) {
	prio := func(n int) *int { return &n }
	cases := []struct {
		name string
		a    types.ActionEntry
		want string // substring of the error, empty when it runs
	}{
		{"cost zero", types.ActionEntry{Type: "ospf_set_cost", Iface: "eth1", Cost: 0}, "missing required field"},
		{"cost too big", types.ActionEntry{Type: "ospf_set_cost", Iface: "eth1", Cost: 70000}, "between 1 and 65535"},
		{"cost fine", types.ActionEntry{Type: "ospf_set_cost", Iface: "eth1", Cost: 100}, ""},
		{"priority zero", types.ActionEntry{Type: "ospf_set_priority", Iface: "eth1", Priority: prio(0)}, ""},
		{"priority too big", types.ActionEntry{Type: "ospf_set_priority", Iface: "eth1", Priority: prio(256)}, "between 0 and 255"},
		{"dead not longer", types.ActionEntry{Type: "ospf_set_timers", Iface: "eth1", HelloInterval: 10, DeadInterval: 10}, "longer than hello_interval"},
		{"timers fine", types.ActionEntry{Type: "ospf_set_timers", Iface: "eth1", HelloInterval: 5, DeadInterval: 20}, ""},
		{"network type unknown", types.ActionEntry{Type: "ospf_set_network_type", Iface: "eth1", NetworkType: "serial"}, "network_type must be one of"},
		{"network type fine", types.ActionEntry{Type: "ospf_set_network_type", Iface: "eth1", NetworkType: "point-to-point"}, ""},
		{"area type unknown", types.ActionEntry{Type: "ospf_set_area_type", OSPFArea: "2", AreaType: "totally"}, "area_type must be one of"},
		{"area type fine", types.ActionEntry{Type: "ospf_set_area_type", OSPFArea: "2", AreaType: "totally-stub"}, ""},
		{"auth type unknown", types.ActionEntry{Type: "ospf_set_authentication", Iface: "eth1", AuthType: "sha", AuthKey: "k"}, "auth_type must be one of"},
		{"plain key too long", types.ActionEntry{Type: "ospf_set_authentication", Iface: "eth1", AuthType: "plain", AuthKey: "123456789"}, "at most 8 characters"},
		{"md5 key fine", types.ActionEntry{Type: "ospf_set_authentication", Iface: "eth1", AuthType: "md5", AuthKey: "1234567890123456"}, ""},
		{"key id too big", types.ActionEntry{Type: "ospf_set_authentication", Iface: "eth1", AuthType: "md5", AuthKey: "k", AuthKeyID: 300}, "between 1 and 255"},
		{"remove auth defaults the key id", types.ActionEntry{Type: "ospf_remove_authentication", Iface: "eth1", AuthType: "md5"}, ""},
	}
	for _, c := range cases {
		d, ok := actions.Lookup(c.a.Type)
		if !ok {
			t.Fatalf("%s: action %s not registered", c.name, c.a.Type)
		}
		err := d.Validate(c.a)
		if err == nil {
			_, err = d.Run(FRRRouterDriver{}, c.a)
		}
		if c.want == "" && err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

func TestFRRBGP(t *testing.T) {
	var d FRRRouterDriver
	conf := []string{"vtysh", "-c", "configure terminal"}
	bgp := func(lines ...string) [][]string {
		argv := append(append([]string{}, conf...), "-c", "router bgp")
		for _, l := range lines {
			argv = append(argv, "-c", l)
		}
		return [][]string{argv}
	}
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"router with id", d.BGPSetRouter(65001, "10.255.0.1"), [][]string{append(append([]string{}, conf...), "-c", "router bgp 65001", "-c", "no bgp ebgp-requires-policy", "-c", "bgp router-id 10.255.0.1")}},
		{"router without id", d.BGPSetRouter(65001, ""), [][]string{append(append([]string{}, conf...), "-c", "router bgp 65001", "-c", "no bgp ebgp-requires-policy", "-c", "no bgp router-id")}},
		{"no router", d.BGPRemoveRouter(), [][]string{append(append([]string{}, conf...), "-c", "no router bgp")}},
		{"neighbor v4 plain", d.BGPAddNeighbor("10.0.0.2", 65002, "", 0, false), bgp("neighbor 10.0.0.2 remote-as 65002", "no neighbor 10.0.0.2 update-source", "no neighbor 10.0.0.2 ebgp-multihop", "address-family ipv4 unicast", "neighbor 10.0.0.2 activate")},
		{"neighbor v6 with options", d.BGPAddNeighbor("fd00::2", 65003, "eth1", 2, true), bgp("neighbor fd00::2 remote-as 65003", "neighbor fd00::2 update-source eth1", "neighbor fd00::2 ebgp-multihop 2", "address-family ipv6 unicast", "neighbor fd00::2 activate")},
		{"no neighbor", d.BGPRemoveNeighbor("10.0.0.2"), bgp("no neighbor 10.0.0.2")},
		{"network v4", d.BGPAddNetwork("10.1.0.0/24", false), bgp("address-family ipv4 unicast", "network 10.1.0.0/24")},
		{"no network v6", d.BGPRemoveNetwork("fd00:1::/64", true), bgp("address-family ipv6 unicast", "no network fd00:1::/64")},
		{"redistribute ospf v6", d.BGPRedistribute("ospf", true), bgp("address-family ipv6 unicast", "redistribute ospf6")},
		{"redistribute rip v6", d.BGPRedistribute("rip", true), bgp("address-family ipv6 unicast", "redistribute ripng")},
		{"no redistribute v4", d.BGPRemoveRedistribute("connected", false), bgp("address-family ipv4 unicast", "no redistribute connected")},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}
