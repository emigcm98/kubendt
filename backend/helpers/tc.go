package helpers

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"kubendt/executor"
)

// tcToolboxStartTimeout bounds the wait for the netshoot toolbox to come up.
// The first pull of the image dominates it; later shaping reuses the container.
const tcToolboxStartTimeout = 120 * time.Second

// tcNoNativeBinary remembers pods whose main container ships no `tc`, so later
// shaping ops skip a doomed main-container exec and go straight to the toolbox.
// A running pod never gains or loses the binary, so the flag is stable.
var tcNoNativeBinary sync.Map // "namespace/pod" -> bool

// tcToolboxLocks serializes toolbox creation per pod, so concurrent shaping
// calls reuse one container instead of each injecting its own.
var tcToolboxLocks sync.Map // "namespace/pod" -> *sync.Mutex

func init() {
	// Wire the tc_universal executor to the pod-exec + toolbox machinery here,
	// since the executor package must not import helpers.
	executor.RunTCHook = RunTC
}

// RunTC runs a tc command in a pod's network namespace. Shaping is universal:
// it prefers the pod's own tc (one exec, the common case) and falls back to an
// ephemeral netshoot toolbox (ships tc, has NET_ADMIN) for images without it.
// The qdisc lives in the shared pod netns, so it persists after the exec ends.
func RunTC(namespace, podName string, args []string) (stdout, stderr string, err error) {
	key := namespace + "/" + podName
	if v, ok := tcNoNativeBinary.Load(key); ok && v.(bool) {
		return runTCInToolbox(namespace, podName, args)
	}
	stdout, stderr, err = ExecInPod(namespace, podName, args)
	if err != nil && isBinaryNotFound(err, stderr) {
		tcNoNativeBinary.Store(key, true)
		return runTCInToolbox(namespace, podName, args)
	}
	return stdout, stderr, err
}

// runTCInToolbox runs the tc command inside a shared netshoot debug container,
// injecting one and waiting for it to start if the pod has none yet. Creation
// is serialized per pod so parallel shaping calls converge on one container.
func runTCInToolbox(namespace, podName string, args []string) (string, string, error) {
	container, err := ensureTCToolbox(namespace, podName)
	if err != nil {
		return "", "", err
	}
	return ExecInContainer(namespace, podName, container, args)
}

func ensureTCToolbox(namespace, podName string) (string, error) {
	key := namespace + "/" + podName
	lkAny, _ := tcToolboxLocks.LoadOrStore(key, &sync.Mutex{})
	lk := lkAny.(*sync.Mutex)
	lk.Lock()
	defer lk.Unlock()

	container, reused, err := EnsureDebugContainer(namespace, podName)
	if err != nil {
		return "", fmt.Errorf("could not ensure tc toolbox container: %w", err)
	}
	// A freshly injected container is not exec-ready yet; block until it runs so
	// the caller does not race a "container not found" exec.
	if !reused {
		if werr := WaitEphemeralRunning(namespace, podName, container, tcToolboxStartTimeout); werr != nil {
			return "", fmt.Errorf("tc toolbox container did not start: %w", werr)
		}
	}
	return container, nil
}

// isBinaryNotFound reports whether an exec failed because tc is absent from the
// image, the signal to retry inside the toolbox instead.
func isBinaryNotFound(err error, stderr string) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error() + " " + stderr)
	return strings.Contains(s, "executable file not found") ||
		strings.Contains(s, "no such file or directory")
}
