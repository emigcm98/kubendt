# Deployment and Configuration

## In the cluster or outside it

The backend runs either inside the cluster it manages or outside it, and that decides how it reaches the cluster and what it serves. The cluster page says which, and so does `GET /kube/config` in its `mode`.

| | In the cluster (`in-cluster`) | Outside it (`kubeconfig`) |
| --- | --- | --- |
| Installed with | `kubectl apply -k deploy/k8s` | Docker Compose, or `go run .` from source |
| Reaches the cluster with | its ServiceAccount, the verbs of `deploy/k8s/rbac.yaml` | a kubeconfig, with the rights of its user |
| Serves | that cluster, fixed | the context it starts on, and the owner may switch to the kubeconfig's other contexts |
| The owner's first password | the Secret `kubendt-backend-env` | `KUBENDT_ADMIN_PASSWORD` in the compose file or the shell |
| The UI at | a port-forward, a NodePort or an Ingress | `http://localhost` with the compose, `:3000` from source |
| Meant for | the install a group shares | one person's lab, such as kind or minikube on a laptop, or a development cluster |

Each cluster is its own KubeNDT either way. Its accounts, API tokens, sessions, settings and labs live in it, in the system namespace (`kubendt-system`) and the lab namespaces, and nothing is kept with the backend. So two backends on one cluster, one inside and one outside, are the same KubeNDT, and a context on another cluster is another one.

### Switching contexts outside the cluster

The owner may switch between the kubeconfig's contexts, on the cluster page or with `POST /kube/context`. A context on the same cluster only changes the credentials. A context on another cluster is another KubeNDT: the switch asks for its owner's password, then everyone is signed out and the owner is signed in there. Where KubeNDT has never run, the owner types their own password, which that cluster takes. A switch waits until no namespace has an operation in progress and this backend has no shell, logs, capture, trace or throughput test open.

The kubeconfig is never written. A switch lasts until the backend restarts, which comes back to the configured context (`KUBE_CONTEXT`, or the kubeconfig's own). To add a context, edit the kubeconfig: run from source, the backend reads it on every call and the new context shows on a refresh; under the compose, restart the service. Without a kubeconfig at all, mount one and restart.

## Deployment in the cluster

KubeNDT can run inside the cluster it manages, on a ServiceAccount, with no kubeconfig: `deploy/k8s` holds the manifests, a kustomization that installs the `kubendt-system` namespace, the ServiceAccount with its ClusterRole, the backend and the UI.

```bash
# The owner's first password. Without this Secret one is generated and printed once in the backend's log.
kubectl create namespace kubendt-system
kubectl -n kubendt-system create secret generic kubendt-backend-env --from-literal=KUBENDT_ADMIN_PASSWORD='change-me'

kubectl apply -k "https://github.com/emigcm98/kubendt/deploy/k8s?ref=v1.13.0"   # a release: its kustomization pins its images
kubectl apply -k deploy/k8s                      # a checkout: the images of the release it was cut at

kubectl -n kubendt-system port-forward svc/kubendt-frontend 8080:80   # then http://localhost:8080
```

The UI's nginx proxies `/api` to the backend's Service, so one host serves both; `deploy/k8s/optional/ingress.yaml` is an Ingress to adapt. The backend keeps the platform's objects in the namespace it runs in and needs nothing else: no volume, no database. One replica: the state is in the cluster, but a capture, a shell or a trace lives in the backend that opened it, so a second replica would need the Service to keep each client on one pod; when a lab is heavy, give the one replica more memory in `backend.yaml` (its limit is 1 GiB, its CPU unlimited) rather than a second one. The heavy traffic of a lab, the exec streams of shells, captures and actions, goes through the API server and the kubelets whichever way the backend runs; in the cluster it only loses a hop. `deploy/k8s/rbac.yaml` is the exact list of what the ServiceAccount may do, verb by verb, the same the state browser shows in use.

The backend's variables (the reference below) come from two objects the manifests name and the install provides, `deploy/k8s/optional/config.example.yaml` being both filled in as an example: the Secret `kubendt-backend-env` for the password and anything secret, and a ConfigMap `kubendt-backend-config` for plain settings, every key of both an environment variable. The manifests set `KUBENDT_SYSTEM_NAMESPACE` to the backend's own namespace and leave `KUBENDT_DB_PATH` empty, nothing else. A change is read at start, so it takes `kubectl -n kubendt-system rollout restart deploy/kubendt-backend`. The password is the exception, it only counts on a cluster with no owner yet (see `KUBENDT_ADMIN_PASSWORD` below):

```bash
kubectl -n kubendt-system create configmap kubendt-backend-config \
  --from-literal=KUBENDT_PLACEMENT=spread --from-literal=KUBENDT_CAPTURE_MAX_MB=200
kubectl -n kubendt-system rollout restart deploy/kubendt-backend
```

The UI's only variable is `BACKEND_HOST`, set in `frontend.yaml` to the backend's Service.

Meshnet and, for VM nodes, `/dev/kvm` on the workers are the cluster's side, as for any install; metrics-server is optional.

A compose install of 1.11 or older moving to this version keeps its `backend-db` and `kubendt-files` volumes for one more start: the backend moves what they hold (the admin password, the API tokens, and every lab's layout, history, link attributes and files) into the cluster as it starts and says so in its log, after which the volumes can go. To carry such an install into its cluster, start it once on this version where it is, then install the manifests and point the proxy at them; users keep their URL, their password and their tokens.

For a reverse proxy outside the cluster, `deploy/k8s/optional/nodeport.yaml` puts the UI on port 30080 of every node, so the proxy's upstream can be the workers; behind HTTPS set `KUBENDT_COOKIE_SECURE=true` in the ConfigMap. The proxy must pass WebSockets (`proxy_http_version 1.1`, the `Upgrade` and `Connection` headers) with long read and send timeouts, as the shells and captures are open connections.

## Deployment with Docker Compose

The compose runs KubeNDT outside the cluster, on your kubeconfig. Two compose files are provided, both running the backend plus a combined nginx+frontend image:

- `docker-compose.prod.yml`: pulls the published images from GHCR (nothing to build). The "just use it" path.
  ```bash
  docker compose -f docker-compose.prod.yml up -d
  ```
- `docker-compose.yml`: builds both images from source (for local changes).
  ```bash
  docker compose up --build
  ```

Configuration lives **inline** in each compose file's `environment:` block. The defaults expect your kubeconfig at `~/.kube/config` (mounted read-only) and set a placeholder admin password (`admin123`), which the owner gets at the first start on a cluster KubeNDT has not run on. Change it in the file before that, or on the profile page after. See the reference below for every variable.

The backend container copies the mounted kubeconfig at every start, so a context added to `~/.kube/config` shows once the service restarts (`docker compose restart backend`). It starts on the kubeconfig's current context unless `KUBE_CONTEXT` names one. A cluster another KubeNDT already serves keeps its owner's password, as the variable only sets the first one.

The compose files keep the `kubendt-files` and `backend-db` volumes an older backend wrote its files and database to; a backend of 1.12 reads them once to move that into the cluster, after which they can go.

## Running Locally (Development)

Install the required tools listed in [CONTRIBUTING.md](../CONTRIBUTING.md), then run each component in a separate terminal.

**Backend** (from `backend/`):

```bash
go run .
```

Runs on http://localhost:8080. Requires Go 1.26+ with CGO, gcc, and libsqlite3-dev.

**Frontend** (from `frontend/`):

```bash
npm ci
npm start
```

Runs on http://localhost:3000.

The frontend dev server proxies `/api` requests to the backend at `localhost:8080`.

The backend reads configuration from environment variables at startup, there is no auto-loaded `.env` file. Set any needed variables in your shell before running:

```bash
export CORS_ALLOWED_ORIGINS=http://localhost:3000 # default when unset
export KUBENDT_ADMIN_PASSWORD=admin123            # or a generated one is printed once
go run .
```

The state lives in the cluster. `KUBENDT_DB_PATH` and `FILES_BASE_PATH` only matter to move what a backend older than 1.12 kept in a database and on disk.

Or create a local file and source it (not committed to git):

```bash
# backend/.env.local , not committed
CORS_ALLOWED_ORIGINS=http://localhost:3000
FILES_BASE_PATH=./files
```

```bash
set -a && source backend/.env.local && set +a
```

## What the backend asks of the cluster

Kubernetes 1.25 or newer, 1.30 recommended. What sets the floor, by API, with the version it has been stable since:

| API or feature | Stable since | Used for |
| --- | --- | --- |
| Ephemeral containers (`pods/ephemeralcontainers`) | 1.25 (beta on by default since 1.23) | the toolbox injected for captures, traceroute, traffic control, throughput tests and the actions an image lacks a binary for |
| Pod exec over WebSocket | 1.30 (beta on by default), 1.31 | every exec; an older API server falls back to SPDY with `KUBENDT_EXEC_TRANSPORT=spdy` |
| CustomResourceDefinitions `apiextensions.k8s.io/v1` | 1.16 | Meshnet's `Topology` objects and the network attachment definition |
| Leases `coordination.k8s.io/v1` | 1.14 | the operation lock of a namespace, `kubendt-internal-lock` |
| Pod sysctls with the kubelet's `allowedUnsafeSysctls` | 1.11 | forwarding on router and switch pods |
| StatefulSets `apps/v1` | 1.9 | every node |
| Pod affinity and anti-affinity | 1.6 | the `placement` of a topology |
| Pods, ConfigMaps, Secrets, Namespaces, Nodes, Events, Services `v1` | always | everything else |
| metrics-server (`metrics.k8s.io`) | optional | the cluster page's CPU and memory; the page says when it is missing |

The backend's credentials (the kubeconfig, or the ServiceAccount of `deploy/k8s/rbac.yaml`, which lists every verb) need those resources in the lab namespaces and in `kubendt-system`, Namespaces and Nodes cluster-wide. The backend is built with client-go 0.35, which Kubernetes supports against servers within one minor on either side and which works against the 1.32 and 1.37 clusters it is tried on. A worker with `/dev/kvm` is needed only for VM nodes (VyOS, EXOS).

## Environment Variables Reference

The owner may override some of these from the portal's settings: the password's minimum length, the sessions' hours, the default quotas, the pods' requests and limits, the largest capture, the placement and the link MTU. A value saved there wins over the environment until it is dropped, and is kept in the ConfigMap `kubendt-internal-settings` of the system namespace, so every backend follows it. The rest are the process's own and only the environment sets them.

### Backend

- `KUBECONFIG`: kubeconfig path, read and never written.
- `KUBE_CONTEXT`: optional kube context override, the context a restart comes back to.
- `PORT`: API port (default `8080`).
- `CORS_ALLOWED_ORIGINS`: comma-separated list of allowed frontend origins (default: `http://localhost:3000`).
- `FILES_BASE_PATH`: the folder an older backend kept the namespace files in (default: `files` relative to cwd). The files live in the cluster now, as ConfigMaps and Secrets of the namespace; what is on disk moves there the first time a namespace is touched and stays behind a marker, so the variable and its volume are only needed to migrate.
- `KUBENDT_DB_PATH`: SQLite database path. Optional: the backend runs without one, the state lives in the cluster, and a file is only read to move what an older backend kept in it (its layouts, history, link attributes, file flags, admin password and API tokens). Unset, a `kubendt.db` in the working directory is still opened when there is one.
- `KUBENDT_SYSTEM_NAMESPACE`: the namespace the platform keeps its own objects in, created at start (default `kubendt-system`): the admin password hash, the API tokens, the key the sessions are signed with, and the layouts of deleted namespaces waiting for a namespace of the same name. The cluster in use holds them, and a switch to another cluster takes that cluster's.
- `KUBECTL_EXEC_TIMEOUT_SECONDS`: max wall-clock seconds for any single in-pod exec (kubectl exec, `ssh_qemu` into a QEMU guest, batched VyOS commits, the one script that runs a pod's consecutive Linux actions in a configure or a replay). Default `30`. Bump it if you run against a slow cluster or busy guests and start seeing "exec timeout" errors from network-configure or modify operations. The SSH layer detects dead sessions independently within ~11s, so this deadline only bounds genuinely-long commands.
- `KUBENDT_K8S_QPS` / `KUBENDT_K8S_BURST`: client-side rate limit for requests to the Kubernetes API (default `100` requests per second, burst `200`). client-go's own default is 5/10, which queues per-pod operations for tens of seconds on large topologies. Lower them only on a shared API server under pressure; it applies its own priority and fairness on top.
- `SWAGGER_BASE_PATH`: base path prefix for Swagger UI (e.g. `/api/` when served behind a proxy).
- `KUBENDT_VERSION`: optional build version metadata.
- `KUBENDT_COMMIT`: optional build commit metadata.
- `KUBENDT_BUILD_DATE`: optional build date metadata.
- `KUBENDT_TOOLBOX_IMAGE`: image of the toolbox, the ephemeral container injected into a pod for capture, traceroute, traffic control and throughput tests, and for `ip`, `bridge`, `tc`, `iptables` and `tayga` when the node's image lacks them (default `ghcr.io/emigcm98/kubendt-toolbox:<backend version>`, built from `deploy/custom_images/toolbox/` and published with every release, `latest` for a development backend, pulled afresh on every injection so a change to the toolbox reaches it without a release; `nicolaka/netshoot` works too). `KUBENDT_CAPTURE_IMAGE` is still read.
- `KUBENDT_CAPTURE_MAX_MB`: the size a capture's pcap may reach before it stops by itself (default `100`). The file lives in the toolbox's writable layer, which counts as the pod's ephemeral storage on the worker, so the cap is what keeps a capture left running from filling the disk and getting the lab evicted.
- `KUBENDT_EXEC_TRANSPORT`: the transport of every pod exec, `websocket` by default (what kubectl uses, on by default in the API server since Kubernetes 1.30; a server or a proxy that refuses the upgrade moves the backend to SPDY for good after the first exec, with a line in the log), `spdy` to skip the attempt on a cluster you know is older.
- `KUBENDT_PLACEMENT`: the placement a topology gets when it names none, `pack` (one worker for the whole namespace when it fits, every link a veth) or `spread` (a different worker per pod); unset, the scheduler decides.
- `KUBENDT_LINK_MTU`: the MTU the platform sets on every link after wiring, both pod ends and the guest of a VM node (default `1450`, the most a VXLAN link carries on a 1500 underlay). Set it to what your cluster carries, `8950` on a jumbo underlay for instance; a value a worker cannot carry fails the operation naming the link and the worker, while the default is lowered with a warning when it does not fit. VM nodes receive it in their pod environment so the guest boots with it. See the cluster guide.

#### Authentication

- `KUBENDT_ADMIN_PASSWORD`: the owner's password on a cluster that has no owner yet, so at the first start. After that it is ignored and the owner changes theirs on the profile page. Unset, a random one is generated, printed once in the log and its hash stored. To reset a lost one, delete the Secret `kubendt-user-admin` of the system namespace and restart the backend: it takes the variable again or generates one, the owner's sessions end and their API tokens stay.
- `KUBENDT_AUTH_DISABLED`: set to `true` to run the API **unauthenticated** (dev / trusted network only). A loud warning is logged at startup.
- `KUBENDT_COOKIE_SECURE`: set to `true` when serving over HTTPS so the session cookie is only sent over TLS (default `false`, since the stock compose serves plain HTTP behind nginx).
- `KUBENDT_QUOTA_NAMESPACES`, `KUBENDT_QUOTA_PODS`, `KUBENDT_QUOTA_PODS_PER_NAMESPACE`: the quota of an account that has none of its own, namespaces owned, pods across them and pods in any one of them; `0` or unset is no limit. Admins are never counted; a lab counts against its owner whoever deploys into it.
- `KUBENDT_POD_CPU_REQUEST`, `KUBENDT_POD_MEMORY_REQUEST`, `KUBENDT_POD_CPU_LIMIT`, `KUBENDT_POD_MEMORY_LIMIT`: quantities (`100m`, `256Mi`) a LimitRange written into every new lab namespace gives each container without its own, so the scheduler counts the pods and a lab cannot starve a worker. Unset, no LimitRange is written. A VM node needs memory enough for its guest, so set the limit with those in mind.
- `KUBENDT_PASSWORD_MIN_LENGTH`: the least a chosen password may be long (default `8`). Generated passwords and `KUBENDT_ADMIN_PASSWORD` itself are not checked. Accounts, levels and the sign-in limits are in [USERS.md](USERS.md).
- `KUBENDT_SESSION_IDLE_HOURS`: idle timeout for browser sessions (default `12`).
- `KUBENDT_SESSION_MAX_HOURS`: absolute session lifetime (default `168` = 7 days). A session is a signed cookie, accepted by every backend of the cluster and surviving their restarts; without a cluster the key is the process's own and sessions end with it.

Programmatic access uses **API tokens** (created in the dashboard or via `POST /auth/tokens` with the admin password, optionally with an expiry via `expires_in_days`), sent as `Authorization: Bearer <token>`.

### Frontend

- `REACT_APP_API_BASE_URL`: API base URL, read at build time (`/api` in the published image, which proxies it).
- `PORT`: frontend dev server port (default `3000`).
- `BACKEND_HOST`: in the published image, the host its nginx proxies `/api` to (default `backend`, the compose service; `kubendt-backend` in the cluster manifests).
