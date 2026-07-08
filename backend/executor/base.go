package executor

import (
	"fmt"
	"strings"
)

// KubectlBase is the template struct for executors that dispatch commands via
// "kubectl exec". Concrete executor types embed this struct and are registered
// in the executor registry.
//
// It mirrors the pattern used by drivers_meta.Meta and capability Base structs.
type KubectlBase struct {
	name       string
	prefix     []string
	dispatchAs KubectlDispatchMode
}

type KubectlDispatchMode string

const (
	// Generic ────────────────────────────────────────────────────────────────

	// DispatchAsArgs passes Command.Args tokens directly as arguments
	// to the prefix binary (or to kubectl exec if no prefix is set).
	DispatchAsArgs KubectlDispatchMode = "args"

	// DispatchAsSingleArg passes the entire Command.Line as a single string
	// argument to the prefix binary (needed for CLIs like xr_cli that expect a string).
	DispatchAsSingleArg KubectlDispatchMode = "single-arg"

	// XRD ─────────────────────────────────────────────────────────

	// DispatchAsXRApply wraps Command.Args as IOS-XR config lines
	// inside an xrapply_string call via the ZTP helper (bash -lc).
	// Each element of Args is a line of the configure block
	// (e.g. ["interface X", "shutdown"]) and they are sent as an atomic transaction.
	DispatchAsXRApply KubectlDispatchMode = "xr-apply"

	// VyOS ─────────────────────────────────────────────────────────

	// DispatchAsVyOSApply wraps Command.Args as VyOS configure-mode lines
	// inside a herestring evaluated by bash -c in the pod. Requires
	// 'source script-template' to load the VyOS environment before configure.
	DispatchAsVyOSApply KubectlDispatchMode = "vyos-apply"
)

// NewKubectlBase creates a KubectlBase with the given name and optional
// command prefix (e.g. []string{"ssh_qemu"} to route via the qemu wrapper).
func NewKubectlBase(name string, prefix []string, dispatchAs KubectlDispatchMode) KubectlBase {
	return KubectlBase{name: name, prefix: prefix, dispatchAs: dispatchAs}
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

	execArgs, err := b.toExecArgs(command)
	if err != nil {
		return "", err
	}

	return execViaKubectl(podName, namespace, b.prefix, execArgs, b.name)
}

func (b KubectlBase) toExecArgs(command Command) ([]string, error) {
	switch b.dispatchAs {
	case DispatchAsArgs:
		switch command.Kind {
		case CommandKindArgs:
			return command.Args, nil
		case CommandKindLine:
			return nil, fmt.Errorf("executor %q expects args commands, but got line command", b.name)
		default:
			return nil, fmt.Errorf("executor %q got unknown command kind %q", b.name, command.Kind)
		}
	case DispatchAsSingleArg:
		switch command.Kind {
		case CommandKindLine:
			return []string{command.Line}, nil
		case CommandKindArgs:
			return nil, fmt.Errorf("executor %q expects line commands, but got args command", b.name)
		default:
			return nil, fmt.Errorf("executor %q got unknown command kind %q", b.name, command.Kind)
		}
	case DispatchAsXRApply:
		switch command.Kind {
		case CommandKindArgs:
			// Join config lines with \n (IOS-XR block format) and wrap in xrapply_string.
			inner := strings.Join(command.Args, "\\n ")
			script := fmt.Sprintf(
				"source /pkg/bin/ztp_helper.sh >/dev/null 2>&1; xrapply_string $'%s\\n!'",
				inner,
			)
			return []string{script}, nil
		default:
			return nil, fmt.Errorf("executor %q expects args commands, but got %q", b.name, command.Kind)
		}
	case DispatchAsVyOSApply:
		switch command.Kind {
		case CommandKindArgs:
			// bash in the pod evaluates the herestring $'...' and passes it as stdin to
			// vbash through ssh_qemu. 'source script-template' is included to
			// load the VyOS environment (configure/commit functions) before executing.
			// Confirmed working; do not use printf|ssh_qemu or vbash -c.
			lines := []string{
				"source /opt/vyatta/etc/functions/script-template",
				"configure",
			}
			// Escape single quotes in args so they don't break the $'...' bash ANSI-C
			// quoting. An unescaped ' inside $'...' would prematurely terminate the
			// here-string, causing 'commit' and 'exit' to never be sent to vbash, all
			// configure changes would be silently discarded (no commit).
			escaped := make([]string, len(command.Args))
			for i, a := range command.Args {
				escaped[i] = strings.ReplaceAll(a, "'", `\'`)
			}
			lines = append(lines, escaped...)
			lines = append(lines, "commit", "exit")
			inner := strings.Join(lines, "\\n")
			script := fmt.Sprintf("ssh_qemu /bin/vbash -s <<< $'%s'", inner)
			return []string{script}, nil
		default:
			return nil, fmt.Errorf("executor %q expects args commands, but got %q", b.name, command.Kind)
		}
	default:
		return nil, fmt.Errorf("executor %q has invalid dispatch mode %q", b.name, b.dispatchAs)
	}
}

// ExecutorMeta is embedded in driver structs to declare the default executor.
// Drivers can still override per-action via ResolveActionExecutionPlan
// (e.g. VyOS op-mode → vyos_cli, config-mode → vyos_apply).
type ExecutorMeta struct {
	executorName string
}

// NewExecutorMeta creates an ExecutorMeta for the given executor name.
func NewExecutorMeta(name string) ExecutorMeta { return ExecutorMeta{executorName: name} }

// ExecutorName satisfies the driverExecutorNamer interface checked by
// executor.ResolveForDriver.
func (m ExecutorMeta) ExecutorName() string { return m.executorName }
