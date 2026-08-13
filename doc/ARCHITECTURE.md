# KubeNDT Architecture

This document complements the top-level README with technical details.

## Core Model

KubeNDT maps a topology model into Kubernetes resources:

- Nodes are deployed as StatefulSets.
- Links are represented as Meshnet Topology CRDs.
- Driver capabilities define runtime configuration actions.

## Execution Layers

1. API layer (`backend/handlers`): validates requests and orchestrates operations.
2. Domain/helper layer (`backend/helpers`): applies topology, reconciliation, and pod operations.
3. Driver layer (`backend/drivers` + `backend/capabilities`): translates actions into executor commands.
4. Kubernetes layer (`backend/kubeclient`): client-go access to resources.

## Authentication

Two middlewares run before any handler:

- `RequireAuth`: needs a valid session cookie or API token, else 401. Allowlist: `/healthz`, `/readyz`, `/version`, `/auth/*`.
- `RequireKubeconfig`: cluster operations return 503 until a kubeconfig is loaded.

Login checks the admin password (bcrypt) and issues an HttpOnly session cookie. Sessions are opaque tokens in SQLite, expire by idle/absolute timeout, and are cleared on restart. API tokens are long-lived bearer tokens for scripts, revocable and optionally expiring. Token management (create/revoke) needs a session or the password, not a token. Credential checks go through an `Authenticator` interface, so another backend such as LDAP can be added later.

## Drivers and Capabilities

Drivers encode node behavior and can combine capability bases:

- `L2Capable`
- `L3Capable`
- `SwitchCapable`
- `NATCapable`
- `OSPFCapable`

Traffic control is not a driver capability. It runs on any pod through the pod's own `tc`, with an ephemeral toolbox container as fallback, so every node can be shaped.

The same action type can use different executors depending on driver/runtime constraints (for example guest CLI wrappers in QEMU-based platforms).

## Reconciliation

Reconciliation keeps desired and observed state aligned by:

- checking pod readiness,
- validating link/interface state,
- nudging topology/pod reconcile loops,
- replaying persisted driver operations after restart when applicable.

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

Driver history is used for restart-time replay and cleanup of obsolete operations.

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
