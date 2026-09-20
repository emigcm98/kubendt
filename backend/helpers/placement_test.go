package helpers

import (
	"strings"
	"testing"

	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
