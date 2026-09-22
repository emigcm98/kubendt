# Experiments

Scripts that measure KubeNDT through its public API and `kubectl`. They produced the figures in the paper's evaluation and they are the way to reproduce them, or to re-measure after a change. They need a real cluster and a running backend, so they are not part of CI.

## Prerequisites

- Python 3.10 or newer, standard library only.
- `kubectl` pointing at the cluster the backend manages (`KUBECTL_CONTEXT` selects a context). metrics-server for the footprint and telemetry scripts, two workers for the placement ones, KVM on a worker for the VM ones.
- A backend. `KUBENDT_URL` (default `http://localhost:8080`) plus `KUBENDT_PASSWORD` or `KUBENDT_TOKEN`, or a backend started with `KUBENDT_AUTH_DISABLED=true`.
- Images pulled on every worker before timing anything. `deploy_scaling.py` does one unrecorded warmup deploy for that.

Every script takes `--runs`, `--out`, `--keep` and `--help`. Namespaces are named `exp-*` and removed at the end unless `--keep`.

## Scripts

| Script | What it measures | Main knobs |
| --- | --- | --- |
| `regression.py` | The six `deploy/examples/` end to end, as their READMEs describe, with a restart per example to exercise replay. Release gate. | example numbers, `KEEP`, `SKIP_RAW` |
| `deploy_scaling.py` | Deploy time by topology size, with the per-pod timeline: creation and readiness spread, critical path, platform overhead over the Kubernetes path, affine fit. Also times a fixed topology (`--topology`, `--configure`). | `--sizes`, `--shape`, `--image` |
| `configure_throughput.py` | One action to every pod at once: wall time, sequential-equivalent time, speedup, per-pod exec time. | `--sizes` |
| `modify_vs_redeploy.py` | Each in-place operation (add/delete node, add/delete link, scale up/down, restart) against clear + deploy: time, pods recreated, unavailability per pod, loss on a link nobody touched. | `--nodes`, `--grace` |
| `restart_latency.py` | One restart split into preparation, termination, scheduling, sandbox and CNI, container start, readiness, detection, replay of the pod's history, re-application on its neighbours. | `--grace`, `--depth` |
| `replay_verify.py` | Snapshot, restart, snapshot, diff: addresses, routes, FRR config, NAT, bridge ports, qdiscs, on the restarted pod and its neighbours, with pings across routers before and after. Then replay time against history depth with mixed actions. | `--targets`, `--depth`, `--vyos-image` |
| `placement_fidelity.py` | Same-worker (veth) against cross-worker (VXLAN) link: MTU, largest DF packet, RTT, loss, TCP throughput and retransmissions, UDP loss and jitter, pod CPU. `--vyos-image` adds the QEMU/TAP path. | `--workers`, `--udp-rate` |
| `tc_fidelity.py` | netem delay and loss and tbf rate as configured against as measured, with the qdisc read back through the API and a stated tolerance. | `--delays`, `--losses`, `--rates`, `--cross` |
| `vm_scaling.py` | VyOS VMs against FRR containers, 1 to N routers: deploy time split by phase, readiness (guest boot) and footprint. | `--sizes`, `--node-selector` |
| `footprint.py` | Steady-state CPU and memory per pod and per namespace from the same samples, plus the backend process or container and the in-cluster helpers. Deploys a topology first when asked, importing a zip of mounts before it. | `--namespace`, `--topology`, `--configure`, `--zip`, `--backend-pid` |
| `telemetry_overhead.py` | Latency of the endpoints the dashboard polls and backend CPU and memory under N concurrent pollers. | `--namespace`, `--clients`, `--period` |
| `inventory.py` | Versions, node resources, image digests, compressed and unpacked sizes, entrypoints and readiness probes for a set of topologies or a namespace. | `--topologies`, `--namespace` |
| `gen_topology.py` | The synthetic topologies the scripts use (sparse, ring, line), importable and as a CLI. | `--shape`, `--sizes` |

`common.py` holds the API client, the kubectl helpers, the timeline math and the result recorder.

## Output

Each run writes `results/<script>/<timestamp>/` with `meta.json` (backend version, repository commit, cluster nodes, arguments with any password or token redacted), `rows.jsonl` appended as the run goes, `rows.csv` and `summary.json` at the end, `notes.log`, and the raw API responses the run relied on. `results/` is ignored by git. The data behind the paper lives in its artifacts repository, not here.

## Reading the numbers

- Restarts always go through `PATCH /pods/restart`. A raw `kubectl delete pod` does not replay, by design.
- The timeline mixes two clocks, Kubernetes stamps at 1 s resolution and backend milliseconds. `doc/TIMING.md` explains the fields and why phases are a critical path, not a sum.
- Ready means something different per node type. For a plain container it is the image up, for VyOS the guest's HTTP API answering, for FRR its daemons answering, for OVS `ovsdb-server` up. Compare readiness figures with that in mind.
- The default host image is `alpine:3.24.2` running `sleep infinity`, chosen because it is Ready as soon as it runs and other platforms can run the same thing.
