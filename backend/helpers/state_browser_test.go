package helpers

import (
	"strings"
	"testing"
	"time"

	"kubendt/types"

	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/fake"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	coordinationclient "k8s.io/client-go/kubernetes/typed/coordination/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/utils/ptr"
)

func fakeStateCluster(t *testing.T, objs ...runtime.Object) {
	t.Helper()
	cs := fake.NewClientset(objs...)
	prevNS, prevSTS, prevPods, prevCM, prevSec, prevLease, prevTopo := stateNamespaces, stateStatefulSets, statePods, stateConfigMaps, stateSecrets, stateLeases, stateTopologies
	stateNamespaces = func() corev1client.NamespaceInterface { return cs.CoreV1().Namespaces() }
	stateStatefulSets = func(ns string) appsv1client.StatefulSetInterface { return cs.AppsV1().StatefulSets(ns) }
	statePods = func(ns string) corev1client.PodInterface { return cs.CoreV1().Pods(ns) }
	stateConfigMaps = func(ns string) corev1client.ConfigMapInterface { return cs.CoreV1().ConfigMaps(ns) }
	stateSecrets = func(ns string) corev1client.SecretInterface { return cs.CoreV1().Secrets(ns) }
	stateLeases = func(ns string) coordinationclient.LeaseInterface { return cs.CoordinationV1().Leases(ns) }
	dyn := fakeTopologies(t, topologyCR("lab", "r1-0",
		map[string]interface{}{"uid": int64(1), "local_intf": "eth1", "peer_pod": "h1-0", "peer_intf": "eth1"},
		map[string]interface{}{"uid": int64(2), "local_intf": "eth2", "peer_pod": "h2-0", "peer_intf": "eth1"}))
	stateTopologies = func(ns string) dynamic.ResourceInterface { return dyn.Resource(TopologyGVR).Namespace(ns) }
	t.Setenv(SystemNamespaceEnv, "")
	t.Cleanup(func() {
		stateNamespaces, stateStatefulSets, statePods, stateConfigMaps, stateSecrets, stateLeases, stateTopologies = prevNS, prevSTS, prevPods, prevCM, prevSec, prevLease, prevTopo
	})
}

func labelled(kind string, more map[string]string) map[string]string {
	l := internalLabels(kind)
	for k, v := range more {
		l[k] = v
	}
	return l
}

func stateFixture(t *testing.T) {
	t.Helper()
	history, _ := encodeHistory([]PersistedDriverOperation{
		{ID: 1, DriverType: "frr", ActionType: "set_ip", Action: types.ActionEntry{Type: "set_ip", Iface: "eth1", CIDR: "10.0.0.1/24"}, ExecutedAt: "2026-11-20T07:00:00Z"},
		{ID: 2, DriverType: "frr", ActionType: "add_static_route", Action: types.ActionEntry{Type: "add_static_route", DstCIDR: "10.1.0.0/16", Gateway: "10.0.0.254"}, ExecutedAt: "2026-11-20T07:00:01Z"},
	})
	now := metav1.NewTime(time.Now())
	fakeStateCluster(t,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "lab", Labels: map[string]string{"kubendt/enabled": "true"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kubendt-system", Labels: map[string]string{"kubendt/system": "true"}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "other"}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "r1", Namespace: "lab"}, Spec: appsv1.StatefulSetSpec{Replicas: ptr.To(int32(2)),
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"kubendt/type": "router", "kubendt/driver": "FRRRouterDriver", "app": "r1"}}}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "r1-0", Namespace: "lab", Labels: map[string]string{"app": "r1", "kubendt/type": "router"}},
			Spec: corev1.PodSpec{NodeName: "worker1"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "r1-env", Namespace: "lab"}, Data: map[string]string{"A": "1", "B": "2"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "kube-root-ca.crt", Namespace: "lab"}, Data: map[string]string{"ca.crt": "x"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "kubendt-internal-history-r1-0", Namespace: "lab", Labels: historyLabels("r1-0")}, Data: map[string]string{historyDataKey: history}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "kubendt-internal-layout", Namespace: "lab", Labels: internalLabels("layout")},
			Data: map[string]string{layoutKey: `{"positions":{"r1":{"x":1,"y":2},"h1":{"x":3,"y":4}},"annotations":[{"id":"a","type":"note"}]}`}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "kubendt-file-test-sh", Namespace: "lab",
			Labels: map[string]string{"kubendt/kind": "file", "kubendt/mount-file": "true"}, Annotations: map[string]string{MountFilePathAnnotation: "test.sh"}},
			Data: map[string]string{"test.sh": "echo hi"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: IfaceCountsConfigMapName, Namespace: "lab", Labels: map[string]string{"kubendt/internal": "true"}}, Data: map[string]string{"r1": "2"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "kubendt-secret-file-secret-env", Namespace: "lab",
			Labels: map[string]string{"kubendt/kind": "file", "kubendt/sensitive": "true"}, Annotations: map[string]string{MountFilePathAnnotation: "secret.env"}},
			Data: map[string][]byte{"secret.env": []byte("TOKEN=abc")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "default-token", Namespace: "lab"}, Type: corev1.SecretTypeServiceAccountToken},
		&coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: LockLeaseName, Namespace: "lab", Labels: internalLabels("lock"), Annotations: map[string]string{lockOperationAnnotation: "deploy"}},
			Spec: coordinationv1.LeaseSpec{HolderIdentity: ptr.To("backend-a"), RenewTime: &metav1.MicroTime{Time: now.Time}, LeaseDurationSeconds: ptr.To(int32(60))}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "kubendt-user-admin", Namespace: "kubendt-system", Labels: labelled("user", map[string]string{"kubendt/user": "admin"})}, Data: map[string][]byte{"password_hash": []byte("$2a$10$hash")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "kubendt-token-1", Namespace: "kubendt-system", Labels: internalLabels("token")}, Data: map[string][]byte{"name": []byte("ci"), "hash": []byte("h")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "kubendt-internal-session-key", Namespace: "kubendt-system", Labels: internalLabels("session-key")}, Data: map[string][]byte{"key": []byte("0123456789abcdef0123456789abcdef")}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "kubendt-internal-layout-gone", Namespace: "kubendt-system", Labels: labelled("layout-attic", map[string]string{"kubendt/namespace": "gone"})}, Data: map[string]string{layoutKey: "{}"}},
	)
}

func rowsByName(rows []StateObject) map[string]StateObject {
	out := map[string]StateObject{}
	for _, r := range rows {
		out[r.Kind+"/"+r.Name] = r
	}
	return out
}

func TestStateBrowserTellsEveryObjectForWhatItIs(t *testing.T) {
	stateFixture(t)
	namespaces, err := ListStateNamespaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(namespaces) != 2 || namespaces[0].Name != "lab" || namespaces[0].Role != "lab" || namespaces[1].Name != "kubendt-system" || namespaces[1].Role != "system" {
		t.Fatalf("namespaces: %+v", namespaces)
	}
	lab := namespaces[0].Objects
	order := make([]string, 0, len(lab))
	for _, r := range lab {
		order = append(order, r.Role+":"+r.Name)
	}
	want := []string{"namespace:lab", "node:r1", "pod:r1-0", "topology:r1-0", "env:r1-env", "file:kubendt-file-test-sh", "file:kubendt-secret-file-secret-env",
		"history:kubendt-internal-history-r1-0", "layout:kubendt-internal-layout", "lock:" + LockLeaseName, "iface-counts:" + IfaceCountsConfigMapName}
	if strings.Join(order, " ") != strings.Join(want, " ") {
		t.Fatalf("lab rows:\n got %v\nwant %v", order, want)
	}
	rows := rowsByName(lab)
	checks := map[string]string{
		"StatefulSet/r1":                          "router FRRRouterDriver, 2 replicas",
		"Pod/r1-0":                                "Running on worker1",
		"Topology/r1-0":                           "2 links",
		"ConfigMap/r1-env":                        "2 variables",
		"ConfigMap/kubendt-file-test-sh":          "test.sh, 7 bytes",
		"Secret/kubendt-secret-file-secret-env":   "secret.env, 9 bytes, sensitive",
		"ConfigMap/kubendt-internal-history-r1-0": "r1-0, 2 operations",
		"ConfigMap/kubendt-internal-layout":       "2 positions, 1 annotation",
		"Lease/" + LockLeaseName:                  "held by backend-a for deploy",
		"ConfigMap/" + IfaceCountsConfigMapName:   "1 node",
	}
	for key, detail := range checks {
		if rows[key].Detail != detail {
			t.Errorf("%s: detail %q, want %q", key, rows[key].Detail, detail)
		}
	}
	if rows["StatefulSet/r1"].Node != "r1" || rows["Pod/r1-0"].Node != "r1" || rows["ConfigMap/kubendt-internal-history-r1-0"].Node != "r1" || rows["ConfigMap/r1-env"].Node != "r1" {
		t.Error("the node of each object is the StatefulSet it belongs to")
	}
	if rows["StatefulSet/r1"].Size == 0 || rows["Namespace/lab"].Role != "namespace" {
		t.Error("size and the namespace row")
	}

	system := rowsByName(namespaces[1].Objects)
	for key, detail := range map[string]string{
		"Secret/kubendt-user-admin":              "admin, password hash",
		"Secret/kubendt-token-1":                 "ci",
		"Secret/kubendt-internal-session-key":    "what the sessions are signed with",
		"ConfigMap/kubendt-internal-layout-gone": "layout of the deleted namespace gone",
	} {
		if system[key].Detail != detail {
			t.Errorf("%s: detail %q, want %q", key, system[key].Detail, detail)
		}
	}
	if len(namespaces[1].Objects) != 5 {
		t.Errorf("system rows: %d, want the namespace and four objects", len(namespaces[1].Objects))
	}
}

func TestStateObjectIsReadableAndRedacted(t *testing.T) {
	stateFixture(t)
	doc, text, err := GetStateObject("lab", "secret", "kubendt-secret-file-secret-env")
	if err != nil {
		t.Fatal(err)
	}
	if doc["kind"] != "Secret" || doc["apiVersion"] != "v1" {
		t.Errorf("type: %v %v", doc["kind"], doc["apiVersion"])
	}
	data := doc["data"].(map[string]any)
	if data["secret.env"] != "<9 bytes>" || strings.Contains(text, "TOKEN") || !strings.Contains(text, "kind: Secret") {
		t.Errorf("the secret's values are replaced by their sizes: %v\n%s", data, text)
	}
	if _, _, err := GetStateObject("lab", "topology", "r1-0"); err != nil {
		t.Errorf("topology: %v", err)
	}
	if doc, _, err := GetStateObject("lab", "statefulset", "r1"); err != nil || doc["kind"] != "StatefulSet" || doc["apiVersion"] != "apps/v1" {
		t.Errorf("statefulset: %v %v", doc, err)
	}
	if _, _, err := GetStateObject("lab", "deployment", "x"); err == nil || !strings.HasPrefix(err.Error(), "unknown kind") {
		t.Errorf("an unknown kind is refused: %v", err)
	}
	if _, _, err := GetStateObject("lab", "pod", "nope"); err == nil {
		t.Error("a missing object is an error")
	}
}
