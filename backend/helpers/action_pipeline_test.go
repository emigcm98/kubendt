package helpers_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"kubendt/executor"
	"kubendt/helpers"
	"kubendt/types"
)

// recordingExecutor stands in for a pod: it records what it is asked to run
// and fails on the commands told to.
type recordingExecutor struct {
	name string
	mu   sync.Mutex
	ran  []string
	fail map[string]error
}

func (e *recordingExecutor) Name() string { return e.name }

func (e *recordingExecutor) ExecCommand(podName, namespace string, command executor.Command) error {
	_, err := e.ExecCommandAndGet(podName, namespace, command)
	return err
}

func (e *recordingExecutor) ExecCommandAndGet(podName, namespace string, command executor.Command) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	line := command.String()
	e.ran = append(e.ran, line)
	for needle, err := range e.fail {
		if strings.Contains(line, needle) {
			return "", err
		}
	}
	return "ran: " + line, nil
}

func (e *recordingExecutor) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ran = nil
	e.fail = map[string]error{}
}

var (
	plainExec = &recordingExecutor{name: "test_plain", fail: map[string]error{}}
	batchExec = &recordingExecutor{name: "test_batch", fail: map[string]error{}}
)

func init() {
	executor.MustRegister(plainExec)
	executor.MustRegister(batchExec)
	executor.BatchableExecutors[batchExec.name] = true
}

// custom actions route to the test executors by name, so the pipeline runs
// end to end without a cluster.
func customOn(execName, command string, capture bool) types.ActionEntry {
	a := types.ActionEntry{Type: "custom", Command: json.RawMessage(fmt.Sprintf("%q", command)), Executor: execName}
	if capture {
		a.Options = &types.ActionOptions{CaptureOutput: &capture}
	}
	return a
}

func TestPlanActions(t *testing.T) {
	driver := loadDriver(t, "FRRRouterDriver")
	plans := helpers.PlanActions("ns", "pod", driver, nil, []types.ActionEntry{
		{Type: "set_ip", Iface: "eth1", CIDR: "10.0.0.1/24"},
		{Type: "set_ip", Iface: "eth1"},
		{Type: "setup_bridge", Bridge: "br0"},
		{Type: "nope"},
	})
	if plans[0].Err != nil || plans[0].Executor != "kubectl" || plans[0].Exec == nil || plans[0].Desc == nil {
		t.Errorf("valid action: %+v", plans[0])
	}
	if got := strings.Join(plans[0].CommandStrings(), ";"); got != "ip addr replace 10.0.0.1/24 dev eth1" {
		t.Errorf("command strings: %q", got)
	}
	if plans[1].Err == nil || !strings.Contains(plans[1].Err.Error(), `"cidr"`) {
		t.Errorf("invalid action should carry its error: %v", plans[1].Err)
	}
	if plans[2].Err == nil || !strings.Contains(plans[2].Err.Error(), "does not support Switch") {
		t.Errorf("unsupported action: %v", plans[2].Err)
	}
	if plans[3].Err == nil || plans[3].Desc != nil {
		t.Errorf("unknown action: %+v", plans[3])
	}

	// Without a driver only universal actions plan.
	noDriver := helpers.PlanActions("ns", "pod", nil, fmt.Errorf("no label"), []types.ActionEntry{
		{Type: "set_ip", Iface: "eth1", CIDR: "10.0.0.1/24"},
		customOn("kubectl", "echo hi", false),
	})
	if noDriver[0].Err == nil || !strings.Contains(noDriver[0].Err.Error(), "no driver for pod") {
		t.Errorf("driver action without driver: %v", noDriver[0].Err)
	}
	if noDriver[1].Err != nil {
		t.Errorf("universal action without driver: %v", noDriver[1].Err)
	}
}

func TestRunPlannedActionsBatchesAndCaptures(t *testing.T) {
	plainExec.reset()
	batchExec.reset()
	plans := helpers.PlanActions("ns", "pod", nil, nil, []types.ActionEntry{
		customOn("test_batch", "set a", false),
		customOn("test_batch", "set b", false),
		customOn("test_plain", "echo one", true),
		customOn("test_batch", "set c", true),
		customOn("test_batch", "set d", false),
		{Type: "nope"},
		customOn("test_plain", "echo two", false),
	})
	plans[6].Skip = "not today"

	outcomes, abortErr := helpers.RunPlannedActions("ns", "pod", plans, helpers.RunOptions{})
	if abortErr != nil {
		t.Fatalf("unexpected abort: %v", abortErr)
	}
	// a and b go as one batch, c captures output so it runs alone, d alone again.
	if got := strings.Join(batchExec.ran, " | "); got != "sh -c set a sh -c set b | sh -c set c | sh -c set d" {
		t.Errorf("batch executor ran: %q", got)
	}
	if got := strings.Join(plainExec.ran, " | "); got != "sh -c echo one" {
		t.Errorf("plain executor ran: %q", got)
	}
	for i := 0; i < 5; i++ {
		if !outcomes[i].Ran || outcomes[i].Err != nil {
			t.Errorf("action %d: %+v", i, outcomes[i])
		}
	}
	if outcomes[2].Output != "ran: sh -c echo one" || outcomes[3].Output != "ran: sh -c set c" || outcomes[0].Output != "" {
		t.Errorf("captured outputs: %q %q %q", outcomes[0].Output, outcomes[2].Output, outcomes[3].Output)
	}
	if outcomes[5].Ran || outcomes[5].Err == nil {
		t.Errorf("unknown action must not run: %+v", outcomes[5])
	}
	if outcomes[6].Ran || outcomes[6].Err != nil {
		t.Errorf("skipped action must not run: %+v", outcomes[6])
	}
}

func TestRunPlannedActionsFailures(t *testing.T) {
	plainExec.reset()
	batchExec.reset()
	batchExec.fail["set b"] = fmt.Errorf("commit failed")
	plainExec.fail["echo bad"] = fmt.Errorf("exit status 1")
	plans := helpers.PlanActions("ns", "pod", nil, nil, []types.ActionEntry{
		customOn("test_batch", "set a", false),
		customOn("test_batch", "set b", false),
		customOn("test_plain", "echo bad", false),
		customOn("test_plain", "echo fine", false),
	})
	outcomes, abortErr := helpers.RunPlannedActions("ns", "pod", plans, helpers.RunOptions{})
	if abortErr != nil {
		t.Fatalf("unexpected abort: %v", abortErr)
	}
	// Every action of a failed batch fails, the ones after it still run.
	for i := 0; i < 2; i++ {
		if !outcomes[i].Ran || outcomes[i].Err == nil || !strings.Contains(outcomes[i].Err.Error(), "commit failed") {
			t.Errorf("batched action %d: %+v", i, outcomes[i])
		}
	}
	if !outcomes[2].Ran || outcomes[2].Err == nil {
		t.Errorf("failing action: %+v", outcomes[2])
	}
	if !outcomes[3].Ran || outcomes[3].Err != nil {
		t.Errorf("action after a failure: %+v", outcomes[3])
	}
}

func TestRunPlannedActionsRetryAndAbort(t *testing.T) {
	plainExec.reset()
	calls := 0
	plans := helpers.PlanActions("ns", "pod", nil, nil, []types.ActionEntry{
		customOn("test_plain", "echo first", false),
		customOn("test_plain", "echo flaky", false),
		customOn("test_plain", "echo never", false),
	})
	plainExec.fail["echo flaky"] = fmt.Errorf("Connection refused")
	retry := func(run func() error) error {
		calls++
		return run()
	}
	outcomes, abortErr := helpers.RunPlannedActions("ns", "pod", plans, helpers.RunOptions{
		Exec:  retry,
		Abort: func(err error) bool { return strings.Contains(err.Error(), "Connection refused") },
	})
	if abortErr == nil || calls != 2 {
		t.Fatalf("expected an abort on the second command, got %v after %d calls", abortErr, calls)
	}
	if !outcomes[0].Ran || outcomes[0].Err != nil {
		t.Errorf("first action: %+v", outcomes[0])
	}
	if outcomes[1].Ran || outcomes[1].Err == nil {
		t.Errorf("the aborting action is reported as not run, with the error: %+v", outcomes[1])
	}
	if outcomes[2].Ran || outcomes[2].Err != nil {
		t.Errorf("actions after the abort are left alone: %+v", outcomes[2])
	}
	if got := strings.Join(plainExec.ran, " | "); got != "sh -c echo first | sh -c echo flaky" {
		t.Errorf("ran: %q", got)
	}
}
