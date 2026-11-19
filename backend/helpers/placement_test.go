package helpers

import (
	"context"
	"strings"
	"testing"

	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

func clusterNode(name string, ready bool, lbls map[string]string) v1.Node {
	status := v1.ConditionFalse
	if ready {
		status = v1.ConditionTrue
	}
	return v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: lbls},
		Status:     v1.NodeStatus{Conditions: []v1.NodeCondition{{Type: v1.NodeReady, Status: status}}},
	}
}

func TestCheckPlacement(t *testing.T) {
	cluster := []v1.Node{
		clusterNode("w1", true, map[string]string{"kubendt/kvm": "true"}),
		clusterNode("w2", true, nil),
		clusterNode("w3", false, map[string]string{"zone": "b"}),
	}
	cases := []struct {
		name    string
		spec    types.NodeSpec
		wantErr string
	}{
		{"no constraint", types.NodeSpec{Name: "r1"}, ""},
		{"nodeName exists", types.NodeSpec{Name: "r1", NodeName: "w2"}, ""},
		{"nodeName missing", types.NodeSpec{Name: "r1", NodeName: "w9"}, "does not exist"},
		{"selector matches ready node", types.NodeSpec{Name: "r1", NodeSelector: map[string]string{"kubendt/kvm": "true"}}, ""},
		{"selector only matches not-ready node", types.NodeSpec{Name: "r1", NodeSelector: map[string]string{"zone": "b"}}, "no Ready cluster node"},
		{"selector matches nothing", types.NodeSpec{Name: "r1", NodeSelector: map[string]string{"gpu": "yes"}}, "no Ready cluster node"},
		{"nodeName and selector agree", types.NodeSpec{Name: "r1", NodeName: "w1", NodeSelector: map[string]string{"kubendt/kvm": "true"}}, ""},
		{"nodeName lacks selector labels", types.NodeSpec{Name: "r1", NodeName: "w2", NodeSelector: map[string]string{"kubendt/kvm": "true"}}, "does not carry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPlacement([]types.NodeSpec{tc.spec}, cluster)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestResolvePlacement(t *testing.T) {
	cases := []struct {
		request, env, want string
		bad                bool
	}{
		{"", "", "", false},
		{"pack", "", "pack", false},
		{" Spread ", "pack", "spread", false},
		{"", "pack", "pack", false},
		{"", "SPREAD", "spread", false},
		{"packed", "", "", true},
		{"", "nope", "", true},
	}
	for _, c := range cases {
		t.Setenv(PlacementEnv, c.env)
		got, err := ResolvePlacement(c.request)
		if c.bad {
			if err == nil {
				t.Errorf("request %q env %q: want an error, got %q", c.request, c.env, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("request %q env %q: got %q, %v, want %q", c.request, c.env, got, err, c.want)
		}
	}
}

func TestPlacementAffinity(t *testing.T) {
	if placementAffinity("") != nil {
		t.Fatal("no placement should mean no affinity")
	}
	pack := placementAffinity(PlacementPack)
	if pack.PodAffinity == nil || pack.PodAntiAffinity != nil {
		t.Fatalf("pack: want a pod affinity only, got %+v", pack)
	}
	term := pack.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0]
	if term.Weight != 100 || term.PodAffinityTerm.TopologyKey != "kubernetes.io/hostname" {
		t.Errorf("pack: unexpected term %+v", term)
	}
	if req := term.PodAffinityTerm.LabelSelector.MatchExpressions[0]; req.Key != "kubendt/type" || req.Operator != metav1.LabelSelectorOpExists {
		t.Errorf("pack: the term should match every KubeNDT pod, got %+v", req)
	}
	spread := placementAffinity(PlacementSpread)
	if spread.PodAntiAffinity == nil || spread.PodAffinity != nil {
		t.Fatalf("spread: want an anti-affinity only, got %+v", spread)
	}
}

// The placement lives on the namespace: set, read back, removed.
func TestNamespacePlacementRoundTrip(t *testing.T) {
	prev := placementNamespaces
	cs := fake.NewClientset(&v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "lab"}})
	placementNamespaces = func() corev1.NamespaceInterface { return cs.CoreV1().Namespaces() }
	t.Cleanup(func() { placementNamespaces = prev })

	if err := SetNamespacePlacement("lab", PlacementPack); err != nil {
		t.Fatal(err)
	}
	if got := NamespacePlacement("lab"); got != PlacementPack {
		t.Fatalf("after set: got %q", got)
	}
	if err := SetNamespacePlacement("lab", ""); err != nil {
		t.Fatal(err)
	}
	ns, _ := cs.CoreV1().Namespaces().Get(context.TODO(), "lab", metav1.GetOptions{})
	if _, still := ns.Annotations[PlacementAnnotation]; still {
		t.Errorf("an empty placement should remove the annotation, got %v", ns.Annotations)
	}
	if got := NamespacePlacement("missing"); got != "" {
		t.Errorf("a namespace that cannot be read has no placement, got %q", got)
	}
	if err := SetNamespacePlacement("missing", ""); err != nil {
		t.Errorf("dropping the placement of a namespace that is gone is fine, got %v", err)
	}
}
