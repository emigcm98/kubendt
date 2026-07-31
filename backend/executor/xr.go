package executor

import (
	"fmt"
	"strings"
)

// XRD (Cisco IOS-XR) executors.
const (
	XRCLIExecutorName   = "xr_cli"   // op-mode commands via /pkg/bin/xr_cli
	XRApplyExecutorName = "xr_apply" // configure-mode blocks via xrapply_string (ZTP)
)

// XRCLIExecutor routes operational commands through /pkg/bin/xr_cli.
// Accepts a single quoted IOS-XR CLI string as argument.
// e.g.: kubectl exec <pod> -- /pkg/bin/xr_cli "show version"
type XRCLIExecutor struct{ KubectlBase }

// XRApplyExecutor applies multi-line IOS-XR configuration blocks using the
// ZTP helper's xrapply_string function, the only reliable method for
// config transactions in XRd (xr_cli rejects configure-mode syntax).
// e.g.: kubectl exec <pod> -- bash -lc "source /pkg/bin/ztp_helper.sh; xrapply_string $'interface X\n shutdown\n!'"
type XRApplyExecutor struct{ KubectlBase }

// dispatchAsXRApply wraps Command.Args as IOS-XR config lines inside an
// xrapply_string call via the ZTP helper (bash -lc). Each element of Args is
// a line of the configure block (e.g. ["interface X", "shutdown"]) and they
// are sent as an atomic transaction.
var dispatchAsXRApply DispatchFunc = func(name string, command Command) ([]string, error) {
	if command.Kind != CommandKindArgs {
		return nil, fmt.Errorf("executor %q expects args commands, but got %q", name, command.Kind)
	}
	// Join config lines with \n (IOS-XR block format) and wrap in xrapply_string.
	inner := strings.Join(command.Args, "\\n ")
	script := fmt.Sprintf(
		"source /pkg/bin/ztp_helper.sh >/dev/null 2>&1; xrapply_string $'%s\\n!'",
		inner,
	)
	return []string{script}, nil
}

func init() {
	MustRegister(XRCLIExecutor{KubectlBase: NewKubectlBase(XRCLIExecutorName, []string{"/pkg/bin/xr_cli"}, DispatchAsSingleArg)})
	MustRegister(XRApplyExecutor{KubectlBase: NewKubectlBase(XRApplyExecutorName, []string{"bash", "-lc"}, dispatchAsXRApply)})

	BatchableExecutors[XRApplyExecutorName] = true
}
