package helpers

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"kubendt/kubeclient"
	"kubendt/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func topology(name string, spec, status []map[string]interface{}, plumbing string) *unstructured.Unstructured {
	toSlice := func(in []map[string]interface{}) []interface{} {
		out := make([]interface{}, 0, len(in))
		for _, m := range in {
			out = append(out, m)
		}
		return out
	}
	st := map[string]interface{}{"links": toSlice(status)}
	if plumbing != "" {
		st["plumbing_error"] = plumbing
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "networkop.co.uk/v1beta1",
		"kind":       "Topology",
		"metadata":   map[string]interface{}{"name": name, "namespace": "lab"},
		"spec":       map[string]interface{}{"links": toSlice(spec)},
		"status":     st,
	}}
}

func specLink(uid int64, intf string) map[string]interface{} {
	return map[string]interface{}{"uid": uid, "local_intf": intf}
}

func shownLink(uid int64, intf, state, errMsg string) map[string]interface{} {
	return map[string]interface{}{"uid": uid, "local_intf": intf, "state": state, "error": errMsg}
}

func TestTopologyLinksUnsettled(t *testing.T) {
	for name, tc := range map[string]struct {
		obj  *unstructured.Unstructured
		want string // a word of the reason, "" for settled
	}{
		"settled":                   {topology("a", []map[string]interface{}{specLink(1, "eth1")}, []map[string]interface{}{shownLink(1, "eth1", "up", "")}, ""), ""},
		"a link off with no peer":   {topology("a", []map[string]interface{}{specLink(1, "eth1")}, []map[string]interface{}{shownLink(1, "eth1", "off", "")}, ""), ""},
		"no links at all":           {topology("a", nil, nil, ""), ""},
		"declared, not shown yet":   {topology("a", []map[string]interface{}{specLink(1, "eth1"), specLink(2, "eth2")}, []map[string]interface{}{shownLink(1, "eth1", "up", "")}, ""), "eth2 is not wired"},
		"removed, still shown":      {topology("a", nil, []map[string]interface{}{shownLink(1, "eth1", "up", "")}, ""), "eth1 is still wired"},
		"failed":                    {topology("a", []map[string]interface{}{specLink(1, "eth1")}, []map[string]interface{}{shownLink(1, "eth1", "failed", "no such netns")}, ""), "no such netns"},
		"a plumbing error":          {topology("a", []map[string]interface{}{specLink(1, "eth1")}, []map[string]interface{}{shownLink(1, "eth1", "up", "")}, "boom"), "boom"},
		"a daemon that writes none": {&unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{"links": []interface{}{specLink(1, "eth1")}}}}, "eth1 is not wired"},
	} {
		got := TopologyLinksUnsettled(tc.obj.Object)
		if (tc.want == "") != (got == "") || (tc.want != "" && !strings.Contains(got, tc.want)) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestAwaitForkLinks(t *testing.T) {
	oldClient, oldTimeout, oldPoll := kubeclient.DynamicClient, forkSettleTimeout, forkSettlePoll
	t.Cleanup(func() { kubeclient.DynamicClient, forkSettleTimeout, forkSettlePoll = oldClient, oldTimeout, oldPoll })
	forkSettleTimeout, forkSettlePoll = 300*time.Millisecond, 10*time.Millisecond
	kinds := map[schema.GroupVersionResource]string{TopologyGVR: "TopologyList"}

	settled := topology("a-0", []map[string]interface{}{specLink(1, "eth1")}, []map[string]interface{}{shownLink(1, "eth1", "up", "")}, "")
	pending := topology("b-0", []map[string]interface{}{specLink(1, "eth1"), specLink(2, "eth2")}, []map[string]interface{}{shownLink(1, "eth1", "up", "")}, "")
	other := topology("c-0", []map[string]interface{}{specLink(9, "eth1")}, nil, "")
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, settled, pending, other)
	kubeclient.DynamicClient = client

	// Only the pods asked for count, and a pod with no Topology is left out.
	got := AwaitForkLinks("lab", []string{"a-0", "gone-0"})
	if len(got) != 0 {
		t.Errorf("settled pod reported %v", got)
	}
	got = AwaitForkLinks("lab", []string{"a-0", "b-0"})
	if len(got) != 1 || got["b-0"] == "" {
		t.Errorf("got %v, want only b-0 unsettled", got)
	}

	// The daemon shows the missing link while the wait is on.
	go func() {
		time.Sleep(60 * time.Millisecond)
		fixed := topology("b-0", []map[string]interface{}{specLink(1, "eth1"), specLink(2, "eth2")}, []map[string]interface{}{shownLink(1, "eth1", "up", ""), shownLink(2, "eth2", "up", "")}, "")
		fixed.SetResourceVersion("")
		_, _ = client.Resource(TopologyGVR).Namespace("lab").Update(t.Context(), fixed, metav1.UpdateOptions{})
	}()
	forkSettleTimeout = 2 * time.Second
	start := time.Now()
	if got := AwaitForkLinks("lab", []string{"b-0"}); len(got) != 0 {
		t.Errorf("still unsettled after the daemon caught up: %v", got)
	}
	if time.Since(start) > time.Second {
		t.Errorf("waited %v, the daemon had caught up", time.Since(start))
	}
}

func TestForkSettleSet(t *testing.T) {
	nodes := []types.NodeSpec{{Name: "h", Replicas: 3}, {Name: "r", Replicas: 1}}
	set := func(pods ...string) map[string]struct{} {
		m := map[string]struct{}{}
		for _, p := range pods {
			m[p] = struct{}{}
		}
		return m
	}
	add := []types.LinkSpec{
		{Node: "h-0", LocalIntf: "eth1", PeerNode: "r-0", PeerIntf: "eth1"},
		{Node: "r-0", LocalIntf: "eth2", PeerNode: "external", PeerIntf: "ens18"},
	}
	got := ForkSettleSet(add, nodes,
		[]map[string]struct{}{set("h-2"), set("h-1"), set("h-1", "gone-0")},
		[]map[string]struct{}{set("r-0"), set("gone-0")})
	want := []string{"h-0", "h-1", "h-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v: new and touched pods and link ends, minus the recreated and the removed, never the uplink", got, want)
	}
	if got := ForkSettleSet(nil, nodes, nil, nil); len(got) != 0 {
		t.Errorf("nothing touched gave %v", got)
	}
}

func TestUnsettledDetail(t *testing.T) {
	unsettled := map[string]string{"b-0": "eth2 is not wired yet", "a-0": "eth1 failed: boom"}
	got := UnsettledDetail(unsettled, true)
	if !strings.Contains(got, "a-0 (eth1 failed: boom), b-0 (eth2 is not wired yet)") {
		t.Errorf("pods out of order or reasons missing: %q", got)
	}
	if !strings.Contains(got, "heal pass ran") || strings.Contains(UnsettledDetail(unsettled, false), "heal pass") {
		t.Error("the heal pass is named only when it ran")
	}
}
