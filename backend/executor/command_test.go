package executor

import (
	"reflect"
	"testing"
)

// The rows of one tc action become a single batch, shown back as the tc
// commands they stand for.
func TestTCRowsBecomeOneBatch(t *testing.T) {
	rows := [][]string{
		{"tc", "qdisc", "replace", "dev", "eth1", "root", "noqueue"},
		{"tc", "qdisc", "replace", "dev", "eth1", "root", "netem", "delay", "10ms"},
	}
	cmds := CommandsFromLegacyForExecutor(rows, TCUniversalExecutorName)
	if len(cmds) != 1 || cmds[0].Kind != CommandKindBatch {
		t.Fatalf("expected one batch command, got %+v", cmds)
	}
	want := []string{"qdisc replace dev eth1 root noqueue", "qdisc replace dev eth1 root netem delay 10ms"}
	if !reflect.DeepEqual(cmds[0].Lines, want) {
		t.Errorf("lines = %v, want %v", cmds[0].Lines, want)
	}
	if got := cmds[0].String(); got != "tc qdisc replace dev eth1 root noqueue && tc qdisc replace dev eth1 root netem delay 10ms" {
		t.Errorf("display = %q", got)
	}
	if err := cmds[0].Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
	if err := NewBatchCommand("tc", nil).Validate(); err == nil {
		t.Errorf("an empty batch must not validate")
	}
	// Other executors keep one command per row.
	if got := CommandsFromLegacyForExecutor(rows, DefaultExecutorName); len(got) != 2 || got[0].Kind != CommandKindArgs {
		t.Errorf("kubectl rows = %+v", got)
	}
}
