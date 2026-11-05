# EXOS switch node

Extreme Networks' Switch Engine (EXOS) VM running as a QEMU/KVM guest inside a pod: a vendor switch with its own CLI and API next to VyOS, the vendor router. A simple lab switch, VLANs and spanning tree with a real CLI: the VM has no OpenFlow and its link aggregation does not come up, so the SDN and LACP labs stay on Open vSwitch. Same mechanics as the VyOS node (taps, tc redirects, a loopback hostfwd for the control channel), see [README-vyos.md](../vyos-router/README-vyos.md) for the deep dive on the QEMU wiring; this file covers what differs.

> Last reviewed: 5 Nov 2026

## What the image is

- **Extreme's own VM image**, from [extremenetworks/Virtual_EXOS](https://github.com/extremenetworks/Virtual_EXOS): a QCOW2 Extreme publishes for lab use under a BSD-style notice, not supported by its TAC, "not all features implemented" (VXLAN VTEP and VPLS have no data plane in the VM). It is not redistributed from this repository: each site builds its own image, as with VyOS.
- **The driver was written against 33.6.1.14**, the release `build-exos-qcow2.sh` pins. The script knows the releases Extreme lists and their md5 sums (`--list`).
- **What the VM needs**: 1 GB of RAM, two vCPUs (its API answers a command in half a second with two and in three to five with one, every call spawns its CLI), KVM with the host CPU model (`-cpu host`; with a generic model the bootloader stops at its menu with "Could not determine the CPU Family"), virtio NICs, an IDE disk. At most 12 data ports.
- **What it does not have**: OpenFlow (`enable openflow` is a run time error, the licence is Core with no feature packs), a working LACP (`enable sharing ... lacp` fails in the VM), VXLAN or VPLS data planes, a Linux shell or iperf3, so the trace and throughput tools are unavailable on an EXOS node (the capture, counters and traffic control work, they watch the pod's interfaces).

## Build

Two steps, like VyOS: the qcow2, then the container image.

### 1. The qcow2

```bash
./build-exos-qcow2.sh            # the pinned release, writes ./exos.qcow2
./build-exos-qcow2.sh --list     # the releases the script knows
./build-exos-qcow2.sh latest     # the newest one, to be tested
```

The script downloads Extreme's qcow2 (280 MB, cached), checks its md5, and boots it once inside a throwaway container (podman or docker, whichever reaches `/dev/kvm`) to check that it comes up and answers its HTTP API on this host, the pod's own readiness test; `--no-boot` skips that. The image is used exactly as Extreme publishes it: the API is on by default and the first-login questions only concern a human on the serial console. Nothing is saved into it on purpose, a saved configuration makes every boot wait 150 s for "all applications" before loading it, which doubled the boot.

### 2. The container image

```bash
podman build -t exos-switch:dev -f Dockerfile.kubendt .
podman save -o exos-switch.tar exos-switch:dev
```

Then on every worker (there is no registry for this image either):

```bash
scp exos-switch.tar user@worker:~
sudo ctr -n k8s.io images import exos-switch.tar     # lands as localhost/exos-switch:dev
```

The driver lists `localhost/exos-switch:dev` as its known image; another tag works and deploys with an `image_untested` warning.

## How a pod runs it

`entrypoint.sh` does, in order:

1. Waits for the pod's data interfaces, as many as the backend wrote in the `kubendt-internal-iface-counts` ConfigMap, since a NIC cannot be added to the guest afterwards.
2. Creates the API wrappers `exos_api` (one JSON-RPC call per CLI line, stops at the first line the switch rejects; a line starting with `-` may fail, and `@clear-port N` takes port N out of every VLAN it is in) and `exos_show` (the same, for reads), and renders `startup.xsf.tpl` with the pod's values (`__POD_NAME__`): the lines every pod runs once its API answers: the switch's name, and a link-local address on VLAN Default, since a factory-default switch probes DHCP on every VLAN tag out of every port once a minute until that VLAN has one (`disable auto-provision` stops it too, but leaves the API dead for two minutes). Applied live, never saved into the switch.
3. Makes an overlay of the qcow2 under `/run/exos`, so the pod's writes stay small and a restart starts clean.
4. Wires every `ethN` to a `tapN` with tc redirects (the L2 passthrough of the VyOS node) and sets the link MTU on both when the backend hands one over.
5. Starts QEMU with the management NIC first (QEMU's user network, the API on `127.0.0.1:8080` of the pod through a hostfwd to the guest's port 80; its MAC is the pod's `eth0` MAC with the locally administered bit, since the guest makes it its system MAC and so its bridge id, which must differ between switches), the taps after it in name order, and a virtio rng device, since the guest's 5.10 kernel seeds its random pool slowly on its own.
6. Watches the boot. One boot in five EXOS stalls 150 s "waiting for all applications" and sometimes never recovers, so the console goes through `tee` to the pod's stdio and a file, and the guest is rebooted on the same taps when the file says "Failed to load configuration" or the switch is not OPERATIONAL after five minutes, three boots at most. The pod is Ready once the startup lines are in and the API answers again after them (`/run/exos/ready`).

The guest numbers its ports by NIC order after the management one, so the first data interface is port 1 whatever its name, and `eth3` is port 2 when there is no `eth2`. The entrypoint writes which interface became which port in `/run/kubendt/exos-ports` and the driver reads it once per pod incarnation, so holes in the names are fine and an interface that was not there when the switch booted is reported as such (restart the node, a QEMU guest cannot take a NIC afterwards; a modify that adds a link restarts it on its own). The pod keeps its `eth0`, the cluster never enters the guest, so `kubectl exec` and the toolbox work as on any pod.

The serial console is the EXOS CLI: `-serial mon:stdio`, so the node's "serial console" shell in the UI (or `kubectl attach`) lands on it.

Environment knobs, with their defaults: `RAM_MB=1024`, `CPU_CORES=2`, `NIC_MODEL=virtio-net-pci`, `API_FORWARD_PORT=8080`, `IFACE_WAIT_MAX=60`, `STARTUP_TEMPLATE=/startup.xsf.tpl`, `BOOT_TIMEOUT=300`, `MAX_BOOTS=3`.

## Driver integration

`EXOSSwitchDriver` (type `switch`) declares the QEMU runtime and `/dev/kvm`, a readiness probe that waits for `show switch` to report the switch operational (about 80 s after the pod starts), the interface name constraint `eth1` to `eth12` (the VM's twelve ports), the port map above for the names, and the `exos_api` executor: every action is CLI lines sent one by one through the API, consecutive actions in one exec. A restart or a modify that touches the node replays its configuration through the same path once the switch is Ready; the wait covers the probe's budget.

| Capability | How EXOS does it |
| --- | --- |
| `L2Capable` | `enable ports N`, `disable ports N` |
| `SwitchCapable` | A bridge is a VLAN: `create vlan <name>` and the ports added untagged, which moves them out of `Default`; `delete vlan` leaves them in no VLAN, forwarding nothing, as the ports of a deleted Linux bridge |
| `VLANCapable` | A VLAN per tag named `v<tag>`, `configure vlan v10 add ports N untagged` for an access port, `tagged` for a trunk, the native VLAN untagged. A port's membership is rewritten whole: the wrapper's `@clear-port N` takes it out of every VLAN first, since EXOS has no command for that, and `-create vlan` may find the VLAN there |
| `STPCapable` | The one domain `s0`, which the bridge (a VLAN) joins with `enable stpd s0 auto-bind vlan <name>`; `rstp` is `dot1w` and the default, `stp` is `dot1d`, and the mode is the domain's, so the last enable sets it for every VLAN in it. Priority with `configure stpd s0 priority`, port cost and priority with `configure stpd s0 ports cost|port-priority` |

The inspections read `show ports vlan` (port, VLAN and tag status per membership), `show stpd s0` (the domain's mode, priority, bridge and root ids) and the table of `show stpd s0 ports` (role, state, cost and priority per port), all through the port map so the rows carry the pod's interface names.

The UI's interface list and states come from `show ports information`: a port is up when enabled (the E flag), what the link actions toggle.

## Troubleshooting

- **The pod takes four minutes to get Ready.** One boot in five, EXOS prints "Failed to load configuration: timed out (after 150 seconds) while waiting for all applications to get ready", and sometimes it then sits at the login prompt with the API dead. The entrypoint reboots the guest when it sees that line (`[WARN] ... rebooting the guest` in `kubectl logs`), the driver's readiness budget is nine minutes for two of those, and the deploy waits. `kubectl logs` shows the serial console: a normal boot takes 50 to 60 s to the login prompt and the API another 30 s.
- **The pod never gets Ready.** If the bootloader menu stays on screen, the worker has no KVM or exposes a generic CPU model to the VM (`egrep -c vmx /proc/cpuinfo` on the worker must not be 0).
- **A port is missing in the guest, or an action says an interface "was not there when the switch booted".** The interface was not there when QEMU started: `kubectl logs` lists the interfaces found and the port map, the iface-counts ConfigMap tells the entrypoint how many to wait for. Restart the node.
- **Every host sees a DHCP request per VLAN tag once a minute.** The switch's auto-provision is still running: `kubectl logs` should show `startup: configure vlan Default ipaddress 169.254.0.2/16`, and `exos_show 'show auto-provision'` says Disabled on both VRs.
- **An action fails with "Run time error".** EXOS rejected the line; `exos_api` prints the reply as received, the command is in it. `kubectl exec <pod> -- exos_show 'show log'` has the switch's own log.
- **The serial console asks questions at the first login** (telnet, SNMP, a failsafe account, a tour of the CLI). Answer `N` to each, they are cosmetic: the API works without them and nothing is saved across restarts anyway.
