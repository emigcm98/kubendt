package helpers

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"kubendt/kubeclient"
	"kubendt/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// The store against client-go's fake clientset, no cluster and, unless a
// test opens one, no database.
func fakeLayoutStore(t *testing.T) *fake.Clientset {
	t.Helper()
	cs := fake.NewClientset()
	prev := layoutConfigMaps
	layoutConfigMaps = func(ns string) corev1.ConfigMapInterface { return cs.CoreV1().ConfigMaps(ns) }
	prevDB := DB
	DB = nil
	t.Cleanup(func() { layoutConfigMaps = prev; DB = prevDB })
	return cs
}

func TestLayoutEmptyNamespaceCreatesNothing(t *testing.T) {
	cs := fakeLayoutStore(t)
	l, err := GetLayout("lab")
	if err != nil || len(l.Positions) != 0 || len(l.Annotations) != 0 {
		t.Fatalf("want an empty layout, got %v %v", l, err)
	}
	if _, err := cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), LayoutConfigMapName, metav1.GetOptions{}); err == nil {
		t.Errorf("a read must not create the ConfigMap")
	}
}

func TestLayoutPutAndGet(t *testing.T) {
	cs := fakeLayoutStore(t)
	in := types.Layout{
		Positions:   map[string]types.NodePosition{"r1-0": {X: 10.4, Y: 20.6}},
		Annotations: []types.Annotation{{ID: "a1", Type: "rect", X: 1, Y: 2, Width: 300, Height: 200, Text: "Site A", Color: "blue"}},
	}
	if err := PutLayout("lab", in); err != nil {
		t.Fatal(err)
	}
	cm, err := cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), LayoutConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cm.Labels["kubendt/kind"] != "layout" || cm.Labels["kubendt/internal"] != "true" {
		t.Errorf("labels: %v", cm.Labels)
	}
	var stored types.Layout
	if err := json.Unmarshal([]byte(cm.Data[layoutKey]), &stored); err != nil {
		t.Fatal(err)
	}
	// Positions are rounded on the way in.
	if p := stored.Positions["r1-0"]; p.X != 10 || p.Y != 21 {
		t.Errorf("rounded position: %v", p)
	}
	out, err := GetLayout("lab")
	if err != nil || len(out.Annotations) != 1 || out.Annotations[0].Text != "Site A" {
		t.Errorf("read back: %v %v", out, err)
	}
	// A second put replaces the whole document.
	if err := PutLayout("lab", types.Layout{}); err != nil {
		t.Fatal(err)
	}
	out, _ = GetLayout("lab")
	if len(out.Positions) != 0 || len(out.Annotations) != 0 {
		t.Errorf("put must replace, got %v", out)
	}
}

func TestSavePositionsMergesPerNode(t *testing.T) {
	fakeLayoutStore(t)
	if err := PutLayout("lab", types.Layout{Annotations: []types.Annotation{{ID: "n", Type: "note", Text: "keep me"}}}); err != nil {
		t.Fatal(err)
	}
	if err := SavePositions("lab", map[string]types.NodePosition{"a": {X: 1, Y: 1}, "b": {X: 2, Y: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := SavePositions("lab", map[string]types.NodePosition{"b": {X: 9, Y: 9}}); err != nil {
		t.Fatal(err)
	}
	l, _ := GetLayout("lab")
	if l.Positions["a"].X != 1 || l.Positions["b"].X != 9 || len(l.Annotations) != 1 {
		t.Errorf("merge: %v", l)
	}
	// Clearing the positions keeps the annotations, and drops the object when there are none.
	if err := ClearPositions("lab"); err != nil {
		t.Fatal(err)
	}
	l, _ = GetLayout("lab")
	if len(l.Positions) != 0 || len(l.Annotations) != 1 {
		t.Errorf("clear: %v", l)
	}
	if err := PutLayout("lab", types.Layout{Positions: map[string]types.NodePosition{"a": {X: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := ClearPositions("lab"); err != nil {
		t.Fatal(err)
	}
	if _, err := layoutConfigMaps("lab").Get(context.TODO(), LayoutConfigMapName, metav1.GetOptions{}); err == nil {
		t.Errorf("a layout with nothing left must go")
	}
}

func TestValidateLayout(t *testing.T) {
	bad := []types.Layout{
		{Annotations: []types.Annotation{{ID: "", Type: "rect"}}},
		{Annotations: []types.Annotation{{ID: "a", Type: "circle"}}},
		{Annotations: []types.Annotation{{ID: "a", Type: "rect"}, {ID: "a", Type: "note"}}},
		{Annotations: []types.Annotation{{ID: "a", Type: "note", Text: strings.Repeat("x", maxAnnotationText+1)}}},
		{Positions: map[string]types.NodePosition{"": {}}},
	}
	for i, l := range bad {
		if err := ValidateLayout(&l); err == nil {
			t.Errorf("case %d must fail", i)
		}
	}
	var many types.Layout
	for i := 0; i <= maxAnnotations; i++ {
		many.Annotations = append(many.Annotations, types.Annotation{ID: strings.Repeat("a", 1) + string(rune('0'+i%10)) + string(rune('a'+i%26)) + string(rune('A'+(i/26)%26)) + string(rune('0'+(i/260)%10)), Type: "note"})
	}
	if err := ValidateLayout(&many); err == nil {
		t.Errorf("too many annotations must fail")
	}
	var nilDoc types.Layout
	if err := ValidateLayout(&nilDoc); err != nil || nilDoc.Positions != nil || nilDoc.Annotations != nil {
		t.Errorf("an empty document validates and keeps its nil collections, got %v %v", nilDoc, err)
	}
}

func TestMergeLayoutKeepsWhatTheFileLeavesOut(t *testing.T) {
	fakeLayoutStore(t)
	saved := types.Layout{
		Positions:   map[string]types.NodePosition{"r1-0": {X: 1, Y: 2}, "old-0": {X: 3, Y: 4}},
		Annotations: []types.Annotation{{ID: "a", Type: "note", Text: "drawn here"}},
	}
	if err := PutLayout("ns", saved); err != nil {
		t.Fatal(err)
	}
	// positions only: the map is replaced whole, the annotations stay
	if err := MergeLayout("ns", types.Layout{Positions: map[string]types.NodePosition{"r1-0": {X: 10, Y: 20}}}); err != nil {
		t.Fatal(err)
	}
	got, _ := GetLayout("ns")
	if len(got.Positions) != 1 || got.Positions["r1-0"].X != 10 || len(got.Annotations) != 1 {
		t.Errorf("positions replaced and annotations kept, got %+v", got)
	}
	// annotations only, an empty list: cleared on purpose, positions stay
	if err := MergeLayout("ns", types.Layout{Annotations: []types.Annotation{}}); err != nil {
		t.Fatal(err)
	}
	got, _ = GetLayout("ns")
	if len(got.Positions) != 1 || len(got.Annotations) != 0 {
		t.Errorf("annotations cleared and positions kept, got %+v", got)
	}
	// nothing: nothing changes
	if err := MergeLayout("ns", types.Layout{}); err != nil {
		t.Fatal(err)
	}
	got, _ = GetLayout("ns")
	if len(got.Positions) != 1 || len(got.Annotations) != 0 {
		t.Errorf("an empty file layout changes nothing, got %+v", got)
	}
	// a bad one is refused before anything is touched
	if err := MergeLayout("ns", types.Layout{Annotations: []types.Annotation{{ID: "x", Type: "triangle"}}}); err == nil {
		t.Errorf("a bad annotation must fail")
	}
}

func TestModifyLayoutByIdAndPerPod(t *testing.T) {
	fakeLayoutStore(t)
	saved := types.Layout{
		Positions:   map[string]types.NodePosition{"r1-0": {X: 1, Y: 2}},
		Annotations: []types.Annotation{{ID: "a", Type: "note", Text: "old"}, {ID: "b", Type: "rect"}},
	}
	if err := PutLayout("ns", saved); err != nil {
		t.Fatal(err)
	}
	err := ModifyLayout("ns",
		[]types.Annotation{{ID: "a", Type: "note", Text: "new"}, {ID: "c", Type: "ellipse"}},
		[]string{"b", "nope"},
		map[string]types.NodePosition{"r2-0": {X: 30, Y: 40}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := GetLayout("ns")
	ids := ""
	for _, a := range got.Annotations {
		ids += a.ID + ":" + a.Text + " "
	}
	if ids != "a:new c: " {
		t.Errorf("a replaced in place, b gone, c joined, got %q", ids)
	}
	if len(got.Positions) != 2 || got.Positions["r1-0"].X != 1 || got.Positions["r2-0"].Y != 40 {
		t.Errorf("positions merged per pod, got %+v", got.Positions)
	}
	// the same id added and removed in one call: it goes
	if err := ModifyLayout("ns", []types.Annotation{{ID: "c", Type: "rect"}}, []string{"c"}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = GetLayout("ns")
	if len(got.Annotations) != 1 || got.Annotations[0].ID != "a" {
		t.Errorf("c gone, got %+v", got.Annotations)
	}
}

func TestDeleteLayoutDropsTheDocument(t *testing.T) {
	cs := fakeLayoutStore(t)
	if err := PutLayout("ns", types.Layout{Positions: map[string]types.NodePosition{"r1-0": {X: 1}}, Annotations: []types.Annotation{{ID: "a", Type: "rect"}}}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteLayout("ns"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().ConfigMaps("ns").Get(context.TODO(), LayoutConfigMapName, metav1.GetOptions{}); err == nil {
		t.Errorf("the ConfigMap must be gone")
	}
	if err := DeleteLayout("ns"); err != nil {
		t.Errorf("deleting twice is fine, got %v", err)
	}
	got, err := GetLayout("ns")
	if err != nil || len(got.Positions) != 0 || len(got.Annotations) != 0 {
		t.Errorf("an empty layout after the delete, got %+v %v", got, err)
	}
}

func TestLayoutMigratesAndMirrorsTheDatabase(t *testing.T) {
	cs := fakeLayoutStore(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Skip("no sqlite:", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE node_positions (cluster_id TEXT NOT NULL DEFAULT '', namespace TEXT, node_id TEXT, x REAL, y REAL, PRIMARY KEY (cluster_id, namespace, node_id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO node_positions VALUES ('c1', 'lab', 'r1-0', 100, 200), ('c1', 'other', 'x', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	DB = db
	kubeclient.SetClusterIDForTest("c1")
	t.Cleanup(func() { kubeclient.SetClusterIDForTest("") })

	// The first read with no ConfigMap takes the rows and creates it.
	l, err := GetLayout("lab")
	if err != nil || l.Positions["r1-0"].X != 100 {
		t.Fatalf("migration: %v %v", l, err)
	}
	if _, err := cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), LayoutConfigMapName, metav1.GetOptions{}); err != nil {
		t.Errorf("the migration must create the ConfigMap: %v", err)
	}
	// A save goes to the ConfigMap and the rows follow.
	if err := SavePositions("lab", map[string]types.NodePosition{"r1-0": {X: 5, Y: 6}, "sw-0": {X: 7, Y: 8}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM node_positions WHERE cluster_id = 'c1' AND namespace = 'lab'`).Scan(&n); err != nil || n != 2 {
		t.Errorf("mirror rows: %d %v", n, err)
	}
	var x float64
	if err := db.QueryRow(`SELECT x FROM node_positions WHERE namespace = 'lab' AND node_id = 'r1-0'`).Scan(&x); err != nil || x != 5 {
		t.Errorf("mirror value: %v %v", x, err)
	}
	// The other namespace's rows are untouched.
	if err := db.QueryRow(`SELECT COUNT(*) FROM node_positions WHERE namespace = 'other'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("other namespace: %d %v", n, err)
	}
}
