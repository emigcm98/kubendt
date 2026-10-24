package helpers

import "testing"

func TestToolClassOf(t *testing.T) {
	cases := []struct {
		argv []string
		want toolClass
	}{
		{[]string{"ip", "addr", "replace", "10.0.0.1/24", "dev", "eth1"}, toolNetns},
		{[]string{"bridge", "link"}, toolNetns},
		{[]string{"sh", "-c", "! ip -o addr show dev eth1 | grep -qF ' 10.0.0.1/24 ' || ip addr del 10.0.0.1/24 dev eth1"}, toolNetns},
		{[]string{"sh", "-c", "ip link set dev eth1 mtu 1450; ip addr replace fd00::1/64 dev eth1"}, toolNetns},
		{[]string{"sh", "-c", "iptables -t nat -C POSTROUTING -o eth0 -j MASQUERADE || iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE"}, toolIptables},
		{[]string{"iptables", "-t", "nat", "-S", "POSTROUTING"}, toolIptables},
		{[]string{"vtysh", "-c", "configure terminal", "-c", "no ip route 10.1.0.0/16 10.0.0.254"}, toolNone},
		{[]string{"sh", "-c", "vtysh -c 'show running-config' | grep '^ip route 0\\.0\\.0\\.0/0 ' | while read -r l; do vtysh -c 'configure terminal' -c \"no $l\"; done"}, toolNone},
		{[]string{"sh", "-c", "grep -q '^nameserver 8.8.8.8$' /etc/resolv.conf || echo 'nameserver 8.8.8.8' | tee -a /etc/resolv.conf > /dev/null"}, toolNone},
		{[]string{"cat", "/proc/net/dev"}, toolNone},
		{nil, toolNone},
	}
	for _, c := range cases {
		if got := toolClassOf(c.argv); got != c.want {
			t.Errorf("%v: got %q want %q", c.argv, got, c.want)
		}
	}
	rows := [][]string{{"ip", "link", "set", "eth1", "up"}, {"sh", "-c", "iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE"}}
	if got := toolClassOfRows(rows); got != toolIptables {
		t.Errorf("rows with iptables: got %q", got)
	}
}

func TestDefaultToolboxImageFollowsTheVersion(t *testing.T) {
	t.Setenv("KUBENDT_TOOLBOX_IMAGE", "")
	t.Setenv("KUBENDT_CAPTURE_IMAGE", "")
	t.Setenv("KUBENDT_VERSION", "")
	if got := ToolboxImage(); got != "ghcr.io/emigcm98/kubendt-toolbox:latest" {
		t.Errorf("dev backend: %s", got)
	}
	t.Setenv("KUBENDT_VERSION", "1.7.0")
	if got := ToolboxImage(); got != "ghcr.io/emigcm98/kubendt-toolbox:1.7.0" {
		t.Errorf("released backend: %s", got)
	}
	t.Setenv("KUBENDT_TOOLBOX_IMAGE", "nicolaka/netshoot:v0.16")
	if got := ToolboxImage(); got != "nicolaka/netshoot:v0.16" {
		t.Errorf("override: %s", got)
	}
}
