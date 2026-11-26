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

Every command a node runs goes through a pod exec, a round trip to the API server and the kubelet. The transport is WebSocket, what kubectl uses since Kubernetes 1.29 (on by default in the API server from 1.30, GA in 1.31), with SPDY as the fallback when a server or a proxy cannot upgrade, remembered after the first refusal so later execs do not pay the attempt (`KUBENDT_EXEC_TRANSPORT=spdy` skips it); measured on dev, an exec costs about 240 ms over WebSocket against 360 to 480 over SPDY.

## Authentication

Two middlewares run before any handler:

- `RequireAuth`: needs a valid session cookie or API token, else 401. Allowlist: `/healthz`, `/readyz`, `/version`, `/auth/*`.
- `RequireKubeconfig`: cluster operations return 503 while the backend has no cluster, neither a kubeconfig nor a ServiceAccount.

Login checks the admin password (bcrypt) and issues an HttpOnly session cookie. Sessions are opaque tokens in SQLite, expire by idle/absolute timeout, and are cleared on restart. API tokens are long-lived bearer tokens for scripts, revocable and optionally expiring. Token management (create/revoke) needs a session or the password, not a token. Credential checks go through an `Authenticator` interface, so another backend such as LDAP can be added later.

## Drivers and Capabilities

How to write a driver, and every optional interface the platform asks of one, is in [DRIVERS.md](DRIVERS.md).

Drivers encode node behavior and can combine capability bases:

- `L2Capable`
- `L3Capable`
- `SwitchCapable`
- `VLANCapable`
- `SubinterfaceCapable`
- `STPCapable`
- `OpenFlowCapable`
- `NATCapable`
- `OSPFCapable`

Every action is declared once, next to its capability interface, in the `actions` registry: type, label, parameters (JSON name, kind, required), the method that builds its commands, and the state it sets or clears on the node with the key that tells two pieces of it apart (an address, a default route per family, a static route by prefix and next hop, the link state of an interface). Dispatch looks the action up, validates the request against the declared parameters, checks that the driver implements the capability and runs it. `GET /drivers` lists the same declarations.

The configure request, the replay after a restart, the peer replay and the link attributes of the topology run actions through one pipeline (`PlanActions`, `RunPlannedActions`): plan every action, then execute in order, merging consecutive actions that share a guest commit into one batch, and consecutive Linux actions of a pod into one shell script with a marker per action, so a configure or a replay is one exec per pod and still reports each action on its own; where the driver declares a precondition for a capability (FRR checks the daemon's vty socket), the batch checks it before the action and fails it with the driver's message.

Traffic control is not a driver capability. It runs on any pod through the pod's own `tc`, with an ephemeral toolbox container as fallback, so every node can be shaped. The same fallback serves every netns tool: a kubectl action, the heal and MTU passes and the interface reads run in the pod's main container when its image ships `ip`, `bridge` or `iptables`, and in the toolbox when not, decided once per pod and tool at first contact (`helpers.NetnsContainer`); the toolbox shares the network namespace, so the outcome is the same, and a node is Ready as soon as its container runs. It and `custom` are universal actions, registered against any driver with their own executor.

A driver names the executor its actions run through: `kubectl` for Linux images, the guest's HTTP API for VyOS. A driver whose guest names interfaces differently from the pod maps them (`GuestInterfaceMapper`) before an action runs. A `custom` action may name its executor, to reach a guest shell for instance.

Addresses and routes are IPv4 or IPv6 alike. A link's two ends share a family (validated before deploy), Meshnet wires either from the Topology CRD, and the L3 builders pick the form from the address: `ip -6` for a v6 default route, the prefix itself for a static route. `remove_default_route` carries no address, so it takes `family` (`4` by default) and its v6 side is an optional interface next to the capability, `DefaultRoute6Remover`, which `L3Base` implements; a driver without it fails the action instead of deleting the v4 route. Router and switch pods set both forwarding sysctls (a new network namespace inherits the worker's v4 settings but not the v6 ones), and the interface listing reports `ipv6` next to `ipv4`. NAT is iptables and stays IPv4. On an FRR router the static and default routes go through staticd rather than `ip route`, so they are in its running-config and `redistribute static` sees them, while addresses stay on `ip` and zebra takes them as connected; the pod's own default route through `eth0` is dropped when a default route is set or removed, since it would win on metric otherwise.

Reads are inspections, not actions: each capability declares the live reads a driver can offer (routes, neighbours, protocol state) with their columns, served by `GET /pods/inspect/{namespace}/{pod}` and listed by `GET /drivers`; nothing of them touches the history.

## Heal pass

After a deploy or a modify, the backend runs a bounded heal pass (`HealMissingInterfaces`) over the links the operation touched. It is a check, not the mechanism that wires links: a link added between two running pods is wired by recreating one of its endpoints in the modify's own restart phase, and the pass only confirms the result. It lists each pod's interfaces once (`ip -o link show`) and checks every declared interface against that, re-checks a miss a few times over three seconds in case the status has not settled, and only then restarts the endpoint that is missing its interface, waits for it, and replays its persisted operation history. At most two rounds; if interfaces are still missing after that, the operation reports the failure and leaves the pods as they are.

## MTU pass

Meshnet sets no MTU on the links it creates: a veth pair comes up at 1500 and a VXLAN device at the worker NIC's MTU minus the encapsulation (1450 on a 1500 underlay), so a lab's links would differ by placement, and a Linux bridge drops a frame larger than its egress port without any ICMP. After every wiring (deploy, modify, restart, the heal pass's own restarts) the backend therefore sets one MTU on every link of the namespace (`ApplyPlatformMTU`): `KUBENDT_LINK_MTU`, 1450 by default, written with `ip link set` on both pod ends, on the tap of a VM node, and inside the guest through the driver's `GuestMTUSetter` (VyOS by its API). It takes the heal pass's reads for the pods it listed (a pod the heal restarted, and its neighbours, are read again) and reads the rest itself, learns the VXLAN ceiling of each worker from the devices created since it started (the kernel reports no ceiling, it only refuses to go above it, and a refusal on an end it had no memory of is taken as the answer), and changes only what differs. A value the operator set that a worker cannot carry fails the operation naming the link and the worker; the default that does not fit is lowered with a warning. A link may declare its own `mtu` in the topology, or get one later through the `links` section of a configure request; it is kept with the link's attributes, checked against `KUBENDT_LINK_MTU` and the worker's ceiling, and the pass writes it on that link instead of the namespace value. The MTU is platform-owned: it is not part of the operation history and replay never touches it, the pass simply runs again on every recreation (which is also why `export-conf`, built from the history, does not carry it while the topology export does). A guest is asked before it is committed to, through the driver's interface inspector, so a VyOS pod booted with the value (its image takes `KUBENDT_LINK_MTU` from the pod environment the backend sets) costs one cached read and no commit. Its cost is at most one write per pod plus one read per guest, reported as the `mtu` phase of the operation timeline.

The same pass carries the addresses a link declares beyond the first on each end (`localIp` and `peerIp` take an array). Meshnet sets one address per end from the Topology CRD, so the rest are kept with the link's attributes and written in the same per-pod script with `ip addr replace`, idempotent, on every wiring; a VM guest gets them at boot through `KUBENDT_LINK_ADDRESSES` in its environment and the pass commits through the driver's `SetIP` only what the guest's inspector does not show. They are not operations of the history either, and the pass runs before the history is replayed on a restart or a modify, so a replayed route through one of them finds it in place; with one courtesy to the history, an address the pod's history removed with `remove_ip` stays off until a later `set_ip` of it. `GET /network/get-network` and the topology export echo the array; `export-conf` does not carry declared addresses, as it does not carry the MTU.

This is deliberately not a controller loop. Kubernetes readers may expect "reconciliation" to mean a process that watches the cluster and drives it toward a desired state indefinitely. KubeNDT does not do that: nothing runs between operations, the pass is triggered by the backend, does a fixed amount of work and returns. A wiring failure in Meshnet is a one-off event that follows an operation, not a drift that keeps happening, so a bounded repair right after the operation is enough and keeps the backend stateless with respect to the cluster. The two things that do reconcile continuously are Meshnet's own controller and the StatefulSet controller, and the backend nudges the former (annotation updates on Pod and Topology objects) when it wants a link re-evaluated without restarting anything.

The duration of the pass is reported as `timeline.backend_ms.heal`, and under the older `took_time.reconciliation` key that is kept for compatibility.

## Persistence

KubeNDT keeps its state in the cluster: the files of a namespace are ConfigMaps and Secrets in it (see `doc/FILE_MANAGER.md`), the rest is described below. SQLite (`KUBENDT_DB_PATH`) is optional and read to move what an older backend kept in it; `FILES_BASE_PATH` likewise only names the disk the files were on.

SQLite tables, where a database is open:

- `node_positions` — a mirror of the layout, which lives in the namespace as the `kubendt-internal-layout` ConfigMap (`GET` and `PUT /network/layout/:namespace`, the positions endpoints on the same document)
- `driver_operation_history` — the operations an older backend kept, read once when a namespace's history moves into the cluster
- `namespace_file_meta` — the `sensitive` flags an older backend kept, read once when its files move into the cluster
- `clusters` — registry of known clusters (see below)
- `auth_config`, `api_tokens`, `sessions` — the admin hash and the tokens an older backend kept, moved to the system namespace when the backend first binds to a cluster, the sessions dropped

The rest of the control-plane state lives in the cluster. The operation lock of a namespace is a Lease named `kubendt-internal-lock` in it, created to acquire, renewed while the operation runs, deleted to release and taken over once it expires, so two backends can share a cluster. Whether a namespace has a topology is read from its StatefulSets and Topology objects, and the link uids in use from the Topology objects of every namespace. The attributes a topology declares on a link (the shaping, the MTU, the addresses beyond the first) are the `kubendt/link-attrs` annotation of the Topology object of the pod the topology named, keyed by interface, so they die with the object; rows an older backend left in SQLite move there the first time the namespace is touched. The system namespace (`KUBENDT_SYSTEM_NAMESPACE`, `kubendt-system` by default, created at start) holds what belongs to no lab: the admin's bcrypt hash and the moment before which no session counts (`kubendt-user-admin`), one Secret per API token (`kubendt-token-<id>`, labelled `kubendt/kind=token`, cached in the backend and re-listed when an unknown token shows up), the key the sessions are signed with (`kubendt-internal-session-key`, made once per cluster so every backend accepts the others' cookies), and a namespace deleted keeping its layout leaves it there as `kubendt-internal-layout-<namespace>`, for a namespace created with that name to take back. The first cluster the backend connects to is its home cluster and keeps these; a context switch later does not move them. A browser session is the cookie itself, signed claims with who, when it was issued and when it was last seen, refreshed every few minutes while in use; nothing of it is kept on the server, and a password change or an explicit invalidation ends them all by the stored moment. Without a cluster the backend signs with a key of its own and the admin password is the environment's or a generated one, until a kubeconfig is loaded.

All of it is on view: `GET /state` lists every object KubeNDT creates or uses in each lab namespace and in the system one, `GET /state/{namespace}` one namespace, each object told for what it is to the platform (a node, a pod, its links, a file, a history, the layout, the lock, a user, a token), and `GET /state/{namespace}/{kind}/{name}` returns one as a document and as YAML, without managed fields and with a Secret's values replaced by their sizes. The page behind it, reached from Home and from a namespace, is `kubectl get` from the UI and documents by itself what KubeNDT puts in a cluster.

The operations applied to a pod are its history, one ConfigMap in the namespace named `kubendt-internal-history-<pod>` (`kubendt/kind=history`) holding an ordered JSON list, each entry with an id that is the moment it was applied in microseconds. A batch (a configure request, a replay, the link attributes of a wiring) opens the pod's history, changes it in memory and writes it once, retried on what another writer left when the object changed meanwhile, so a request of many actions costs one write per pod and any backend sees the same history. A pod with nothing applied has no object.

Driver history is replayed when KubeNDT recreates a pod (restart, modify, heal pass), and the neighbours of that pod get the operations that touch the interface Meshnet recreated re-applied too, since a veth dies with the old netns and a VXLAN device is rebuilt on the next CNI ADD. Recreations that do not go through KubeNDT (a raw `kubectl delete pod`, an eviction) are not replayed. The history is also pruned of operations the current driver no longer supports.

An entry's `executed_at` says when that payload was last applied: re-applying an action replaces its entry, and a successful replay refreshes the stamp. Configure skips an action as a duplicate only if a matching entry is newer than the target pod's creation and nothing on the same state came after it, so entries left by an earlier incarnation never mask a missing configuration and a set, its undo and the same set again all run. Entries older than the Namespace object itself are dropped at deploy time, since they can only come from a same-named namespace that was deleted outside KubeNDT.

What a node carries falls into three kinds. The topology's own: the first address of each link end, which Meshnet wires, the extra addresses and the MTU, which the pass writes after every wiring from the link's attributes; none of it is an operation. Operations: everything a configure request applies, persisted by default and replayed on every recreation, the shaping declared on links included. And one-offs: an action sent with `options.persist_history: false`, run once and forgotten, which is how the dashboard toggles an interface up and down. Among the operations the history keeps the last word on each piece of state: saving an action drops the earlier entries on the same state and key, and an undo is kept only where the platform or the cluster would put the state back by itself (`remove_ip`, since the link or the pass restores the address; `remove_default_route`, since the cluster gives the pod one; `link_down`, since an interface comes up on its own); elsewhere an undo and the action it undoes cancel each other and neither stays, as with `del_qdisc`, a static route removed or a bridge torn down. An action may also take other states down with its own (`ClearsAlso`), on its own key when it has one and on every key otherwise: an interface leaving IS-IS takes its passive flag and metric, stopping BGP or IS-IS removes the entries of everything under the process, which the router has dropped with it. `custom` is the exception that runs every time it is asked for and is never a duplicate.

### Cluster scoping

A backend on a kubeconfig can switch between the clusters of its contexts (`POST /kube/context`, the owner's, each cluster being its own KubeNDT with its own accounts, see [DEPLOYMENT.md](DEPLOYMENT.md#in-the-cluster-or-outside-it)), and different clusters routinely reuse the same namespace names (e.g. `prueba` in two clusters). To keep that data from colliding, all per-namespace state is scoped by a **canonical cluster ID**: the `metadata.uid` of the cluster's `kube-system` namespace. That UID is created at cluster bootstrap, never changes, and is unique per cluster — the same convention used by OpenTelemetry (`k8s.cluster.uid`) and kube-state-metrics. It is resolved and cached per active context (`kubeclient.CurrentClusterID`) and re-resolved whenever the active client changes.

Concretely:

- Every per-namespace table above carries a `cluster_id` column and is keyed by `(cluster_id, namespace, …)`; queries filter by the active cluster's ID.
- Namespace files live in the namespace itself, as ConfigMaps and Secrets. A backend older than 1.12 kept them under `FILES_BASE_PATH/<cluster_id>/<namespace>/…`, which is read once to move them.
- Within a cluster, the namespace **name** remains the identity (KubeNDT cleans up a namespace's data when it is deleted through the UI).

Cluster IDs are opaque UUIDs, so a registry maps them back to something readable for operators: the `clusters` table and a mirrored `FILES_BASE_PATH/clusters.json` map each `cluster_id` to its last-seen context name, API server URL, and timestamp. This is written on startup and on every context switch (`helpers.RecordActiveCluster`), only while a database from an older backend is open. It is purely for operability — the cluster ID stays the canonical key everywhere else.

### Schema versioning

The schema version is tracked in the SQLite file via `PRAGMA user_version`. `applyMigrations` (in `backend/database/database.go`) runs any pending migration steps in order and stamps the current version; new databases are created at the latest schema directly by `createTables`. The first released schema is version `1`, so there is intentionally no migration below it. Future schema changes bump `schemaVersion` and add a guarded step to `applyMigrations`.

## Runtime Types

- Linux-based runtime: direct container execution.
- QEMU-based runtime: VM-in-pod workflows for network operating systems requiring guest-level control.

For extended design and flow diagrams, see:

- [doc/ARCHITECTURE_FLOWS.md](ARCHITECTURE_FLOWS.md)
- [doc/IMPLEMENTATION_SUMMARY.md](IMPLEMENTATION_SUMMARY.md)
- [doc/IMPLEMENTATION_DETAILS.md](IMPLEMENTATION_DETAILS.md)
