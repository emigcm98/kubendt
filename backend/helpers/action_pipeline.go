package helpers

import (
	"fmt"
	"log"
	"strings"

	"kubendt/actions"
	"kubendt/executor"
	"kubendt/types"
)

// PlannedAction is one action resolved for a pod: the executor it runs
// through and its commands, or the reason it cannot run. Skip marks an
// action that is reported but not run, a duplicate for instance.
type PlannedAction struct {
	Action   types.ActionEntry
	Flags    types.ActionFlags
	Desc     *actions.Descriptor // nil for an unknown action
	Executor string
	Exec     executor.CommandExecutor
	Commands [][]string
	Skip     string
	Err      error
}

// CommandStrings renders the commands the way the executor runs them, for
// reports and logs.
func (p PlannedAction) CommandStrings() []string {
	structured := executor.CommandsFromLegacyForExecutor(p.Commands, p.Executor)
	out := make([]string, 0, len(structured))
	for _, cmd := range structured {
		out = append(out, cmd.String())
	}
	return out
}

// PlanActions resolves every action of a pod. A planning failure is recorded
// on its item and never stops the rest. driverErr says why the pod has no
// driver, only universal actions run without one.
func PlanActions(namespace, podName string, driver any, driverErr error, acts []types.ActionEntry) []PlannedAction {
	out := make([]PlannedAction, 0, len(acts))
	for _, a := range acts {
		p := PlannedAction{Action: a, Flags: ResolveActionFlags(a)}
		p.Desc, _ = actions.Lookup(a.Type)
		if driverErr != nil && (p.Desc == nil || !p.Desc.Universal) {
			p.Err = fmt.Errorf("no driver for pod '%s': %v", podName, driverErr)
			out = append(out, p)
			continue
		}
		execName, commands, err := ResolveDriverExecutionPlanForPod(namespace, podName, driver, a)
		if err == nil && commands == nil {
			err = fmt.Errorf("action %q produced no commands", a.Type)
		}
		if err == nil {
			p.Exec, err = executor.Get(execName)
		}
		p.Executor = execName
		p.Commands = commands
		p.Err = err
		out = append(out, p)
	}
	return out
}

// ActionOutcome is what running one planned action produced. Ran is false
// for an action that was skipped, failed to plan, or was left untouched
// after an abort.
type ActionOutcome struct {
	Ran    bool
	Output string
	Err    error
}

// RunOptions tunes the execute phase for its caller.
type RunOptions struct {
	// Exec wraps every command execution, for retries. nil runs it once.
	Exec func(run func() error) error
	// Abort says which execution error stops the run. The action it hit is
	// reported as not run with that error, the ones after it are left alone,
	// and the error comes back from RunPlannedActions.
	Abort func(error) bool
}

// RunPlannedActions executes planned actions in order and returns one
// outcome per action. Consecutive actions that share a batchable executor
// (a guest commit) and capture no output go as one command, so a run of
// VyOS actions is one commit. An action stops at its first failing command.
func RunPlannedActions(namespace, podName string, plans []PlannedAction, opts RunOptions) ([]ActionOutcome, error) {
	exec := opts.Exec
	if exec == nil {
		exec = func(run func() error) error { return run() }
	}
	outcomes := make([]ActionOutcome, len(plans))
	for i := range plans {
		outcomes[i].Err = plans[i].Err
	}

	i := 0
	for i < len(plans) {
		p := plans[i]
		if p.Skip != "" || p.Err != nil {
			i++
			continue
		}

		if executor.BatchableExecutors[p.Executor] && !p.Flags.Get {
			j := i
			var allArgs []string
			for j < len(plans) {
				gp := plans[j]
				if gp.Skip != "" || gp.Err != nil || gp.Executor != p.Executor || gp.Flags.Get {
					break
				}
				for _, row := range gp.Commands {
					allArgs = append(allArgs, row...)
				}
				j++
			}
			var output string
			execErr := exec(func() error {
				var err error
				output, err = p.Exec.ExecCommandAndGet(podName, namespace, executor.NewArgsCommand(allArgs))
				return err
			})
			if output != "" {
				log.Printf("ℹ️ Batch exec output [%s] pod=%s:\n%s", p.Executor, podName, output)
			}
			if execErr != nil && opts.Abort != nil && opts.Abort(execErr) {
				outcomes[i].Err = execErr
				return outcomes, execErr
			}
			for k := i; k < j; k++ {
				outcomes[k] = ActionOutcome{Ran: true, Err: execErr}
				if execErr != nil {
					log.Printf("❌ %s on %s/%s: %v", plans[k].Action.Type, namespace, podName, execErr)
				}
			}
			i = j
			continue
		}

		var outputs []string
		var actionErr error
		for _, cmd := range executor.CommandsFromLegacyForExecutor(p.Commands, p.Executor) {
			var output string
			execErr := exec(func() error {
				var err error
				if p.Flags.Get {
					output, err = p.Exec.ExecCommandAndGet(podName, namespace, cmd)
				} else {
					err = p.Exec.ExecCommand(podName, namespace, cmd)
				}
				return err
			})
			if execErr != nil {
				if opts.Abort != nil && opts.Abort(execErr) {
					outcomes[i].Err = execErr
					return outcomes, execErr
				}
				log.Printf("❌ %s on %s/%s: %v", p.Action.Type, namespace, podName, execErr)
				actionErr = execErr
				break
			}
			if out := strings.TrimSpace(output); out != "" {
				outputs = append(outputs, out)
			}
		}
		outcomes[i] = ActionOutcome{Ran: true, Output: strings.Join(outputs, "\n"), Err: actionErr}
		i++
	}
	return outcomes, nil
}
