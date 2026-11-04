package drivers

import (
	"reflect"
	"testing"

	"kubendt/capabilities"
)

func TestParseOVSVLANPorts(t *testing.T) {
	out := "br0\n---\n" + `{"data":[["br0",["set",[]],["set",[]],["set",[]]],["eth1","access",10,["set",[]]],["eth2","trunk",["set",[]],["set",[10,20]]],["eth3","native-untagged",99,["set",[10,20]]],["eth4",["set",[]],["set",[]],["set",[]]],["eth5",["set",[]],["set",[]],30]],"headings":["name","vlan_mode","tag","trunks"]}`
	want := capabilities.Rows{
		{"port": "eth1", "mode": "access", "vlans": "10", "native": ""},
		{"port": "eth2", "mode": "trunk", "vlans": "10,20", "native": ""},
		{"port": "eth3", "mode": "native-untagged", "vlans": "10,20", "native": "99"},
		{"port": "eth4", "mode": "none", "vlans": "", "native": ""},
		{"port": "eth5", "mode": "trunk", "vlans": "30", "native": ""},
	}
	got, err := parseOVSVLANPorts(out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}
