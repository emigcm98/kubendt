# Troubleshooting

## Pods missing network interfaces

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
- The SSH layer used by `ssh_qemu` detects dead sessions independently within ~11s (ConnectTimeout 5 + ServerAliveInterval 3 × ServerAliveCountMax 2), so raising this deadline does not delay real failure detection. It only gives more room to genuinely-long commands.
