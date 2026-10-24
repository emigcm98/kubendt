package helpers

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"kubendt/actions"
	drivers_meta "kubendt/drivers/meta"
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
	// Container runs the kubectl commands: the pod's own when empty, its
	// toolbox when the image lacks the netns tool the action needs.
	Container    string
	Precondition *Precondition
	Skip         string
	Err          error
}

// Precondition is what the action's capability needs on the pod, checked
// right before its commands: a command that exits 0 when the need is met,
// and the message the action fails with otherwise.
type Precondition struct {
	Command []string
	Failure string
}

// preconditionRC is the exit status the guard leaves, so the batch parser
// reports the message as the error rather than an exit status.
const preconditionRC = 96

// The check runs in a subshell so its exit ends the action, not the script.
func preconditionGuard(pc *Precondition) string {
	return "( " + shellJoin(pc.Command) + " || { echo " + shellQuote(pc.Failure) + "; exit " + strconv.Itoa(preconditionRC) + "; }; )"
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
		if err == nil && execName == executor.DefaultExecutorName && p.Desc != nil {
			if pc, ok := driver.(drivers_meta.CapabilityPrecondition); ok {
				if cmd, failure, has := pc.CapabilityPrecondition(p.Desc.Capability, a); has {
					p.Precondition = &Precondition{Command: cmd, Failure: failure}
					// Outside the batch the guard is the action's first command.
					if p.Flags.Get {
						p.Commands = append([][]string{{"sh", "-c", preconditionGuard(p.Precondition)}}, p.Commands...)
					}
				}
			}
		}
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
// VyOS actions is one commit; consecutive kubectl actions go as one shell
// script, so a configure or a replay is one exec per pod. An action stops
// at its first failing command.
func RunPlannedActions(namespace, podName string, plans []PlannedAction, opts RunOptions) ([]ActionOutcome, error) {
	exec := opts.Exec
	if exec == nil {
		exec = func(run func() error) error { return run() }
	}
	outcomes := make([]ActionOutcome, len(plans))
	for i := range plans {
		outcomes[i].Err = plans[i].Err
	}
	// Where each kubectl action runs: the pod, or its toolbox when the image
	// lacks the netns tool the action needs. Decided here, at run time, so
	// planning never touches the cluster.
	for i := range plans {
		p := &plans[i]
		if p.Err != nil || p.Skip != "" || p.Executor != executor.DefaultExecutorName {
			continue
		}
		class := toolClassOfRows(p.Commands)
		if class == toolNone {
			continue
		}
		c, err := NetnsContainer(namespace, podName, class)
		if err != nil {
			p.Err = fmt.Errorf("the pod has no %s and no toolbox could be injected: %w", toolProbe(class), err)
			outcomes[i].Err = p.Err
			continue
		}
		p.Container = c
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

		if p.Executor == executor.DefaultExecutorName && !p.Flags.Get {
			j := i
			for j < len(plans) {
				gp := plans[j]
				if gp.Skip != "" || gp.Err != nil || gp.Executor != p.Executor || gp.Flags.Get || gp.Container != p.Container {
					break
				}
				j++
			}
			var output string
			execErr := exec(func() error {
				var err error
				cmd := executor.NewArgsCommand([]string{"sh", "-c", shellScript(plans[i:j])})
				cmd.Container = p.Container
				output, err = p.Exec.ExecCommandAndGet(podName, namespace, cmd)
				return err
			})
			if execErr != nil {
				if opts.Abort != nil && opts.Abort(execErr) {
					outcomes[i].Err = execErr
					return outcomes, execErr
				}
				for k := i; k < j; k++ {
					outcomes[k] = ActionOutcome{Ran: true, Err: execErr}
					log.Printf("❌ %s on %s/%s: %v", plans[k].Action.Type, namespace, podName, execErr)
				}
				i = j
				continue
			}
			results := parseShellScript(output, j-i)
			for k := i; k < j; k++ {
				r := results[k-i]
				outcomes[k] = ActionOutcome{Ran: true, Output: r.Output, Err: r.err()}
				if r.err() != nil {
					log.Printf("❌ %s on %s/%s: %v", plans[k].Action.Type, namespace, podName, r.err())
				}
			}
			i = j
			continue
		}

		var outputs []string
		var actionErr error
		for _, cmd := range executor.CommandsFromLegacyForExecutor(p.Commands, p.Executor) {
			cmd.Container = p.Container
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

// Consecutive kubectl actions of a pod run as one shell script, one exec
// instead of one per command (each exec is a round trip to the API server
// and the kubelet). The commands of an action run in order and stop at the
// first failure, as they do one by one, and a marker after each action
// carries its index and exit status so the outcomes come apart again. The
// pod's sh is part of the baseline every node image has.

const batchMarker = "__kubendt_rc__"

// shellJoin quotes an argv for sh, with the tool runner's shellQuote.
func shellJoin(argv []string) string {
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = shellQuote(a)
	}
	return strings.Join(words, " ")
}

func shellScript(plans []PlannedAction) string {
	var b strings.Builder
	for k, p := range plans {
		cmds := make([]string, 0, len(p.Commands)+1)
		if p.Precondition != nil {
			cmds = append(cmds, preconditionGuard(p.Precondition))
		}
		for _, row := range p.Commands {
			if len(row) > 0 {
				cmds = append(cmds, shellJoin(row))
			}
		}
		if len(cmds) == 0 {
			cmds = []string{"true"}
		}
		fmt.Fprintf(&b, "{ %s; } 2>&1; echo \"%s %d $?\"\n", strings.Join(cmds, " && "), batchMarker, k)
	}
	return b.String()
}

// batchResult is what one action of the script produced.
type batchResult struct {
	Output string
	RC     int
	Seen   bool
}

func (r batchResult) err() error {
	switch {
	case !r.Seen:
		return errors.New("exec error: the action left no result in the batch")
	case r.RC == 0:
		return nil
	case r.RC == preconditionRC && r.Output != "":
		return errors.New(r.Output)
	case r.RC == 127:
		if prog := executor.MissingProgram(r.Output); prog != "" {
			return fmt.Errorf("'%s' is not in the pod's image: add it to the image or install it from the node's commands (%s)", prog, r.Output)
		}
	}
	return fmt.Errorf("exec error: exit status %d | output: %s", r.RC, r.Output)
}

// parseShellScript splits the script's output back into one result per
// action, by the marker lines.
func parseShellScript(out string, n int) []batchResult {
	res := make([]batchResult, n)
	var cur []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == batchMarker {
			k, errK := strconv.Atoi(f[1])
			rc, errRC := strconv.Atoi(f[2])
			if errK == nil && errRC == nil && k >= 0 && k < n {
				res[k] = batchResult{Output: strings.TrimSpace(strings.Join(cur, "\n")), RC: rc, Seen: true}
				cur = nil
				continue
			}
		}
		cur = append(cur, line)
	}
	return res
}
