# Writing a driver

A driver is what KubeNDT knows about one kind of node: the commands behind each action, where they run, what the pod needs and what the node offers. One Go file under `backend/drivers/drivers/`, registered once in `backend/drivers/register_all.go`. Everything else asks the driver: the pod spec builder, the action pipeline, the tools, the shells, `GET /drivers` and the pod listing. The frontend knows no driver by name, it reads what the backend publishes.

## The minimum

A driver is a struct with a name and a type, the capabilities its type requires, and an executor when its commands do not run as plain argv in the pod.

```go
package drivers

import (
	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
)

type AcmeRouterDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.L3Base
	capabilities.NATBase
}

func NewAcmeRouterDriver() *AcmeRouterDriver {
	return &AcmeRouterDriver{Meta: drivers_meta.NewMeta("AcmeRouterDriver", "router")}
}

var _ capabilities.L2Capable = (*AcmeRouterDriver)(nil)
var _ capabilities.L3Capable = (*AcmeRouterDriver)(nil)
var _ capabilities.NATCapable = (*AcmeRouterDriver)(nil)
```

- **Name and type.** `drivers_meta.NewMeta(name, type)`, with type `host`, `switch` or `router`. The name is what a topology's `driver` field takes and what the pod's `kubendt/driver` label carries.
- **Capabilities.** The registry enforces a minimum per type and panics at startup otherwise: every driver implements `L2Capable`, hosts and routers `L3Capable`, switches `SwitchCapable`. More is welcome, `NATCapable` and `OSPFCapable` exist today. Embed the bases in `backend/capabilities` (`L2Base`, `L3Base`, `SwitchBase`, `NATBase`) to get the Linux commands, iproute2, iptables and bridge tools, and override the methods the node does differently; or implement the interfaces from scratch when the node has a CLI of its own, as `VyOSRouterDriver` does. A method returns the command rows for one action, `[][]string`; the actions themselves, their parameters, validation and the state they set or clear (what the history keeps the last word on) are declared once per capability in `backend/capabilities/*.go` and apply to every driver that satisfies the interface.
- **Executor.** By default a command row is an argv run with `kubectl exec` in the pod's main container. A node whose commands are lines of a CLI, or a batch through an API, names its executor: embed `executor.ExecutorMeta` with `executor.NewExecutorMeta(name)`. An executor is a `KubectlBase` built with `NewKubectlBase(name, prefix, dispatch)` and registered with `MustRegister` in an `init` of `backend/executor`; it may implement `CommandKinder` when it takes one line or a batch instead of argv, and join `BatchableExecutors` when consecutive actions can go in one call. `backend/executor/vyos.go` has the five of VyOS.
- **Register.** One line in `RegisterAllDrivers`: `drivers_registry.Register(drivers.NewAcmeRouterDriver)`. `DefaultDriverByType` names the driver a node gets when the topology sets none.

## What the platform asks a driver

Everything beyond the minimum is an optional interface, all of them in `backend/drivers/meta`. Implement what the node has, say nothing about the rest, and add a `var _ Interface = (*Driver)(nil)` line per interface so the compiler keeps it true.

| Interface | Method | When it is missing | Today |
| --- | --- | --- | --- |
| `RuntimeProvider` | `Runtime() string` | A container node | VyOS |
| `DeviceProvider` | `Devices() []string` | No host devices | VyOS |
| `ShellProvider` | `Shells() []ShellSpec` | The pod shell, plus the serial console on a VM | FRR, VyOS |
| `GuestToolProvider` | `GuestWrapper() []string`, `GuestTools() []string` | A container runs every tool in its toolbox; a VM offers none | VyOS |
| `InterfaceNameConstrainer` | `InterfaceNameConstraints()` | The kernel's rules only | VyOS |
| `ReadinessProbeProvider` | `ReadinessProbeCommands()` | `command -v ip`, the node is Ready when its image has `ip` | FRR, OVS, VyOS |
| `EffectiveInterfaceInspector`, `EffectiveSingleInterfaceInspector`, `EffectiveInterfaceStateInspector` | `GetEffectiveInterfaces`, `GetEffectiveInterface`, `GetEffectiveInterfaceStates` | The pod's own `ip a` and link states | VyOS |
| `SNATInterfaceInspector` | `GetSNATInterface()` | The namespace summary shows no NAT interface | VyOS |
| `GuestInterfaceMapper` | `GuestInterface(ns, pod, iface)` | Interface names reach the actions as the pod names them | Guest-named dataplanes |

What each one feeds:

- **Runtime** is the one declaration that turns a node into a VM: the pod gets `/dev/kvm` through `Devices`, privileged mode, stdin and a TTY, the interface-count ConfigMap and the `kubendt/runtime: qemu` label, and from then on the shells, the tools and the summary treat it as a guest. The backend reads that label in one place, `helpers.RuntimeOf`.
- **Devices** are host paths added to every pod of the driver unless the topology lists them already.
- **Shells** are what the node menu offers under Shell besides the pod shell: a mode, a label and the argv exec'd with a TTY. The pod listing publishes them as `shells` and `GET /shell/ws?mode=` takes any of them.
- **GuestTools** says which of the platform's tools the guest image ships, `trace` and `iperf`, and `GuestWrapper` how an argv gets into the guest (`ssh_qemu sudo` on VyOS, a script the image's entrypoint creates). The pod listing and `GET /drivers` publish `tools`; the menus show a missing one dimmed with the reason.
- **InterfaceNameConstraints** are checked at deploy and modify, so a name the node's configuration schema would reject fails before anything is wired.
- **ReadinessProbeCommands** is what Ready means for the node: a VM is Ready when the backend can reach the guest, a router when its daemons answer.
- **The inspectors** replace the pod-side reads where the dataplane lives elsewhere: the interface list and states of the node panel, the NAT interface of the summary.
- **GuestInterface** renames interface parameters before an action runs, for a dataplane that calls `eth1` something else.

Two more sit next to a capability rather than in `drivers/meta`: `GuestMTUSetter` (`GuestMTUCommands(iface, mtu)`), which the MTU pass uses to set a VM guest's interface, and `DefaultRoute6Remover` (`RemoveDefaultRoute6()`), which `remove_default_route` with `family: 6` needs; `L3Base` has the latter, so a driver embedding it gets it for free.

## Where a tool runs

Capture, interface counters and traffic control watch the wire: they run in the pod on `ethN`, or on `tapN` for a VM, whatever the driver. The trace and the throughput test send traffic from the node's addresses, so they need its IP stack: a container node runs them in its toolbox, the ephemeral container the backend injects, and a VM node runs them inside the guest through `GuestToolProvider`. A VM driver that declares nothing offers neither, and the menus say so instead of failing. `helpers.ToolRunner` is the one place that decides.

A guest that declares `trace` ships `traceroute`, `tcptraceroute` for the tcp method and `mtr` with `--json`. One that declares `iperf` ships iperf3 3.17 or later, for `--json-stream`. The toolbox images already do; a guest image is the driver author's to build. The VyOS build shows the pattern: a static iperf3 built in a stage of `Dockerfile.kubendt` and uploaded into the guest's overlay by `customize-qcow2.sh`, since the version VyOS ships is too old; see `deploy/custom_images/qemu/vyos-router/README-vyos.md`.

## Tests and the rest

- `go test ./drivers/...` runs the contract test: the registry refuses a driver that lacks the capabilities of its type.
- The expected plans pin what every driver sends for every action. Add the driver to `plannedDrivers` in `backend/helpers/action_plan_test.go`, run `go test ./helpers -run TestActionPlans -update`, and review the new `backend/helpers/testdata/plans/<Driver>.json` before committing it: those rows are what the pods will receive. A driver whose plan needs the cluster, one that maps interface names from the pod for every action, stays out of that list.
- `GET /drivers` shows the result: capabilities with their actions and parameters, the executor, the constraints and the tools.
- A driver that ships with an image gets an example under `deploy/examples/`, or extends one, and a block in `experiments/regression.py`, which is the release gate.
- A line in `CHANGELOG.md` under `Added`.

## Checklist

1. Struct with `Meta`, the bases or the capability interfaces of its type, `ExecutorMeta` when needed.
2. The optional interfaces the node really has, each with its compile-time assertion.
3. Registered in `RegisterAllDrivers`.
4. Expected plans generated and reviewed.
5. Example, regression block and CHANGELOG line.
