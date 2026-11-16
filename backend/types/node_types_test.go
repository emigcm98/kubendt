package types

import "testing"

func TestRadioRolesAreHosts(t *testing.T) {
	for _, typ := range []string{"host", "switch", "router", "ue", "gnb"} {
		if !ValidNodeType(typ) {
			t.Errorf("%q should be a valid node type", typ)
		}
	}
	if ValidNodeType("phone") || ValidNodeType("") {
		t.Error("unknown types must be refused")
	}
	if BaseNodeType("ue") != "host" || BaseNodeType("gnb") != "host" {
		t.Error("ue and gnb are hosts to the platform")
	}
	if BaseNodeType("router") != "router" || BaseNodeType("switch") != "switch" {
		t.Error("the base types map to themselves")
	}
}
