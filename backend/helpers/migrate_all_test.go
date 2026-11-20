package helpers

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"kubendt/kubeclient"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

func fakeEnabledNamespaces(t *testing.T, names ...string) {
	t.Helper()
	var objs []corev1.Namespace
	for _, n := range names {
		objs = append(objs, corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: n, Labels: map[string]string{"kubendt/enabled": "true"}}})
	}
	gone := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "gone", Labels: map[string]string{"kubendt/enabled": "true"}, DeletionTimestamp: &metav1.Time{}}}
	cs := fake.NewClientset(&gone)
	for i := range objs {
		if _, err := cs.CoreV1().Namespaces().Create(context.TODO(), &objs[i], metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	prev := stateNamespaces
	stateNamespaces = func() corev1client.NamespaceInterface { return cs.CoreV1().Namespaces() }
	t.Cleanup(func() { stateNamespaces = prev })
}

func TestMigrateAllNamespacesIsNothingWithoutADatabaseOrADisk(t *testing.T) {
	fakeFileStore(t)
	prev := stateNamespaces
	stateNamespaces = func() corev1client.NamespaceInterface { t.Fatal("no namespace should be listed"); return nil }
	t.Cleanup(func() { stateNamespaces = prev })
	if err := MigrateAllNamespaces(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateAllNamespacesMovesEveryStoreAtStart(t *testing.T) {
	files := fakeFileStore(t)
	history := fakeHistoryStore(t)
	layout := fakeLayoutStore(t)
	fakeLinkAttrsTopologies(t)
	fakeEnabledNamespaces(t, "lab")
	kubeclient.SetClusterIDForTest("c1")

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Skip("sqlite driver not available")
	}
	defer func() { _ = db.Close() }()
	for _, ddl := range []string{
		`CREATE TABLE node_positions (cluster_id TEXT, namespace TEXT, node_id TEXT, x REAL, y REAL)`,
		`CREATE TABLE link_attributes (cluster_id TEXT, namespace TEXT, node TEXT, intf TEXT, peer_node TEXT, peer_intf TEXT, delay TEXT DEFAULT '', jitter TEXT DEFAULT '', loss TEXT DEFAULT '', bandwidth TEXT DEFAULT '', mtu INTEGER DEFAULT 0, local_addresses TEXT DEFAULT '', peer_addresses TEXT DEFAULT '')`,
		`CREATE TABLE driver_operation_history (id INTEGER PRIMARY KEY AUTOINCREMENT, cluster_id TEXT, namespace TEXT, pod_name TEXT, driver_type TEXT, action_type TEXT, action_json TEXT, executed_at TEXT)`,
		`CREATE TABLE namespace_file_meta (cluster_id TEXT, namespace TEXT, path TEXT, sensitive INTEGER)`,
		`INSERT INTO node_positions VALUES ('c1', 'lab', 'a', 10, 20)`,
		`INSERT INTO link_attributes (cluster_id, namespace, node, intf, peer_node, peer_intf, delay) VALUES ('c1', 'lab', 'a-0', 'eth1', 'b-0', 'eth1', '5ms')`,
		`INSERT INTO driver_operation_history (cluster_id, namespace, pod_name, driver_type, action_type, action_json, executed_at) VALUES ('c1', 'lab', 'a-0', 'frr', 'set_ip', '{"type":"set_ip","iface":"eth1","cidr":"10.0.0.1/24"}', '2026-11-20T10:00:00Z')`,
		`INSERT INTO namespace_file_meta VALUES ('c1', 'lab', 'secret.env', 1)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	DB = db
	dir := filepath.Join(os.Getenv("FILES_BASE_PATH"), "c1", "lab")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.env"), []byte("TOKEN=x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateAllNamespaces(); err != nil {
		t.Fatal(err)
	}

	if cm, err := layout.CoreV1().ConfigMaps("lab").Get(context.TODO(), LayoutConfigMapName, metav1.GetOptions{}); err != nil || cm.Data[layoutKey] == "" {
		t.Errorf("the layout did not move: %v", err)
	}
	if _, err := history.CoreV1().ConfigMaps("lab").Get(context.TODO(), historyObjectName("a-0"), metav1.GetOptions{}); err != nil {
		t.Errorf("the history did not move: %v", err)
	}
	if _, err := files.CoreV1().Secrets("lab").Get(context.TODO(), "kubendt-secret-file-secret-env", metav1.GetOptions{}); err != nil {
		t.Errorf("the sensitive file did not move as a Secret: %v", err)
	}
	if obj, err := topologyObjects("lab").Get(context.TODO(), "a-0", metav1.GetOptions{}); err != nil || obj.GetAnnotations()["kubendt/link-attrs"] == "" {
		t.Errorf("the link attributes did not move: %v", err)
	}
	for _, table := range []string{"node_positions", "link_attributes", "driver_operation_history", "namespace_file_meta"} {
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE namespace = 'lab'`).Scan(&n)
		if table != "node_positions" && n != 0 {
			t.Errorf("%s still has %d rows", table, n)
		}
	}
}
