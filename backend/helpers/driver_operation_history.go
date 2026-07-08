package helpers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"kubendt/executor"
	"kubendt/kubeclient"
	"kubendt/types"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"
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

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}

	actionJSON, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("error serializing action %q: %w", action.Type, err)
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

func DriverOperationExists(namespace, podName, driverType string, action types.ActionEntry) (bool, error) {
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

	var exists int
	err = DB.QueryRow(
		`SELECT 1
		 FROM driver_operation_history
		 WHERE cluster_id = ?
		   AND namespace = ?
		   AND pod_name = ?
		   AND driver_type = ?
		   AND action_type = ?
		   AND action_json = ?
		 LIMIT 1`,
		clusterID,
		namespace,
		podName,
		driverType,
		action.Type,
		string(actionJSON),
	).Scan(&exists)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("error checking driver operation history duplicate for pod %s/%s: %w", namespace, podName, err)
	}

	return true, nil
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

	driverExecutor, driverExecutorName, err := executor.ResolveForDriver(driver)
	if err != nil {
		return DriverReplayStats{}, fmt.Errorf("cannot resolve executor for replay in %s/%s: %w", namespace, podName, err)
	}

	// ── RESOLVE PHASE ────────────────────────────────────────────────────────
	// Collect resolved items. Non-persist and error ops are pruned here.
	type replayItem struct {
		op       PersistedDriverOperation
		execName string
		execInst executor.CommandExecutor
		commands [][]string
	}
	items := make([]replayItem, 0, len(ops))
	pruned := 0
	errs := make([]string, 0)

	for _, op := range ops {
		actionType := op.ActionType
		if actionType == "" {
			actionType = op.Action.Type
		}
		action := op.Action
		if action.Type == "" {
			action.Type = actionType
		}

		flags := ResolveActionFlags(action)
		if !flags.Persist {
			if delErr := DeleteDriverOperationHistoryByID(op.ID); delErr != nil {
				errs = append(errs, fmt.Sprintf("non-persist op id=%d should not be persisted and could not be deleted: %v", op.ID, delErr))
			} else {
				pruned++
				log.Printf("🧹 Removed persisted non-persist operation id=%d for %s/%s", op.ID, namespace, podName)
			}
			continue
		}

		actionExecutorName, commands, resolveErr := ResolveDriverExecutionPlanForPod(namespace, podName, driver, action)
		if resolveErr != nil {
			if delErr := DeleteDriverOperationHistoryByID(op.ID); delErr != nil {
				errs = append(errs, fmt.Sprintf("op id=%d resolve failure and could not delete entry: %v", op.ID, delErr))
			} else {
				pruned++
				log.Printf("🧹 Removed persisted operation id=%d for %s/%s after resolve error: %v", op.ID, namespace, podName, resolveErr)
			}
			continue
		}
		if commands == nil {
			if delErr := DeleteDriverOperationHistoryByID(op.ID); delErr != nil {
				errs = append(errs, fmt.Sprintf("op id=%d unsupported by current driver and failed to delete stale entry: %v", op.ID, delErr))
			} else {
				pruned++
				log.Printf("🧹 Removed stale persisted operation id=%d for %s/%s (unsupported by current driver)", op.ID, namespace, podName)
			}
			continue
		}

		execInst := driverExecutor
		execModeName := driverExecutorName
		if strings.TrimSpace(actionExecutorName) != "" && actionExecutorName != driverExecutorName {
			execOverride, execErr := executor.Get(actionExecutorName)
			if execErr != nil {
				if delErr := DeleteDriverOperationHistoryByID(op.ID); delErr != nil {
					errs = append(errs, fmt.Sprintf("op id=%d executor resolve failure and could not delete entry: %v", op.ID, delErr))
				} else {
					pruned++
					log.Printf("🧹 Removed persisted operation id=%d for %s/%s after executor resolve error: %v", op.ID, namespace, podName, execErr)
				}
				continue
			}
			execInst = execOverride
			execModeName = actionExecutorName
		}

		items = append(items, replayItem{
			op:       op,
			execName: execModeName,
			execInst: execInst,
			commands: commands,
		})
	}

	// ── EXECUTE PHASE (with batching) ────────────────────────────────────────
	// Consecutive items sharing the same batchable executor are merged into a
	// single configure→commit→exit round trip, matching the initial apply logic.
	replayed := 0
	i := 0
	for i < len(items) {
		item := items[i]

		// Collect a batchable run.
		if executor.BatchableExecutors[item.execName] {
			j := i
			group := make([]replayItem, 0)
			for j < len(items) && items[j].execName == item.execName {
				group = append(group, items[j])
				j++
			}

			// Log each op in the batch.
			for _, g := range group {
				log.Printf("🔁 Replaying persisted operation id=%d type=%s for pod %s/%s", g.op.ID, g.op.ActionType, namespace, podName)
			}

			// Flatten all command arg groups into one batch command.
			var allArgs []string
			for _, g := range group {
				for _, cmdGroup := range g.commands {
					allArgs = append(allArgs, cmdGroup...)
				}
			}
			batchCmd := executor.NewArgsCommand(allArgs)
			_, execErr := item.execInst.ExecCommandAndGet(podName, namespace, batchCmd)

			if execErr != nil {
				if isTransientExecError(execErr) {
					log.Printf("⏳ Transient executor error replaying batch for %s/%s, aborting replay (ops preserved): %v",
						namespace, podName, execErr)
					return DriverReplayStats{Total: len(ops), Replayed: replayed, Pruned: pruned},
						&TransientReplayError{Namespace: namespace, PodName: podName, Err: execErr}
				}
				// Permanent error, prune all ops in the batch.
				for _, g := range group {
					if delErr := DeleteDriverOperationHistoryByID(g.op.ID); delErr != nil {
						errs = append(errs, fmt.Sprintf("op id=%d batch failure and could not delete entry: %v", g.op.ID, delErr))
					} else {
						pruned++
						log.Printf("🧹 Removed persisted operation id=%d for %s/%s after batch replay error: %v", g.op.ID, namespace, podName, execErr)
					}
				}
			} else {
				for _, g := range group {
					replayed++
					log.Printf("✅ Replayed persisted operation id=%d for pod %s/%s", g.op.ID, namespace, podName)
				}
			}

			i = j
			continue
		}

		// Non-batchable: execute individually.
		log.Printf("🔁 Replaying persisted operation id=%d type=%s for pod %s/%s", item.op.ID, item.op.ActionType, namespace, podName)
		pruneStale := false
		for _, cmd := range executor.CommandsFromLegacyForExecutor(item.commands, item.execName) {
			if execErr := item.execInst.ExecCommand(podName, namespace, cmd); execErr != nil {
				if isTransientExecError(execErr) {
					log.Printf("⏳ Transient executor error replaying op id=%d for %s/%s, aborting replay (ops preserved): %v",
						item.op.ID, namespace, podName, execErr)
					return DriverReplayStats{Total: len(ops), Replayed: replayed, Pruned: pruned},
						&TransientReplayError{Namespace: namespace, PodName: podName, Err: execErr}
				}
				if delErr := DeleteDriverOperationHistoryByID(item.op.ID); delErr != nil {
					errs = append(errs, fmt.Sprintf("op id=%d replay failure and could not delete entry: %v", item.op.ID, delErr))
				} else {
					pruned++
					log.Printf("🧹 Removed persisted operation id=%d for %s/%s after replay error: %v", item.op.ID, namespace, podName, execErr)
				}
				pruneStale = true
				break
			}
		}
		if !pruneStale {
			replayed++
			log.Printf("✅ Replayed persisted operation id=%d for pod %s/%s", item.op.ID, namespace, podName)
		}
		i++
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
