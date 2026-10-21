package helpers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"kubendt/kubeclient"
	"kubendt/types"
	"log"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PersistedDriverOperation struct {
	ID         int64
	Namespace  string
	PodName    string
	DriverType string
	ActionType string
	Action     types.ActionEntry
	ExecutedAt string
}

type DriverReplayStats struct {
	Total    int `json:"total"`
	Replayed int `json:"replayed"`
	Pruned   int `json:"pruned"`
}

// deleteQdiscHistoryForIface removes any persisted add_qdisc/del_qdisc entries
// for the given pod and interface, so a new qdisc operation supersedes the
// previous one instead of stacking on top of it.
func deleteQdiscHistoryForIface(namespace, podName, iface string) error {
	ops, err := ListDriverOperationsForPod(namespace, podName)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if (op.ActionType == "add_qdisc" || op.ActionType == "del_qdisc") && op.Action.Iface == iface {
			if delErr := DeleteDriverOperationHistoryByID(op.ID); delErr != nil && delErr != sql.ErrNoRows {
				return delErr
			}
		}
	}
	return nil
}

func SaveDriverOperation(namespace, podName, driverType string, action types.ActionEntry) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// Reconcile qdisc operations so the history reflects the current desired
	// state of each interface rather than an append-only log. A new qdisc op
	// supersedes the previous one for the same interface; del_qdisc removes the
	// prior add_qdisc and persists nothing (no add_qdisc == no shaping on replay).
	if action.Type == "add_qdisc" || action.Type == "del_qdisc" {
		if err := deleteQdiscHistoryForIface(namespace, podName, action.Iface); err != nil {
			return fmt.Errorf("error reconciling qdisc history for %s/%s iface %q: %w", namespace, podName, action.Iface, err)
		}
		if action.Type == "del_qdisc" {
			return nil
		}
	}

	// An address action supersedes its inverse on the same address, so the
	// history keeps the last word on it and a later identical action is not
	// taken for a duplicate (set, remove, set again used to skip the third).
	if inverse := addressInverse(action); len(inverse) > 0 {
		if err := deleteAddressOps(namespace, podName, inverse, action.Iface, action.CIDR); err != nil {
			return fmt.Errorf("error reconciling address history for %s/%s: %w", namespace, podName, err)
		}
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}

	actionJSON, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("error serializing action %q: %w", action.Type, err)
	}

	// Re-applying an action replaces its earlier row instead of stacking a
	// duplicate, so executed_at always says when this payload was last applied
	// and replay runs it once, at its latest position.
	if _, err := DB.Exec(
		`DELETE FROM driver_operation_history WHERE cluster_id = ? AND namespace = ? AND pod_name = ? AND driver_type = ? AND action_type = ? AND action_json = ?`,
		clusterID, namespace, podName, driverType, action.Type, string(actionJSON),
	); err != nil {
		return fmt.Errorf("error replacing earlier driver operation history row: %w", err)
	}
	_, err = DB.Exec(
		`INSERT INTO driver_operation_history (cluster_id, namespace, pod_name, driver_type, action_type, action_json, executed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		clusterID,
		namespace,
		podName,
		driverType,
		action.Type,
		string(actionJSON),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("error saving driver operation history: %w", err)
	}

	return nil
}

// addressInverse names the actions a new address action undoes: a remove
// drops the earlier set or replace of the same address and the other way
// round.
func addressInverse(action types.ActionEntry) []string {
	switch action.Type {
	case "remove_ip":
		return []string{"set_ip", "replace_ip"}
	case "set_ip", "replace_ip":
		return []string{"remove_ip"}
	}
	return nil
}

func deleteAddressOps(namespace, podName string, actionTypes []string, iface, cidr string) error {
	ops, err := ListDriverOperationsForPod(namespace, podName)
	if err != nil {
		return err
	}
	for _, op := range ops {
		t := op.Action.Type
		if t == "" {
			t = op.ActionType
		}
		if !slices.Contains(actionTypes, t) || op.Action.Iface != iface || op.Action.CIDR != cidr {
			continue
		}
		if err := DeleteDriverOperationHistoryByID(op.ID); err != nil {
			return err
		}
	}
	return nil
}

// parseExecutedAt reads the timestamp column. Go writes RFC3339Nano, rows from
// the earliest releases may carry SQLite's CURRENT_TIMESTAMP layout.
func parseExecutedAt(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// DriverOperationAppliedSince reports whether the same operation is in the
// history with a timestamp at or after since. Configure passes the target
// pod's creation time: an entry older than the pod was applied to a previous
// incarnation, or to an earlier namespace of the same name, and says nothing
// about the pod that exists now. Rows whose timestamp cannot be parsed count
// as applied, the conservative reading.
func DriverOperationAppliedSince(namespace, podName, driverType string, action types.ActionEntry, since time.Time) (bool, error) {
	if DB == nil {
		return false, fmt.Errorf("database not initialized")
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return false, err
	}
	actionJSON, err := json.Marshal(action)
	if err != nil {
		return false, fmt.Errorf("error serializing action %q: %w", action.Type, err)
	}
	rows, err := DB.Query(
		`SELECT executed_at FROM driver_operation_history
		 WHERE cluster_id = ? AND namespace = ? AND pod_name = ? AND driver_type = ? AND action_type = ? AND action_json = ?`,
		clusterID, namespace, podName, driverType, action.Type, string(actionJSON))
	if err != nil {
		return false, fmt.Errorf("error checking driver operation history for pod %s/%s: %w", namespace, podName, err)
	}
	defer rows.Close()
	var applied []time.Time
	for rows.Next() {
		var executedAt string
		if err := rows.Scan(&executedAt); err != nil {
			return false, err
		}
		t, ok := parseExecutedAt(executedAt)
		if !ok {
			t = time.Time{}
		}
		if !ok || !t.Before(since) {
			applied = append(applied, t)
		}
	}
	if err := rows.Err(); err != nil || len(applied) == 0 {
		return false, err
	}
	// A replay re-stamps every row, so set, remove and set again would find
	// the first set "applied" forever: an address action undone later by its
	// inverse does not count.
	inverse := addressInverse(action)
	if len(inverse) == 0 {
		return true, nil
	}
	ops, err := ListDriverOperationsForPod(namespace, podName)
	if err != nil {
		return false, err
	}
	for _, t := range applied {
		if !undoneAfter(ops, inverse, action.Iface, action.CIDR, t) {
			return true, nil
		}
	}
	return false, nil
}

// undoneAfter says whether an inverse action on the same address ran after
// appliedAt.
func undoneAfter(ops []PersistedDriverOperation, inverse []string, iface, cidr string, appliedAt time.Time) bool {
	for _, op := range ops {
		t := op.Action.Type
		if t == "" {
			t = op.ActionType
		}
		if !slices.Contains(inverse, t) || op.Action.Iface != iface || op.Action.CIDR != cidr {
			continue
		}
		if at, ok := parseExecutedAt(op.ExecutedAt); ok && at.After(appliedAt) {
			return true
		}
	}
	return false
}

// PurgeStaleDriverOperations drops every operation of the namespace recorded
// before the Namespace object itself was created. Such rows belong to an
// earlier namespace of the same name that was deleted behind KubeNDT's back,
// and left alone they make configure skip actions as "already applied" and
// replay push a dead topology's configuration onto new pods. Returns how many
// rows went.
func PurgeStaleDriverOperations(namespace string) (int, error) {
	ns, err := kubeclient.Clientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("could not read namespace %s: %w", namespace, err)
	}
	return PurgeDriverOperationsBefore(namespace, ns.CreationTimestamp.Time)
}

// PurgeDriverOperationsBefore removes the namespace's operations older than t.
func PurgeDriverOperationsBefore(namespace string, t time.Time) (int, error) {
	if DB == nil {
		return 0, fmt.Errorf("database not initialized")
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return 0, err
	}
	rows, err := DB.Query(`SELECT id, executed_at FROM driver_operation_history WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace)
	if err != nil {
		return 0, fmt.Errorf("error listing driver operation history for %s: %w", namespace, err)
	}
	stale := make([]int64, 0)
	for rows.Next() {
		var id int64
		var executedAt string
		if err := rows.Scan(&id, &executedAt); err != nil {
			rows.Close()
			return 0, err
		}
		if at, ok := parseExecutedAt(executedAt); ok && at.Before(t) {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if err := DeleteDriverOperationHistoryByID(id); err != nil {
			return 0, err
		}
	}
	return len(stale), nil
}

// TouchDriverOperation stamps the operation as applied now, used after a
// successful replay so configure sees it as applied to the current pod.
func TouchDriverOperation(id int64) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`UPDATE driver_operation_history SET executed_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func ListDriverOperationsForPod(namespace, podName string) ([]PersistedDriverOperation, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return nil, err
	}

	rows, err := DB.Query(
		`SELECT id, namespace, pod_name, driver_type, action_type, action_json, executed_at
		 FROM driver_operation_history
		 WHERE cluster_id = ? AND namespace = ? AND pod_name = ?
		 ORDER BY id ASC`,
		clusterID,
		namespace,
		podName,
	)
	if err != nil {
		return nil, fmt.Errorf("error listing driver operation history: %w", err)
	}
	defer rows.Close()

	operations := make([]PersistedDriverOperation, 0)
	for rows.Next() {
		var (
			op         PersistedDriverOperation
			actionJSON string
		)
		if err := rows.Scan(&op.ID, &op.Namespace, &op.PodName, &op.DriverType, &op.ActionType, &actionJSON, &op.ExecutedAt); err != nil {
			return nil, fmt.Errorf("error scanning driver operation history row: %w", err)
		}
		if err := json.Unmarshal([]byte(actionJSON), &op.Action); err != nil {
			return nil, fmt.Errorf("error deserializing action for operation id=%d: %w", op.ID, err)
		}
		operations = append(operations, op)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating driver operation history rows: %w", err)
	}

	return operations, nil
}

func ListDriverOperationsForNamespace(namespace string) ([]PersistedDriverOperation, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return nil, err
	}

	rows, err := DB.Query(
		`SELECT id, namespace, pod_name, driver_type, action_type, action_json, executed_at
		 FROM driver_operation_history
		 WHERE cluster_id = ? AND namespace = ?
		 ORDER BY id ASC`,
		clusterID,
		namespace,
	)
	if err != nil {
		return nil, fmt.Errorf("error listing driver operation history by namespace: %w", err)
	}
	defer rows.Close()

	operations := make([]PersistedDriverOperation, 0)
	for rows.Next() {
		var (
			op         PersistedDriverOperation
			actionJSON string
		)
		if err := rows.Scan(&op.ID, &op.Namespace, &op.PodName, &op.DriverType, &op.ActionType, &actionJSON, &op.ExecutedAt); err != nil {
			return nil, fmt.Errorf("error scanning driver operation history row: %w", err)
		}
		if err := json.Unmarshal([]byte(actionJSON), &op.Action); err != nil {
			return nil, fmt.Errorf("error deserializing action for operation id=%d: %w", op.ID, err)
		}
		operations = append(operations, op)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating driver operation history rows: %w", err)
	}

	return operations, nil
}

// TransientReplayError wraps an exec error that is considered transient
// (pod executor not yet ready, e.g. SSH in a QEMU pod still booting).
// Persisted operations are kept in the database so they can be retried.
type TransientReplayError struct {
	Namespace string
	PodName   string
	Err       error
}

func (e *TransientReplayError) Error() string {
	return fmt.Sprintf("transient exec error for %s/%s: %v", e.Namespace, e.PodName, e.Err)
}

func (e *TransientReplayError) Unwrap() error { return e.Err }

// isTransientExecError reports whether err is a temporary connectivity failure
// (executor / SSH not yet available) rather than a permanent config error.
func isTransientExecError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "exit status 255") ||
		strings.Contains(s, "Connection timed out") ||
		strings.Contains(s, "banner exchange") ||
		strings.Contains(s, "Connection refused")
}

func ReplayDriverOperationsForPod(namespace, podName string) (int, error) {
	stats, err := ReplayDriverOperationsForPodWithStats(namespace, podName)
	return stats.Replayed, err
}

// replayExecAttempts is how many times a failing replay command is tried
// before its operation is treated as permanently broken and pruned. Right
// after a restart the pod is Ready but its daemons may still be coming up
// (ovsdb, the VyOS HTTP API answering 502), and pruning history on that race
// destroys the very state replay exists to restore.
const replayExecAttempts = 4

// execWithReplayRetry runs fn until it succeeds, gives up after
// replayExecAttempts with a growing pause in between, and returns transient
// transport errors immediately so the caller can abort and keep the history.
func execWithReplayRetry(fn func() error) error {
	var err error
	for attempt := 1; attempt <= replayExecAttempts; attempt++ {
		if err = fn(); err == nil || isTransientExecError(err) {
			return err
		}
		if attempt < replayExecAttempts {
			time.Sleep(time.Duration(2*attempt) * time.Second)
		}
	}
	return err
}

func ReplayDriverOperationsForPodWithStats(namespace, podName string) (DriverReplayStats, error) {
	ops, err := ListDriverOperationsForPod(namespace, podName)
	if err != nil {
		return DriverReplayStats{}, err
	}
	if len(ops) == 0 {
		return DriverReplayStats{}, nil
	}

	driver, err := GetDriverForPod(namespace, podName)
	if err != nil {
		return DriverReplayStats{}, fmt.Errorf("cannot resolve driver for replay in %s/%s: %w", namespace, podName, err)
	}

	// ── RESOLVE PHASE ────────────────────────────────────────────────────────
	// Non-persist ops and ops that no longer resolve are pruned here, the
	// rest are planned in order.
	plans := make([]PlannedAction, 0, len(ops))
	planOps := make([]PersistedDriverOperation, 0, len(ops))
	pruned := 0
	errs := make([]string, 0)
	prune := func(op PersistedDriverOperation, why string, cause error) {
		if delErr := DeleteDriverOperationHistoryByID(op.ID); delErr != nil {
			errs = append(errs, fmt.Sprintf("op id=%d %s and could not delete entry: %v", op.ID, why, delErr))
			return
		}
		pruned++
		log.Printf("🧹 Removed persisted operation id=%d for %s/%s after %s: %v", op.ID, namespace, podName, why, cause)
	}

	for _, op := range ops {
		action := op.Action
		if action.Type == "" {
			action.Type = op.ActionType
		}
		if !ResolveActionFlags(action).Persist {
			if delErr := DeleteDriverOperationHistoryByID(op.ID); delErr != nil {
				errs = append(errs, fmt.Sprintf("non-persist op id=%d should not be persisted and could not be deleted: %v", op.ID, delErr))
			} else {
				pruned++
				log.Printf("🧹 Removed persisted non-persist operation id=%d for %s/%s", op.ID, namespace, podName)
			}
			continue
		}
		p := PlanActions(namespace, podName, driver, nil, []types.ActionEntry{action})[0]
		if p.Err != nil {
			prune(op, "resolve error", p.Err)
			continue
		}
		plans = append(plans, p)
		planOps = append(planOps, op)
	}

	// ── EXECUTE PHASE ────────────────────────────────────────────────────────
	// Consecutive ops on one guest commit go as a single batch, like the first
	// apply. A transient transport error aborts the replay and keeps the ops,
	// a permanent error prunes the op (every op of a failed batch).
	for i := range plans {
		log.Printf("🔁 Replaying persisted operation id=%d type=%s for pod %s/%s", planOps[i].ID, plans[i].Action.Type, namespace, podName)
	}
	outcomes, abortErr := RunPlannedActions(namespace, podName, plans, RunOptions{Exec: execWithReplayRetry, Abort: isTransientExecError})
	replayed := 0
	for i, o := range outcomes {
		if !o.Ran {
			continue
		}
		if o.Err != nil {
			prune(planOps[i], "replay error", o.Err)
			continue
		}
		replayed++
		if err := TouchDriverOperation(planOps[i].ID); err != nil {
			log.Printf("⚠️ Could not refresh executed_at of op id=%d: %v", planOps[i].ID, err)
		}
		log.Printf("✅ Replayed persisted operation id=%d for pod %s/%s", planOps[i].ID, namespace, podName)
	}
	if abortErr != nil {
		log.Printf("⏳ Transient executor error replaying %s/%s, aborting replay (ops preserved): %v", namespace, podName, abortErr)
		return DriverReplayStats{Total: len(ops), Replayed: replayed, Pruned: pruned},
			&TransientReplayError{Namespace: namespace, PodName: podName, Err: abortErr}
	}

	if len(errs) > 0 {
		return DriverReplayStats{Total: len(ops), Replayed: replayed, Pruned: pruned}, fmt.Errorf("errors replaying operations for %s/%s: %s", namespace, podName, strings.Join(errs, " | "))
	}

	log.Printf("♻️ Persisted operations replay summary for pod %s/%s: total=%d replayed=%d pruned=%d", namespace, podName, len(ops), replayed, pruned)
	return DriverReplayStats{Total: len(ops), Replayed: replayed, Pruned: pruned}, nil
}

func DeleteDriverOperationHistoryByID(id int64) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	res, err := DB.Exec(`DELETE FROM driver_operation_history WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("error deleting driver operation history id=%d: %w", id, err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("error checking deleted rows for driver operation history id=%d: %w", id, err)
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

func ReplayDriverOperationsForPods(namespace string, podNames []string) error {
	if len(podNames) == 0 {
		return nil
	}

	poolSize := runtime.NumCPU()
	if poolSize > len(podNames) {
		poolSize = len(podNames)
	}
	if poolSize < 1 {
		poolSize = 1
	}

	jobs := make(chan string, len(podNames))
	errChan := make(chan string, len(podNames))

	var wg sync.WaitGroup
	for i := 0; i < poolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for podName := range jobs {
				if _, err := ReplayDriverOperationsForPodWithStats(namespace, podName); err != nil {
					errChan <- err.Error()
				}
			}
		}()
	}

	for _, podName := range podNames {
		jobs <- podName
	}
	close(jobs)

	wg.Wait()
	close(errChan)

	errs := make([]string, 0)
	for errText := range errChan {
		errs = append(errs, errText)
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, " | "))
	}

	return nil
}

func DeleteNamespaceDriverOperationHistory(namespace string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}

	if _, err := DB.Exec(`DELETE FROM driver_operation_history WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace); err != nil {
		return fmt.Errorf("error deleting driver operation history for namespace %s: %w", namespace, err)
	}

	return nil
}

func DeleteDriverOperationHistoryForPod(namespace, podName string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}

	if _, err := DB.Exec(`DELETE FROM driver_operation_history WHERE cluster_id = ? AND namespace = ? AND pod_name = ?`, clusterID, namespace, podName); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("error deleting driver operation history for pod %s/%s: %w", namespace, podName, err)
	}

	return nil
}
