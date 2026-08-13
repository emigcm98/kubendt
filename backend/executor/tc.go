package executor

import (
	"fmt"
	"strings"
)

// TCUniversalExecutorName runs a tc command against a pod's network namespace.
// Traffic shaping is a generic netns operation, not a driver capability, so any
// pod can be shaped. The real execution (the pod's own tc, or an ephemeral
// toolbox for images that ship none) lives in the helpers package, which owns
// pod exec and ephemeral-container injection. It is wired in through RunTCHook
// so this package keeps its one-way dependency and never imports helpers.
const TCUniversalExecutorName = "tc_universal"

// RunTCHook is set by the helpers package at startup. args carries the raw tc
// tokens, e.g. ["tc","qdisc","replace","dev","eth1","root","netem","delay","50ms"].
var RunTCHook func(namespace, podName string, args []string) (stdout, stderr string, err error)

// tcUniversalExecutor adapts RunTCHook to the CommandExecutor interface so the
// configure and replay paths dispatch tc like any other executor.
type tcUniversalExecutor struct{}

func (tcUniversalExecutor) Name() string { return TCUniversalExecutorName }

func (e tcUniversalExecutor) ExecCommand(podName, namespace string, command Command) error {
	_, err := e.ExecCommandAndGet(podName, namespace, command)
	return err
}

func (tcUniversalExecutor) ExecCommandAndGet(podName, namespace string, command Command) (string, error) {
	if command.Kind != CommandKindArgs {
		return "", fmt.Errorf("executor %q expects args commands, but got %q", TCUniversalExecutorName, command.Kind)
	}
	if RunTCHook == nil {
		return "", fmt.Errorf("executor %q is not wired (RunTCHook is nil)", TCUniversalExecutorName)
	}
	stdout, stderr, err := RunTCHook(namespace, podName, command.Args)
	if err != nil {
		if s := strings.TrimSpace(stderr); s != "" {
			return "", fmt.Errorf("%w: %s", err, s)
		}
		return "", err
	}
	return stdout, nil
}

func init() {
	MustRegister(tcUniversalExecutor{})
}
