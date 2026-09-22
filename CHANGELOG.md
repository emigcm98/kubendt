# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Nodes accept an optional `terminationGracePeriodSeconds` in the topology JSON (deploy and modify add). KubeNDT now defaults it to 2 s instead of inheriting the Kubernetes default of 30 s, since emulated nodes are stateless (their configuration is replayed after a restart) and the usual `sh -c "... && sleep infinity"` entrypoint ignores SIGTERM, so the pod was killed after the full 30 s anyway. Every operation that recreates a pod (restart, delete node or link, scale down) gets faster by roughly that amount. Measured on an FRR router: restart went from ~43.6 s to ~18.6 s. Set it higher per node for workloads that need an orderly shutdown.
- Deploy, modify and restart responses carry a new `timeline` block next to `took_time`. For every pod the operation created or recreated it lists the lifecycle stamps Kubernetes wrote on the Pod (created, scheduled, sandbox and CNI ready, container started, Ready) and, in milliseconds on the backend clock, when the backend issued the delete, saw the old pod disappear, and saw each transition and Ready. `backend_ms` breaks down the backend's own phases (validation, resource creation, wait, replay, heal). Together they separate KubeNDT's overhead from the substrate's, per pod and with Kubernetes as the source of the pod-side numbers. See `doc/TIMING.md`.
- Nodes accept `nodeSelector` and `nodeName` in the topology JSON (deploy and modify add), passed straight to the pod spec. `nodeSelector` keeps a node on workers with given labels, the typical case being QEMU nodes on KVM-capable workers (the VyOS example README shows how). `nodeName` pins a node to one worker, which also fixes whether Meshnet realizes each link as a veth pair (same worker) or a VXLAN tunnel (different workers), so the same topology reproduces the same data plane on every deploy. Both are validated against the cluster before anything is created: an unknown worker or a selector no Ready worker satisfies returns a `400` naming the problem, where Kubernetes would have left the pods Pending. Both apply to every replica of a node, and `nodeName` puts all of them on that worker. `GET /network/get-network` echoes both fields, and now also `terminationGracePeriodSeconds`, so an exported topology imports back identically.
- New `GET /network/links/{namespace}` lists every link as it exists on the cluster, with the worker behind each endpoint and its `realization`: `veth` when both pods share a worker, `vxlan` when they do not, `external` for host uplinks, `pending` while an endpoint is unscheduled. The link panel in the dashboard shows it (`veth pair` / `VXLAN tunnel`) together with the worker each endpoint runs on. The graph draws both kinds the same way, since the difference is invisible to the emulated network. Together with `nodeName` this makes the data-plane realization of a topology both declarable and verifiable.
- `doc/FAILURE_MODEL.md` describes what KubeNDT survives and what it does not: where each piece of state lives and whether it outlives a backend restart or the loss of the backend volume, how interrupted operations and stale locks are recovered, which pod recreations replay covers and which come back bare, and the steps that stay manual.
- `experiments/`: scripts that measure the platform through its public API (deploy time by size with the per-pod timeline, configure throughput, in-place modification against redeploy, restart decomposition, state verification after replay, veth against VXLAN links, traffic-control fidelity, VM against container routers, footprint, telemetry cost, environment inventory) plus the end-to-end regression over the six examples. They need a cluster and are not part of CI. See `experiments/README.md`.
- Restart and modify responses time the re-application of neighbour state apart from the replay: `took_time.peer_replay` on a restart and `timeline.backend_ms.peer_replay` on both. `replay` covers the recreated pods' own history only, so a replay-time figure no longer carries the neighbours' recovery cost inside it.

### Changed

- The default readiness probe (`command -v ip`) no longer waits 5 s before its first run and probes every 2 s instead of 5. A container that starts in a second is Ready in about a second, where before it took 5 to 10 s. Nodes that install tools at startup stay NotReady and are re-probed until `ip` appears, as before. The failure threshold is raised to 30 so that a minute of transient exec failures under load is needed before a healthy pod flips back to NotReady. Drivers that ship their own probe (VyOS) are unchanged.
- Deploy, modify and restart no longer poll for pod readiness every 5 s. The backend now watches pod events and reacts the moment the kubelet reports Ready, which removes a 5 s floor and up to 5 s of detection lag from every operation. The fixed settle sleeps that followed (5 s after deploy, 2 s after a modify add, 1.5 s after a restart) are gone too. Instead, the interface validation re-checks a link that looks broken a few times over 3 s before restarting a pod, so the common case costs nothing and a genuinely late interface still heals.
- The post-operation interface check and repair is now called the heal pass everywhere (code, logs, docs) instead of "reconciliation", which for Kubernetes readers means a continuous controller loop. KubeNDT's pass is bounded and triggered by the backend after a deploy or modify, and `doc/ARCHITECTURE.md` now says exactly what it does and why it is not an operator. No behavior change. `took_time.reconciliation` keeps its key for compatibility and is the same figure as `timeline.backend_ms.heal`.
- Ready now means configurable for nodes with a control daemon that starts after the container. The VyOS probe also requires the guest's HTTP API to answer (it used to check SSH only, and nginx returned `502 Bad Gateway` for 7 to 10 s after SSH came up, exactly when a configure or a replay right after Ready would land). Open vSwitch nodes get their own probe, `ovs-vsctl show`, instead of the default `command -v ip`, which passed seconds before `ovsdb-server` listened. VyOS becomes Ready a few seconds later than before.
- Example images are pinned to version tags. FRR moves to `quay.io/frrouting/frr:10.7.1` (the Docker Hub repository is abandoned at v8.4.1), base images go to current releases, and the end-of-life `ubuntu:20.04` and `mongo:6.0` are replaced. `globocom/openvswitch`, `networkstatic/iperf3` and `docker_open5gs` publish no version tags and keep their moving tag. The capture and traffic-control helper is pinned to `nicolaka/netshoot:v0.16`.
- FRR nodes in the examples start through the image's own init (`watchfrr` under `tini`), which supervises the daemons and stops on SIGTERM. The startup command installs `iptables`, which the Quay image lacks and the NAT actions need, and enables `ospfd`. The hostname is the pod name.
- FRR nodes are Ready only when zebra and every daemon enabled in `/etc/frr/daemons` answer on their vty socket.
- Deploy validation reads the namespace's Topology objects once to check for interface conflicts instead of fetching one per pod. On an empty namespace those were as many sequential round trips answering 404 as pods. The whole validation phase now takes about 0.3 s whatever the size.
- The cluster deployment guide puts the kubelet's `allowedUnsafeSysctls` in the KubeletConfiguration that kubeadm manages (a `kubeadm init --config` file, or the `kubelet-config` ConfigMap plus `kubeadm upgrade node phase kubelet-config` on an existing cluster) instead of a hand edit of `/var/lib/kubelet/config.yaml`, which the next minor upgrade rewrites without the key and leaves switch and router pods in `SysctlForbidden`.

### Fixed

- Restarting a pod left its neighbours half configured. Meshnet recreates the neighbour's interface together with the pod (a veth dies with the old netns, a VXLAN device is rebuilt on the next CNI ADD), so anything a driver had put on it was lost: a Linux switch dropped the port from its bridge, qdiscs and non-CRD addresses vanished, and traffic through that link died even though the restarted pod's own history had been replayed. Now every recreation KubeNDT orchestrates (Restart, modify, heal pass) also re-applies, on each neighbour, the persisted operations that touch the recreated interface, including the default and static routes the kernel drops with it, and refreshes the TC redirect of guest-VM neighbours (previously only a modify did that, a plain Restart next to a VyOS router left the link dead). Reported as `peer_replay` in the restart response. Replay also retries a failing command a few times over ~12 s before pruning the operation from history: a pod is Ready before its daemons are (ovsdb, the VyOS HTTP API), and the old behavior wiped the whole history of a VyOS router restarted at the wrong second. The OVS `setup_bridge` and `add_port` actions use `ovs-vsctl --may-exist`, so re-applying them on a port OVS already re-attached by name is a no-op instead of an error.
- Operation history left behind by a deleted namespace could silently disable configuration in a new namespace with the same name. Configure compared each action against the history by payload alone, so actions recorded for an earlier namespace (deleted outside KubeNDT, or by a release without the purge) were skipped as already applied, with `failures: 0`, and a restart replayed the dead namespace's operations onto the new pods. Now an action only counts as already applied when its row is newer than the target pod, re-applying an action replaces its row and a replay refreshes the timestamp, and rows older than the Namespace object are dropped on the next deploy and when the namespace is created.
- Adding a link between two pods that already existed left the wiring to the heal pass, which first re-checked for interfaces that cannot appear without a restart, then restarted an endpoint, then validated again: about 20 s for an operation whose restart takes 8, and the modify response reported no restarted pod and no timeline for it. The endpoint is now recreated in the modify's own restart phase, one per new link (a link with a new pod or with a peer already being restarted needs nothing), so the response lists it in `restarted_pods` with its timeline and the heal pass only verifies.
- Every request to the Kubernetes API went through client-go's default client-side throttle, 5 requests per second with a burst of 10, and KubeNDT issues at least one per pod. `KUBENDT_K8S_QPS` and `KUBENDT_K8S_BURST` tune it.

## [1.3.0] - 2026-08-13

### Added

- The running build version is shown as a badge next to the title on the Home dashboard, read from the public `GET /version` endpoint. Releases show `vX.Y.Z`, local builds show `dev`, and hovering reveals the commit and build date.
- VyOS routers now own their pod's cluster IP. The primary CNI interface (eth0) is passed through to the guest VM like the data interfaces, so a VyOS node is reachable at its pod IP and can act as the twin's internet gateway with `enable_snat` on eth0, matching the native-container routers.
- The backend drives VyOS through the guest's HTTP API. Reads come from a single `POST /retrieve` (config as JSON, briefly cached and deduplicated) and configure actions are merged into one atomic `POST /configure` per batch. Namespace IP polling on VyOS drops from ~2.2 s to ~1.1 s and a 13-action cold commit from 10-20 s to ~7 s. SSH (`ssh_qemu`) remains as the rescue path.
- `build-vyos-qcow2.sh` builds the virgin VyOS qcow2 unattended. It downloads the chosen rolling ISO (latest by default, `--list` to browse) and answers the installer over the serial console inside a throwaway container, using whichever engine (podman or docker) can reach /dev/kvm.
- New VyOS image tunables `CPU_CORES` (vCPUs, default 1) and `HTTPS_FORWARD_PORT`.
- The interactive shell window can be resized from its bottom-right corner. The terminal reflows as you drag, snaps to whole rows so no blank strip is left at the bottom, and tells the pod its new size. Minimizing and restoring keeps the size and position it had.
- File Manager can now take files dropped straight from your computer, onto the sidebar (root or a folder) or onto the empty editor. Archives are extracted, other files are uploaded into the target folder, folders over 1 MiB are skipped and folder drops are rejected with a hint to zip them.
- Empty File Manager namespaces show a real empty state with New file / New folder / Import actions, right-clicking the empty editor opens the same create/import menu, files can be downloaded one at a time from their right-click menu, and the sidebar shows a file count.
- The File Manager export now opens a dialog to pick the archive format (ZIP or gzipped tar), backed by a new `format` query param on `GET /file-ops/{namespace}/export` (`zip` default, or `tar.gz`).

### Changed

- VyOS management secrets (SSH keypair, HTTP API key) are generated per pod at startup.
- Faster VyOS boot. GRUB menu timeout is patched to 0 wherever the release keeps it, the ephemeral qcow2 runs with cache=unsafe, ssh_qemu multiplexes SSH sessions, and the readiness probe starts at 30 s. Observed Ready time went from ~95 s to ~75 s with 2 vCPUs.
- VyOS executors renamed by transport, `vyos_ssh_cli`/`vyos_ssh_apply` (rescue) and `vyos_api`/`vyos_api_apply` (hot path), and the executor package is now organized per platform.
- Removed the `MOVE_POD_IPS_TO_GUEST` and `IFACE_SETTLE_SLEEP` env vars from the VyOS image. Moving IPs to the guest is now always on, and the settle fallback is fixed at 5 s.
- License declarations unified to AGPL-3.0-or-later (README, `CITATION.cff`, `.zenodo.json` and frontend package metadata previously said AGPL-3.0-only), and the LICENSE text now matches the canonical gnu.org copy byte for byte (https URLs).
- All Markdown docs now use single-line paragraphs, so they render correctly when pasted into GitHub releases and PRs. Enforced by the root `.prettierrc` (`proseWrap: never`).
- Traceroute now works from VyOS routers. Guest-VM drivers run the probe inside the guest through a new optional `GuestProbeProvider` driver interface, using the guest routing table, instead of a debug container in a pod netns that has no connectivity. Guest drivers without probe support get a clear error.
- The capture and traceroute panels now share a tokenized dark palette (`--tool-*` design tokens), collapsing the near-duplicate colors that had drifted between them. Component-specific semantic colors (protocol rows, hop kinds) stay local.
- The interactive shell palette moved to `--term-*` design tokens, and xterm's canvas theme now reads them so CSS and terminal colors cannot drift apart.
- The Swagger UI now has a working search box that filters endpoints by path, summary or tag (so "deploy" finds `POST /network/deploy-network`), and drops the "Explore" spec-URL bar, which was not a search. The Bearer token also persists across page refreshes.
- Nodes added through a topology modify now settle into the organic force layout next to the neighbours they connect to without overlapping existing nodes, instead of stacking in a fixed vertical grid. Existing nodes keep their positions, and the resulting layout is saved so a reload shows the same arrangement instead of relaying everything out.
- The File Manager export and delete-all controls are disabled when there are no files, the File Manager warns before you reload or close the tab with unsaved edits.
- Importing an archive into a File Manager namespace that already has files now asks for confirmation first, since it can overwrite same-path files.
- Destructive confirmations (clear topology, delete namespace, delete history, delete file or folder, delete all files) now share one modal with a consistent look: warning icon, a red confirm button, an "action cannot be undone" note, and Esc-to-close.
- Traffic control (tc/qdisc) is applied and read on any pod, using the pod's own `tc` when present and falling back to an ephemeral toolbox container for node images that ship none. Shaping no longer depends on the node image bundling `tc`.
- Traffic control now lives in its own floating panel, a peer of the packet capture and traceroute windows, instead of an inline editor in the node panel. Can be opened by right-clicking a link, from the link info panel, or from a node's Links tab, which now shows the shaping status and an Open button.
- Traffic control is no longer a driver capability. The `TCCapable` interface is gone and the driver capabilities API no longer lists it, since shaping now works the same on every node.

### Fixed

- Long Kubernetes node names no longer widen their card into a horizontal scroll on the Home cluster status. The node name now fits wrapping to at most two lines with the full name on hover.
- Actions on the protected eth0 interface (everything except SNAT) are now rejected for VyOS and XRd too. Drivers with a custom execution planner used to bypass the guard entirely.
- Interface renames inside the VyOS guest no longer race the stock config.boot, whose install-time hw-id entry could hijack eth0 and shift every name at coldplug. hw-id lines are stripped from the image at build time.
- Traceroute hops through the Kubernetes fabric (pod-network gateways, node IPs) are now tagged as `cluster` hops, with their own icon in the trace panel, and drawn as the way out to the internet instead of being misattributed to the topology's external network node when a router exits through its cluster interface.
- The interactive shell no longer clips its last terminal row. Its padding sat on an inner xterm element that the fit addon does not measure, so the rows overflowed the window by a few pixels and cut a row on displays whose cell height crossed the rounding threshold. The terminal also re-fits on zoom and display-scaling changes, not only on window resizes.
- The Swagger spec no longer pins `host` or `schemes`, so "Try it out" follows the page's own origin and protocol and works on any host and on `https` deployments behind a reverse proxy.
- Reading a netem qdisc no longer mistakes the internal seed value for the interface jitter when a delay was set without one.
- Applying a `tbf` qdisc no longer fails after editing the burst. The value was only accepted with a capital `Kb` suffix, so other forms were dropped and `tc` rejected the command for a missing burst.

## [1.2.0] - 2026-07-30

### Added

- Meshnet CNI health awareness. The Home dashboard shows whether the Meshnet dataplane is running, both cluster-wide (a badge next to the node count) and per node (on each node card and in the node detail panel), so a missing or partial install is visible instead of failing silently.
- New `meshnet` field on the cluster status and node detail API responses reporting the dataplane state.
- Mounted files whose source no longer exists in the namespace file manager are flagged in the pod detail panel with a warning and a disabled link, instead of silently linking to a missing file. The mount API carries a matching `missing` field.
- Optional node repulsion on the graph: dropping a node too close to another bounces it to the nearest free spot. Toggle it from the graph controls (off by default. Hold Ctrl to place nodes close), next to a new lock toggle. Both preferences persist across sessions.
- Enable or disable a pod interface from the Links tab of the node panel by right-clicking it, the same action already available on the graph.

### Changed

- Replaced UI emojis with a consistent SVG icon set that inherits text color, and reorganized image assets into `nodes/` and `icons/` subfolders.
- Unified the UI styling behind a set of design tokens (colors, radii, elevation) and refreshed the palette for a cleaner look.
- Topology changes now require a running Meshnet CNI. Deploying, and modifying a topology (add, delete or scale), return `412` when Meshnet is not detected, so pods are never left unwired or stuck. Clearing a topology is always allowed, and `?force=true` overrides the check.
- Polished the UI with a tonal button palette (one soft color per action, applied across the graph toolbar, the namespace and File Manager bars, and the Home dashboard), thinner graph links, interface labels that stay aligned to their cable across node types, a pulsing active-node dot, custom animated zoom/fit controls with a wider zoom range, and a cleaner minimap.
- `GET /network/get-network` now loads much faster on remote clusters. It reads every ConfigMap and Secret in the namespace in a single batch and resolves mounted-file paths from one directory walk, instead of one API request per node and one walk per mount.
- Refreshed the node, link and external info panels with consistent typography, tokenized colors, a slide-in and slide-out animation, and a cleaner driver capability view. The link panel now shows its endpoint path (`pod:iface ↔ pod:iface`) as an attribute.
- Unified the app's top bars and buttons: the graph navbar, Home header and Login now share the brand blue with a subtle gradient, the navbar is slimmer.
- The topology graph auto-fits when the topology structure changes (opening a namespace, import, modify) with a smooth animated transition.

### Fixed

- The Swagger UI version badge now reflects the running build version instead of a fixed `1.0`, so it changes across releases.
- Home dashboard no longer clips the cluster/kubeconfig panels on shorter viewports (left column fits and scrolls its node list internally, like the namespaces column).
- Mounted files stored in a subfolder now show their real path (for example `web-server/index.html`) and open the correct file, instead of a sanitized key (`web-server_index.html`) that pointed nowhere.
- Kubendt's internal interface-count ConfigMap no longer appears in a pod's Mounted Files list.
- The import and modify topology dialogs no longer clip their content on shorter viewports. They now use a fixed-height shell that adapts to the screen, with the JSON editor scrolling internally.

## [1.1.0] - 2026-07-16

### Added

- Traceroute from any L3-capable node toward an IP or hostname, with every hop resolved to a topology node so the path can be followed on the graph. The probe runs in a shared ephemeral debug container, so the source image needs no traceroute of its own.
- Two ways to run a trace. A live WebSocket stream emits each hop as it arrives (starting, resolving, tracing, done), and a REST endpoint returns the whole run as a single JSON document for scripting and automation.
- Selectable probe method (ICMP, UDP or TCP SYN to port 80) and a metrics mode built on mtr. Metrics mode runs a configurable number of cycles and reports per-hop loss, average, best, worst, last, standard deviation (jitter), geometric mean and packets sent.
- Per-hop detail beyond plain traceroute. Each hop carries its resolved node and ingress interface, a kind (resolved L3, external IP or timeout), the ICMP unreachable flag when a router drops the probe, and the pod path it crossed, marking whether a segment is a real link or an overlay tunnel such as GTP-U. Runtime-applied IPs and tunnel endpoints (interfaces like `ogstun` or `uesimtun0`) are recognized, not only addresses declared in the topology.
- A final outcome for the run (delivered, unreachable or unreached), with early stop on black holes and explicit unreachable replies.
- Traceroute control panel in the UI, opened from a node's context menu. It lets you pick the destination by typing or choosing a topology node, select method and mode, and adjust the metrics cycles. A packet animation walks the path hop by hop over the graph, drawing tunnels and external exits differently and marking where the packet is delivered or dropped. Playback controls (play, pause, step, scrub) let you replay the traced path, and the full result can be downloaded as JSON.

### Changed

- Improved the Kubernetes cluster deploy guide, clarifying when Meshnet is installed, how to bind the kind API server to a routable host IP, where metrics-server fits per option, and adding install links for Minikube, kind and kubeadm.

## [1.0.0] - 2026-07-09

Initial public release. KubeNDT deploys and operates virtual network topologies on Kubernetes, defined declaratively and materialized as Kubernetes-native resources over the Meshnet CNI.

### Added

- Declarative topologies deployed as StatefulSets over Meshnet CNI, with in-place add, remove and scaling of nodes and links and external uplinks to the host network.
- Driver and capability system covering hosts, routers (Linux, FRR, VyOS) and switches (Linux bridge, OVS), with L2/L3, DNS, traffic control, NAT and OSPF.
- Persisted driver operation history, replayed automatically on pod restart.
- Interactive network graph with status colors, drag, zoom, minimap, search and saved layouts, per-node info panel, interactive shell and metrics.
- Live packet capture on any pod interface, with BPF filter, pcap export and per-packet dissection.
- Per-namespace file manager with zip import and export, mounted into pods as ConfigMaps or Secrets.
- Kubernetes integration for kubeconfig and context selection, cluster status and per-node detail.
- Admin password login with browser sessions plus `kdt_` API tokens, and an option to disable auth for trusted networks.
- Health, readiness and version endpoints, and Swagger docs at `/swagger`.
- Container images published to GHCR and a Docker Compose deployment.

[Unreleased]: https://github.com/emigcm98/kubendt/compare/v1.3.0...HEAD
[1.3.0]: https://github.com/emigcm98/kubendt/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/emigcm98/kubendt/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/emigcm98/kubendt/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/emigcm98/kubendt/releases/tag/v1.0.0
