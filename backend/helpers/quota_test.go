package helpers

import (
	"context"
	"errors"
	"testing"

	"kubendt/types"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/utils/ptr"
)

func fakeQuotaCluster(t *testing.T, objs ...runtime.Object) *fake.Clientset {
	t.Helper()
	cs := fake.NewClientset(objs...)
	prevNS, prevSTS, prevLR := stateNamespaces, nodeStatefulSets, stateLimitRanges
	stateNamespaces = func() corev1client.NamespaceInterface { return cs.CoreV1().Namespaces() }
	nodeStatefulSets = func(ns string) appsv1client.StatefulSetInterface { return cs.AppsV1().StatefulSets(ns) }
	stateLimitRanges = func(ns string) corev1client.LimitRangeInterface { return cs.CoreV1().LimitRanges(ns) }
	t.Cleanup(func() {
		stateNamespaces, nodeStatefulSets, stateLimitRanges = prevNS, prevSTS, prevLR
		forgetAccess("lab-a")
		forgetAccess("lab-b")
	})
	forgetAccess("lab-a")
	forgetAccess("lab-b")
	return cs
}

func sts(ns, name string, replicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: appsv1.StatefulSetSpec{Replicas: ptr.To(replicas),
		Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"kubendt/type": "host", "kubendt/driver": "HostDriver"}}}}}
}

func TestPodsAfterModifyCountsWhatWouldBeThere(t *testing.T) {
	existing := []types.NodeSpec{{Name: "r1", Replicas: 1}, {Name: "pc", Replicas: 3}, {Name: "gone", Replicas: 2}}
	plans := []ScalePlan{{Node: types.NodeSpec{Name: "pc"}, CurrentReplicas: 3, NewReplicas: 5}}
	added := []types.NodeSpec{{Name: "sw"}, {Name: "web", Replicas: 2}}
	if n := PodsAfterModify(existing, map[string]struct{}{"gone": {}}, plans, added); n != 9 {
		t.Errorf("1 + 5 + 1 + 2 = 9, got %d", n)
	}
	if n := PodsAfterModify(nil, nil, nil, added); n != 3 {
		t.Errorf("a deploy counts its nodes, a replicas of zero is one: got %d", n)
	}
}

func TestEnvironmentDefaultsAndLimitRange(t *testing.T) {
	t.Setenv(EnvPodCPURequest, "")
	t.Setenv(EnvPodMemoryRequest, "")
	t.Setenv(EnvPodCPULimit, "")
	t.Setenv(EnvPodMemoryLimit, "")
	if lr, err := LimitRangeFromEnv(); err != nil || lr != nil {
		t.Errorf("nothing named, no LimitRange: %v %v", lr, err)
	}
	t.Setenv(EnvPodCPURequest, "100m")
	t.Setenv(EnvPodMemoryLimit, "512Mi")
	lr, err := LimitRangeFromEnv()
	if err != nil || lr == nil || lr.Spec.Limits[0].DefaultRequest.Cpu().String() != "100m" || lr.Spec.Limits[0].Default.Memory().String() != "512Mi" {
		t.Fatalf("LimitRange: %+v %v", lr, err)
	}
	t.Setenv(EnvPodMemoryLimit, "lots")
	if _, err := LimitRangeFromEnv(); err == nil {
		t.Error("a quantity that is not one")
	}
	t.Setenv(EnvPodMemoryLimit, "512Mi")

	cs := fakeQuotaCluster(t)
	if err := EnsureLimitRange("lab-a"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLimitRange("lab-a"); err != nil {
		t.Fatal("twice is fine:", err)
	}
	got, err := cs.CoreV1().LimitRanges("lab-a").Get(context.TODO(), LimitRangeName, metav1.GetOptions{})
	if err != nil || got.Labels["kubendt/kind"] != "limits" {
		t.Errorf("the LimitRange in the namespace: %v %v", got, err)
	}
	if d := limitRangeDetail(got); d != "every container cpu 100m, memory up to 512Mi" {
		t.Errorf("detail: %q", d)
	}
}

func TestPodQuotaCountsTheOwnersNamespaces(t *testing.T) {
	fakeQuotaCluster(t,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "lab-a", Labels: map[string]string{"kubendt/enabled": "true"}, Annotations: map[string]string{OwnerAnnotation: "alice"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "lab-b", Labels: map[string]string{"kubendt/enabled": "true"}, Annotations: map[string]string{OwnerAnnotation: "alice"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "old", Labels: map[string]string{"kubendt/enabled": "true"}}},
		sts("lab-b", "r1", 2), sts("lab-b", "pc", 3),
	)
	// No account record: the environment's defaults are the quota.
	t.Setenv(EnvQuotaNamespaces, "2")
	t.Setenv(EnvQuotaPods, "8")
	t.Setenv(EnvQuotaPodsPerNamespace, "6")
	usage, err := UsageOf("alice")
	if err != nil || usage.Namespaces != 2 || usage.Pods != 5 {
		t.Fatalf("usage: %+v %v", usage, err)
	}
	if err := CheckNamespaceQuota("alice"); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("two namespaces owned of two: %v", err)
	}
	if err := CheckPodQuota("lab-a", 3); err != nil {
		t.Errorf("5 elsewhere plus 3 is 8, within: %v", err)
	}
	if err := CheckPodQuota("lab-a", 4); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("5 elsewhere plus 4 is 9, over 8: %v", err)
	}
	if err := CheckPodQuota("lab-b", 7); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("7 in one namespace, over 6: %v", err)
	}
	if err := CheckPodQuota("lab-b", 6); err != nil {
		t.Errorf("6 in lab-b with nothing elsewhere: %v", err)
	}
	if err := CheckPodQuota("old", 100); err != nil {
		t.Errorf("a namespace with no owner is nobody's quota: %v", err)
	}
	t.Setenv(EnvQuotaPods, "0")
	t.Setenv(EnvQuotaPodsPerNamespace, "0")
	if err := CheckPodQuota("lab-a", 100); err != nil {
		t.Errorf("zero is no limit: %v", err)
	}
}
