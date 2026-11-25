package helpers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"kubendt/kubeclient"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationclient "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/utils/ptr"
)

// The operation lock of a namespace is a Lease in it: created to acquire,
// renewed while the operation runs, deleted to release, and taken over by
// whoever comes once it expires, so a backend that died mid-operation never
// leaves a namespace wedged and two backends can share a cluster.
const (
	LockLeaseName           = "kubendt-internal-lock"
	lockLeaseSeconds        = int32(60)
	lockRenewEvery          = 20 * time.Second
	lockOperationAnnotation = "kubendt/operation"
	lockStartedAnnotation   = "kubendt/started-at"
)

type NamespaceOperationLock struct {
	Namespace     string `json:"namespace"`
	OperationType string `json:"operationType"`
	StartedAt     string `json:"startedAt"`
}

// leases is the client the lock goes through, a variable so the tests can
// hand it a fake clientset.
var leases = func(namespace string) coordinationclient.LeaseInterface {
	return kubeclient.Clientset.CoordinationV1().Leases(namespace)
}

// lockNow is the clock, a variable so the tests can age a lease.
var lockNow = time.Now

// lockHolder names this backend process on every Lease it holds.
var lockHolder = func() string {
	host, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("kubendt-backend-%s-%s", host, hex.EncodeToString(b))
}()

var (
	renewMu  sync.Mutex
	renewers = map[string]chan struct{}{}
)

func leaseExpired(l *coordinationv1.Lease, now time.Time) bool {
	if l.Spec.RenewTime == nil || l.Spec.LeaseDurationSeconds == nil {
		return true
	}
	return l.Spec.RenewTime.Add(time.Duration(*l.Spec.LeaseDurationSeconds) * time.Second).Before(now)
}

func lockOf(l *coordinationv1.Lease) *NamespaceOperationLock {
	return &NamespaceOperationLock{
		Namespace:     l.Namespace,
		OperationType: l.Annotations[lockOperationAnnotation],
		StartedAt:     l.Annotations[lockStartedAnnotation],
	}
}

// AcquireNamespaceOperationLock takes the namespace's lock for an operation.
// When another operation holds it, the lock comes back with acquired false
// and the holder's operation and start time for the 409.
func AcquireNamespaceOperationLock(namespace, operationType string) (*NamespaceOperationLock, bool, error) {
	if operationType == "" {
		operationType = "unknown"
	}
	ctx := context.TODO()
	now := lockNow().UTC()
	spec := coordinationv1.LeaseSpec{
		HolderIdentity:       ptr.To(lockHolder),
		LeaseDurationSeconds: ptr.To(lockLeaseSeconds),
		AcquireTime:          &metav1.MicroTime{Time: now},
		RenewTime:            &metav1.MicroTime{Time: now},
	}
	annotations := map[string]string{
		lockOperationAnnotation: operationType,
		lockStartedAnnotation:   now.Format(time.RFC3339),
	}
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: LockLeaseName, Namespace: namespace, Labels: internalLabels("lock"), Annotations: annotations},
		Spec:       spec,
	}
	created, err := leases(namespace).Create(ctx, lease, metav1.CreateOptions{})
	if err == nil {
		startRenewer(namespace)
		return lockOf(created), true, nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return nil, false, fmt.Errorf("could not acquire the operation lock of %s: %w", namespace, err)
	}
	current, err := leases(namespace).Get(ctx, LockLeaseName, metav1.GetOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("namespace %s has an operation in progress and its lock could not be read: %w", namespace, err)
	}
	if !leaseExpired(current, now) {
		return lockOf(current), false, nil
	}
	// Whoever held it is gone: the lease is ours to take, unless someone else
	// takes it in the same instant, which the resourceVersion tells.
	previous := ptr.Deref(current.Spec.HolderIdentity, "")
	current.Spec = spec
	current.Labels = lease.Labels
	current.Annotations = annotations
	taken, err := leases(namespace).Update(ctx, current, metav1.UpdateOptions{})
	if err != nil {
		if apierrors.IsConflict(err) {
			if again, getErr := leases(namespace).Get(ctx, LockLeaseName, metav1.GetOptions{}); getErr == nil {
				return lockOf(again), false, nil
			}
		}
		return nil, false, fmt.Errorf("could not take over the expired operation lock of %s: %w", namespace, err)
	}
	log.Printf("ℹ️ Took over the expired operation lock of '%s' (%s held it)", namespace, previous)
	startRenewer(namespace)
	return lockOf(taken), true, nil
}

// ReleaseNamespaceOperationLock drops the lock when this backend holds it. A
// lease another backend took over after ours expired is theirs and stays.
func ReleaseNamespaceOperationLock(namespace string) error {
	stopRenewer(namespace)
	ctx := context.TODO()
	current, err := leases(namespace).Get(ctx, LockLeaseName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not read the operation lock of %s: %w", namespace, err)
	}
	if ptr.Deref(current.Spec.HolderIdentity, "") != lockHolder {
		log.Printf("ℹ️ The operation lock of '%s' is held by %s now, left alone", namespace, ptr.Deref(current.Spec.HolderIdentity, ""))
		return nil
	}
	opts := metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &current.UID}}
	if err := leases(namespace).Delete(ctx, LockLeaseName, opts); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("could not release the operation lock of %s: %w", namespace, err)
	}
	return nil
}

// HeldOperationLock is a lock in force in any namespace of the cluster, nil
// when none is, so a switch to another cluster waits for every operation.
func HeldOperationLock() (*NamespaceOperationLock, error) {
	list, err := leases(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{FieldSelector: "metadata.name=" + LockLeaseName})
	if err != nil {
		return nil, fmt.Errorf("could not read the operation locks: %w", err)
	}
	now := lockNow().UTC()
	for i := range list.Items {
		l := &list.Items[i]
		if l.Name == LockLeaseName && !leaseExpired(l, now) {
			return lockOf(l), nil
		}
	}
	return nil, nil
}

// GetNamespaceOperationLock is the lock in force, nil when there is none or
// the one there expired.
func GetNamespaceOperationLock(namespace string) (*NamespaceOperationLock, error) {
	current, err := leases(namespace).Get(context.TODO(), LockLeaseName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the operation lock of %s: %w", namespace, err)
	}
	if leaseExpired(current, lockNow().UTC()) {
		return nil, nil
	}
	return lockOf(current), nil
}

// startRenewer keeps the lease fresh while the operation runs; a renewal
// that finds another holder stops, the lease is not ours any more.
func startRenewer(namespace string) {
	stop := make(chan struct{})
	renewMu.Lock()
	if prev, ok := renewers[namespace]; ok {
		close(prev)
	}
	renewers[namespace] = stop
	renewMu.Unlock()
	go func() {
		t := time.NewTicker(lockRenewEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if !renewLease(namespace) {
					return
				}
			}
		}
	}()
}

func renewLease(namespace string) bool {
	ctx := context.TODO()
	current, err := leases(namespace).Get(ctx, LockLeaseName, metav1.GetOptions{})
	if err != nil {
		log.Printf("⚠️ Could not renew the operation lock of '%s': %v", namespace, err)
		return !apierrors.IsNotFound(err)
	}
	if ptr.Deref(current.Spec.HolderIdentity, "") != lockHolder {
		return false
	}
	current.Spec.RenewTime = &metav1.MicroTime{Time: lockNow().UTC()}
	if _, err := leases(namespace).Update(ctx, current, metav1.UpdateOptions{}); err != nil {
		log.Printf("⚠️ Could not renew the operation lock of '%s': %v", namespace, err)
	}
	return true
}

func stopRenewer(namespace string) {
	renewMu.Lock()
	defer renewMu.Unlock()
	if stop, ok := renewers[namespace]; ok {
		close(stop)
		delete(renewers, namespace)
	}
}
