package capabilities

import (
	"reflect"
	"testing"
)

func TestParseBridgeVLAN(t *testing.T) {
	iproute2 := `port              vlan-id  
br0               1 PVID Egress Untagged
eth1              10 PVID Egress Untagged
eth2              10
                  20
eth3              10
                  99 PVID Egress Untagged
`
	want := Rows{
		{"port": "br0", "mode": "access", "vlans": "1", "native": "1"},
		{"port": "eth1", "mode": "access", "vlans": "10", "native": "10"},
		{"port": "eth2", "mode": "trunk", "vlans": "10,20", "native": ""},
		{"port": "eth3", "mode": "trunk", "vlans": "10", "native": "99"},
	}
	if got := parseBridgeVLAN(iproute2); !reflect.DeepEqual(got, want) {
		t.Errorf("iproute2 format: got %v, want %v", got, want)
	}
	old := "port\tvlan ids\neth1\t 1 PVID Egress Untagged\n\t 10\n"
	want = Rows{{"port": "eth1", "mode": "trunk", "vlans": "10", "native": "1"}}
	if got := parseBridgeVLAN(old); !reflect.DeepEqual(got, want) {
		t.Errorf("old format: got %v, want %v", got, want)
	}
}

func TestVLANBaseCommands(t *testing.T) {
	cmds := VLANBase{}.VLANSetTrunk("eth3", []int{10, 20, 99}, 99)
	if len(cmds) != 1 || len(cmds[0]) != 3 {
		t.Fatalf("one sh -c expected, got %v", cmds)
	}
	script := cmds[0][2]
	for _, want := range []string{"vlan_filtering 1", "bridge vlan del dev eth3 vid 1-4094", "bridge vlan add dev eth3 vid 10;", "bridge vlan add dev eth3 vid 20;", "bridge vlan add dev eth3 vid 99 pvid untagged"} {
		if !containsStr(script, want) {
			t.Errorf("trunk script lacks %q: %s", want, script)
		}
	}
	if containsStr(script, "vid 99;") {
		t.Errorf("the native VLAN must not be added tagged too: %s", script)
	}
}

func containsStr(s, sub string) bool { return len(sub) == 0 || indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
