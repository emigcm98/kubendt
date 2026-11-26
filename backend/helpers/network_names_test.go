package helpers

import (
	"strings"
	"testing"

	"kubendt/types"
)

// eth0 and lo stay a pod's own, on either end of a link; on the external end
// the name is a worker's NIC, where only lo is refused.
func TestLinkInterfaceNamesByEndpoint(t *testing.T) {
	nodes := []types.NodeSpec{{Name: "r1", Driver: ""}, {Name: "sw", Driver: ""}}
	cases := []struct {
		link    types.LinkSpec
		refused string
	}{
		{types.LinkSpec{Node: "r1-0", LocalIntf: "eth4", PeerNode: "external", PeerIntf: "eth0"}, ""},
		{types.LinkSpec{Node: "external", LocalIntf: "eth0", PeerNode: "r1-0", PeerIntf: "eth4"}, ""},
		{types.LinkSpec{Node: "r1-0", LocalIntf: "eth4", PeerNode: "external", PeerIntf: "ens18"}, ""},
		{types.LinkSpec{Node: "r1-0", LocalIntf: "eth4", PeerNode: "external", PeerIntf: "lo"}, "loopback"},
		{types.LinkSpec{Node: "r1-0", LocalIntf: "eth0", PeerNode: "external", PeerIntf: "ens18"}, "reserved"},
		{types.LinkSpec{Node: "r1-0", LocalIntf: "eth1", PeerNode: "sw-0", PeerIntf: "eth0"}, "reserved"},
		{types.LinkSpec{Node: "r1-0", LocalIntf: "eth1", PeerNode: "external", PeerIntf: "a/b"}, "invalid"},
	}
	for _, c := range cases {
		err := ValidateLinkInterfaceNamesForDrivers([]types.LinkSpec{c.link}, nodes)
		switch {
		case c.refused == "" && err != nil:
			t.Errorf("%+v refused: %v", c.link, err)
		case c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)):
			t.Errorf("%+v: want an error with %q, got %v", c.link, c.refused, err)
		}
	}
}
