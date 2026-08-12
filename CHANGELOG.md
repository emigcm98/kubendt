# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- The running build version is shown as a badge next to the title on the Home dashboard, read from the public `GET /version` endpoint. Releases show `vX.Y.Z`, local builds show `dev`, and hovering reveals the commit and build date.
- VyOS routers now own their pod's cluster IP. The primary CNI interface (eth0) is passed through to the guest VM like the data interfaces, so a VyOS node is reachable at its pod IP and can act as the twin's internet gateway with `enable_snat` on eth0, matching the native-container routers.
- The backend drives VyOS through the guest's HTTP API. Reads come from a single `POST /retrieve` (config as JSON, briefly cached and deduplicated) and configure actions are merged into one atomic `POST /configure` per batch. Namespace IP polling on VyOS drops from ~2.2 s to ~1.1 s and a 13-action cold commit from 10-20 s to ~7 s. SSH (`ssh_qemu`) remains as the rescue path.
- `build-vyos-qcow2.sh` builds the virgin VyOS qcow2 unattended. It downloads the chosen rolling ISO (latest by default, `--list` to browse) and answers the installer over the serial console inside a throwaway container, using whichever engine (podman or docker) can reach /dev/kvm.
- New VyOS image tunables `CPU_CORES` (vCPUs, default 1) and `HTTPS_FORWARD_PORT`.
- The interactive shell window can be resized from its bottom-right corner. The terminal reflows as you drag, snaps to whole rows so no blank strip is left at the bottom, and tells the pod its new size. Minimizing and restoring keeps the size and position it had.

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

### Fixed

- Long Kubernetes node names no longer widen their card into a horizontal scroll on the Home cluster status. The node name now fits wrapping to at most two lines with the full name on hover.
- Actions on the protected eth0 interface (everything except SNAT) are now rejected for VyOS and XRd too. Drivers with a custom execution planner used to bypass the guard entirely.
- Interface renames inside the VyOS guest no longer race the stock config.boot, whose install-time hw-id entry could hijack eth0 and shift every name at coldplug. hw-id lines are stripped from the image at build time.
- Traceroute hops through the Kubernetes fabric (pod-network gateways, node IPs) are now tagged as `cluster` hops, with their own icon in the trace panel, and drawn as the way out to the internet instead of being misattributed to the topology's external network node when a router exits through its cluster interface.
- The interactive shell no longer clips its last terminal row. Its padding sat on an inner xterm element that the fit addon does not measure, so the rows overflowed the window by a few pixels and cut a row on displays whose cell height crossed the rounding threshold. The terminal also re-fits on zoom and display-scaling changes, not only on window resizes.
- The Swagger spec no longer pins `host` or `schemes`, so "Try it out" follows the page's own origin and protocol and works on any host and on `https` deployments behind a reverse proxy.

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

[Unreleased]: https://github.com/emigcm98/kubendt/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/emigcm98/kubendt/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/emigcm98/kubendt/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/emigcm98/kubendt/releases/tag/v1.0.0
