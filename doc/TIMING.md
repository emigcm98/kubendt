# Operation timelines

Deploy, modify and restart responses carry a `timeline` block next to the usual `took_time`. It exists to answer one question: of the time an operation took, how much was KubeNDT and how much was Kubernetes and the node images underneath.

`took_time` is a coarse, human-readable summary and stays as it was. `timeline` is data for measurement.

## Shape

```json
"timeline": {
  "request_started_at": "2026-09-20T12:47:47.120Z",
  "backend_ms": { "prepare": 1180, "wait_ready": 5300, "replay": 40, "total": 6760 },
  "pods": [
    {
      "pod": "r1-0",
      "kubernetes": {
        "created": "2026-09-20T12:47:51Z",
        "scheduled": "2026-09-20T12:47:51Z",
        "sandbox_ready": "2026-09-20T12:47:52Z",
        "container_started": "2026-09-20T12:47:52Z",
        "ready": "2026-09-20T12:47:53Z"
      },
      "observed_ms": {
        "delete_issued": 1180,
        "old_pod_gone": 4210,
        "scheduled": 4300,
        "sandbox_ready": 5100,
        "container_started": 5900,
        "ready_seen": 6480
      }
    }
  ]
}
```

## Two clocks, two resolutions

`kubernetes.*` are copied from the Pod object exactly as Kubernetes wrote them (`metadata.creationTimestamp`, the `lastTransitionTime` of each condition, `containerStatuses[].state.running.startedAt`). They have 1 s resolution and come from the API server and kubelet clocks. They are the same values `kubectl get pod -o yaml` shows, which is the point: the backend does not time these phases, it reports what Kubernetes recorded.

`observed_ms` are milliseconds since `request_started_at` on the backend clock. They mark when the backend's pod watch delivered each transition. A step that had already happened when the backend started watching is omitted rather than stamped with a wrong time, so these fields can be missing, in particular `scheduled` for pods that were placed within the first milliseconds of a deploy.

Comparing `kubernetes.ready` with `observed_ms.ready_seen` gives the platform's detection lag and, with NTP in place, a sanity check that the two clocks agree.

## Per-pod phases

| Phase | From | To |
| --- | --- | --- |
| Termination of the previous pod | `observed_ms.delete_issued` | `observed_ms.old_pod_gone` |
| Scheduling | `kubernetes.created` | `kubernetes.scheduled` |
| Sandbox and CNI attachment (Meshnet wiring) | `kubernetes.scheduled` | `kubernetes.sandbox_ready` |
| Image pull and container start | `kubernetes.sandbox_ready` | `kubernetes.container_started` |
| Readiness (probe) | `kubernetes.container_started` | `kubernetes.ready` |
| Detection by KubeNDT | `kubernetes.ready` | `observed_ms.ready_seen` |

`sandbox_ready` is the `PodReadyToStartContainers` condition and needs Kubernetes 1.29 or newer. On older clusters the field is empty. `Initialized` is not used as a fallback because the kubelet sets it at scheduling time for pods without init containers, long before the sandbox exists.

One caveat on `sandbox_ready`. A condition's `lastTransitionTime` is the moment the kubelet first reported it, not the moment the sandbox became ready, while `container_started` comes from the container runtime. When a container starts within the same kubelet status sync as the sandbox, the two land in one status update: `observed_ms.sandbox_ready` and `observed_ms.container_started` are then identical, and with 1 s rounding `kubernetes.sandbox_ready` can even read one second after `kubernetes.container_started`. In that case the CNI attachment and the container start cannot be told apart, and the honest figure is `kubernetes.scheduled` to `kubernetes.container_started` for the two together.

The `delete_*` and `old_pod_gone` fields only appear for pods that were recreated (restart, and modify operations that restart a peer).

## Backend phases

`backend_ms` lists the phases the backend itself runs, in ms. Which ones appear depends on the operation:

| Field | Deploy | Modify | Restart | Meaning |
| --- | --- | --- | --- | --- |
| `validation` | yes |  |  | Input parsing, node and link checks, driver resolution |
| `resource_creation` | yes |  |  | Topology CRDs, ConfigMaps, StatefulSets created |
| `prepare` |  | yes | yes | Everything before the wait starts: topology updates, peer interface cleanup, delete calls |
| `wait_ready` | yes | yes | yes | From the wait start until every affected pod is Ready |
| `replay` |  | if pods restarted | yes | Operation history replayed on recreated pods |
| `heal` | yes | yes |  | Interface validation and healing after Ready |
| `total` | yes | yes | yes | Until the response is built |

## Phases overlap

Pods progress in parallel. In a deploy of ten nodes the ten timelines overlap almost completely, and in a modify that restarts two peers both terminate and come back at the same time. `backend_ms.total` is therefore a critical path, not the sum of the per-pod phases, and the backend phases themselves do not add up to `total` either (small steps such as the post-restart nudges are not broken out). Treat the block as timestamps to reason about, not as a stacked bar to sum.
