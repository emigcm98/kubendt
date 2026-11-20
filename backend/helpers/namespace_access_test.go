package helpers

import (
	"context"
	"testing"

	"kubendt/auth"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

func TestNamespaceAccessLivesOnTheNamespace(t *testing.T) {
	cs := fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "lab", Labels: map[string]string{"kubendt/enabled": "true"},
			Annotations: map[string]string{OwnerAnnotation: "alice", AccessAnnotation: "bob=editor, carol=viewer,dave=king"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "old", Labels: map[string]string{"kubendt/enabled": "true"}}},
	)
	prev := stateNamespaces
	stateNamespaces = func() corev1client.NamespaceInterface { return cs.CoreV1().Namespaces() }
	t.Cleanup(func() { stateNamespaces = prev; forgetAccess("lab"); forgetAccess("old") })
	forgetAccess("lab")
	forgetAccess("old")

	access, err := ReadNamespaceAccess("lab")
	if err != nil || access.Owner != "alice" || access.Guests["bob"] != auth.LevelEditor || access.Guests["carol"] != auth.LevelViewer || access.Guests["dave"] != "" {
		t.Fatalf("access: %+v %v", access, err)
	}
	user := func(name string) *auth.Principal { return &auth.Principal{Identity: name, Role: auth.RoleUser} }
	if LevelOf(access, user("alice")) != auth.LevelOwner || LevelOf(access, user("bob")) != auth.LevelEditor || LevelOf(access, user("eve")) != "" {
		t.Error("the levels of the owner, a guest and a stranger")
	}
	if LevelOf(access, &auth.Principal{Identity: "x", Role: auth.RoleAdmin}) != auth.LevelOwner || LevelOf(access, nil) != auth.LevelOwner {
		t.Error("admins and a disabled auth hold the owner level")
	}
	// A namespace from before the accounts belongs to nobody: admins only.
	if level, err := LevelForNamespace("old", user("alice")); err != nil || level != "" {
		t.Errorf("an unowned namespace: %q %v", level, err)
	}
	if level, _ := LevelForNamespace("gone", user("alice")); level != "" {
		t.Error("a namespace that is not there is no access")
	}

	// The owner and the guests are written as annotations, read back fresh.
	if err := SetNamespaceOwner("old", "frank"); err != nil {
		t.Fatal(err)
	}
	if err := SetNamespaceGuests("old", map[string]string{"gina": auth.LevelOperator}); err != nil {
		t.Fatal(err)
	}
	if err := SetNamespaceGuests("old", map[string]string{"gina": "king"}); err == nil {
		t.Error("a level that is not one")
	}
	ns, _ := cs.CoreV1().Namespaces().Get(context.TODO(), "old", metav1.GetOptions{})
	if ns.Annotations[OwnerAnnotation] != "frank" || ns.Annotations[AccessAnnotation] != "gina=operator" {
		t.Errorf("annotations: %v", ns.Annotations)
	}
	if level, _ := LevelForNamespace("old", user("gina")); level != auth.LevelOperator {
		t.Errorf("the cache forgets a namespace that was written: %q", level)
	}
	if err := SetNamespaceGuests("old", nil); err != nil {
		t.Fatal(err)
	}
	ns, _ = cs.CoreV1().Namespaces().Get(context.TODO(), "old", metav1.GetOptions{})
	if _, has := ns.Annotations[AccessAnnotation]; has {
		t.Error("no guests, no annotation")
	}
}
