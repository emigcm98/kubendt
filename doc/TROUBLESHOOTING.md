# Troubleshooting

For what the platform recovers from on its own and what needs a hand, see [FAILURE_MODEL.md](FAILURE_MODEL.md).

## Pods missing network interfaces

This is usually Meshnet not running. Without its per-node dataplane the Topology CRDs are still created and the pods start, but their extra interfaces never get wired, so nothing errors out.

KubeNDT surfaces this so you don't have to guess:

- The Home page shows a **Meshnet** badge next to the node count (green when the DaemonSet is ready on every node, amber when some nodes are missing it, red when no meshnet DaemonSet is found).
- Each node card and the node detail panel show whether a meshnet pod is running on it, so an amber "2/3" badge tells you which node to look at.
- Any topology change (deploy, add, delete or scale) is blocked with a `412` when meshnet is missing, since it would leave pods unwired or stuck. Pass `?force=true` to proceed anyway. Clearing a topology and deleting the namespace are always allowed, so a full teardown never gets blocked.

To check by hand:

- Verify Meshnet is installed:

```bash
kubectl get daemonset -n kube-system | grep meshnet
```

- Verify Topology CRDs in namespace:

```bash
kubectl get topologies -n <namespace>
```

- Inspect pod events:

```bash
kubectl describe pod -n <namespace> <pod-name>
```

## A node never becomes Ready

The deploy ends with `timeout waiting for pods to be Ready: <pod> (running-not-ready, readiness probe failed: ...)`. A plain node has no probe and is Ready when its container runs, so this comes from a driver's probe and its output says what is missing. FRR, Open vSwitch and VyOS nodes have their own probe (the vty sockets, `ovs-vsctl`, SSH into the guest), so for them the message points at the daemon that is not up yet. `kubectl describe pod -n <namespace> <pod>` shows the same events.

## A node exits and the deploy is rolled back

The deploy fails with `pod <name> exited (Completed, exit code 0); a node's process has to keep running...` and `rolledBack: true`; a modify that recreates or adds such a node answers with the same message. The node's main process finished, so Kubernetes restarts it forever: `commands` that end (an install with nothing after it) or an image whose entrypoint runs once and exits. A node without `commands` runs its image with a tty and an open stdin, so a shell entrypoint (alpine, ubuntu) stays up by itself; with `commands` the last one has to stay in the foreground (`sleep infinity`, the daemon itself), and a one-shot image needs `commands` that do.

A process that exits with an error instead is a `node_crashed` warning in the response (`exited (Error, exit code 255) and restarted`), not a failure: an application may fail until what it depends on is up (the Open5GS functions exit until the NRF answers) and start fine on a later restart. If RESTARTS keeps climbing in `kubectl get pods -n <namespace>`, the node's logs say why it does not start.

## An OSPF area range on FRR shows an odd Link State ID

`show ip ospf database summary` on FRR routers lists the summary of an `ospf_add_area_range` with the last address of the block as Link State ID (`172.22.16.127` for `172.22.16.64/26`), while `show running-config` shows the range as written. The driver sets the range with its host bits on purpose: ospfd crashes when a range summary takes the Link State ID of a more specific summary the router already announced (RFC 2328 E.3, FRR 9.1 to 10.7.1), and an ID with the host bits set is what appendix E assigns to a colliding summary anyway. Receivers mask it, so the route is the block itself. The host bits protect the ABR that sets the range, not another ABR that already announced the covered subnets and has to re-summarize them when the aggregate arrives, so on FRR set the ranges in the same configuration as the network statements and never on a converged network with several ABRs.

## A burst of identical packets across workers loses its first ones

A link between two workers carries the lab's packets inside a VXLAN flow that leaves the pod through its cluster interface, so the worker's own conntrack (kube-proxy, the CNI) tracks it. The outer flow is keyed by the inner one, and when several identical packets of a new flow arrive within microseconds (a traceroute firing its probes at once, a ping flood's first packets), conntrack's clash resolution drops the simultaneous ones; everything after the first is fine. The trace tool spaces its probes for this reason. A shell traceroute from the toolbox wants `-N 1` or `-z 0.01`; the pod's own BusyBox traceroute probes one hop at a time and is unaffected.

## Router and switch pods stay in SysctlForbidden

`kubectl get pods` shows every router and switch of the namespace at `0/1 SysctlForbidden` and the deploy times out. Those pods set `net.ipv4.ip_forward` and `net.ipv6.conf.all.forwarding` through the pod security context, and the kubelet only applies sysctls it has been told to allow. Add both to `allowedUnsafeSysctls` on every node, as the [cluster guide](How-to-deploy-a-K8s-cluster.md) does for minikube, kind and kubeadm; its troubleshooting section shows how to add them to a cluster that is already running. An allowlist with only `net.ipv4.ip_forward` ran every version before 1.6.0, which added the IPv6 sysctl, so an older cluster needs the second entry.

## The deploy fails naming a link's MTU

The response says `KUBENDT_LINK_MTU=N does not fit link a:eth1 ↔ b:eth1: worker X carries at most M over VXLAN`, or the same for a link that declares its own `mtu`, and a deploy rolls back. The two pods landed on different workers, so the link is a VXLAN tunnel, and the value asked for is more than the worker's NIC carries minus the 50 bytes of the encapsulation. Lower the variable or the link's `mtu` to what the message names, or raise the underlay: section 11 of the [cluster guide](How-to-deploy-a-K8s-cluster.md) explains the default of 1450 and the jumbo path. When the default itself does not fit, the backend lowers it with a warning in the response instead of failing, so this only happens with a value you set.

## An action fails with "'X' is not in the pod's image"

The command behind the action needs a program the image does not ship and the toolbox does not carry, `vtysh` on an image that is not FRR for instance. The netns tools (`ip`, `bridge`, `tc`, `iptables`) never give this: when the image lacks them the action runs from the pod's toolbox. For anything else, add the program to the image or install it at startup through the node's `commands`.

## An action fails with "ospfd is not running"

The capability behind the action needs a daemon the FRR image starts only when its line in `/etc/frr/daemons` says `yes`, and the node was started without it (`ospfd=no` is the file's default). FRR does not start daemons on the fly, so the backend checks the daemon's vty socket before the actions of that capability and fails them with this message instead of a vtysh error. It happens on a node that sets its own `commands` without enabling the daemon: when a node sets none, `FRRRouterDriver` enables every daemon behind its capabilities at startup. Enable it in those `commands` (`sed -i 's/^ospfd=no/ospfd=yes/' /etc/frr/daemons` before FRR starts) or drop them, and restart or redeploy the node.

## A deploy warns `image_untested`

The node's image is not one its driver was tried with (`GET /drivers` lists them as `knownImages`). The deploy goes on: the warning says the platform checked nothing on that image, not that it fails. Use the image the driver names, or try the node and, if it works, add the reference to the driver's list in `backend/drivers/drivers/`.

## Backend changes not reflected during development

- Make sure you are running `go run .` directly and not a stale binary.
- If you are using a file watcher (e.g. `air`), ensure `.air.toml` points to the correct entry point.
- After adding or changing routes, regenerate Swagger docs:

```bash
go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g main.go
```

## CORS issues from frontend

- Check `CORS_ALLOWED_ORIGINS` matches frontend origin.
- In reverse-proxy same-origin deployments, CORS is usually unnecessary.

## Heal pass loops or failed pods

- Check pod logs:

```bash
kubectl logs -n <namespace> <pod-name>
```

- Verify driver registry endpoint:

```bash
curl http://localhost:8080/drivers
```

- Check Meshnet controller logs:

```bash
kubectl logs -n kube-system -l app=meshnet-controller
```

## QEMU-based nodes fail to start

- Verify KVM modules:

```bash
lsmod | grep kvm
```

- Verify device availability:

```bash
ls -la /dev/kvm /dev/net/tun
```

- Ensure privileged/security settings required by the image are present.
- Review platform-specific image docs under `deploy/custom_images/`.

## "exec timeout after Xs" during network-configure or modify

Symptom: backend logs show `⏱️ Exec TIMEOUT executor='...' pod='...' after 30s ...` and one or more actions (`replace_ip`, `enable_snat`, `ospf_*`, etc.) are reported as failed in the result dialog. Frequently the changes are visible inside the pod afterwards, the command just took longer than the deadline.

This is the global in-pod exec deadline. It bounds every single `kubectl exec` and every `ssh_qemu` into a QEMU guest. Since the consecutive Linux actions of a pod in a configure or a replay run as one exec, the deadline covers that whole script, not each action on its own. For QEMU-based pods, a single batched VyOS commit can legitimately take 10-20s the first time after the guest boots, when configd caches are cold.

- Default is **30 seconds**, dimensioned for first-boot VyOS commits with many subsystems touched (interfaces, NAT, OSPF, DNS, firewall).
- If your cluster is slow, the K8s API server is under pressure, or your guest images take longer to apply config, raise it via the `KUBECTL_EXEC_TIMEOUT_SECONDS` env var on the backend. Values of 45-60s are reasonable on shared lab clusters.
- The backend also logs `⚠️ Slow exec ...` when a command succeeds but eats more than 70% of the deadline. Watch those: if you see them frequently, raise the timeout before they start tripping.
- The SSH layer used by `ssh_qemu` detects dead sessions independently within ~14s (ConnectTimeout 5 + ServerAliveInterval 3 × ServerAliveCountMax 3), so raising this deadline does not delay real failure detection. It only gives more room to genuinely-long commands. Note that `ssh_qemu` multiplexes connections (ControlMaster/ControlPersist), so only the first command after a quiet period pays the SSH handshake.
