package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"kubendt/actions"
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

// The history keeps the last word on each piece of state an action declares
// (actions.Action.State): saving an action drops the earlier rows on the same
// state and key, so a set, its undo and a set again never stack, and an undo
// nothing would reverse is not kept at all.

// stateOf is the state an operation writes or clears and its key, "" for an
// action independent of the others or unknown to this build.
func stateOf(action types.ActionEntry) (state, key string, clears, undoPersists bool) {
	d, ok := actions.Lookup(action.Type)
	if !ok || d.State == "" {
		return "", "", false, false
	}
	return d.State, d.StateKey(action), d.Clears, d.UndoPersists
}

// opAction is the action a row holds, with its type filled in for rows from
// the earliest releases, which only carried action_type.
func opAction(op PersistedDriverOperation) types.ActionEntry {
	a := op.Action
	if a.Type == "" {
		a.Type = op.ActionType
	}
	return a
}

// rowsOnState picks the rows that write or clear the same state and key.
func rowsOnState(ops []PersistedDriverOperation, state, key string) []PersistedDriverOperation {
	var out []PersistedDriverOperation
	for _, op := range ops {
		if s, k, _, _ := stateOf(opAction(op)); s == state && k == key {
			out = append(out, op)
		}
	}
	return out
}

// clearedWith is the list of states an action takes down besides its own,
// and whether only the rows on the action's own key go.
func clearedWith(action types.ActionEntry) ([]string, bool) {
	if d, ok := actions.Lookup(action.Type); ok {
		return d.ClearsAlso, d.Key != nil
	}
	return nil, false
}

// rowsOnStates is every row on any of the states, on the given key when
// keyed and whatever its key otherwise.
func rowsOnStates(ops []PersistedDriverOperation, states []string, key string, keyed bool) []PersistedDriverOperation {
	var out []PersistedDriverOperation
	for _, op := range ops {
		s, k, _, _ := stateOf(opAction(op))
		if keyed && k != key {
			continue
		}
		for _, want := range states {
			if s == want {
				out = append(out, op)
				break
			}
		}
	}
	return out
}

// ClearsStateOf says whether running prior takes down the state action
// writes, as saving prior to the history does: its own state and key when it
// clears, and the states it clears besides, on its key when it is keyed.
// Configure asks so an action that follows its undo, or a program load, in
// the same request is not skipped as already applied.
func ClearsStateOf(prior, action types.ActionEntry) bool {
	state, key, _, _ := stateOf(action)
	if state == "" {
		return false
	}
	pState, pKey, pClears, _ := stateOf(prior)
	if pClears && pState == state && pKey == key {
		return true
	}
	states, keyed := clearedWith(prior)
	if keyed && pKey != key {
		return false
	}
	for _, s := range states {
		if s == state {
			return true
		}
	}
	return false
}

// supersededAfter says whether another operation on the same state ran after
// appliedAt with a different payload, so the applied one no longer stands.
// Histories written before the state rule could hold such pairs.
func supersededAfter(ops []PersistedDriverOperation, state, key, payload string, appliedAt time.Time) bool {
	for _, op := range rowsOnState(ops, state, key) {
		raw, err := json.Marshal(opAction(op))
		if err != nil || string(raw) == payload {
			continue
		}
		if at, ok := parseExecutedAt(op.ExecutedAt); ok && at.After(appliedAt) {
			return true
		}
	}
	return false
}

// parseExecutedAt reads a stamp. Go writes RFC3339Nano, rows from the
// earliest releases carried SQLite's CURRENT_TIMESTAMP layout.
func parseExecutedAt(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// TransientReplayError wraps an exec error that is considered transient
// (pod executor not yet ready, e.g. SSH in a QEMU pod still booting).
// The operations stay in the history so they can be retried.
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
	h, err := OpenPodHistory(namespace, podName)
	if err != nil {
		return DriverReplayStats{}, err
	}
	ops := h.Operations()
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
		h.Delete(op.ID)
		pruned++
		log.Printf("🧹 Removed persisted operation id=%d for %s/%s after %s: %v", op.ID, namespace, podName, why, cause)
	}

	for _, op := range ops {
		action := op.Action
		if action.Type == "" {
			action.Type = op.ActionType
		}
		if !ResolveActionFlags(action).Persist {
			h.Delete(op.ID)
			pruned++
			log.Printf("🧹 Removed persisted non-persist operation id=%d for %s/%s", op.ID, namespace, podName)
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
		h.Touch(planOps[i].ID)
		log.Printf("✅ Replayed persisted operation id=%d for pod %s/%s", planOps[i].ID, namespace, podName)
	}
	// The history goes back in one write whatever the outcome: the pruned
	// entries are gone and the replayed ones carry today's stamp.
	if err := h.Commit(); err != nil {
		errs = append(errs, fmt.Sprintf("could not write the history back: %v", err))
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
