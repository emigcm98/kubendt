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
