# Failure model

What KubeNDT survives, what it loses, and what you have to fix by hand. The short version: the cluster is the source of truth for the topology, the backend keeps only what the cluster cannot hold (operation history, node positions, file flags, sessions), and nothing runs between operations, so the platform recovers from most of its own failures on the next request and does not notice the ones that happen behind its back.

## Where state lives

| State | Where | Survives backend restart | Survives losing the backend volume |
| --- | --- | --- | --- |
| Topology (nodes, links, IPs from the CRDs) | Kubernetes: StatefulSets, Meshnet Topology CRDs, ConfigMaps, Secrets | yes | yes |
| Configuration applied to nodes (routes, bridges, OSPF, NAT, qdiscs) | inside the pods, plus one history row per applied action in SQLite | yes | the pods keep it, the history is gone |
| Operation history (what replay uses) | SQLite `driver_operation_history` | yes | no |
| Node positions in the graph | SQLite `node_positions` | yes | no, the graph re-lays out |
| Files of the namespace file manager | disk under `FILES_BASE_PATH` (`kubendt-files` volume in compose) | yes | no, mounted ConfigMaps and Secrets stay in the cluster and the file manager flags them as missing their source |
| `sensitive` flag of files | SQLite `namespace_file_meta` | yes | no |
| Link UID registry | SQLite `link_uid_registry`, rebuilt from the CRDs on every sync | yes | re-synced |
| Operation locks | SQLite `namespace_operations` | wiped at startup on purpose | wiped |
| Admin password, sessions, API tokens | SQLite `auth_config`, `sessions`, `api_tokens` | yes | no, the password comes back from `KUBENDT_ADMIN_PASSWORD` or is generated and printed once, tokens must be recreated |

SQLite lives at `KUBENDT_DB_PATH` (`./kubendt.db` by default, `/data/kubendt.db` on the `backend-db` volume in compose). It is a single local file, not replicated. Rows are scoped by cluster id, so one backend can serve several clusters without mixing their state, and a backend pointed at a cluster it has never seen simply starts with an empty history for it.

## Backend failures

**Process restart** (crash, OOM, upgrade). Nothing is lost. At startup the backend wipes every operation lock, since no operation can be in flight in a freshly started process, and lists namespaces by the `kubendt/enabled` label and topologies from the CRDs, so it sees and operates whatever is deployed. A replacement backend on another machine does the same as long as it has the kubeconfig. It only lacks what its own database never had, see the table.

**Interrupted operation.** A deploy that dies half way leaves the resources it created in the cluster. The next deploy into that namespace is refused with `409 already has a deployed topology`, because the guard looks at the CRDs, not at the database. Recovery is one call: `DELETE /network/clear-topology/{namespace}` removes StatefulSets, Topology CRDs, ConfigMaps, Secrets, the UID registry and the history for that namespace, and the deploy can be repeated. A deploy whose pods never become Ready rolls itself back the same way. A modify or a restart that dies mid way leaves the affected pods in whatever state Kubernetes left them, and the heal pass of the next modify, or a Restart of the pod, brings them back.

**Lost database or volume.** The topology keeps running untouched. What disappears is the ability to replay: a pod recreated afterwards comes back with the addresses declared in the CRDs and nothing else, until the configuration is applied again through the API. Node positions reset, `sensitive` flags are forgotten (already materialised Secrets stay Secrets), file-manager files are gone while their ConfigMaps remain, and API tokens have to be recreated. Back up the two volumes if the history matters.

**Wrong cluster or credentials.** With no kubeconfig or no permissions the API answers errors and the dashboard shows the cluster as unreachable. Nothing is modified.

## Cluster and pod failures

**Pod recreated by KubeNDT** (the Restart action, a modify that restarts a peer, the heal pass). Handled. The pod's history is replayed in order, the neighbours' operations that touch the interface Meshnet recreated (bridge membership, addresses, routes, qdiscs) are re-applied, guest-VM neighbours get their TC redirect rewired, and a command that fails while a daemon is still starting is retried before the operation is given up on. Restart latency is bounded by the node's `terminationGracePeriodSeconds` (2 s by default) plus its image start and readiness time.

**Pod recreated behind KubeNDT's back** (`kubectl delete pod`, eviction, node failure). Not handled, by design. Nothing watches the cluster between operations. The pod comes back with the CRD-declared addresses only, and its neighbours keep interfaces that lost their driver-applied state. Fix: trigger a Restart of that pod through the API or the dashboard, which runs the full recovery above. If the recreated pod is stuck in `ContainerCreating` with `rename link kokoNNN -> ethX: file exists`, Meshnet failed to clean the peer end of a cross-worker link. Delete that interface in the peer pod (`ip link del ethX`) and the kubelet's next retry succeeds. The Restart action avoids this because it removes the peer ends before deleting the pod.

**Worker node down.** Pods on it are rescheduled by Kubernetes only after the node is marked unreachable, and they come back as recreated behind KubeNDT's back, see above. Pods pinned with `nodeName` to that worker stay Pending until it returns. Links to pods on other workers switch between veth and VXLAN if the placement changes, `GET /network/links` shows the current realization.

**Meshnet not running.** The dashboard shows it and the deploy is refused, since pods would come up with no links wired. A Meshnet pod restarting while a deploy is in progress shows up as missing interfaces, which the heal pass repairs by restarting the affected endpoints, at most two rounds.

## What stays manual

- Recovering a pod that Kubernetes recreated on its own: one Restart per pod through the API.
- A cross-worker peer interface left behind by an external recreation: `ip link del` in the peer pod.
- Anything applied inside a pod by hand (shell) is not in the history and is not replayed.
- Internal state of the workloads themselves (routing tables learnt by protocols, 5G sessions, database contents) is theirs to rebuild. Replay restores what KubeNDT applied, not what the processes derived from it.
- Backups of the two volumes. KubeNDT does not replicate its database.

## Control-plane resources

The backend, the frontend and the database run outside the twin's namespace, on the KubeNDT host or as containers of the compose stack, so the per-namespace resource figures Kubernetes reports for a twin do not include them. Measure them separately (`docker stats` on the compose stack, or the process on the host) and report both numbers.
