package executor

import "time"

// EXOS executor. The guest VM's HTTP API answers JSON-RPC on a loopback
// hostfwd, through the exos_api wrapper entrypoint.sh drops into the pod:
// one call per CLI line, printed as received, stopping at the first line
// the switch rejects. Consecutive actions batch into one exec, the lines
// in order, as the VyOS batches do.
const EXOSAPIExecutorName = "exos_api"

type EXOSAPIExecutor struct{ KubectlBase }

func init() {
	MustRegister(EXOSAPIExecutor{KubectlBase: NewKubectlBase(EXOSAPIExecutorName, []string{"exos_api"}, DispatchAsArgs)})
	BatchableExecutors[EXOSAPIExecutorName] = true
	// The guest answers a command in three to five seconds on a small
	// worker and a VLAN action is five or six of them, batched by the pipeline.
	SetExecDeadline(EXOSAPIExecutorName, 4*time.Minute)
}
