package executor

import (
	"encoding/json"
	"fmt"
	"strings"
)

// VyOS executors. The guest VM is reachable over two channels, both bridged
// by wrappers that entrypoint.sh drops into the pod:
//   - ssh_qemu:  SSH over a loopback hostfwd (rescue/debug path)
//   - vyos_api:  the guest's HTTP API over a second loopback hostfwd
//     (hot path: structured reads and atomic configure batches)
const (
	SSHQemuExecutorName      = "ssh_qemu"       // generic SSH access to the QEMU guest
	VyOSSSHCLIExecutorName   = "vyos_ssh_cli"   // op-mode commands via vyatta-op-cmd-wrapper
	VyOSSSHApplyExecutorName = "vyos_ssh_apply" // configure-mode blocks via vbash herestring (rescue path)
	VyOSAPIExecutorName      = "vyos_api"       // raw API endpoint access: args = [endpoint, json-payload]
	VyOSAPIApplyExecutorName = "vyos_api_apply" // batched configure ops via POST /configure
)

// SSHQemuExecutor routes raw commands through the "ssh_qemu" wrapper that lives
// inside the pod's QEMU container (created by entrypoint.sh as /usr/local/bin/ssh_qemu).
// Base guest access; VyOS drivers use the more specific executors below.
type SSHQemuExecutor struct{ KubectlBase }

// VyOSSSHCLIExecutor routes operational commands through ssh_qemu and
// vyatta-op-cmd-wrapper inside the VyOS guest.
// e.g.: kubectl exec <pod> -- ssh_qemu /opt/vyatta/bin/vyatta-op-cmd-wrapper show interfaces
type VyOSSSHCLIExecutor struct{ KubectlBase }

// VyOSSSHApplyExecutor applies configure-mode blocks over SSH: bash -c in the
// pod pipes a herestring into vbash through ssh_qemu. Rescue path only, the
// action pipeline uses VyOSAPIApplyExecutor.
// e.g.: kubectl exec <pod> -- bash -c "ssh_qemu /bin/vbash -s <<< $'source /opt/vyatta/etc/functions/script-template\nconfigure\nset ...\ncommit\nexit'"
type VyOSSSHApplyExecutor struct{ KubectlBase }

// VyOSAPIExecutor POSTs to an arbitrary endpoint of the guest's HTTP API
// through the vyos_api wrapper in the pod. Command args = [endpoint, json].
// e.g.: kubectl exec <pod> -- vyos_api retrieve '{"op": "showConfig", "path": []}'
type VyOSAPIExecutor struct{ KubectlBase }

// VyOSAPIApplyExecutor applies configure-mode batches through the guest's
// HTTP API. Driver-emitted CLI lines are converted to /configure JSON ops
// and sent as one request, one atomic commit for the whole batch, against
// configd's persistent session (no vbash session setup per batch).
// e.g.: kubectl exec <pod> -- vyos_api configure '[{"op":"set","path":["interfaces",...]}]'
type VyOSAPIApplyExecutor struct{ KubectlBase }

// dispatchAsVyOSApply wraps Command.Args in a configure/commit herestring for
// vbash. 'source script-template' loads the configure/commit functions first.
// Confirmed working; do not use printf|ssh_qemu or vbash -c.
var dispatchAsVyOSApply DispatchFunc = func(name string, command Command) ([]string, error) {
	if command.Kind != CommandKindArgs {
		return nil, fmt.Errorf("executor %q expects args commands, but got %q", name, command.Kind)
	}
	lines := []string{
		"source /opt/vyatta/etc/functions/script-template",
		"configure",
	}
	// Escape single quotes: an unescaped ' would terminate the $'...' quoting
	// early, 'commit' would never reach vbash and changes would be silently lost.
	escaped := make([]string, len(command.Args))
	for i, a := range command.Args {
		escaped[i] = strings.ReplaceAll(a, "'", `\'`)
	}
	lines = append(lines, escaped...)
	lines = append(lines, "commit", "exit")
	inner := strings.Join(lines, "\\n")
	script := fmt.Sprintf("ssh_qemu /bin/vbash -s <<< $'%s'", inner)
	return []string{script}, nil
}

// dispatchAsVyOSAPIApply converts Command.Args (configure CLI lines) into the
// JSON op array for POST /configure. One argument, one atomic commit.
var dispatchAsVyOSAPIApply DispatchFunc = func(name string, command Command) ([]string, error) {
	if command.Kind != CommandKindArgs {
		return nil, fmt.Errorf("executor %q expects args commands, but got %q", name, command.Kind)
	}
	payload, err := VyOSConfigureOpsJSON(command.Args)
	if err != nil {
		return nil, fmt.Errorf("executor %q: %w", name, err)
	}
	return []string{payload}, nil
}

// vyosConfigureOp is one operation of a POST /configure request. Always sent
// as a list so a merged action batch becomes one atomic commit.
type vyosConfigureOp struct {
	Op   string   `json:"op"`
	Path []string `json:"path"`
}

// vyosAPIConfigureOps are the configure-mode verbs the HTTP API understands.
var vyosAPIConfigureOps = map[string]bool{
	"set":     true,
	"delete":  true,
	"comment": true,
}

// VyOSConfigureOpsJSON converts driver-emitted VyOS CLI lines
// ("set interfaces ethernet eth1 address 10.0.0.1/24") into the JSON op
// array for POST /configure. Tokens are whitespace-split, which is safe for
// every command the drivers generate (no quoted values with spaces).
func VyOSConfigureOpsJSON(lines []string) (string, error) {
	ops := make([]vyosConfigureOp, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", fmt.Errorf("configure line too short for API conversion: %q", line)
		}
		if !vyosAPIConfigureOps[fields[0]] {
			return "", fmt.Errorf("unsupported configure op %q in line %q", fields[0], line)
		}
		ops = append(ops, vyosConfigureOp{Op: fields[0], Path: fields[1:]})
	}
	if len(ops) == 0 {
		return "", fmt.Errorf("empty configure batch")
	}
	payload, err := json.Marshal(ops)
	if err != nil {
		return "", fmt.Errorf("marshalling configure ops: %w", err)
	}
	return string(payload), nil
}

func init() {
	MustRegister(SSHQemuExecutor{KubectlBase: NewKubectlBase(SSHQemuExecutorName, []string{"ssh_qemu"}, DispatchAsArgs)})
	MustRegister(VyOSSSHCLIExecutor{KubectlBase: NewKubectlBase(VyOSSSHCLIExecutorName, []string{"ssh_qemu", "/opt/vyatta/bin/vyatta-op-cmd-wrapper"}, DispatchAsArgs)})
	MustRegister(VyOSSSHApplyExecutor{KubectlBase: NewKubectlBase(VyOSSSHApplyExecutorName, []string{"bash", "-c"}, dispatchAsVyOSApply)})
	MustRegister(VyOSAPIExecutor{KubectlBase: NewKubectlBase(VyOSAPIExecutorName, []string{"vyos_api"}, DispatchAsArgs)})
	MustRegister(VyOSAPIApplyExecutor{KubectlBase: NewKubectlBase(VyOSAPIApplyExecutorName, []string{"vyos_api", "configure"}, dispatchAsVyOSAPIApply)})

	BatchableExecutors[VyOSSSHApplyExecutorName] = true
	BatchableExecutors[VyOSAPIApplyExecutorName] = true
}
