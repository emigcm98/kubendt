package executor

// ─── Executor names ──────────────────────────────────────────────────────────
//
// The generic executor does not need a constant here: it uses DefaultExecutorName
// ("kubectl"), defined in exec.go.

// XRD (Cisco IOS-XR)
const (
	XRCLIExecutorName   = "xr_cli"   // op-mode commands via /pkg/bin/xr_cli
	XRApplyExecutorName = "xr_apply" // configure-mode blocks via xrapply_string (ZTP)
)

// VyOS (QEMU guest with ssh_qemu as the access wrapper)
const (
	SSHQemuExecutorName   = "ssh_qemu"   // generic SSH access to the QEMU guest (base for the two below)
	VyOSCLIExecutorName   = "vyos_cli"   // op-mode commands via vyatta-op-cmd-wrapper
	VyOSApplyExecutorName = "vyos_apply" // configure-mode blocks via vbash herestring
)

// BatchableExecutors lists executor names that support batching multiple
// configure-mode action command sets into a single invocation. When the
// configurator detects a run of consecutive actions all targeting the same
// batchable executor, it merges their Args into one Command and executes
// once, avoiding redundant configure→commit→exit round trips.
var BatchableExecutors = map[string]bool{
	XRApplyExecutorName:   true,
	VyOSApplyExecutorName: true,
}

// ─── Executor types ───────────────────────────────────────────────────────────

// KubectlExecutor runs commands directly via "kubectl exec" (default).
// Used by drivers that interact with the pod itself (no guest VM inside).
type KubectlExecutor struct{ KubectlBase }

// ── XRD executors ─────────────────────────────────────────────────────────────

// XRCLIExecutor routes operational commands through /pkg/bin/xr_cli.
// Accepts a single quoted IOS-XR CLI string as argument.
// e.g.: kubectl exec <pod> -- /pkg/bin/xr_cli "show version"
type XRCLIExecutor struct{ KubectlBase }

// XRApplyExecutor applies multi-line IOS-XR configuration blocks using the
// ZTP helper's xrapply_string function, the only reliable method for
// config transactions in XRd (xr_cli rejects configure-mode syntax).
// e.g.: kubectl exec <pod> -- bash -lc "source /pkg/bin/ztp_helper.sh; xrapply_string $'interface X\n shutdown\n!'"
type XRApplyExecutor struct{ KubectlBase }

// ── VyOS executors (access via ssh_qemu to the QEMU guest) ───────────────────────

// SSHQemuExecutor routes raw commands through the "ssh_qemu" wrapper that lives
// inside the pod's QEMU container (created by launch.sh as /usr/local/bin/ssh_qemu).
// Base guest access; VyOS drivers use VyOSCLIExecutor/VyOSApplyExecutor.
type SSHQemuExecutor struct{ KubectlBase }

// VyOSCLIExecutor routes operational commands through ssh_qemu and
// vyatta-op-cmd-wrapper inside the VyOS guest.
// e.g.: kubectl exec <pod> -- ssh_qemu /opt/vyatta/bin/vyatta-op-cmd-wrapper show interfaces
type VyOSCLIExecutor struct{ KubectlBase }

// VyOSApplyExecutor applies multi-line VyOS configure-mode commands by running
// bash -c on the pod, which evaluates a herestring and pipes it as stdin to
// vbash through ssh_qemu. Requires sourcing script-template to load the VyOS
// environment (configure, commit functions) before entering configure mode.
// e.g.: kubectl exec <pod> -- bash -c "ssh_qemu /bin/vbash -s <<< $'source /opt/vyatta/etc/functions/script-template\nconfigure\nset ...\ncommit\nexit'"
type VyOSApplyExecutor struct{ KubectlBase }

// ─── Registration ─────────────────────────────────────────────────────────────

func init() {
	// Generic
	MustRegister(KubectlExecutor{KubectlBase: NewKubectlBase(DefaultExecutorName, nil, DispatchAsArgs)})

	// XRD
	MustRegister(XRCLIExecutor{KubectlBase: NewKubectlBase(XRCLIExecutorName, []string{"/pkg/bin/xr_cli"}, DispatchAsSingleArg)})
	MustRegister(XRApplyExecutor{KubectlBase: NewKubectlBase(XRApplyExecutorName, []string{"bash", "-lc"}, DispatchAsXRApply)})

	// VyOS
	MustRegister(SSHQemuExecutor{KubectlBase: NewKubectlBase(SSHQemuExecutorName, []string{"ssh_qemu"}, DispatchAsArgs)})
	MustRegister(VyOSCLIExecutor{KubectlBase: NewKubectlBase(VyOSCLIExecutorName, []string{"ssh_qemu", "/opt/vyatta/bin/vyatta-op-cmd-wrapper"}, DispatchAsArgs)})
	MustRegister(VyOSApplyExecutor{KubectlBase: NewKubectlBase(VyOSApplyExecutorName, []string{"bash", "-c"}, DispatchAsVyOSApply)})
}
