package helpers

import (
	"reflect"
	"testing"
)

func TestParseInterfaceListing(t *testing.T) {
	out := `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
    inet6 ::1/128 scope host
       valid_lft forever preferred_lft forever
2: eth0@if12: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1450 qdisc noqueue state UP
    link/ether 7a:a3:7d:a5:45:c8 brd ff:ff:ff:ff:ff:ff
    inet 10.244.1.201/24 brd 10.244.1.255 scope global eth0
       valid_lft forever preferred_lft forever
    inet6 fe80::78a3:7dff:fea5:45c8/64 scope link
       valid_lft forever preferred_lft forever
17875: eth1: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1450 qdisc noqueue state UNKNOWN qlen 1000
    link/ether 1e:37:f6:bb:08:0d brd ff:ff:ff:ff:ff:ff
    inet6 fe80::1c37:f6ff:febb:80d/64 scope link
       valid_lft forever preferred_lft forever
    inet6 fd00:12::1/64 scope global
       valid_lft forever preferred_lft forever
    inet6 fd00:12::11/64 scope global
       valid_lft forever preferred_lft forever
17877: eth2@if17878: <BROADCAST,MULTICAST> mtu 1500 qdisc noop state DOWN qlen 1000
    link/ether 5e:0e:52:63:6f:fb brd ff:ff:ff:ff:ff:ff
    inet 10.0.2.1/30 brd 10.0.2.3 scope global eth2
       valid_lft forever preferred_lft forever
    inet6 fd00:2::1/64 scope global
       valid_lft forever preferred_lft forever
20: tap1: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1450 qdisc fq_codel state UNKNOWN qlen 1000
    link/ether 02:aa:bb:cc:dd:ee brd ff:ff:ff:ff:ff:ff
`
	got, err := parseInterfaceListing(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]string{
		{"interface": "eth0", "mac": "7a:a3:7d:a5:45:c8", "ipv4": "10.244.1.201/24", "ipv6": "", "state": "up", "mtu": "1450"},
		{"interface": "eth1", "mac": "1e:37:f6:bb:08:0d", "ipv4": "", "ipv6": "fd00:12::1/64", "state": "up", "mtu": "1450"},
		{"interface": "eth2", "mac": "5e:0e:52:63:6f:fb", "ipv4": "10.0.2.1/30", "ipv6": "fd00:2::1/64", "state": "down", "mtu": "1500"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listing:\n got  %v\n want %v", got, want)
	}
}
