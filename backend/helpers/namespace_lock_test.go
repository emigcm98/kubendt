package helpers

import (
	"context"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	coordinationclient "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/utils/ptr"
)

// The lock against client-go's fake clientset, with the clock in hand.
func fakeLockStore(t *testing.T) *fake.Clientset {
	t.Helper()
	cs := fake.NewClientset()
	prevLeases, prevNow, prevHolder := leases, lockNow, lockHolder
	leases = func(ns string) coordinationclient.LeaseInterface { return cs.CoordinationV1().Leases(ns) }
	lockHolder = "backend-a"
	t.Cleanup(func() {
		leases, lockNow, lockHolder = prevLeases, prevNow, prevHolder
		stopRenewer("lab")
	})
	return cs
}

func TestOperationLockAcquireHoldRelease(t *testing.T) {
	cs := fakeLockStore(t)
	lock, acquired, err := AcquireNamespaceOperationLock("lab", "deploy-network")
	if err != nil || !acquired || lock.OperationType != "deploy-network" || lock.StartedAt == "" {
		t.Fatalf("first acquire: %+v %v %v", lock, acquired, err)
	}
	held, acquired, err := AcquireNamespaceOperationLock("lab", "modify-network")
	if err != nil || acquired || held.OperationType != "deploy-network" {
		t.Fatalf("a held lock must come back with the holder's operation: %+v %v %v", held, acquired, err)
	}
	if got, _ := GetNamespaceOperationLock("lab"); got == nil || got.OperationType != "deploy-network" {
		t.Fatalf("the lock in force should read back, got %+v", got)
	}
	l, _ := cs.CoordinationV1().Leases("lab").Get(context.TODO(), LockLeaseName, metav1.GetOptions{})
	if l.Labels["kubendt/kind"] != "lock" || ptr.Deref(l.Spec.HolderIdentity, "") != "backend-a" {
		t.Errorf("the lease should carry the kind and the holder, got %v %v", l.Labels, l.Spec.HolderIdentity)
	}
	if err := ReleaseNamespaceOperationLock("lab"); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetNamespaceOperationLock("lab"); got != nil {
		t.Fatalf("released, there should be no lock, got %+v", got)
	}
	if _, acquired, err := AcquireNamespaceOperationLock("lab", "clear-topology"); err != nil || !acquired {
		t.Fatalf("after a release the lock is free again: %v %v", acquired, err)
	}
	if err := ReleaseNamespaceOperationLock("other"); err != nil {
		t.Errorf("releasing a lock that is not there is fine, got %v", err)
	}
}

func TestOperationLockExpiresAndIsTakenOver(t *testing.T) {
	cs := fakeLockStore(t)
	old := time.Now().Add(-5 * time.Minute)
	stale := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: LockLeaseName, Namespace: "lab", Annotations: map[string]string{lockOperationAnnotation: "deploy-network", lockStartedAnnotation: old.Format(time.RFC3339)}},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       ptr.To("backend-b"),
			LeaseDurationSeconds: ptr.To(lockLeaseSeconds),
			AcquireTime:          &metav1.MicroTime{Time: old},
			RenewTime:            &metav1.MicroTime{Time: old},
		},
	}
	if _, err := cs.CoordinationV1().Leases("lab").Create(context.TODO(), stale, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetNamespaceOperationLock("lab"); got != nil {
		t.Fatalf("an expired lease is no lock, got %+v", got)
	}
	lock, acquired, err := AcquireNamespaceOperationLock("lab", "modify-network")
	if err != nil || !acquired || lock.OperationType != "modify-network" {
		t.Fatalf("the expired lease should be taken over: %+v %v %v", lock, acquired, err)
	}
	l, _ := cs.CoordinationV1().Leases("lab").Get(context.TODO(), LockLeaseName, metav1.GetOptions{})
	if ptr.Deref(l.Spec.HolderIdentity, "") != "backend-a" {
		t.Errorf("the taker should hold it now, got %v", l.Spec.HolderIdentity)
	}
}

func TestOperationLockLeavesAnotherHolderAlone(t *testing.T) {
	cs := fakeLockStore(t)
	now := time.Now()
	theirs := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: LockLeaseName, Namespace: "lab", Annotations: map[string]string{lockOperationAnnotation: "deploy-network"}},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       ptr.To("backend-b"),
			LeaseDurationSeconds: ptr.To(lockLeaseSeconds),
			RenewTime:            &metav1.MicroTime{Time: now},
		},
	}
	if _, err := cs.CoordinationV1().Leases("lab").Create(context.TODO(), theirs, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseNamespaceOperationLock("lab"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoordinationV1().Leases("lab").Get(context.TODO(), LockLeaseName, metav1.GetOptions{}); err != nil {
		t.Fatalf("another backend's lease must survive our release: %v", err)
	}
	if !renewLease("lab") {
		t.Log("a renewal finding another holder stops, as it should")
	} else {
		t.Error("renewing a lease another backend holds should stop the renewer")
	}
}
