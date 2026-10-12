package helpers

import (
	"strings"
	"testing"

	"kubendt/types"
)

func TestMergeTargets(t *testing.T) {
	resolve := func(ref string) string {
		if ref == "router1" {
			return "router1-0"
		}
		return ref
	}
	targets := []types.PodConfig{
		{Pod: "router1", Actions: []types.ActionEntry{{Type: "set_ip"}, {Type: "link_up"}}},
		{Pod: "host-0", Actions: []types.ActionEntry{{Type: "replace_ip"}}},
		{Pod: "router1-0", Actions: []types.ActionEntry{{Type: "add_qdisc"}}},
		{Pod: "nope", Actions: []types.ActionEntry{{Type: "set_ip"}}},
		{Pod: "host-0", Actions: []types.ActionEntry{{Type: "del_qdisc"}}},
	}
	got := mergeTargets(targets, resolve)

	types := func(p types.PodConfig) string {
		out := []string{}
		for _, a := range p.Actions {
			out = append(out, a.Type)
		}
		return p.Pod + ":" + strings.Join(out, ",")
	}
	want := []string{"router1:set_ip,link_up,add_qdisc", "host-0:replace_ip,del_qdisc", "nope:set_ip"}
	if len(got) != len(want) {
		t.Fatalf("got %d targets, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if types(got[i]) != want[i] {
			t.Errorf("target %d: got %q, want %q", i, types(got[i]), want[i])
		}
	}
	// The request is left untouched.
	if len(targets[0].Actions) != 2 {
		t.Errorf("input mutated: %+v", targets[0])
	}
}
