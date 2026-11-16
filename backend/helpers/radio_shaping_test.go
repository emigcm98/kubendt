package helpers

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"kubendt/executor"
	"kubendt/types"
)

// TestInPlaceShapingCommands checks the single-replace command of an in-place update.
func TestInPlaceShapingCommands(t *testing.T) {
	got, err := buildShapingCommands("eth1", &types.TCParamEntry{Qdisc: "netem", Rate: "20mbit", Delay: "4ms", InPlace: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("in place wants one command, got %d: %v", len(got), got)
	}
	line := strings.Join(got[0], " ")
	if !strings.HasPrefix(line, "tc qdisc replace dev eth1 root netem ") || strings.Contains(line, "noqueue") {
		t.Fatalf("unexpected command %q", line)
	}
	for _, want := range []string{"rate 20", "corrupt 0%", "reorder 0%"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is missing %q", line, want)
		}
	}
	if strings.Contains(line, "rate 0bit") {
		t.Errorf("%q resets a rate the request sets", line)
	}

	got, err = buildShapingCommands("eth1", &types.TCParamEntry{Loss: "100%", InPlace: true})
	if err != nil {
		t.Fatal(err)
	}
	if line := strings.Join(got[0], " "); !strings.Contains(line, "rate 0bit") {
		t.Errorf("%q keeps the previous rate", line)
	}

	got, _ = buildShapingCommands("eth1", &types.TCParamEntry{Rate: "20mbit"})
	if len(got) != 2 || got[0][len(got[0])-1] != "noqueue" {
		t.Errorf("default shaping lost its reset: %v", got)
	}

	if _, err := buildShapingCommands("eth1", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", InPlace: true}); err == nil {
		t.Error("in_place on htb should be refused")
	}
}

func tcPlan(t *testing.T, iface, rate string) PlannedAction {
	t.Helper()
	cmds, err := buildShapingCommands(iface, &types.TCParamEntry{Rate: rate, InPlace: true})
	if err != nil {
		t.Fatal(err)
	}
	ex, err := executor.Get(executor.TCUniversalExecutorName)
	if err != nil {
		t.Fatal(err)
	}
	return PlannedAction{Action: types.ActionEntry{Type: "add_qdisc", Iface: iface}, Executor: executor.TCUniversalExecutorName, Exec: ex, Commands: cmds}
}

// TestTCActionsShareOneExec checks that the tc actions of a pod share one batch.
func TestTCActionsShareOneExec(t *testing.T) {
	prev := executor.RunTCBatchHook
	t.Cleanup(func() { executor.RunTCBatchHook = prev })

	var batches [][]string
	executor.RunTCBatchHook = func(namespace, podName string, lines []string) (string, string, error) {
		batches = append(batches, lines)
		return "", "", nil
	}
	plans := []PlannedAction{tcPlan(t, "eth1", "1mbit"), tcPlan(t, "eth2", "2mbit"), tcPlan(t, "eth3", "3mbit")}
	outcomes, err := RunPlannedActions("ns", "pod", plans, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || len(batches[0]) != 3 {
		t.Fatalf("want one batch of three lines, got %v", batches)
	}
	for i, o := range outcomes {
		if !o.Ran || o.Err != nil {
			t.Errorf("action %d: %+v", i, o)
		}
	}

	batches = nil
	executor.RunTCBatchHook = func(namespace, podName string, lines []string) (string, string, error) {
		batches = append(batches, lines)
		for _, l := range lines {
			if strings.Contains(l, "eth2") {
				return "", "Cannot find device \"eth2\"", errors.New("exit status 1")
			}
		}
		return "", "", nil
	}
	outcomes, err = RunPlannedActions("ns", "pod", plans, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sizes := []int{}
	for _, b := range batches {
		sizes = append(sizes, len(b))
	}
	if !reflect.DeepEqual(sizes, []int{3, 1, 1, 1}) {
		t.Fatalf("want the merged batch then one per action, got %v", sizes)
	}
	if outcomes[0].Err != nil || outcomes[1].Err == nil || outcomes[2].Err != nil {
		t.Errorf("only eth2 should fail: %+v", outcomes)
	}
}
