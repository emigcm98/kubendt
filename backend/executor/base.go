package executor

import (
	"fmt"
)

// KubectlBase is the template struct for executors that dispatch commands via
// "kubectl exec". Concrete executor types embed this struct and are registered
// in the executor registry. Platform-specific executors (VyOS, XRd) live in
// their own files (vyos.go, xr.go) together with their dispatch functions and
// registrations; this file only holds the generic machinery.
//
// It mirrors the pattern used by drivers_meta.Meta and capability Base structs.
type KubectlBase struct {
	name     string
	prefix   []string
	dispatch DispatchFunc
}

// DispatchFunc turns a Command into the argument list appended after the
// executor's prefix on the kubectl exec command line. The executor name is
// passed for error messages only.
type DispatchFunc func(name string, command Command) ([]string, error)

// DispatchAsArgs passes Command.Args tokens directly as arguments to the
// prefix binary (or to kubectl exec if no prefix is set).
var DispatchAsArgs DispatchFunc = func(name string, command Command) ([]string, error) {
	switch command.Kind {
	case CommandKindArgs:
		return command.Args, nil
	case CommandKindLine:
		return nil, fmt.Errorf("executor %q expects args commands, but got line command", name)
	default:
		return nil, fmt.Errorf("executor %q got unknown command kind %q", name, command.Kind)
	}
}

// DispatchAsSingleArg passes the entire Command.Line as a single string
// argument to the prefix binary (needed for CLIs like xr_cli that expect a string).
var DispatchAsSingleArg DispatchFunc = func(name string, command Command) ([]string, error) {
	switch command.Kind {
	case CommandKindLine:
		return []string{command.Line}, nil
	case CommandKindArgs:
		return nil, fmt.Errorf("executor %q expects line commands, but got args command", name)
	default:
		return nil, fmt.Errorf("executor %q got unknown command kind %q", name, command.Kind)
	}
}

// BatchableExecutors lists executor names that support batching multiple
// configure-mode action command sets into a single invocation. When the
// configurator detects a run of consecutive actions all targeting the same
// batchable executor, it merges their Args into one Command and executes
// once, avoiding redundant commit round trips. Platform files add their
// entries from init().
var BatchableExecutors = map[string]bool{}

// NewKubectlBase creates a KubectlBase with the given name, optional command
// prefix (e.g. []string{"ssh_qemu"} to route via the qemu wrapper) and
// dispatch function.
func NewKubectlBase(name string, prefix []string, dispatch DispatchFunc) KubectlBase {
	return KubectlBase{name: name, prefix: prefix, dispatch: dispatch}
}

func (b KubectlBase) Name() string { return b.name }

func (b KubectlBase) ExecCommand(podName, namespace string, command Command) error {
	_, err := b.ExecCommandAndGet(podName, namespace, command)
	return err
}

func (b KubectlBase) ExecCommandAndGet(podName, namespace string, command Command) (string, error) {
	if err := command.Validate(); err != nil {
		return "", fmt.Errorf("invalid command for executor %q: %w", b.name, err)
	}

	if b.dispatch == nil {
		return "", fmt.Errorf("executor %q has no dispatch function", b.name)
	}
	execArgs, err := b.dispatch(b.name, command)
	if err != nil {
		return "", err
	}

	return execViaKubectl(podName, namespace, b.prefix, execArgs, b.name)
}

// KubectlExecutor runs commands directly via "kubectl exec" (default).
// Used by drivers that interact with the pod itself (no guest VM inside).
type KubectlExecutor struct{ KubectlBase }

func init() {
	MustRegister(KubectlExecutor{KubectlBase: NewKubectlBase(DefaultExecutorName, nil, DispatchAsArgs)})
}

// ExecutorMeta is embedded in driver structs to declare the default executor.
// Drivers can still override per-action via ResolveActionExecutionPlan
// (e.g. VyOS op-mode → vyos_ssh_cli, config-mode → vyos_api_apply).
type ExecutorMeta struct {
	executorName string
}

// NewExecutorMeta creates an ExecutorMeta for the given executor name.
func NewExecutorMeta(name string) ExecutorMeta { return ExecutorMeta{executorName: name} }

// ExecutorName satisfies the driverExecutorNamer interface checked by
// executor.ResolveForDriver.
func (m ExecutorMeta) ExecutorName() string { return m.executorName }
