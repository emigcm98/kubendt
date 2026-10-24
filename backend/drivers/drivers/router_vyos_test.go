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
