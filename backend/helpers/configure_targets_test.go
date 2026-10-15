package helpers

import (
	"strings"
	"testing"

	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestPodResolver(t *testing.T) {
	pod := func(name, node string) v1.Pod {
		return v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"app": node}}}
	}
	resolve := podResolver([]v1.Pod{
		pod("router1-0", "router1"),
		pod("host-0", "host"),
		pod("host-1", "host"),
	})
	cases := map[string]string{
		"router1-0": "router1-0", // exact name
		"router1":   "router1-0", // base name of a node with one pod
		"host":      "host",      // two replicas, ambiguous, left as written
		"host-1":    "host-1",
		"host-7":    "host-7", // indexed name that does not exist
		"nope":      "nope",
	}
	for ref, want := range cases {
		if got := resolve(ref); got != want {
			t.Errorf("resolve(%q) = %q, want %q", ref, got, want)
		}
	}
}
