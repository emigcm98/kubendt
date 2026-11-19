package helpers

import (
	"context"
	"testing"

	"kubendt/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// The attic against the fake clientset: the system namespace is created on
// demand and the layout ConfigMaps go through the same client.
func fakeAttic(t *testing.T) {
	t.Helper()
	cs := fakeLayoutStore(t)
	prev := systemNamespaces
	systemNamespaces = func() corev1.NamespaceInterface { return cs.CoreV1().Namespaces() }
	t.Cleanup(func() { systemNamespaces = prev })
}

func TestSystemNamespaceIsCreatedOnce(t *testing.T) {
	fakeAttic(t)
	t.Setenv(SystemNamespaceEnv, "")
	if err := EnsureSystemNamespace(); err != nil {
		t.Fatal(err)
	}
	ns, err := systemNamespaces().Get(context.TODO(), DefaultSystemNamespace, metav1.GetOptions{})
	if err != nil || ns.Labels["kubendt/system"] != "true" || ns.Labels["kubendt/enabled"] != "" {
		t.Fatalf("the system namespace should exist, labelled as such and never as a lab: %v %v", ns, err)
	}
	if err := EnsureSystemNamespace(); err != nil {
		t.Fatalf("ensuring it twice is fine: %v", err)
	}
	t.Setenv(SystemNamespaceEnv, " platform ")
	if SystemNamespace() != "platform" {
		t.Errorf("the env names it, got %q", SystemNamespace())
	}
}

func TestLayoutGoesToTheAtticAndBack(t *testing.T) {
	fakeAttic(t)
	t.Setenv(SystemNamespaceEnv, "")
	layout := types.Layout{
		Positions:   map[string]types.NodePosition{"r1-0": {X: 10, Y: 20}},
		Annotations: []types.Annotation{{ID: "a1", Type: "rect", X: 1, Y: 2, Width: 30, Height: 40, Text: "LAN"}},
	}
	if err := PutLayout("lab", layout); err != nil {
		t.Fatal(err)
	}
	if err := StoreLayoutInAttic("lab"); err != nil {
		t.Fatal(err)
	}
	cm, err := layoutConfigMaps(DefaultSystemNamespace).Get(context.TODO(), "kubendt-internal-layout-lab", metav1.GetOptions{})
	if err != nil || cm.Labels["kubendt/kind"] != "layout-attic" || cm.Labels["kubendt/namespace"] != "lab" {
		t.Fatalf("the attic copy should be in the system namespace with its labels: %v %v", cm, err)
	}
	// The namespace goes, and comes back with the name: the layout returns.
	if err := DeleteLayout("lab"); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreLayoutFromAttic("lab")
	if err != nil || !restored {
		t.Fatalf("restore: %v %v", restored, err)
	}
	back, err := GetLayout("lab")
	if err != nil || back.Positions["r1-0"].X != 10 || len(back.Annotations) != 1 || back.Annotations[0].Text != "LAN" {
		t.Fatalf("the layout should be back as it was: %+v %v", back, err)
	}
	if _, err := layoutConfigMaps(DefaultSystemNamespace).Get(context.TODO(), "kubendt-internal-layout-lab", metav1.GetOptions{}); err == nil {
		t.Error("the attic copy should go once restored")
	}
	if restored, err := RestoreLayoutFromAttic("lab"); err != nil || restored {
		t.Errorf("nothing to restore the second time: %v %v", restored, err)
	}
}

func TestEmptyLayoutLeavesNothingInTheAtticAndDropIsQuiet(t *testing.T) {
	fakeAttic(t)
	t.Setenv(SystemNamespaceEnv, "")
	if err := StoreLayoutInAttic("bare"); err != nil {
		t.Fatal(err)
	}
	if _, err := layoutConfigMaps(DefaultSystemNamespace).Get(context.TODO(), "kubendt-internal-layout-bare", metav1.GetOptions{}); err == nil {
		t.Error("a namespace with no layout leaves nothing in the attic")
	}
	if err := DropLayoutAttic("bare"); err != nil {
		t.Errorf("dropping what is not there is fine, got %v", err)
	}
	if err := PutLayout("kept", types.Layout{Positions: map[string]types.NodePosition{"x": {X: 1, Y: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := StoreLayoutInAttic("kept"); err != nil {
		t.Fatal(err)
	}
	if err := DropLayoutAttic("kept"); err != nil {
		t.Fatal(err)
	}
	if _, err := layoutConfigMaps(DefaultSystemNamespace).Get(context.TODO(), "kubendt-internal-layout-kept", metav1.GetOptions{}); err == nil {
		t.Error("dropped, the attic copy should be gone")
	}
}
