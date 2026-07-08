package helpers

import (
	"strings"
	"testing"

	drivers "kubendt/drivers"
	"kubendt/types"
)

func TestValidateContainerPath(t *testing.T) {
	ok := []string{"/etc/frr/ospfd.conf", "/dev/net/tun", "/a"}
	for _, p := range ok {
		if err := validateContainerPath("mount target", p); err != nil {
			t.Errorf("expected %q to be valid, got: %v", p, err)
		}
	}
	bad := []string{"", "etc/frr/x.conf", "../escape", "/etc/../../x"}
	for _, p := range bad {
		if err := validateContainerPath("mount target", p); err == nil {
			t.Errorf("expected %q to be rejected", p)
		}
	}
}

func TestValidateLinkIPs(t *testing.T) {
	good := []types.LinkSpec{
		{LocalIP: "10.0.0.1/24", PeerIP: "10.0.0.2/24"},
		{LocalIP: "", PeerIP: ""}, // L2-only link
		{LocalIP: "192.168.1.5"},  // bare IP allowed
	}
	if err := ValidateLinkIPs(good); err != nil {
		t.Errorf("expected valid links to pass, got: %v", err)
	}

	bad := [][]types.LinkSpec{
		{{LocalIP: "10.0.0.999/24"}},
		{{PeerIP: "not-an-ip"}},
		{{LocalIP: "10.0.0.1/33"}},
	}
	for _, links := range bad {
		if err := ValidateLinkIPs(links); err == nil {
			t.Errorf("expected %+v to be rejected", links)
		}
	}
}

func TestValidateNodeInputsEnvAndImage(t *testing.T) {
	// Missing image.
	if err := ValidateNodeInputs("ns", []types.NodeSpec{{Name: "n1", Type: "host"}}); err == nil {
		t.Error("expected missing image to be rejected")
	}
	// Bad env var name.
	err := ValidateNodeInputs("ns", []types.NodeSpec{{
		Name: "n1", Type: "host", Image: "img:latest",
		Env: map[string]string{"1BAD": "x"},
	}})
	if err == nil || !strings.Contains(err.Error(), "environment variable name") {
		t.Errorf("expected env var name rejection, got: %v", err)
	}
	// Valid node with good env, no mounts (avoids needing a cluster).
	if err := ValidateNodeInputs("ns", []types.NodeSpec{{
		Name: "n1", Type: "host", Image: "img:latest",
		Env: map[string]string{"MY_VAR": "1", "_ok2": "y"},
	}}); err != nil {
		t.Errorf("expected valid node to pass, got: %v", err)
	}
}

func TestValidateLinkIPCapabilities(t *testing.T) {
	drivers.RegisterAllDrivers()

	nodes := []types.NodeSpec{
		{Name: "sw1", Type: "switch"}, // default driver: L2-only switch
		{Name: "r1", Type: "router"},  // default driver: L3-capable
	}

	// IP on a switch endpoint must be rejected.
	swLink := []types.LinkSpec{{Node: "sw1", PeerNode: "r1", LocalIP: "10.0.0.1/24"}}
	if err := ValidateLinkIPCapabilities(swLink, nodes); err == nil {
		t.Error("expected IP on a switch to be rejected")
	} else if !strings.Contains(err.Error(), "no L3 support") {
		t.Errorf("unexpected error: %v", err)
	}

	// IP on the peer side that is a switch must also be rejected.
	peerLink := []types.LinkSpec{{Node: "r1", PeerNode: "sw1", PeerIP: "10.0.0.2/24"}}
	if err := ValidateLinkIPCapabilities(peerLink, nodes); err == nil {
		t.Error("expected IP on peer switch to be rejected")
	}

	// IP between two routers is fine.
	okLink := []types.LinkSpec{{Node: "r1", PeerNode: "r1", LocalIP: "10.0.0.1/24", PeerIP: "10.0.0.2/24"}}
	if err := ValidateLinkIPCapabilities(okLink, nodes); err != nil {
		t.Errorf("expected router-router IP link to pass, got: %v", err)
	}

	// L2-only link to a switch (no IP) is fine.
	l2Link := []types.LinkSpec{{Node: "sw1", PeerNode: "r1"}}
	if err := ValidateLinkIPCapabilities(l2Link, nodes); err != nil {
		t.Errorf("expected L2-only switch link to pass, got: %v", err)
	}
}
