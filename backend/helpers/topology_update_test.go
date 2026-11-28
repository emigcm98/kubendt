package helpers

import (
	"errors"
	"testing"
	"time"

	"kubendt/kubeclient"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestUpdateTopologyRetriesOnConflict(t *testing.T) {
	oldClient, oldBackoff := kubeclient.DynamicClient, topologyBackoff
	t.Cleanup(func() { kubeclient.DynamicClient, topologyBackoff = oldClient, oldBackoff })
	topologyBackoff.Duration = time.Millisecond
	kinds := map[schema.GroupVersionResource]string{TopologyGVR: "TopologyList"}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, topologyWith("a-0", declares("a-0", "eth1", "b-0", "eth2")))
	kubeclient.DynamicClient = client

	// The daemon wrote the status after the first read: that write is refused
	// and the next attempt starts from a new read.
	conflicts := 2
	client.PrependReactor("update", "topologies", func(k8stesting.Action) (bool, runtime.Object, error) {
		if conflicts > 0 {
			conflicts--
			return true, nil, apierrors.NewConflict(TopologyGVR.GroupResource(), "a-0", errors.New("the object has been modified"))
		}
		return false, nil, nil
	})
	reads := 0
	changed, err := updateTopology("lab", "a-0", func(obj *unstructured.Unstructured) (bool, error) {
		reads++
		return true, unstructured.SetNestedField(obj.Object, "touched", "metadata", "annotations", "x")
	})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v, want the write to land after the conflicts", changed, err)
	}
	if reads != 3 {
		t.Errorf("mutate ran %d times, want once per attempt, 3", reads)
	}
	got, _ := client.Resource(TopologyGVR).Namespace("lab").Get(t.Context(), "a-0", metav1.GetOptions{})
	if v, _, _ := unstructured.NestedString(got.Object, "metadata", "annotations", "x"); v != "touched" {
		t.Errorf("the change is not in the object: %v", got.Object["metadata"])
	}
}

func TestUpdateTopologyGivesUpAndLeavesOtherErrors(t *testing.T) {
	oldClient, oldBackoff := kubeclient.DynamicClient, topologyBackoff
	t.Cleanup(func() { kubeclient.DynamicClient, topologyBackoff = oldClient, oldBackoff })
	topologyBackoff.Duration = time.Millisecond
	kinds := map[schema.GroupVersionResource]string{TopologyGVR: "TopologyList"}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, topologyWith("a-0"))
	kubeclient.DynamicClient = client

	// A Topology that keeps changing is an error after the attempts, not a loop.
	client.PrependReactor("update", "topologies", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(TopologyGVR.GroupResource(), "a-0", errors.New("busy"))
	})
	if changed, err := updateTopology("lab", "a-0", func(*unstructured.Unstructured) (bool, error) { return true, nil }); err == nil || changed || !apierrors.IsConflict(err) {
		t.Errorf("changed=%v err=%v, want a conflict after the attempts", changed, err)
	}

	// Nothing to change is no write, and a missing Topology comes back as not found.
	writes := 0
	client.PrependReactor("update", "topologies", func(k8stesting.Action) (bool, runtime.Object, error) { writes++; return false, nil, nil })
	if changed, err := updateTopology("lab", "a-0", func(*unstructured.Unstructured) (bool, error) { return false, nil }); err != nil || changed || writes != 0 {
		t.Errorf("changed=%v err=%v writes=%d, want no write", changed, err, writes)
	}
	if _, err := updateTopology("lab", "gone-0", func(*unstructured.Unstructured) (bool, error) { return true, nil }); !apierrors.IsNotFound(err) {
		t.Errorf("err=%v, want not found", err)
	}
}
