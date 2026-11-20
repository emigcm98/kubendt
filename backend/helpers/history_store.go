package helpers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"kubendt/kubeclient"
	"kubendt/types"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// The history of a pod is one ConfigMap in its namespace,
// kubendt-internal-history-<pod>, an ordered JSON list of the operations
// applied to it. A batch (configure, replay, the link attributes) opens the
// pod's history, changes it in memory and writes it once, retried when
// another writer got there first. The SQLite table only feeds the migration
// of an older backend's rows.
const (
	historyObjectPrefix  = "kubendt-internal-history-"
	historyKind          = "history"
	historyDataKey       = "operations"
	historyPodLabel      = "kubendt/pod"
	historySelector      = "kubendt/kind=" + historyKind
	historyWriteAttempts = 5
	// MaxHistoryBytes is what the list may take in its object, under the
	// 1 MiB a ConfigMap holds, some 300 bytes per operation.
	MaxHistoryBytes = 960 << 10
)

var ErrOperationNotFound = errors.New("operation not in the history")

// historyConfigMaps is the client the store goes through, a variable so the
// tests can hand it a fake clientset. "" lists every namespace.
var historyConfigMaps = func(namespace string) corev1client.ConfigMapInterface {
	return kubeclient.Clientset.CoreV1().ConfigMaps(namespace)
}

func historyObjectName(pod string) string { return historyObjectPrefix + pod }

func historyLabels(pod string) map[string]string {
	labels := internalLabels(historyKind)
	labels[historyPodLabel] = pod
	return labels
}

func historyPodOf(cm *corev1.ConfigMap) string {
	if pod := cm.Labels[historyPodLabel]; pod != "" {
		return pod
	}
	return strings.TrimPrefix(cm.Name, historyObjectPrefix)
}

// historyEntry is one operation as the object stores it, the names the API
// answers with so kubectl shows the same thing.
type historyEntry struct {
	ID         int64             `json:"id"`
	DriverType string            `json:"driver_type"`
	ActionType string            `json:"action_type"`
	Action     types.ActionEntry `json:"action"`
	ExecutedAt string            `json:"executed_at"`
}

func decodeHistory(namespace, pod string, cm *corev1.ConfigMap) ([]PersistedDriverOperation, error) {
	raw := cm.Data[historyDataKey]
	if strings.TrimSpace(raw) == "" {
		return []PersistedDriverOperation{}, nil
	}
	var entries []historyEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("the history of %s/%s is not readable: %w", namespace, pod, err)
	}
	ops := make([]PersistedDriverOperation, 0, len(entries))
	for _, e := range entries {
		ops = append(ops, PersistedDriverOperation{ID: e.ID, Namespace: namespace, PodName: pod, DriverType: e.DriverType, ActionType: e.ActionType, Action: e.Action, ExecutedAt: e.ExecutedAt})
	}
	return ops, nil
}

func encodeHistory(ops []PersistedDriverOperation) (string, error) {
	entries := make([]historyEntry, 0, len(ops))
	for _, op := range ops {
		entries = append(entries, historyEntry{ID: op.ID, DriverType: op.DriverType, ActionType: op.ActionType, Action: op.Action, ExecutedAt: op.ExecutedAt})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	if len(raw) > MaxHistoryBytes {
		return "", fmt.Errorf("%d operations take %d bytes, more than the history object holds", len(ops), len(raw))
	}
	return string(raw), nil
}

func readPodHistory(namespace, pod string) ([]PersistedDriverOperation, error) {
	obj, err := historyConfigMaps(namespace).Get(context.TODO(), historyObjectName(pod), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return []PersistedDriverOperation{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the history of %s/%s: %w", namespace, pod, err)
	}
	return decodeHistory(namespace, pod, obj)
}

// writePodHistory reads the pod's history, lets mutate change it and writes
// it back once, again when another writer got there first. An empty list
// deletes the object, so a pod with nothing applied has no history, and a
// list that did not change is not written.
func writePodHistory(namespace, pod string, mutate func([]PersistedDriverOperation) []PersistedDriverOperation) ([]PersistedDriverOperation, error) {
	name := historyObjectName(pod)
	for attempt := 0; attempt < historyWriteAttempts; attempt++ {
		obj, err := historyConfigMaps(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		exists := err == nil
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("could not read the history of %s/%s: %w", namespace, pod, err)
		}
		var ops []PersistedDriverOperation
		if exists {
			if ops, err = decodeHistory(namespace, pod, obj); err != nil {
				return nil, err
			}
		}
		ops = mutate(ops)
		if len(ops) == 0 {
			if !exists {
				return ops, nil
			}
			rv := obj.ResourceVersion
			err = historyConfigMaps(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{ResourceVersion: &rv}})
			if err == nil || apierrors.IsNotFound(err) {
				return ops, nil
			}
			if apierrors.IsConflict(err) {
				continue
			}
			return nil, fmt.Errorf("could not delete the history of %s/%s: %w", namespace, pod, err)
		}
		raw, err := encodeHistory(ops)
		if err != nil {
			return nil, fmt.Errorf("the history of %s/%s: %w", namespace, pod, err)
		}
		if !exists {
			obj = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: historyLabels(pod)},
				Data:       map[string]string{historyDataKey: raw},
			}
			if _, err = historyConfigMaps(namespace).Create(context.TODO(), obj, metav1.CreateOptions{}); err == nil {
				return ops, nil
			}
			if apierrors.IsAlreadyExists(err) {
				continue
			}
			return nil, fmt.Errorf("could not write the history of %s/%s: %w", namespace, pod, err)
		}
		if obj.Data[historyDataKey] == raw {
			return ops, nil
		}
		obj.Data = map[string]string{historyDataKey: raw}
		for k, v := range historyLabels(pod) {
			if obj.Labels == nil {
				obj.Labels = map[string]string{}
			}
			obj.Labels[k] = v
		}
		if _, err = historyConfigMaps(namespace).Update(context.TODO(), obj, metav1.UpdateOptions{}); err == nil {
			return ops, nil
		} else if !apierrors.IsConflict(err) {
			return nil, fmt.Errorf("could not write the history of %s/%s: %w", namespace, pod, err)
		}
	}
	return nil, fmt.Errorf("the history of %s/%s kept changing while it was written", namespace, pod)
}

// PodHistory is a pod's history opened for a batch: the reads see the
// changes at once, the object is written on Commit. The changes are kept as
// functions of the list, so a write that lost a race replays them on what
// the other writer left.
type PodHistory struct {
	Namespace string
	Pod       string
	ops       []PersistedDriverOperation
	changes   []func([]PersistedDriverOperation) []PersistedDriverOperation
}

func OpenPodHistory(namespace, pod string) (*PodHistory, error) {
	if err := migrateDriverHistory(namespace); err != nil {
		return nil, err
	}
	ops, err := readPodHistory(namespace, pod)
	if err != nil {
		return nil, err
	}
	return &PodHistory{Namespace: namespace, Pod: pod, ops: ops}, nil
}

// Operations is the list as the batch sees it, the uncommitted changes in.
func (h *PodHistory) Operations() []PersistedDriverOperation { return h.ops }

func (h *PodHistory) change(m func([]PersistedDriverOperation) []PersistedDriverOperation) {
	h.changes = append(h.changes, m)
	h.ops = m(h.ops)
}

// Save records an action applied now, keeping the last word on its state.
func (h *PodHistory) Save(driverType string, action types.ActionEntry) {
	now := time.Now().UTC()
	namespace, pod := h.Namespace, h.Pod
	h.change(func(ops []PersistedDriverOperation) []PersistedDriverOperation {
		return saveOperation(ops, namespace, pod, driverType, action, now)
	})
}

// Touch stamps the operation as applied now, after a successful replay, so
// configure sees it as applied to the current pod.
func (h *PodHistory) Touch(id int64) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	h.change(func(ops []PersistedDriverOperation) []PersistedDriverOperation {
		out := make([]PersistedDriverOperation, 0, len(ops))
		for _, op := range ops {
			if op.ID == id {
				op.ExecutedAt = now
			}
			out = append(out, op)
		}
		return out
	})
}

func (h *PodHistory) Delete(id int64) {
	h.change(func(ops []PersistedDriverOperation) []PersistedDriverOperation {
		return withoutIDs(ops, map[int64]bool{id: true})
	})
}

// AppliedSince reports whether the same operation is in the history with a
// stamp at or after since. Configure passes the pod's creation time: an
// entry older than the pod was applied to a previous incarnation, or to an
// earlier namespace of the same name, and says nothing about the pod that
// exists now. An entry whose stamp cannot be read counts as applied.
func (h *PodHistory) AppliedSince(driverType string, action types.ActionEntry, since time.Time) bool {
	payload := actionPayload(action)
	var applied []time.Time
	for _, op := range h.ops {
		if op.DriverType != driverType || op.ActionType != action.Type || actionPayload(opAction(op)) != payload {
			continue
		}
		t, ok := parseExecutedAt(op.ExecutedAt)
		if !ok || !t.Before(since) {
			applied = append(applied, t)
		}
	}
	if len(applied) == 0 {
		return false
	}
	// A replay re-stamps every entry, so an action undone later by its pair
	// would look applied forever: it counts only while no other operation
	// on the same state came after it.
	state, key, _, _ := stateOf(action)
	if state == "" {
		return true
	}
	for _, t := range applied {
		if !supersededAfter(h.ops, state, key, payload, t) {
			return true
		}
	}
	return false
}

// Commit writes the batch's changes in one go, nothing when there are none.
func (h *PodHistory) Commit() error {
	if len(h.changes) == 0 {
		return nil
	}
	changes := h.changes
	ops, err := writePodHistory(h.Namespace, h.Pod, func(ops []PersistedDriverOperation) []PersistedDriverOperation {
		for _, m := range changes {
			ops = m(ops)
		}
		return ops
	})
	if err != nil {
		return err
	}
	h.ops, h.changes = ops, nil
	return nil
}

func actionPayload(action types.ActionEntry) string {
	raw, _ := json.Marshal(action)
	return string(raw)
}

func withoutIDs(ops []PersistedDriverOperation, drop map[int64]bool) []PersistedDriverOperation {
	out := make([]PersistedDriverOperation, 0, len(ops))
	for _, op := range ops {
		if !drop[op.ID] {
			out = append(out, op)
		}
	}
	return out
}

// nextOperationID is the moment in microseconds, which fits a JavaScript
// number, bumped past the entries already there so a list never repeats one.
func nextOperationID(ops []PersistedDriverOperation, now time.Time) int64 {
	id := now.UnixMicro()
	for _, op := range ops {
		if op.ID >= id {
			id = op.ID + 1
		}
	}
	return id
}

// saveOperation is the history rule: the entries on the state the action
// writes or clears go, an undo nothing would reverse is not kept, and the
// same payload applied again moves to the end with a fresh stamp instead of
// stacking a duplicate, so replay runs it once, at its latest position.
func saveOperation(ops []PersistedDriverOperation, namespace, pod, driverType string, action types.ActionEntry, now time.Time) []PersistedDriverOperation {
	if state, key, clears, undoPersists := stateOf(action); state != "" {
		states, keyed := clearedWith(action)
		drop := map[int64]bool{}
		for _, op := range rowsOnState(ops, state, key) {
			drop[op.ID] = true
		}
		for _, op := range rowsOnStates(ops, states, key, keyed) {
			drop[op.ID] = true
		}
		ops = withoutIDs(ops, drop)
		if clears && !undoPersists {
			return ops
		}
	}
	payload := actionPayload(action)
	kept := make([]PersistedDriverOperation, 0, len(ops)+1)
	for _, op := range ops {
		if op.DriverType == driverType && op.ActionType == action.Type && actionPayload(opAction(op)) == payload {
			continue
		}
		kept = append(kept, op)
	}
	return append(kept, PersistedDriverOperation{
		ID:         nextOperationID(kept, now),
		Namespace:  namespace,
		PodName:    pod,
		DriverType: driverType,
		ActionType: action.Type,
		Action:     action,
		ExecutedAt: now.Format(time.RFC3339Nano),
	})
}

func ListDriverOperationsForPod(namespace, podName string) ([]PersistedDriverOperation, error) {
	if err := migrateDriverHistory(namespace); err != nil {
		return nil, err
	}
	return readPodHistory(namespace, podName)
}

// ListDriverOperationsForNamespace is every pod's history in the order the
// operations were applied, the ids being the moments they were.
func ListDriverOperationsForNamespace(namespace string) ([]PersistedDriverOperation, error) {
	if err := migrateDriverHistory(namespace); err != nil {
		return nil, err
	}
	list, err := historyConfigMaps(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: historySelector})
	if err != nil {
		return nil, fmt.Errorf("could not list the history of namespace %s: %w", namespace, err)
	}
	ops := make([]PersistedDriverOperation, 0)
	for i := range list.Items {
		podOps, err := decodeHistory(namespace, historyPodOf(&list.Items[i]), &list.Items[i])
		if err != nil {
			return nil, err
		}
		ops = append(ops, podOps...)
	}
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].ID < ops[j].ID })
	return ops, nil
}

// PurgeStaleDriverOperations drops every operation of the namespace recorded
// before the Namespace object itself was created. Such entries belong to an
// earlier namespace of the same name that was deleted behind KubeNDT's back,
// and left alone they make configure skip actions as "already applied" and
// replay push a dead topology's configuration onto new pods. Returns how many
// went.
func PurgeStaleDriverOperations(namespace string) (int, error) {
	ns, err := kubeclient.Clientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("could not read namespace %s: %w", namespace, err)
	}
	return PurgeDriverOperationsBefore(namespace, ns.CreationTimestamp.Time)
}

// PurgeDriverOperationsBefore removes the namespace's operations older than t.
func PurgeDriverOperationsBefore(namespace string, t time.Time) (int, error) {
	if err := migrateDriverHistory(namespace); err != nil {
		return 0, err
	}
	list, err := historyConfigMaps(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: historySelector})
	if err != nil {
		return 0, fmt.Errorf("could not list the history of namespace %s: %w", namespace, err)
	}
	purged := 0
	for i := range list.Items {
		stale := 0
		_, err := writePodHistory(namespace, historyPodOf(&list.Items[i]), func(ops []PersistedDriverOperation) []PersistedDriverOperation {
			stale = 0
			kept := make([]PersistedDriverOperation, 0, len(ops))
			for _, op := range ops {
				if at, ok := parseExecutedAt(op.ExecutedAt); ok && at.Before(t) {
					stale++
					continue
				}
				kept = append(kept, op)
			}
			return kept
		})
		if err != nil {
			return purged, err
		}
		purged += stale
	}
	return purged, nil
}

// DeleteDriverOperationHistoryByID removes one operation wherever it is: the
// id says nothing about the namespace, so the histories are listed across
// the cluster. Rows of a namespace an older backend wrote and this one has
// not touched yet keep their own ids, and are tried last.
func DeleteDriverOperationHistoryByID(id int64) error {
	list, err := historyConfigMaps("").List(context.TODO(), metav1.ListOptions{LabelSelector: historySelector})
	if err != nil {
		return fmt.Errorf("could not list the histories: %w", err)
	}
	for i := range list.Items {
		cm := &list.Items[i]
		ops, err := decodeHistory(cm.Namespace, historyPodOf(cm), cm)
		if err != nil {
			return err
		}
		if !containsID(ops, id) {
			continue
		}
		found := false
		_, err = writePodHistory(cm.Namespace, historyPodOf(cm), func(ops []PersistedDriverOperation) []PersistedDriverOperation {
			found = containsID(ops, id)
			return withoutIDs(ops, map[int64]bool{id: true})
		})
		if err != nil {
			return err
		}
		if found {
			return nil
		}
		return ErrOperationNotFound
	}
	if DB != nil {
		res, err := DB.Exec(`DELETE FROM driver_operation_history WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("error deleting driver operation history id=%d: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
	}
	return ErrOperationNotFound
}

// HistoryEntryNamespace is the namespace an operation id belongs to, the
// access check of a delete by id asking before the delete looks it up again.
func HistoryEntryNamespace(id int64) (string, bool) {
	list, err := historyConfigMaps("").List(context.TODO(), metav1.ListOptions{LabelSelector: historySelector})
	if err != nil {
		return "", false
	}
	for i := range list.Items {
		cm := &list.Items[i]
		if ops, err := decodeHistory(cm.Namespace, historyPodOf(cm), cm); err == nil && containsID(ops, id) {
			return cm.Namespace, true
		}
	}
	return "", false
}

func containsID(ops []PersistedDriverOperation, id int64) bool {
	for _, op := range ops {
		if op.ID == id {
			return true
		}
	}
	return false
}

// DeleteNamespaceDriverOperationHistory drops every pod's history, the rows
// an older backend kept with it. The namespace may be on its way out, so
// nothing is migrated first.
func DeleteNamespaceDriverOperationHistory(namespace string) error {
	if DB != nil {
		clusterID, err := kubeclient.CurrentClusterID()
		if err != nil {
			return err
		}
		if _, err := DB.Exec(`DELETE FROM driver_operation_history WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace); err != nil {
			return fmt.Errorf("error deleting driver operation history for namespace %s: %w", namespace, err)
		}
	}
	list, err := historyConfigMaps(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: historySelector})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not list the history of namespace %s: %w", namespace, err)
	}
	for i := range list.Items {
		if err := historyConfigMaps(namespace).Delete(context.TODO(), list.Items[i].Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("could not delete the history of %s/%s: %w", namespace, historyPodOf(&list.Items[i]), err)
		}
	}
	return nil
}

func DeleteDriverOperationHistoryForPod(namespace, podName string) error {
	if DB != nil {
		clusterID, err := kubeclient.CurrentClusterID()
		if err != nil {
			return err
		}
		if _, err := DB.Exec(`DELETE FROM driver_operation_history WHERE cluster_id = ? AND namespace = ? AND pod_name = ?`, clusterID, namespace, podName); err != nil {
			return fmt.Errorf("error deleting driver operation history for pod %s/%s: %w", namespace, podName, err)
		}
	}
	err := historyConfigMaps(namespace).Delete(context.TODO(), historyObjectName(podName), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("could not delete the history of %s/%s: %w", namespace, podName, err)
	}
	return nil
}

// migratedHistory remembers the namespaces whose rows were moved, or that had
// none, so a namespace costs one query per process.
var migratedHistory sync.Map

// migrateDriverHistory moves the rows an older backend kept for the namespace
// into its pods' history objects and drops them, the first time the namespace
// is touched. A pod whose object already exists keeps it, the object being the
// newer truth. The ids become the moments the rows were applied.
func migrateDriverHistory(namespace string) error {
	if DB == nil {
		return nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	key := clusterID + "/" + namespace
	if _, done := migratedHistory.LoadOrStore(key, true); done {
		return nil
	}
	ok := false
	defer func() {
		if !ok {
			migratedHistory.Delete(key)
		}
	}()
	rows, err := DB.Query(`SELECT id, pod_name, driver_type, action_type, action_json, executed_at FROM driver_operation_history WHERE cluster_id = ? AND namespace = ? ORDER BY id ASC`, clusterID, namespace)
	if err != nil {
		return err
	}
	defer rows.Close()
	byPod := map[string][]PersistedDriverOperation{}
	var pods []string
	for rows.Next() {
		var (
			op         PersistedDriverOperation
			actionJSON string
		)
		if err := rows.Scan(&op.ID, &op.PodName, &op.DriverType, &op.ActionType, &actionJSON, &op.ExecutedAt); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(actionJSON), &op.Action); err != nil {
			log.Printf("⚠️ History row %d of %s/%s is not readable and is left behind: %v", op.ID, namespace, op.PodName, err)
			continue
		}
		op.Namespace = namespace
		if at, parsed := parseExecutedAt(op.ExecutedAt); parsed {
			op.ID = at.UnixMicro()
		} else {
			op.ID = 0
		}
		if _, seen := byPod[op.PodName]; !seen {
			pods = append(pods, op.PodName)
		}
		byPod[op.PodName] = append(byPod[op.PodName], op)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(pods) == 0 {
		ok = true
		return nil
	}
	for _, pod := range pods {
		moved := byPod[pod]
		_, err := writePodHistory(namespace, pod, func(current []PersistedDriverOperation) []PersistedDriverOperation {
			if len(current) > 0 {
				return current
			}
			out := make([]PersistedDriverOperation, 0, len(moved))
			var last int64
			for _, op := range moved {
				if op.ID <= last {
					op.ID = last + 1
				}
				last = op.ID
				out = append(out, op)
			}
			return out
		})
		if err != nil {
			return fmt.Errorf("could not move the history of %s/%s to the cluster: %w", namespace, pod, err)
		}
	}
	if _, err := DB.Exec(`DELETE FROM driver_operation_history WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace); err != nil {
		return err
	}
	log.Printf("ℹ️ Operation history of namespace '%s' moved from the database to the cluster (%d pods)", namespace, len(pods))
	ok = true
	return nil
}
