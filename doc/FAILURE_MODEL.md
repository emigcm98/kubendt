# Failure model

What KubeNDT survives, what it loses, and what you have to fix by hand. The short version: the cluster is the source of truth for the topology and for everything KubeNDT knows about it (the histories, the layouts, the files, the users and tokens), the backend keeps nothing of its own, and nothing runs between operations, so the platform recovers from most of its own failures on the next request and does not notice the ones that happen behind its back.

## Where state lives

| State | Where | Survives backend restart | Survives losing the backend volume |
| --- | --- | --- | --- |
| Topology (nodes, links, IPs from the CRDs) | Kubernetes: StatefulSets, Meshnet Topology CRDs, ConfigMaps, Secrets | yes | yes |
| Configuration applied to nodes (routes, bridges, OSPF, NAT, qdiscs) | inside the pods, plus the pod's history in its namespace | yes | yes, it is in the cluster |
| Operation history (what replay uses) | one `kubendt-internal-history-<pod>` ConfigMap per pod in the namespace, the rows an older backend kept in SQLite `driver_operation_history` moved there on the first touch | yes | yes, it is in the cluster |
| Link attributes (the shaping, `mtu` and extra addresses a link declares) | the `kubendt/link-attrs` annotation on the Topology object of the pod the topology named, read once from SQLite `link_attributes` where an older backend left rows | yes | yes, it is in the cluster |
| Node positions and annotations of the graph | the `kubendt-internal-layout` ConfigMap of the namespace, mirrored into SQLite `node_positions` while a database is open, and `kubendt-internal-layout-<namespace>` in the `kubendt-system` namespace once the namespace is deleted keeping it | yes | yes, it is in the cluster |
| Files of the namespace file manager | one ConfigMap per file, a Secret when sensitive, in the namespace (`kubendt-file-*`, `kubendt-secret-file-*`), the same object the pods mount; what an older backend kept under `FILES_BASE_PATH` moves there on the first touch | yes | yes, they are in the cluster |
| `sensitive` flag of files | the kind of the file's object, a Secret | yes | yes |
| Link uids | read from the Topology objects of every namespace when a deploy needs one | nothing kept | nothing to lose |
| Operation locks | a `kubendt-internal-lock` Lease in the namespace, renewed while the operation runs | yes | taken over by the next operation once it expires, a minute at most |
| Admin password, API tokens | Secrets in the system namespace of the cluster in use (`kubendt-user-admin`, `kubendt-token-<id>`), what an older backend kept in SQLite moved there on the first bind | yes | yes, they are in the cluster |
| Browser sessions | a signed cookie, the key a Secret in the system namespace (`kubendt-internal-session-key`) | yes, every backend of the cluster accepts them | yes, the key is in the cluster |

SQLite is optional. When `KUBENDT_DB_PATH` names a file (`/data/kubendt.db` on the `backend-db` volume in compose) or a `kubendt.db` sits in the working directory, it is opened and what an older backend kept in it moves to the cluster as it is touched; a new install needs none.

## Backend failures

**Process restart** (crash, OOM, upgrade). Nothing is lost. A lock a dead backend left behind is a Lease that expires within a minute and the next operation takes it over, and the backend lists namespaces by the `kubendt/enabled` label and topologies from the CRDs, so it sees and operates whatever is deployed. A replacement backend on another machine, or the pod Kubernetes starts in its place, does the same as long as it reaches the cluster.

**Interrupted operation.** A deploy that dies half way leaves the resources it created in the cluster. The next deploy into that namespace is refused with `409 already has a deployed topology`, because the guard looks at the cluster. Recovery is one call: `DELETE /network/clear-topology/{namespace}` removes the StatefulSets, the Topology objects, the node ConfigMaps, the mounts of deleted files, the lock and the history of that namespace, keeps its files and its layout, and the deploy can be repeated. A deploy whose pods never become Ready rolls itself back the same way. A modify or a restart that dies mid way leaves the affected pods in whatever state Kubernetes left them, and the heal pass of the next modify, or a Restart of the pod, brings them back.

**Lost database or volume.** Nothing, once its rows have moved: the topology, the history the replay uses, the layout, the files, the admin password and the API tokens are objects of the cluster, and a replacement backend with the kubeconfig carries on with them, browser sessions included.

**Wrong cluster or credentials.** With no kubeconfig or no permissions the API answers errors and the dashboard shows the cluster as unreachable. Nothing is modified.

## Cluster and pod failures

**Pod recreated by KubeNDT** (the Restart action, a modify that restarts a peer, the heal pass). Handled. The link pass puts the MTU and the extra addresses of its links back, the pod's history is replayed in order, the neighbours' operations that touch the interface Meshnet recreated (bridge membership, addresses, routes, qdiscs) are re-applied, guest-VM neighbours get their TC redirect rewired, and a command that fails while a daemon is still starting is retried before the operation is given up on. Restart latency is bounded by the node's `terminationGracePeriodSeconds` (2 s by default) plus its image start and readiness time.

**Pod recreated behind KubeNDT's back** (`kubectl delete pod`, eviction, node failure). Not handled, by design. Nothing watches the cluster between operations. The pod comes back with the CRD-declared addresses only, at the MTU Meshnet's device was born with, and its neighbours keep interfaces that lost their driver-applied state. Fix: trigger a Restart of that pod through the API or the dashboard, which runs the full recovery above. If the recreated pod is stuck in `ContainerCreating` with `rename link kokoNNN -> ethX: file exists`, Meshnet failed to clean the peer end of a cross-worker link. Delete that interface in the peer pod (`ip link del ethX`) and the kubelet's next retry succeeds. The Restart action avoids this because it removes the peer ends before deleting the pod.

**Worker node down.** Pods on it are rescheduled by Kubernetes only after the node is marked unreachable, and they come back as recreated behind KubeNDT's back, see above. Pods pinned with `nodeName` to that worker stay Pending until it returns. Links to pods on other workers switch between veth and VXLAN if the placement changes, `GET /network/links` shows the current realization.

**Meshnet not running.** The dashboard shows it and the deploy is refused, since pods would come up with no links wired. A Meshnet pod restarting while a deploy is in progress shows up as missing interfaces, which the heal pass repairs by restarting the affected endpoints, at most two rounds.

## What stays manual

- Recovering a pod that Kubernetes recreated on its own: one Restart per pod through the API.
- A cross-worker peer interface left behind by an external recreation: `ip link del` in the peer pod.
- Anything applied inside a pod by hand (shell) is not in the history and is not replayed.
- Internal state of the workloads themselves (routing tables learnt by protocols, 5G sessions, database contents) is theirs to rebuild. Replay restores what KubeNDT applied, not what the processes derived from it.
- Backups. KubeNDT's state is in the cluster's etcd with everything else, so the cluster's backup is the one that covers it; there is no volume of KubeNDT's own to save.

## Control-plane resources

The backend and the UI run outside the twin's namespace: on the KubeNDT host, as containers of the compose stack, or as the two pods of `kubendt-system` when installed in the cluster. The per-namespace resource figures Kubernetes reports for a twin do not include them, so measure them apart (`docker stats` on the compose stack, the process on the host, or `kubectl top pods -n kubendt-system`) and report both numbers.
