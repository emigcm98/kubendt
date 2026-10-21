# KubeNDT Architecture

This document complements the top-level README with technical details.

## Core Model

KubeNDT maps a topology model into Kubernetes resources:

- Nodes are deployed as StatefulSets.
- Links are represented as Meshnet Topology CRDs.
- Driver capabilities define runtime configuration actions.

## Execution Layers

1. API layer (`backend/handlers`): validates requests and orchestrates operations.
2. Domain/helper layer (`backend/helpers`): applies topology, the heal pass, and pod operations.
3. Driver layer (`backend/drivers` + `backend/capabilities`): translates actions into executor commands.
4. Kubernetes layer (`backend/kubeclient`): client-go access to resources.

## Authentication

Two middlewares run before any handler:

- `RequireAuth`: needs a valid session cookie or API token, else 401. Allowlist: `/healthz`, `/readyz`, `/version`, `/auth/*`.
- `RequireKubeconfig`: cluster operations return 503 until a kubeconfig is loaded.

Login checks the admin password (bcrypt) and issues an HttpOnly session cookie. Sessions are opaque tokens in SQLite, expire by idle/absolute timeout, and are cleared on restart. API tokens are long-lived bearer tokens for scripts, revocable and optionally expiring. Token management (create/revoke) needs a session or the password, not a token. Credential checks go through an `Authenticator` interface, so another backend such as LDAP can be added later.

## Drivers and Capabilities

How to write a driver, and every optional interface the platform asks of one, is in [DRIVERS.md](DRIVERS.md).

Drivers encode node behavior and can combine capability bases:

- `L2Capable`
- `L3Capable`
- `SwitchCapable`
- `NATCapable`
- `OSPFCapable`

Every action is declared once, next to its capability interface, in the `actions` registry: type, label, parameters (JSON name, kind, required) and the method that builds its commands. Dispatch looks the action up, validates the request against the declared parameters, checks that the driver implements the capability and runs it. `GET /drivers` lists the same declarations.

The configure request, the replay after a restart, the peer replay and the link attributes of the topology run actions through one pipeline (`PlanActions`, `RunPlannedActions`): plan every action, then execute in order, merging consecutive actions that share a guest commit into one batch.

Traffic control is not a driver capability. It runs on any pod through the pod's own `tc`, with an ephemeral toolbox container as fallback, so every node can be shaped. It and `custom` are universal actions, registered against any driver with their own executor.

A driver names the executor its actions run through: `kubectl` for Linux images, the guest's HTTP API for VyOS. A driver whose guest names interfaces differently from the pod maps them (`GuestInterfaceMapper`) before an action runs. A `custom` action may name its executor, to reach a guest shell for instance.

Addresses and routes are IPv4 or IPv6 alike. A link's two ends share a family (validated before deploy), Meshnet wires either from the Topology CRD, and the L3 builders pick the form from the address: `ip -6` for a v6 default route, the prefix itself for a static route. `remove_default_route` carries no address, so it takes `family` (`4` by default) and its v6 side is an optional interface next to the capability, `DefaultRoute6Remover`, which `L3Base` implements; a driver without it fails the action instead of deleting the v4 route. Router and switch pods set both forwarding sysctls (a new network namespace inherits the worker's v4 settings but not the v6 ones), and the interface listing reports `ipv6` next to `ipv4`. NAT is iptables and stays IPv4.

## Heal pass

After a deploy or a modify, the backend runs a bounded heal pass (`HealMissingInterfaces`) over the links the operation touched. It is a check, not the mechanism that wires links: a link added between two running pods is wired by recreating one of its endpoints in the modify's own restart phase, and the pass only confirms the result. It lists each pod's interfaces once (`ip -o link show`) and checks every declared interface against that, re-checks a miss a few times over three seconds in case the status has not settled, and only then restarts the endpoint that is missing its interface, waits for it, and replays its persisted operation history. At most two rounds; if interfaces are still missing after that, the operation reports the failure and leaves the pods as they are.

## MTU pass

Meshnet sets no MTU on the links it creates: a veth pair comes up at 1500 and a VXLAN device at the worker NIC's MTU minus the encapsulation (1450 on a 1500 underlay), so a lab's links would differ by placement, and a Linux bridge drops a frame larger than its egress port without any ICMP. After every wiring (deploy, modify, restart, the heal pass's own restarts) the backend therefore sets one MTU on every link of the namespace (`ApplyPlatformMTU`): `KUBENDT_LINK_MTU`, 1450 by default, written with `ip link set` on both pod ends, on the tap of a VM node, and inside the guest through the driver's `GuestMTUSetter` (VyOS by its API). It takes the heal pass's reads for the pods it listed (a pod the heal restarted, and its neighbours, are read again) and reads the rest itself, learns the VXLAN ceiling of each worker from the devices created since it started (the kernel reports no ceiling, it only refuses to go above it, and a refusal on an end it had no memory of is taken as the answer), and changes only what differs. A value the operator set that a worker cannot carry fails the operation naming the link and the worker; the default that does not fit is lowered with a warning. A link may declare its own `mtu` in the topology, or get one later through the `links` section of a configure request; it is kept with the link's attributes, checked against `KUBENDT_LINK_MTU` and the worker's ceiling, and the pass writes it on that link instead of the namespace value. The MTU is platform-owned: it is not part of the operation history and replay never touches it, the pass simply runs again on every recreation (which is also why `export-conf`, built from the history, does not carry it while the topology export does). A guest is asked before it is committed to, through the driver's interface inspector, so a VyOS pod booted with the value (its image takes `KUBENDT_LINK_MTU` from the pod environment the backend sets) costs one cached read and no commit. Its cost is at most one write per pod plus one read per guest, reported as the `mtu` phase of the operation timeline.

The same pass carries the addresses a link declares beyond the first on each end (`localIp` and `peerIp` take an array). Meshnet sets one address per end from the Topology CRD, so the rest are kept with the link's attributes and written in the same per-pod script with `ip addr replace`, idempotent, on every wiring; a VM guest gets them at boot through `KUBENDT_LINK_ADDRESSES` in its environment and the pass commits through the driver's `SetIP` only what the guest's inspector does not show. They are not operations of the history either, with one courtesy to it: an address the pod's history removed with `remove_ip` stays off until a later `set_ip` of it, whichever runs first after a restart, the pass or the replay. `GET /network/get-network` and the topology export echo the array; `export-conf` does not carry declared addresses, as it does not carry the MTU.

This is deliberately not a controller loop. Kubernetes readers may expect "reconciliation" to mean a process that watches the cluster and drives it toward a desired state indefinitely. KubeNDT does not do that: nothing runs between operations, the pass is triggered by the backend, does a fixed amount of work and returns. A wiring failure in Meshnet is a one-off event that follows an operation, not a drift that keeps happening, so a bounded repair right after the operation is enough and keeps the backend stateless with respect to the cluster. The two things that do reconcile continuously are Meshnet's own controller and the StatefulSet controller, and the backend nudges the former (annotation updates on Pod and Topology objects) when it wants a link re-evaluated without restarting anything.

The duration of the pass is reported as `timeline.backend_ms.heal`, and under the older `took_time.reconciliation` key that is kept for compatibility.

## Persistence

KubeNDT stores control-plane state in SQLite (var `KUBENDT_DB_PATH`) and per-namespace files on disk (var `FILES_BASE_PATH`).

SQLite tables:

- `node_positions`
- `namespace_state`
- `namespace_operations`
- `link_uid_registry`
- `driver_operation_history`
- `namespace_file_meta` — per-file flags (e.g. `sensitive`, which materialises a file as a Secret instead of a ConfigMap)
- `clusters` — registry of known clusters (see below)
- `auth_config`, `sessions`, `api_tokens`

Driver history is replayed when KubeNDT recreates a pod (restart, modify, heal pass), and the neighbours of that pod get the operations that touch the interface Meshnet recreated re-applied too, since a veth dies with the old netns and a VXLAN device is rebuilt on the next CNI ADD. Recreations that do not go through KubeNDT (a raw `kubectl delete pod`, an eviction) are not replayed. The history is also pruned of operations the current driver no longer supports.

A history row is keyed by cluster, namespace and pod name, and `executed_at` says when that payload was last applied: re-applying an action replaces its row, and a successful replay refreshes the timestamp. Configure skips an action as a duplicate only if a matching row is newer than the target pod's creation, so rows left by an earlier incarnation never mask a missing configuration. Rows older than the Namespace object itself are dropped at deploy time and on namespace creation, since they can only come from a same-named namespace that was deleted outside KubeNDT.

### Cluster scoping

KubeNDT can manage more than one cluster (context switching via `/kube/context` and `/kube/config`), and different clusters routinely reuse the same namespace names (e.g. `prueba` in two clusters). To keep that data from colliding, all per-namespace state is scoped by a **canonical cluster ID**: the `metadata.uid` of the cluster's `kube-system` namespace. That UID is created at cluster bootstrap, never changes, and is unique per cluster — the same convention used by OpenTelemetry (`k8s.cluster.uid`) and kube-state-metrics. It is resolved and cached per active context (`kubeclient.CurrentClusterID`) and re-resolved whenever the active client changes.

Concretely:

- Every per-namespace table above carries a `cluster_id` column and is keyed by `(cluster_id, namespace, …)`; queries filter by the active cluster's ID.
- Namespace files live under `FILES_BASE_PATH/<cluster_id>/<namespace>/…`.
- Within a cluster, the namespace **name** remains the identity (KubeNDT cleans up a namespace's data when it is deleted through the UI).

Cluster IDs are opaque UUIDs, so a registry maps them back to something readable for operators: the `clusters` table and a mirrored `FILES_BASE_PATH/clusters.json` map each `cluster_id` to its last-seen context name, API server URL, and timestamp. This is written on startup and on every context switch/load (`helpers.RecordActiveCluster`). It is purely for operability — the cluster ID stays the canonical key everywhere else.

### Schema versioning

The schema version is tracked in the SQLite file via `PRAGMA user_version`. `applyMigrations` (in `backend/database/database.go`) runs any pending migration steps in order and stamps the current version; new databases are created at the latest schema directly by `createTables`. The first released schema is version `1`, so there is intentionally no migration below it. Future schema changes bump `schemaVersion` and add a guarded step to `applyMigrations`.

## Runtime Types

- Linux-based runtime: direct container execution.
- QEMU-based runtime: VM-in-pod workflows for network operating systems requiring guest-level control.

For extended design and flow diagrams, see:

- [doc/ARCHITECTURE_FLOWS.md](ARCHITECTURE_FLOWS.md)
- [doc/IMPLEMENTATION_SUMMARY.md](IMPLEMENTATION_SUMMARY.md)
- [doc/IMPLEMENTATION_DETAILS.md](IMPLEMENTATION_DETAILS.md)
