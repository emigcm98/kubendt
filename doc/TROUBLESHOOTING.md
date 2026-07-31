# Troubleshooting

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

## Reconciliation loops or failed pods

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

This is the global in-pod exec deadline. It bounds every single `kubectl exec` and every `ssh_qemu` into a QEMU guest. For QEMU-based pods, a single batched VyOS commit can legitimately take 10-20s the first time after the guest boots, when configd caches are cold.

- Default is **30 seconds**, dimensioned for first-boot VyOS commits with many subsystems touched (interfaces, NAT, OSPF, DNS, firewall).
- If your cluster is slow, the K8s API server is under pressure, or your guest images take longer to apply config, raise it via the `KUBECTL_EXEC_TIMEOUT_SECONDS` env var on the backend. Values of 45-60s are reasonable on shared lab clusters.
- The backend also logs `⚠️ Slow exec ...` when a command succeeds but eats more than 70% of the deadline. Watch those: if you see them frequently, raise the timeout before they start tripping.
- The SSH layer used by `ssh_qemu` detects dead sessions independently within ~14s (ConnectTimeout 5 + ServerAliveInterval 3 × ServerAliveCountMax 3), so raising this deadline does not delay real failure detection. It only gives more room to genuinely-long commands. Note that `ssh_qemu` multiplexes connections (ControlMaster/ControlPersist), so only the first command after a quiet period pays the SSH handshake.
