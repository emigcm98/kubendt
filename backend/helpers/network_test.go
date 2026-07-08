package helpers

import (
	"testing"

	"kubendt/types"
)

func TestCRDPeerPodMapping(t *testing.T) {
	if got := ToCRDPeerPod("external"); got != "localhost" {
		t.Errorf("ToCRDPeerPod(external) = %q, want localhost", got)
	}
	if got := ToCRDPeerPod("router1"); got != "router1" {
		t.Errorf("ToCRDPeerPod(router1) = %q, want router1", got)
	}
	if got := FromCRDPeerPod("localhost"); got != "external" {
		t.Errorf("FromCRDPeerPod(localhost) = %q, want external", got)
	}
	if got := FromCRDPeerPod("router1"); got != "router1" {
		t.Errorf("FromCRDPeerPod(router1) = %q, want router1", got)
	}
}

func TestSplitIndexedPodName(t *testing.T) {
	cases := []struct {
		in       string
		wantBase string
		wantIdx  int
		wantOK   bool
	}{
		{"router-0", "router", 0, true},
		{"router-12", "router", 12, true},
		{"router", "", 0, false},
		{"dmz-sw", "", 0, false}, // non-digit tail is not an indexed pod
	}
	for _, c := range cases {
		base, idx, ok := SplitIndexedPodName(c.in)
		if ok != c.wantOK || (ok && (base != c.wantBase || idx != c.wantIdx)) {
			t.Errorf("SplitIndexedPodName(%q) = (%q,%d,%v), want (%q,%d,%v)",
				c.in, base, idx, ok, c.wantBase, c.wantIdx, c.wantOK)
		}
	}
}

func TestConvertToNodeNameIfSingleReplica(t *testing.T) {
	replicas := map[string]int{"router": 1, "sw": 3}
	if got := ConvertToNodeNameIfSingleReplica("router-0", replicas); got != "router" {
		t.Errorf("single-replica pod not collapsed: got %q", got)
	}
	if got := ConvertToNodeNameIfSingleReplica("sw-0", replicas); got != "sw-0" {
		t.Errorf("multi-replica pod should stay indexed: got %q", got)
	}
	if got := ConvertToNodeNameIfSingleReplica("router", replicas); got != "router" {
		t.Errorf("non-indexed name should be unchanged: got %q", got)
	}
}

func TestResolvePodNameFromLink(t *testing.T) {
	nodes := []types.NodeSpec{{Name: "router", Replicas: 1}}

	if got := ResolvePodNameFromLink("external", nodes); got != "external" {
		t.Errorf("external should be passed through: got %q", got)
	}
	if got := ResolvePodNameFromLink("router", nodes); got != "router-0" {
		t.Errorf("bare node name should resolve to -0: got %q", got)
	}
	if got := ResolvePodNameFromLink("router-1", nodes); got != "router-1" {
		t.Errorf("already-indexed reference should be kept: got %q", got)
	}
}

func TestValidateLinuxInterfaceName(t *testing.T) {
	valid := []string{"eth0", "lo", "veth123", "a"}
	for _, n := range valid {
		if err := ValidateLinuxInterfaceName(n); err != nil {
			t.Errorf("expected %q valid, got error: %v", n, err)
		}
	}
	invalid := []string{"", "this-name-is-too-long", "eth/0", "eth 0", "eth:0"}
	for _, n := range invalid {
		if err := ValidateLinuxInterfaceName(n); err == nil {
			t.Errorf("expected %q invalid, got no error", n)
		}
	}
}
