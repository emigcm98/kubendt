package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"kubendt/kubeclient"
	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// The graph's layout of a namespace, its node positions and annotations,
// lives in the namespace as the ConfigMap kubendt-internal-layout: it goes
// with the lab, any backend sees it, and a label lists it. The node_positions
// table fills it once and mirrors it while a database is open, so a
// namespace recreated under the same name keeps its layout as it always
// did, until 2.0 drops the table.

const (
	LayoutConfigMapName = "kubendt-internal-layout"
	layoutKey           = "layout.json"
	maxLayoutBytes      = 512 << 10
	maxAnnotations      = 500
	maxAnnotationText   = 4 << 10
)

// Every object of the platform's own carries the kubendt-internal- prefix
// and a kind, so the file manager, which lists by kubendt/kind=file, and
// the mount garbage collection never take it for a user's file.
func internalLabels(kind string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "kubendt-backend",
		"kubendt/internal":             "true",
		"kubendt/kind":                 kind,
	}
}

// layoutConfigMaps is the client the store goes through, a variable so the
// tests can hand it a fake clientset.
var layoutConfigMaps = func(namespace string) corev1.ConfigMapInterface {
	return kubeclient.Clientset.CoreV1().ConfigMaps(namespace)
}

var annotationTypes = map[string]bool{"note": true, "rect": true, "ellipse": true}

// ValidateLayout rounds the positions and checks the annotations: a type
// the graph draws, an id of its own, text and document sizes that fit a
// ConfigMap with room to spare.
func ValidateLayout(l *types.Layout) error {
	if l.Positions == nil {
		l.Positions = map[string]types.NodePosition{}
	}
	if l.Annotations == nil {
		l.Annotations = []types.Annotation{}
	}
	for id, p := range l.Positions {
		if id == "" {
			return fmt.Errorf("a position has no node name")
		}
		l.Positions[id] = types.NodePosition{X: math.Round(p.X), Y: math.Round(p.Y)}
	}
	if len(l.Annotations) > maxAnnotations {
		return fmt.Errorf("at most %d annotations, got %d", maxAnnotations, len(l.Annotations))
	}
	seen := map[string]bool{}
	for i, a := range l.Annotations {
		switch {
		case a.ID == "" || len(a.ID) > 64:
			return fmt.Errorf("annotation %d: id must be 1 to 64 characters", i)
		case seen[a.ID]:
			return fmt.Errorf("annotation %d: id %q repeated", i, a.ID)
		case !annotationTypes[a.Type]:
			return fmt.Errorf("annotation %q: type must be note, rect or ellipse, got %q", a.ID, a.Type)
		case len(a.Text) > maxAnnotationText:
			return fmt.Errorf("annotation %q: text over %d bytes", a.ID, maxAnnotationText)
		case len(a.Color) > 32 || len(a.Border) > 32 || len(a.TextSize) > 16:
			return fmt.Errorf("annotation %q: a style value is too long", a.ID)
		}
		seen[a.ID] = true
	}
	return nil
}

func encodeLayout(l types.Layout) (string, error) {
	b, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	if len(b) > maxLayoutBytes {
		return "", fmt.Errorf("the layout is %d bytes, over the %d allowed", len(b), maxLayoutBytes)
	}
	return string(b), nil
}

func decodeLayout(cm *v1.ConfigMap) types.Layout {
	var l types.Layout
	if raw := strings.TrimSpace(cm.Data[layoutKey]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &l)
	}
	if l.Positions == nil {
		l.Positions = map[string]types.NodePosition{}
	}
	if l.Annotations == nil {
		l.Annotations = []types.Annotation{}
	}
	return l
}

// GetLayout reads the namespace's layout. With no ConfigMap yet, the rows
// the database holds for the namespace become one, and a namespace with
// neither answers an empty layout and creates nothing.
func GetLayout(namespace string) (types.Layout, error) {
	cm, err := layoutConfigMaps(namespace).Get(context.TODO(), LayoutConfigMapName, metav1.GetOptions{})
	if err == nil {
		return decodeLayout(cm), nil
	}
	if !apierrors.IsNotFound(err) {
		return types.Layout{}, fmt.Errorf("could not read the layout of %s: %w", namespace, err)
	}
	l := types.Layout{Positions: map[string]types.NodePosition{}, Annotations: []types.Annotation{}}
	if rows := positionsFromDB(namespace); len(rows) > 0 {
		l.Positions = rows
		if err := PutLayout(namespace, l); err != nil {
			return types.Layout{}, err
		}
	}
	return l, nil
}

// PutLayout replaces the namespace's layout, creating the ConfigMap on the
// first write and mirroring the positions to the database while one is open.
func PutLayout(namespace string, l types.Layout) error {
	if err := ValidateLayout(&l); err != nil {
		return err
	}
	raw, err := encodeLayout(l)
	if err != nil {
		return err
	}
	cms := layoutConfigMaps(namespace)
	for attempt := 0; ; attempt++ {
		existing, err := cms.Get(context.TODO(), LayoutConfigMapName, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			cm := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: LayoutConfigMapName, Namespace: namespace, Labels: internalLabels("layout")},
				Data:       map[string]string{layoutKey: raw},
			}
			_, err = cms.Create(context.TODO(), cm, metav1.CreateOptions{})
		case err == nil:
			if existing.Labels == nil {
				existing.Labels = map[string]string{}
			}
			for k, v := range internalLabels("layout") {
				existing.Labels[k] = v
			}
			existing.Data = map[string]string{layoutKey: raw}
			_, err = cms.Update(context.TODO(), existing, metav1.UpdateOptions{})
		}
		if err == nil {
			break
		}
		// Two writers at once: the other one won the version, try again on
		// the fresh object; a create that lost the race is the same case.
		if (apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err)) && attempt < 3 {
			continue
		}
		return fmt.Errorf("could not save the layout of %s: %w", namespace, err)
	}
	mirrorPositionsToDB(namespace, l.Positions)
	return nil
}

// SavePositions merges positions into the layout, each node's replacing its
// own, which is what the graph's auto-save after a modify sends.
func SavePositions(namespace string, positions map[string]types.NodePosition) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		l, err := GetLayout(namespace)
		if err != nil {
			return err
		}
		for id, p := range positions {
			l.Positions[id] = p
		}
		if lastErr = PutLayout(namespace, l); lastErr == nil {
			return nil
		}
	}
	return lastErr
}

// ClearPositions drops the positions of a namespace whose topology is
// going, keeping the annotations, and the ConfigMap if it has any.
func ClearPositions(namespace string) error {
	cms := layoutConfigMaps(namespace)
	cm, err := cms.Get(context.TODO(), LayoutConfigMapName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		mirrorPositionsToDB(namespace, nil)
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not read the layout of %s: %w", namespace, err)
	}
	l := decodeLayout(cm)
	if len(l.Annotations) == 0 {
		if err := cms.Delete(context.TODO(), LayoutConfigMapName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("could not delete the layout of %s: %w", namespace, err)
		}
		mirrorPositionsToDB(namespace, nil)
		return nil
	}
	l.Positions = map[string]types.NodePosition{}
	return PutLayout(namespace, l)
}

// positionsFromDB reads what the node_positions table holds for the
// namespace, nothing when no database or cluster is at hand.
func positionsFromDB(namespace string) map[string]types.NodePosition {
	if DB == nil {
		return nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return nil
	}
	rows, err := DB.Query(`SELECT node_id, x, y FROM node_positions WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]types.NodePosition{}
	for rows.Next() {
		var id string
		var p types.NodePosition
		if rows.Scan(&id, &p.X, &p.Y) == nil {
			out[id] = p
		}
	}
	return out
}

func mirrorPositionsToDB(namespace string, positions map[string]types.NodePosition) {
	if DB == nil {
		return
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return
	}
	tx, err := DB.Begin()
	if err != nil {
		return
	}
	if _, err := tx.Exec(`DELETE FROM node_positions WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace); err != nil {
		_ = tx.Rollback()
		return
	}
	for id, p := range positions {
		if _, err := tx.Exec(`INSERT INTO node_positions (cluster_id, namespace, node_id, x, y) VALUES (?, ?, ?, ?, ?)`, clusterID, namespace, id, p.X, p.Y); err != nil {
			_ = tx.Rollback()
			return
		}
	}
	_ = tx.Commit()
}
