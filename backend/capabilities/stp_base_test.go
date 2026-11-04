package capabilities

import (
	"reflect"
	"testing"
)

// A Linux switch of a three-switch ring, read from sysfs: not the root, eth3
// its root port, eth2 blocking.
const stpSysfsSample = `B br0 1 32768 8000.26c079c3ac0a 8000.0a0d94c4e46d 3 2
P eth1 br0 3 2 32 0x1
P eth2 br0 4 100 32 0x2
P eth3 br0 3 2 32 0x3
`

func TestParseSTPSysfs(t *testing.T) {
	bridges, ports := parseSTPSysfs(stpSysfsSample)
	wantBridges := Rows{{"bridge": "br0", "mode": "stp", "priority": "32768", "bridge_id": "8000.26c079c3ac0a", "root_id": "8000.0a0d94c4e46d", "root": "no", "root_port": "eth3"}}
	if !reflect.DeepEqual(bridges, wantBridges) {
		t.Errorf("bridges: got %v\nwant %v", bridges, wantBridges)
	}
	wantPorts := Rows{
		{"bridge": "br0", "port": "eth1", "role": "designated", "state": "forwarding", "cost": "2", "priority": "32"},
		{"bridge": "br0", "port": "eth2", "role": "alternate", "state": "blocking", "cost": "100", "priority": "32"},
		{"bridge": "br0", "port": "eth3", "role": "root", "state": "forwarding", "cost": "2", "priority": "32"},
	}
	if !reflect.DeepEqual(ports, wantPorts) {
		t.Errorf("ports: got %v\nwant %v", ports, wantPorts)
	}
	if b, p := parseSTPSysfs(""); len(b) != 0 || len(p) != 0 {
		t.Errorf("no bridge: got %v %v", b, p)
	}
}
