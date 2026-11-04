package helpers

import "testing"

func TestToolClassOfBridge(t *testing.T) {
	script := "b=$(ip link show eth1 | sed -n 's/.* master \\([^ ]*\\).*/\\1/p'); bridge vlan add dev eth1 vid 10 pvid untagged"
	if got := toolClassOf([]string{"sh", "-c", script}); got != toolBridge {
		t.Errorf("a script with bridge is class %q, want bridge", got)
	}
	if got := toolClassOf([]string{"bridge", "vlan", "show"}); got != toolBridge {
		t.Errorf("bridge vlan show is class %q, want bridge", got)
	}
	if got := toolClassOf([]string{"ip", "link", "set", "br0", "type", "bridge", "vlan_filtering", "1"}); got != toolBridge {
		t.Errorf("ip ... type bridge is class %q, want bridge", got)
	}
	if got := toolClassOf([]string{"ip", "link", "set", "eth1", "type", "bridge_slave", "cost", "50"}); got != toolBridge {
		t.Errorf("ip ... type bridge_slave is class %q, want bridge", got)
	}
	if got := toolClassOf([]string{"ip", "link", "add", "br0", "type", "bridge"}); got != toolNetns {
		t.Errorf("a bare ip link add type bridge is class %q, want netns", got)
	}
	if got := toolClassOf([]string{"sh", "-c", "ip link show br0 || ip link add br0 type bridge"}); got != toolNetns {
		t.Errorf("setup_bridge's script is class %q, want netns", got)
	}
	rows := [][]string{{"ip", "link", "set", "eth1", "up"}, {"bridge", "vlan", "add", "dev", "eth1", "vid", "10"}}
	if got := toolClassOfRows(rows); got != toolBridge {
		t.Errorf("rows with bridge are class %q, want bridge", got)
	}
}
