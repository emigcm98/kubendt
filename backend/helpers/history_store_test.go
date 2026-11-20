package helpers

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"kubendt/kubeclient"
	"kubendt/types"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	k8stesting "k8s.io/client-go/testing"
)

func fakeHistoryStore(t *testing.T, objs ...runtime.Object) *fake.Clientset {
	t.Helper()
	cs := fake.NewClientset(objs...)
	prev := historyConfigMaps
	historyConfigMaps = func(ns string) corev1client.ConfigMapInterface { return cs.CoreV1().ConfigMaps(ns) }
	prevDB := DB
	DB = nil
	kubeclient.SetClusterIDForTest("c1")
	migratedHistory.Range(func(k, _ any) bool { migratedHistory.Delete(k); return true })
	t.Cleanup(func() {
		historyConfigMaps = prev
		DB = prevDB
		kubeclient.SetClusterIDForTest("")
	})
	return cs
}

func setIP(cidr string) types.ActionEntry {
	return types.ActionEntry{Type: "set_ip", Iface: "eth1", CIDR: cidr}
}

func TestHistorySaveWritesOneObjectPerPod(t *testing.T) {
	cs := fakeHistoryStore(t)
	h, err := OpenPodHistory("lab", "r1-0")
	if err != nil {
		t.Fatal(err)
	}
	h.Save("frr", setIP("10.0.0.1/24"))
	h.Save("frr", types.ActionEntry{Type: "add_static_route", DstCIDR: "10.1.0.0/16", Gateway: "10.0.0.254"})
	if len(h.Operations()) != 2 {
		t.Fatalf("the batch should see its own saves, got %d", len(h.Operations()))
	}
	if len(cs.Actions()) != 1 {
		t.Fatalf("nothing is written before the commit, got %d actions", len(cs.Actions()))
	}
	if err := h.Commit(); err != nil {
		t.Fatal(err)
	}
	cm, err := cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), "kubendt-internal-history-r1-0", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cm.Labels["kubendt/kind"] != "history" || cm.Labels["kubendt/pod"] != "r1-0" || cm.Labels["kubendt/internal"] != "true" {
		t.Errorf("labels: %v", cm.Labels)
	}
	ops, err := ListDriverOperationsForPod("lab", "r1-0")
	if err != nil || len(ops) != 2 {
		t.Fatalf("ops %v err %v", ops, err)
	}
	if ops[0].ID >= ops[1].ID || ops[1].ID >= 1<<53 {
		t.Errorf("ids are ascending moments that fit a JavaScript number: %d %d", ops[0].ID, ops[1].ID)
	}
	if ops[0].ActionType != "set_ip" || ops[1].Action.DstCIDR != "10.1.0.0/16" || ops[0].PodName != "r1-0" || ops[0].Namespace != "lab" {
		t.Errorf("entries: %+v", ops)
	}
}

func TestHistoryKeepsTheLastWordOnAState(t *testing.T) {
	cs := fakeHistoryStore(t)
	h, _ := OpenPodHistory("lab", "r1-0")
	h.Save("frr", setIP("10.0.0.1/24"))
	h.Save("frr", types.ActionEntry{Type: "add_static_route", DstCIDR: "10.1.0.0/16", Gateway: "10.0.0.254"})
	if err := h.Commit(); err != nil {
		t.Fatal(err)
	}
	// The undo of an address stays (the link would put it back), the set goes.
	h, _ = OpenPodHistory("lab", "r1-0")
	h.Save("frr", types.ActionEntry{Type: "remove_ip", Iface: "eth1", CIDR: "10.0.0.1/24"})
	// A route removed cancels the route and is not kept.
	h.Save("frr", types.ActionEntry{Type: "remove_static_route", DstCIDR: "10.1.0.0/16", Gateway: "10.0.0.254"})
	if err := h.Commit(); err != nil {
		t.Fatal(err)
	}
	ops, _ := ListDriverOperationsForPod("lab", "r1-0")
	if len(ops) != 1 || ops[0].ActionType != "remove_ip" {
		t.Fatalf("want the remove_ip alone, got %+v", ops)
	}
	// The same set again replaces the undo; the same payload applied twice
	// is one entry at the end with a fresh stamp.
	h, _ = OpenPodHistory("lab", "r1-0")
	h.Save("frr", setIP("10.0.0.1/24"))
	h.Save("frr", types.ActionEntry{Type: "add_dns_search", DNSDomain: "lab"})
	_ = h.Commit()
	first, _ := ListDriverOperationsForPod("lab", "r1-0")
	time.Sleep(2 * time.Millisecond)
	h, _ = OpenPodHistory("lab", "r1-0")
	h.Save("frr", setIP("10.0.0.1/24"))
	_ = h.Commit()
	ops, _ = ListDriverOperationsForPod("lab", "r1-0")
	if len(ops) != 2 || ops[0].ActionType != "add_dns_search" || ops[1].ActionType != "set_ip" || ops[1].ExecutedAt == first[0].ExecutedAt {
		t.Fatalf("want the set moved to the end with a new stamp, got %+v", ops)
	}
	// Everything undone leaves no object behind.
	h, _ = OpenPodHistory("lab", "r1-0")
	h.Save("frr", types.ActionEntry{Type: "remove_dns_search", DNSDomain: "lab"})
	h.Delete(ops[1].ID)
	_ = h.Commit()
	if _, err := cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), "kubendt-internal-history-r1-0", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("an empty history has no object, got %v", err)
	}
}

func TestHistoryAppliedSince(t *testing.T) {
	fakeHistoryStore(t)
	h, _ := OpenPodHistory("lab", "r1-0")
	h.Save("frr", setIP("10.0.0.1/24"))
	_ = h.Commit()
	h, _ = OpenPodHistory("lab", "r1-0")
	if !h.AppliedSince("frr", setIP("10.0.0.1/24"), time.Now().Add(-time.Minute)) {
		t.Error("applied after the pod was created")
	}
	if h.AppliedSince("frr", setIP("10.0.0.1/24"), time.Now().Add(time.Minute)) {
		t.Error("an entry older than the pod says nothing about it")
	}
	if h.AppliedSince("frr", setIP("10.0.0.2/24"), time.Time{}) || h.AppliedSince("vyos", setIP("10.0.0.1/24"), time.Time{}) {
		t.Error("another payload or driver is another operation")
	}
	h.Save("frr", types.ActionEntry{Type: "remove_ip", Iface: "eth1", CIDR: "10.0.0.1/24"})
	if h.AppliedSince("frr", setIP("10.0.0.1/24"), time.Time{}) {
		t.Error("the undo took the set out of the batch's view")
	}
}

func seedHistory(t *testing.T, namespace, pod string, ops ...PersistedDriverOperation) {
	t.Helper()
	if _, err := writePodHistory(namespace, pod, func([]PersistedDriverOperation) []PersistedDriverOperation { return ops }); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryPurgeBeforeAndDeleteByID(t *testing.T) {
	cs := fakeHistoryStore(t)
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	fresh := time.Now().UTC().Format(time.RFC3339Nano)
	seedHistory(t, "lab", "r1-0",
		PersistedDriverOperation{ID: 10, DriverType: "frr", ActionType: "set_ip", Action: setIP("10.0.0.1/24"), ExecutedAt: old},
		PersistedDriverOperation{ID: 30, DriverType: "frr", ActionType: "set_ip", Action: setIP("10.0.0.2/24"), ExecutedAt: fresh},
	)
	seedHistory(t, "lab", "r2-0", PersistedDriverOperation{ID: 20, DriverType: "frr", ActionType: "set_ip", Action: setIP("10.0.1.1/24"), ExecutedAt: old})
	seedHistory(t, "other", "r1-0", PersistedDriverOperation{ID: 40, DriverType: "frr", ActionType: "set_ip", Action: setIP("10.0.2.1/24"), ExecutedAt: fresh})

	ops, err := ListDriverOperationsForNamespace("lab")
	if err != nil || len(ops) != 3 || ops[0].ID != 10 || ops[1].ID != 20 || ops[2].ID != 30 {
		t.Fatalf("the namespace list is ordered by id across pods: %+v %v", ops, err)
	}
	purged, err := PurgeDriverOperationsBefore("lab", time.Now().Add(-time.Minute))
	if err != nil || purged != 2 {
		t.Fatalf("purged %d err %v", purged, err)
	}
	if _, err := cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), "kubendt-internal-history-r2-0", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Error("r2-0 lost its only entry, its object should go")
	}
	if err := DeleteDriverOperationHistoryByID(40); err != nil {
		t.Fatal(err)
	}
	if err := DeleteDriverOperationHistoryByID(40); !errors.Is(err, ErrOperationNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
	if ops, _ := ListDriverOperationsForNamespace("other"); len(ops) != 0 {
		t.Errorf("other still has %+v", ops)
	}
	if ops, _ := ListDriverOperationsForNamespace("lab"); len(ops) != 1 || ops[0].ID != 30 {
		t.Errorf("lab keeps the fresh entry: %+v", ops)
	}
	if err := DeleteNamespaceDriverOperationHistory("lab"); err != nil {
		t.Fatal(err)
	}
	if list, _ := cs.CoreV1().ConfigMaps("lab").List(context.TODO(), metav1.ListOptions{}); len(list.Items) != 0 {
		t.Errorf("objects left: %d", len(list.Items))
	}
}

func TestHistoryCommitReplaysItsChangesOnConflict(t *testing.T) {
	cs := fakeHistoryStore(t)
	seedHistory(t, "lab", "r1-0", PersistedDriverOperation{ID: 10, DriverType: "frr", ActionType: "set_ip", Action: setIP("10.0.0.1/24"), ExecutedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	conflicts := 0
	cs.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		if conflicts > 0 {
			return false, nil, nil
		}
		conflicts++
		// Another writer added a route in between. The reactor runs under
		// the clientset's lock, so the tracker is written directly.
		raw, _ := encodeHistory([]PersistedDriverOperation{
			{ID: 10, DriverType: "frr", ActionType: "set_ip", Action: setIP("10.0.0.1/24"), ExecutedAt: time.Now().UTC().Format(time.RFC3339Nano)},
			{ID: 11, DriverType: "frr", ActionType: "add_static_route", Action: types.ActionEntry{Type: "add_static_route", DstCIDR: "10.1.0.0/16", Gateway: "10.0.0.254"}, ExecutedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		})
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "kubendt-internal-history-r1-0", Namespace: "lab", Labels: historyLabels("r1-0")}, Data: map[string]string{historyDataKey: raw}}
		if err := cs.Tracker().Update(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, cm, "lab"); err != nil {
			t.Error(err)
		}
		return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, cm.Name, errors.New("the object has been modified"))
	})
	h, _ := OpenPodHistory("lab", "r1-0")
	h.Save("frr", types.ActionEntry{Type: "add_dns_search", DNSDomain: "lab"})
	if err := h.Commit(); err != nil {
		t.Fatal(err)
	}
	ops, _ := ListDriverOperationsForPod("lab", "r1-0")
	if conflicts != 1 || len(ops) != 3 || ops[1].ActionType != "add_static_route" || ops[2].ActionType != "add_dns_search" {
		t.Fatalf("the change was replayed on what the other writer left: conflicts=%d ops=%+v", conflicts, ops)
	}
}

func TestHistoryMigratesTheRowsOfAnOlderBackend(t *testing.T) {
	fakeHistoryStore(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Skip("sqlite driver not available")
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE driver_operation_history (id INTEGER PRIMARY KEY AUTOINCREMENT, cluster_id TEXT NOT NULL DEFAULT '', namespace TEXT NOT NULL, pod_name TEXT NOT NULL, driver_type TEXT NOT NULL, action_type TEXT NOT NULL, action_json TEXT NOT NULL, executed_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	rows := []struct{ ns, pod, typ, json, at string }{
		{"lab", "r1-0", "set_ip", `{"type":"set_ip","iface":"eth1","cidr":"10.0.0.1/24"}`, "2026-11-19T10:00:00.5Z"},
		{"lab", "r1-0", "add_static_route", `{"type":"add_static_route","dst_cidr":"10.1.0.0/16","gateway":"10.0.0.254"}`, "2026-11-19 10:00:01"},
		{"lab", "r2-0", "set_ip", `{"type":"set_ip","iface":"eth1","cidr":"10.0.1.1/24"}`, "2026-11-19T10:00:02Z"},
		{"other", "r1-0", "set_ip", `{"type":"set_ip","iface":"eth1","cidr":"10.9.0.1/24"}`, "2026-11-19T10:00:03Z"},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO driver_operation_history (cluster_id, namespace, pod_name, driver_type, action_type, action_json, executed_at) VALUES ('c1', ?, ?, 'frr', ?, ?, ?)`, r.ns, r.pod, r.typ, r.json, r.at); err != nil {
			t.Fatal(err)
		}
	}
	DB = db
	// r2-0 already has an object, which wins over its rows.
	seedHistory(t, "lab", "r2-0", PersistedDriverOperation{ID: 5, DriverType: "frr", ActionType: "set_ip", Action: setIP("10.0.1.9/24"), ExecutedAt: "2026-11-19T11:00:00Z"})

	ops, err := ListDriverOperationsForNamespace("lab")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 3 || ops[0].ID != 5 || ops[1].ActionType != "set_ip" || ops[1].Action.CIDR != "10.0.0.1/24" || ops[2].ActionType != "add_static_route" {
		t.Fatalf("migrated: %+v", ops)
	}
	if want := time.Date(2026, 11, 19, 10, 0, 0, 500000000, time.UTC).UnixMicro(); ops[1].ID != want || ops[2].ID <= ops[1].ID {
		t.Errorf("ids are the moments the rows were applied: %d %d, want %d first", ops[1].ID, ops[2].ID, want)
	}
	var left int
	_ = db.QueryRow(`SELECT COUNT(*) FROM driver_operation_history WHERE namespace = 'lab'`).Scan(&left)
	if left != 0 {
		t.Errorf("rows left for lab: %d", left)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM driver_operation_history WHERE namespace = 'other'`).Scan(&left)
	if left != 1 {
		t.Errorf("other is untouched until used, rows: %d", left)
	}
	// A namespace delete drops the rows with the objects, with no migration.
	if err := DeleteNamespaceDriverOperationHistory("other"); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM driver_operation_history`).Scan(&left)
	if left != 0 {
		t.Errorf("rows left: %d", left)
	}
}
