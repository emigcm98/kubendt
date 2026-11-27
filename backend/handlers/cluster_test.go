package handlers

import (
	"strings"
	"testing"
	"time"

	"kubendt/helpers"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNodeSchedulable(t *testing.T) {
	taint := func(key string, effect v1.TaintEffect) v1.Taint { return v1.Taint{Key: key, Effect: effect} }
	for name, tc := range map[string]struct {
		node v1.Node
		want bool
	}{
		"a worker": {v1.Node{}, true},
		// kubeadm's control plane, as dev's.
		"a tainted control plane": {v1.Node{Spec: v1.NodeSpec{Taints: []v1.Taint{taint("node-role.kubernetes.io/control-plane", v1.TaintEffectNoSchedule)}}}, false},
		// A single-node kind or k3s: a control plane that runs the labs.
		"an untainted control plane": {v1.Node{}, true},
		"a cordoned worker":          {v1.Node{Spec: v1.NodeSpec{Unschedulable: true}}, false},
		"a soft taint":               {v1.Node{Spec: v1.NodeSpec{Taints: []v1.Taint{taint("spot", v1.TaintEffectPreferNoSchedule)}}}, true},
		"an evicting taint":          {v1.Node{Spec: v1.NodeSpec{Taints: []v1.Taint{taint("node.kubernetes.io/unreachable", v1.TaintEffectNoExecute)}}}, false},
	} {
		if got := nodeSchedulable(&tc.node); got != tc.want {
			t.Errorf("%s: schedulable %v, want %v", name, got, tc.want)
		}
	}
}

func TestMeshnetImage(t *testing.T) {
	sidecar := []v1.Container{{Name: "proxy", Image: "proxy:1"}, {Name: "meshnet", Image: "ghcr.io/emigcm98/meshnet:v1.0.0"}}
	if got := meshnetImage(sidecar); got != "ghcr.io/emigcm98/meshnet:v1.0.0" {
		t.Errorf("got %q, want the meshnet container's image", got)
	}
	if got := meshnetImage([]v1.Container{{Name: "cni", Image: "networkop/meshnet:latest"}}); got != "networkop/meshnet:latest" {
		t.Errorf("got %q, want the only container's image", got)
	}
	if got := meshnetImage(nil); got != "" {
		t.Errorf("got %q for no container", got)
	}
}

func TestMeshnetWarnings(t *testing.T) {
	one := []MeshnetImage{{Image: "a", Nodes: 3}}
	for name, tc := range map[string]struct {
		m    MeshnetStatus
		want []string // a word of each warning expected, in order
	}{
		"the fork with its CRD":      {MeshnetStatus{Flavour: helpers.MeshnetFork, CRD: helpers.MeshnetCRDFork, Images: one}, nil},
		"stock with its CRD":         {MeshnetStatus{Flavour: helpers.MeshnetStock, CRD: helpers.MeshnetCRDStock, Images: one}, nil},
		"the fork on the stock CRD":  {MeshnetStatus{Flavour: helpers.MeshnetFork, CRD: helpers.MeshnetCRDStock, Images: one}, []string{"drops its extra fields"}},
		"stock on the fork's CRD":    {MeshnetStatus{Flavour: helpers.MeshnetStock, CRD: helpers.MeshnetCRDFork, Images: one}, []string{"ignores its extra fields"}},
		"no CRD":                     {MeshnetStatus{Flavour: helpers.MeshnetFork, CRD: helpers.MeshnetCRDMissing, Images: one}, []string{"not installed"}},
		"a CRD that cannot be read":  {MeshnetStatus{Flavour: helpers.MeshnetFork, CRD: helpers.MeshnetCRDUnreadable, Images: one}, nil},
		"an upgrade halfway":         {MeshnetStatus{Flavour: helpers.MeshnetFork, CRD: helpers.MeshnetCRDFork, Images: []MeshnetImage{{Image: "a", Nodes: 2}, {Image: "b", Nodes: 1}}}, []string{"different Meshnet images"}},
		"an unknown daemon, any CRD": {MeshnetStatus{Flavour: helpers.MeshnetNotRecognised, CRD: helpers.MeshnetCRDStock, Images: one}, nil},
	} {
		got := meshnetWarnings(tc.m)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %d warning(s)", name, got, len(tc.want))
			continue
		}
		for i, word := range tc.want {
			if !strings.Contains(got[i], word) {
				t.Errorf("%s: warning %q lacks %q", name, got[i], word)
			}
		}
	}
}

func TestMeshnetSupportWarning(t *testing.T) {
	status := func(image string) MeshnetStatus {
		id := helpers.IdentifyMeshnet(image)
		return MeshnetStatus{Flavour: id.Flavour, Image: id.Image, Version: id.Version, Support: id.Support}
	}
	for _, quiet := range []string{"networkop/meshnet:latest", "ghcr.io/emigcm98/meshnet:v1.0.0", "ghcr.io/emigcm98/meshnet:sha-30ab7f0"} {
		if w := meshnetSupportWarning(status(quiet)); w != nil {
			t.Errorf("%s: unexpected warning %+v", quiet, w)
		}
	}
	// A deploy with no Meshnet identified at all (the DaemonSet not readable) says nothing either.
	if w := meshnetSupportWarning(MeshnetStatus{State: "unknown"}); w != nil {
		t.Errorf("unknown state: unexpected warning %+v", w)
	}
	for image, word := range map[string]string{
		"ghcr.io/emigcm98/meshnet:v0.9.0": "older than",
		"ghcr.io/emigcm98/meshnet:v2.0.0": "newer than",
		"example.org/kne/meshnet:ga":      "does not recognise",
	} {
		w := meshnetSupportWarning(status(image))
		if w == nil || w.Kind != "meshnet_unsupported" || !strings.Contains(w.Detail, word) || !strings.Contains(w.Detail, "from 1.0.0 and below 2.0.0") {
			t.Errorf("%s: got %+v, want a meshnet_unsupported warning with %q and the range", image, w, word)
		}
	}
}

func meshnetPod(name, namespace, node, image string, ready bool) v1.Pod {
	status := v1.ConditionFalse
	if ready {
		status = v1.ConditionTrue
	}
	return v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       v1.PodSpec{NodeName: node, Containers: []v1.Container{{Name: "meshnet", Image: image}}},
		Status:     v1.PodStatus{Conditions: []v1.PodCondition{{Type: v1.PodReady, Status: status}}},
	}
}

func TestPickMeshnetPod(t *testing.T) {
	pods := []v1.Pod{
		meshnetPod("meshnet-exporter", "monitoring", "w1", "exporter:1", true),
		meshnetPod("meshnet-aaaaa", "meshnet", "w2", "a", true),
		meshnetPod("meshnet-bbbbb", "meshnet", "w1", "a", true),
		{ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system"}, Spec: v1.PodSpec{NodeName: "w1"}},
	}
	if p := pickMeshnetPod(pods, "w1", "meshnet"); p == nil || p.Name != "meshnet-bbbbb" {
		t.Errorf("got %v, want the pod of the DaemonSet's namespace", p)
	}
	// No namespace known, or none holds one: the first that looks like it.
	if p := pickMeshnetPod(pods, "w1", ""); p == nil || p.Name != "meshnet-exporter" {
		t.Errorf("got %v, want the first match", p)
	}
	if p := pickMeshnetPod(pods, "w1", "elsewhere"); p == nil || p.Name != "meshnet-exporter" {
		t.Errorf("got %v, want the first match", p)
	}
	// Only this node's pods, whatever the server filtered.
	if p := pickMeshnetPod(pods, "w3", "meshnet"); p != nil {
		t.Errorf("got %v for a node without one", p)
	}
}

func TestMeshnetPodInfo(t *testing.T) {
	started := metav1.NewTime(time.Date(2026, 11, 27, 13, 18, 0, 0, time.UTC))
	p := meshnetPod("meshnet-bbbbb", "meshnet", "w1", "ghcr.io/emigcm98/meshnet:sha-30ab7f0", true)
	p.Status.StartTime = &started
	p.Status.ContainerStatuses = []v1.ContainerStatus{{RestartCount: 2}, {RestartCount: 1}}

	info := meshnetPodInfo(&p, "ghcr.io/emigcm98/meshnet:sha-30ab7f0")
	if !info.Ready || info.Restarts != 3 || info.Started != "2026-11-27T13:18:00Z" || info.Outdated {
		t.Errorf("got %+v", info)
	}
	if info.Flavour != helpers.MeshnetFork || info.Commit != "30ab7f0" || !strings.HasSuffix(info.URL, "/commit/30ab7f0") {
		t.Errorf("identity %+v", info)
	}
	// A pod that still runs the old image once the DaemonSet asks for another.
	if !meshnetPodInfo(&p, "ghcr.io/emigcm98/meshnet:v1.0.0").Outdated {
		t.Error("a pod on another image than its DaemonSet's is not flagged")
	}
	// A DaemonSet that could not be read says nothing of the pod.
	if meshnetPodInfo(&p, "").Outdated {
		t.Error("flagged with no DaemonSet image to compare to")
	}
}
