package capabilities

import (
	"reflect"
	"testing"
)

func TestL3BaseCommands(t *testing.T) {
	var l3 L3Base

	cases := []struct {
		name string
		got  [][]string
		want [][]string
	}{
		{
			"SetIP",
			l3.SetIP("eth0", "10.0.0.1/24"),
			[][]string{{"ip", "addr", "replace", "10.0.0.1/24", "dev", "eth0"}},
		},
		{
			"ReplaceIP",
			l3.ReplaceIP("eth0", "10.0.0.1/24"),
			[][]string{
				{"ip", "addr", "flush", "dev", "eth0"},
				{"ip", "addr", "add", "10.0.0.1/24", "dev", "eth0"},
			},
		},
		{
			"RemoveIP",
			l3.RemoveIP("eth0", "10.0.0.1/24"),
			[][]string{{"sh", "-c", "! ip -o addr show dev eth0 | grep -qF ' 10.0.0.1/24 ' || ip addr del 10.0.0.1/24 dev eth0"}},
		},
		{
			"SetDefaultRoute",
			l3.SetDefaultRoute("10.0.0.254"),
			[][]string{{"ip", "route", "replace", "default", "via", "10.0.0.254"}},
		},
		{
			"RemoveDefaultRoute",
			l3.RemoveDefaultRoute(),
			[][]string{{"ip", "route", "del", "default"}},
		},
		{
			"SetDefaultRoute IPv6",
			l3.SetDefaultRoute("fd00:1::2"),
			[][]string{{"ip", "-6", "route", "replace", "default", "via", "fd00:1::2"}},
		},
		{
			"RemoveDefaultRoute6",
			l3.RemoveDefaultRoute6(),
			[][]string{{"ip", "-6", "route", "del", "default"}},
		},
		{
			"AddStaticRoute IPv6",
			l3.AddStaticRoute("fd00:99::/64", "fd00:1::2", ""),
			[][]string{{"ip", "route", "replace", "fd00:99::/64", "via", "fd00:1::2"}},
		},
		{
			"AddStaticRoute with dev",
			l3.AddStaticRoute("192.168.1.0/24", "10.0.0.254", "eth1"),
			[][]string{{"ip", "route", "replace", "192.168.1.0/24", "via", "10.0.0.254", "dev", "eth1"}},
		},
		{
			"AddStaticRoute without dev",
			l3.AddStaticRoute("192.168.1.0/24", "10.0.0.254", ""),
			[][]string{{"ip", "route", "replace", "192.168.1.0/24", "via", "10.0.0.254"}},
		},
		{
			"RemoveStaticRoute without dev",
			l3.RemoveStaticRoute("192.168.1.0/24", "10.0.0.254", ""),
			[][]string{{"ip", "route", "del", "192.168.1.0/24", "via", "10.0.0.254"}},
		},
	}

	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, c.got, c.want)
		}
	}
}
