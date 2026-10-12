package helpers

import (
	"encoding/json"
	"testing"

	"kubendt/types"
)

func TestBuildNetworkConf(t *testing.T) {
	capture := true
	ops := []PersistedDriverOperation{
		{ID: 1, PodName: "router1-0", Action: types.ActionEntry{Type: "set_ip", Iface: "eth1", CIDR: "10.0.0.1/24"}},
		{ID: 2, PodName: "host-0", Action: types.ActionEntry{Type: "replace_ip", Iface: "eth1", CIDR: "10.0.0.2/24", Options: &types.ActionOptions{CaptureOutput: &capture}}},
		{ID: 3, PodName: "router1-0", ActionType: "enable_snat", Action: types.ActionEntry{Iface: "eth0"}},
		{ID: 4, PodName: "gone-0", Action: types.ActionEntry{Type: "set_ip", Iface: "eth1", CIDR: "10.0.0.9/24"}},
		{ID: 5, PodName: "host-0", Action: types.ActionEntry{Type: "custom", Command: json.RawMessage(`"echo hi"`), Executor: "kubectl"}},
	}
	got := buildNetworkConf(ops, map[string]bool{"router1-0": true, "host-0": true})

	if len(got.Targets) != 2 || got.Targets[0].Pod != "router1-0" || got.Targets[1].Pod != "host-0" {
		t.Fatalf("targets: %+v", got.Targets)
	}
	r := got.Targets[0].Actions
	if len(r) != 2 || r[0].Type != "set_ip" || r[1].Type != "enable_snat" || r[1].Iface != "eth0" {
		t.Errorf("router actions: %+v", r)
	}
	h := got.Targets[1].Actions
	if len(h) != 2 || h[0].Options != nil || h[1].Executor != "kubectl" || string(h[1].Command) != `"echo hi"` {
		t.Errorf("host actions: %+v", h)
	}

	raw, _ := json.Marshal(got)
	if want := `{"targets":[{"pod":"router1-0","actions":[{"type":"set_ip","iface":"eth1","cidr":"10.0.0.1/24"},{"type":"enable_snat","iface":"eth0"}]},{"pod":"host-0","actions":[{"type":"replace_ip","iface":"eth1","cidr":"10.0.0.2/24"},{"type":"custom","command":"echo hi","executor":"kubectl"}]}]}`; string(raw) != want {
		t.Errorf("json:\n got  %s\n want %s", raw, want)
	}

	empty := buildNetworkConf(nil, map[string]bool{})
	if raw, _ := json.Marshal(empty); string(raw) != `{"targets":[]}` {
		t.Errorf("empty history: %s", raw)
	}
}
