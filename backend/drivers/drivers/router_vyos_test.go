package drivers

import (
	"reflect"
	"testing"
)

func TestVyOSRouteFamilies(t *testing.T) {
	var d VyOSRouterDriver
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"default v4", d.SetDefaultRoute("10.0.0.254"), [][]string{{"set protocols static route 0.0.0.0/0 next-hop 10.0.0.254"}}},
		{"default v6", d.SetDefaultRoute("fd00:1::2"), [][]string{{"set protocols static route6 ::/0 next-hop fd00:1::2"}}},
		{"remove default v4", d.RemoveDefaultRoute(), [][]string{{"delete protocols static route 0.0.0.0/0"}}},
		{"remove default v6", d.RemoveDefaultRoute6(), [][]string{{"delete protocols static route6 ::/0"}}},
		{"static v4", d.AddStaticRoute("10.1.0.0/16", "10.0.0.254", ""), [][]string{{"set protocols static route 10.1.0.0/16 next-hop 10.0.0.254"}}},
		{"static v6", d.AddStaticRoute("fd00:99::/64", "fd00:1::2", ""), [][]string{{"set protocols static route6 fd00:99::/64 next-hop fd00:1::2"}}},
		{"remove static v6", d.RemoveStaticRoute("fd00:99::/64", "fd00:1::2", ""), [][]string{{"delete protocols static route6 fd00:99::/64 next-hop fd00:1::2"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}

func TestVyOSInterfaceEntryAddresses(t *testing.T) {
	dual := vyosInterfaceEntry("eth1", map[string]interface{}{
		"address": []interface{}{"10.0.10.1/30", "fd00:10::1/64", "10.0.10.5/30"},
		"hw-id":   "aa:bb:cc:dd:ee:ff",
		"mtu":     "1450",
	})
	want := map[string]string{
		"interface": "eth1", "guestInterface": "eth1", "state": "up",
		"mac": "aa:bb:cc:dd:ee:ff", "ipv4": "10.0.10.1/30", "ipv6": "fd00:10::1/64", "mtu": "1450",
		"addresses": "10.0.10.1/30,fd00:10::1/64,10.0.10.5/30",
	}
	if !reflect.DeepEqual(dual, want) {
		t.Errorf("dual stack entry:\n got  %v\n want %v", dual, want)
	}

	only6 := vyosInterfaceEntry("eth2", map[string]interface{}{"address": "fd00:2::1/64"})
	if _, has := only6["ipv4"]; has || only6["ipv6"] != "fd00:2::1/64" || only6["addresses"] != "fd00:2::1/64" {
		t.Errorf("v6-only entry: %v", only6)
	}

	none := vyosInterfaceEntry("eth3", map[string]interface{}{"disable": map[string]interface{}{}})
	if _, has := none["ipv4"]; has || none["state"] != "down" {
		t.Errorf("unaddressed entry: %v", none)
	}
}

func TestVyOSRIPFamilies(t *testing.T) {
	var d VyOSRouterDriver
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"network v4", d.RIPAddNetwork("10.0.0.0/24", false), [][]string{{"set protocols rip network 10.0.0.0/24"}}},
		{"network v6", d.RIPAddNetwork("fd00::/64", true), [][]string{{"set protocols ripng network fd00::/64"}}},
		{"passive v6", d.RIPPassiveInterface("eth1", true), [][]string{{"set protocols ripng passive-interface eth1"}}},
		{"redistribute ospf v6", d.RIPRedistribute("ospf", true), [][]string{{"set protocols ripng redistribute ospfv3"}}},
		{"remove redistribute v4", d.RIPRemoveRedistribute("connected", false), [][]string{{"delete protocols rip redistribute connected"}}},
		{"timers", d.RIPSetTimers(10, 60, 40, false), [][]string{{"set protocols rip timers update 10"}, {"set protocols rip timers timeout 60"}, {"set protocols rip timers garbage-collection 40"}}},
		{"no timers v6", d.RIPRemoveTimers(true), [][]string{{"delete protocols ripng timers"}}},
		{"ospf redistribute rip", d.OSPFRedistribute("rip"), [][]string{{"set protocols ospf redistribute rip"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}

func TestVyOSOSPF6(t *testing.T) {
	var d VyOSRouterDriver
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"router id", d.OSPF6SetRouterID("10.0.0.1"), [][]string{{"set protocols ospfv3 parameters router-id 10.0.0.1"}}},
		{"interface area", d.OSPF6AddInterface("eth2", "0"), [][]string{{"set protocols ospfv3 interface eth2 area 0"}}},
		{"remove interface", d.OSPF6RemoveInterface("eth2", "0"), [][]string{{"delete protocols ospfv3 interface eth2 area"}}},
		{"passive", d.OSPF6PassiveInterface("eth1"), [][]string{{"set protocols ospfv3 interface eth1 passive"}}},
		{"redistribute", d.OSPF6Redistribute("ripng"), [][]string{{"set protocols ospfv3 redistribute ripng"}}},
		{"originate", d.OSPF6OriginateDefault(), [][]string{{"set protocols ospfv3 default-information originate always"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}

func TestVyOSOSPFInterface(t *testing.T) {
	var d VyOSRouterDriver
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"cost", d.OSPFSetCost("eth2", 100), [][]string{{"set protocols ospf interface eth2 cost 100"}}},
		{"no cost", d.OSPFRemoveCost("eth2"), [][]string{{"delete protocols ospf interface eth2 cost"}}},
		{"priority", d.OSPFSetPriority("eth1", 0), [][]string{{"set protocols ospf interface eth1 priority 0"}}},
		{"timers", d.OSPFSetTimers("eth2", 5, 20), [][]string{{"set protocols ospf interface eth2 hello-interval 5"}, {"set protocols ospf interface eth2 dead-interval 20"}}},
		{"network type", d.OSPFSetNetworkType("eth2", "point-to-point"), [][]string{{"set protocols ospf interface eth2 network point-to-point"}}},
		{"no network type", d.OSPFRemoveNetworkType("eth2"), [][]string{{"delete protocols ospf interface eth2 network"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}

func TestVyOSOSPFAreasAndAuth(t *testing.T) {
	var d VyOSRouterDriver
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"stub", d.OSPFSetAreaType("3", "stub"), [][]string{{"set protocols ospf area 3 area-type stub"}}},
		{"totally stub", d.OSPFSetAreaType("2", "totally-stub"), [][]string{{"set protocols ospf area 2 area-type stub no-summary"}}},
		{"no area type", d.OSPFRemoveAreaType("2", "totally-stub"), [][]string{{"delete protocols ospf area 2 area-type"}}},
		{"range", d.OSPFAddAreaRange("2", "172.22.16.64/26"), [][]string{{"set protocols ospf area 2 range 172.22.16.64/26"}}},
		{"plain auth", d.OSPFSetAuthentication("eth1", "plain", "pass", 1), [][]string{{"set protocols ospf interface eth1 authentication plaintext-password pass"}}},
		{"md5 auth", d.OSPFSetAuthentication("eth1", "md5", "s3cret", 2), [][]string{{"set protocols ospf interface eth1 authentication md5 key-id 2 md5-key s3cret"}}},
		{"no auth", d.OSPFRemoveAuthentication("eth1", "md5", 2), [][]string{{"delete protocols ospf interface eth1 authentication"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}

func TestVyOSBGP(t *testing.T) {
	var d VyOSRouterDriver
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"router with id", d.BGPSetRouter(65001, "10.255.0.1"), [][]string{{"set protocols bgp system-as 65001"}, {"set protocols bgp parameters router-id 10.255.0.1"}}},
		{"router without id", d.BGPSetRouter(65001, ""), [][]string{{"set protocols bgp system-as 65001"}}},
		{"no router", d.BGPRemoveRouter(), [][]string{{"delete protocols bgp"}}},
		{"neighbor v4 plain", d.BGPAddNeighbor("10.0.0.2", 65002, "", 0, false), [][]string{{"set protocols bgp neighbor 10.0.0.2 remote-as 65002"}, {"set protocols bgp neighbor 10.0.0.2 address-family ipv4-unicast"}}},
		{"neighbor v6 with options", d.BGPAddNeighbor("fd00::2", 65003, "eth1", 2, true), [][]string{{"set protocols bgp neighbor fd00::2 remote-as 65003"}, {"set protocols bgp neighbor fd00::2 update-source eth1"}, {"set protocols bgp neighbor fd00::2 ebgp-multihop 2"}, {"set protocols bgp neighbor fd00::2 address-family ipv6-unicast"}}},
		{"no neighbor", d.BGPRemoveNeighbor("10.0.0.2"), [][]string{{"delete protocols bgp neighbor 10.0.0.2"}}},
		{"network v4", d.BGPAddNetwork("10.1.0.0/24", false), [][]string{{"set protocols bgp address-family ipv4-unicast network 10.1.0.0/24"}}},
		{"no network v6", d.BGPRemoveNetwork("fd00:1::/64", true), [][]string{{"delete protocols bgp address-family ipv6-unicast network fd00:1::/64"}}},
		{"redistribute ospf v6", d.BGPRedistribute("ospf", true), [][]string{{"set protocols bgp address-family ipv6-unicast redistribute ospfv3"}}},
		{"redistribute rip v6", d.BGPRedistribute("rip", true), [][]string{{"set protocols bgp address-family ipv6-unicast redistribute ripng"}}},
		{"no redistribute v4", d.BGPRemoveRedistribute("connected", false), [][]string{{"delete protocols bgp address-family ipv4-unicast redistribute connected"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}

func TestVyOSISIS(t *testing.T) {
	var d VyOSRouterDriver
	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{"router level-2", d.ISISSetRouter("49.0001.0000.0000.0001.00", "level-2"), [][]string{{"set protocols isis net 49.0001.0000.0000.0001.00"}, {"set protocols isis lsp-mtu 1277"}, {"set protocols isis level level-2"}}},
		{"router default level", d.ISISSetRouter("49.0001.0000.0000.0001.00", ""), [][]string{{"set protocols isis net 49.0001.0000.0000.0001.00"}, {"set protocols isis lsp-mtu 1277"}}},
		{"no router", d.ISISRemoveRouter(), [][]string{{"delete protocols isis"}}},
		{"interface", d.ISISAddInterface("eth1", "", true, true), [][]string{{"set protocols isis interface eth1"}}},
		{"interface level-1, family ignored", d.ISISAddInterface("eth1", "level-1", false, true), [][]string{{"set protocols isis interface eth1"}, {"set protocols isis interface eth1 circuit-type level-1"}}},
		{"interface level-2 is level-2-only", d.ISISAddInterface("eth1", "level-2", true, true), [][]string{{"set protocols isis interface eth1"}, {"set protocols isis interface eth1 circuit-type level-2-only"}}},
		{"no interface", d.ISISRemoveInterface("eth1"), [][]string{{"delete protocols isis interface eth1"}}},
		{"passive", d.ISISPassiveInterface("eth1"), [][]string{{"set protocols isis interface eth1 passive"}}},
		{"metric", d.ISISSetMetric("eth1", 50), [][]string{{"set protocols isis interface eth1 metric 50"}}},
		{"point-to-point", d.ISISSetNetworkType("eth1", "point-to-point"), [][]string{{"set protocols isis interface eth1 network point-to-point"}}},
		{"no network type", d.ISISRemoveNetworkType("eth1"), [][]string{{"delete protocols isis interface eth1 network"}}},
		{"redistribute v4", d.ISISRedistribute("connected", "level-2", false), [][]string{{"set protocols isis redistribute ipv4 connected level-2"}}},
		{"no redistribute ospf v6 drops the source node", d.ISISRemoveRedistribute("ospf", "level-1", true), [][]string{{"delete protocols isis redistribute ipv6 ospfv3"}}},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}

func TestVyOSNAT66(t *testing.T) {
	var d VyOSRouterDriver
	v4 := d.EnableSNAT("eth2", false)
	v6 := d.EnableSNAT("eth2", true)
	if v4[0][0] != "set nat source rule "+vyosNATRuleNum("eth2", 100)+" outbound-interface name eth2" || v4[1][0] != "set nat source rule "+vyosNATRuleNum("eth2", 100)+" source address 0.0.0.0/0" {
		t.Errorf("SNAT v4: %v", v4)
	}
	if v6[0][0] != "set nat66 source rule "+vyosNATRuleNum("eth2", 100)+" outbound-interface name eth2" || v6[1][0] != "set nat66 source rule "+vyosNATRuleNum("eth2", 100)+" source prefix ::/0" || v6[2][0] != "set nat66 source rule "+vyosNATRuleNum("eth2", 100)+" translation address masquerade" {
		t.Errorf("SNAT v6: %v", v6)
	}
	if got := d.DisableSNAT("eth2", true); got[0][0] != "delete nat66 source rule "+vyosNATRuleNum("eth2", 100) {
		t.Errorf("no SNAT v6: %v", got)
	}
	dnat := d.EnableDNAT("eth2", 8080, "fd00:20::10", 80, "tcp", true)
	rule := vyosNATRuleNum("eth28080tcp", 200)
	if dnat[0][0] != "set nat66 destination rule "+rule+" inbound-interface name eth2" || dnat[3][0] != "set nat66 destination rule "+rule+" translation address fd00:20::10" || dnat[4][0] != "set nat66 destination rule "+rule+" translation port 80" {
		t.Errorf("DNAT v6: %v", dnat)
	}
	if got := d.DisableDNAT("eth2", 8080, "fd00:20::10", 80, "tcp", true); got[0][0] != "delete nat66 destination rule "+rule {
		t.Errorf("no DNAT v6: %v", got)
	}
}

func TestVyOSNAT64(t *testing.T) {
	var d VyOSRouterDriver
	if got := d.NAT64Enable("64:ff9b::/96", "172.22.19.0/24"); len(got) != 3 || got[0][0] != "set nat64 source rule 1 source prefix 64:ff9b::/96" || got[1][0] != "set nat64 source rule 1 translation pool 1 address 172.22.19.0/24" || got[2][0] != "set nat64 source rule 1 translation pool 1 port 1024-32767" {
		t.Errorf("enable: %v", got)
	}
	if got := d.NAT64Disable(); got[0][0] != "delete nat64" {
		t.Errorf("disable: %v", got)
	}
}

func TestVyOSVtyshArgvQuotesTheCommand(t *testing.T) {
	got := vyosVtyshArgv("show bgp summary json")
	if len(got) != 5 || got[0] != "ssh_qemu" || got[1] != "sudo" || got[2] != "vtysh" || got[3] != "-c" || got[4] != "'show bgp summary json'" {
		t.Errorf("argv: %v", got)
	}
}
