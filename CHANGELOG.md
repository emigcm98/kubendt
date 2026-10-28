# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- BGP as a capability of FRR and VyOS routers: `bgp_set_router`, `bgp_add_neighbor`, `bgp_add_network` and `bgp_redistribute`, each with its removal, IPv4 and IPv6 by the address. FRR starts `bgpd` with the other daemons and creates the process with `ebgp-requires-policy` off. Stopping BGP drops its neighbours, networks and redistributions from the history too.
- IS-IS as a capability of FRR and VyOS routers: `isis_set_router` (NET and level), `isis_add_interface` (both families unless `family` says one, with a circuit type), passive, metric and network type per interface, and `isis_redistribute` per family and level, each with its removal. FRR starts `isisd` with the other daemons, both drivers set an LSP MTU that fits any link, and an interface leaving IS-IS or the process stopping drops their rows from the history too.
- NAT works for IPv6 too: `enable_snat` and `disable_snat` take `family: "6"` (ip6tables on Linux routers, `nat66` on VyOS) and a port forward follows the family of its internal address. Example 7 masquerades the lab behind its router's transit address and forwards a port to the web server over IPv6.
- The toolbox ships tayga, built from source since no Alpine release carries it, for the NAT64 of the Linux routers. A toolbox with a moving tag (`latest`) is pulled afresh on every injection, so a change to it reaches a development backend without a release. The toolbox runs under tini, which reaps what the tools leave behind, with the plain sleep as the fallback on an image without it.
- NAT64 as a capability of routers: `nat64_enable` takes the IPv4 pool and an optional prefix (64:ff9b::/96 by default) and `nat64_disable` removes it. Linux routers run tayga from the toolbox, which is injected privileged on a privileged pod for the tun device; VyOS uses its own `nat64`.
- Inspections: live reads of a node, declared per capability and served by `GET /pods/inspect/{namespace}/{pod}?what=<id>`, with `GET /drivers` listing what each driver offers. Routing table and ARP/NDP on every L3 node, NAT rules, the MAC table of a switch, OSPF neighbours and interfaces, OSPFv3 neighbours, RIP routes, the BGP summary and IS-IS neighbours on FRR and VyOS. The regression reads the OSPF adjacencies through it.
- The node panel has a State tab with the inspections of the node, one collapsible section per read grouped by capability, read when opened and on demand. The Driver tab is now Config: the node's history grouped by the capability of each action, with a delete per capability, and the driver's reference folded underneath.

## [1.7.0] - 2026-10-25

### Added

- RIP comes as a capability of FRR and VyOS routers, RIPv2 and RIPng with the same actions: `rip_add_network` (the prefix decides the family), `rip_passive_interface`, `rip_redistribute` (connected, static, ospf, bgp, kernel) and `rip_set_timers`, each with its removal and `family: "6"` for RIPng. OSPF gains `ospf_redistribute` (connected, static, rip, bgp, kernel) and its removal. Example 2 is now `2-test-frr-ospf-rip`: three replicas of one FRR router node, OSPF on one side with two LANs, RIP on the other with two more, and the border router in between redistributing each into the other, in three configuration files (the OSPF side, the RIP side, the redistribution).
- OSPFv3 comes as its own capability of FRR and VyOS routers, configured per interface as the protocol is: `ospf6_set_router_id`, `ospf6_add_interface` (an interface joins an area), `ospf6_passive_interface`, `ospf6_redistribute` (connected, static, ripng, bgp, kernel) and `ospf6_originate_default`, each with its removal; FRR runs it in `ospf6d`, VyOS under `protocols ospfv3`. Example 7 routes IPv6 with it instead of static routes.
- Any image that keeps running can be a node: `ip`, `bridge`, `tc` and `iptables` run from the pod's toolbox when the image lacks them (the heal and MTU passes, the interface reads and the Linux actions alike, decided once per pod at first contact), a plain node is Ready as soon as its container runs instead of when `ip` answers, and NAT works on routers without iptables. The toolbox is KubeNDT's own image now, `ghcr.io/emigcm98/kubendt-toolbox`, built from `deploy/custom_images/toolbox` with iptables added and published with every release under its version, `latest` for a development backend; `KUBENDT_TOOLBOX_IMAGE` still overrides it. Every FRR router of the examples loses its startup command. A node whose startup installs or starts what the lab uses can declare a `readiness` command in the topology JSON, run every 2 s until it exits 0, and a deploy or a modify waits for it, as the debian host of example 3 and the MongoDB node of example 5 do.
- An FRR router on the stock image needs no startup command: when a node sets no `commands`, its driver enables the routing daemons behind its capabilities, creates the files vtysh expects and starts FRR as the image does (`StartupCommandsProvider`, for drivers tied to an image). Example 7 drops the command its routers carried.
- Before the actions of a capability run on an FRR router, the backend checks that the daemon they need is up (`ospfd` for OSPF) and fails them with a message that says what to enable in `/etc/frr/daemons`, instead of a vtysh error; the check rides in the pod's batch, so it costs no exec. A driver declares it with `CapabilityPrecondition`.
- A node that sets no `commands` runs its image with a tty and an open stdin, so an image whose entrypoint is a shell (alpine, ubuntu) stays up without `sleep infinity`; `commands` keeps replacing the entrypoint and then has to stay running itself.
- OSPF interface parameters on FRR and VyOS routers: `ospf_set_cost`, `ospf_set_priority` (the DR and BDR election), `ospf_set_timers` (hello and dead intervals) and `ospf_set_network_type` (point-to-point on a /30 skips the DR election), each with its reset.
- OSPF areas and authentication on FRR and VyOS routers: `ospf_set_area_type` (stub, totally-stub, nssa, totally-nssa, the Cisco names), `ospf_add_area_range` (the ABR summarizes its area; FRR gets the range with its host bits set, since ospfd crashes when the summary's Link State ID collides with a more specific one of its own) and `ospf_set_authentication` (plain or MD5 per interface), each with its removal.
- Example 8, `8-test-two-orgs-rip-ospf`, two organizations on 27 nodes: A on RIPv2 over a six router mesh, B on OSPFv2 in four areas with DR and BDR, stub and totally stubby areas, summarization and MD5, and a border router redistributing between them, in four configuration files.
- The minimap hides behind a button on its corner and comes back from the same corner; the browser remembers the choice.

### Changed

- Static and default routes on FRR routers go through staticd (`ip route` and `ipv6 route` in vtysh) instead of the kernel, so they are in the running-config and `redistribute static` picks them up. Setting or removing a default route also drops the pod's own default route through `eth0`, as `ip route replace` did before.
- The examples no longer install packages at startup: the alpine hosts and switches run their image as it is, the ubuntu switches moved to alpine, the Open vSwitch nodes run the image's own entrypoint, and the mongo and iperf3 nodes start only their service. `commands` is left where the command is the application, and the hosts that install `ping` for their guide declare `readiness` on it.
- `HostDriver` is the one host driver and the default for type `host`; `BasicHostDriver` differed only in a `replace_ip` that skips an address already there, which every Linux driver does now.
- The log stream WebSocket moved from `/pods/logs/ws/{namespace}/{pod}` to `/pods/logstream/{namespace}/{pod}`: with a static `ws` where a namespace goes, a namespace named `ws` could not read its logs.

### Deprecated

- `BasicHostDriver` as a driver name: it resolves to `HostDriver` with a `driver_deprecated` warning in the deploy response and goes in the next version.

### Fixed

- A node whose process ends (`commands` that finish, a one-shot image) deployed as a success and looped on restarts while the heal pass restarted it in vain; the deploy now fails naming the pod when its process finished (exit code 0) and rolls back, a modify reports it the same way, an exit with an error is a warning in the response since the application may start on a later restart, and the heal pass leaves such a pod alone.
- One exec against a container that was not running made the backend fall back to SPDY for every exec from then on: the WebSocket upgrade failure of a 5xx about the pod no longer counts as the API server refusing the upgrade.
- The trace tool spaces its probes 10 ms apart: the toolbox's traceroute fires them at once, and on a link between workers the identical ICMP probes share one VXLAN flow whose simultaneous first packets the worker's conntrack drops, so hops in the middle of a path read as timeouts.
- The trace panel's node picker shows the node it picked, as the throughput panel does.

## [1.6.0] - 2026-10-21

### Added

- Every link gets the same MTU once it is wired, 1450 by default and `KUBENDT_LINK_MTU` to change it, on both ends and inside a VyOS guest, so throughput and OSPF adjacencies no longer depend on where each pod landed. The interface listing and the link panel show it, the pass is the `mtu` phase of the operation timeline and the response of a deploy, modify, restart or configure reports what it did as `link_mtu`.
- A link can declare its own `mtu` in the topology JSON, between 68 and `KUBENDT_LINK_MTU`, and the `links` section of a configure request changes it on a running lab; the platform applies it on both ends like the namespace value, and the link panel lists it with the other declared properties. Examples 3, 4 and 6 drop `ospf_mtu_ignore`, since both ends of a link now share the MTU.
- Links between container nodes take IPv6 addresses, with one family per link: routers and switches forward IPv6 (their pods set `net.ipv6.conf.all.forwarding`, which the kubelet must allow next to `net.ipv4.ip_forward`, as the cluster guide does), the L3 actions build the `ip -6` form for a v6 gateway, `remove_default_route` takes a `family`, and the interface listing reports `ipv6` next to `ipv4`.
- VyOS nodes take IPv6 too: the guest boots with every address of its links in both families, a static or default route with a v6 next hop goes to `protocols static route6`, and the node panel reports the guest's `ipv6`.
- A link end takes several addresses: `localIp` and `peerIp` accept an array (dual stack, a second IPv4), the first is wired with the link and the rest are set by the platform after every wiring, on a VyOS guest at boot. `GET /network/get-network` echoes the array, the interface listing returns every address as `addresses`, and the link, node and interface panels list them.
- The trace and throughput tools take IPv6 targets: `traceroute -6` and `mtr -6` by the address family, the hop index covers every address of every pod, and iperf3 needs nothing. TCP traceroute stays IPv4, tcptraceroute has no IPv6. When a node is picked as the target, the panel lists every address of the node to choose from.
- Example 7, dual stack: two FRR routers with OSPF for IPv4 and static routes for IPv6, dual stack laptops and web server, an IPv6-only sensor, and the tools over IPv6; example 6 gets IPv6 on two of its links and a VyOS `route6`. Both are in the regression.

### Changed

- The interface hover on the graph wears the panels' style, with the interface name as its title, the addresses in monospace and the MAC dimmed.
- `set_ip` uses `ip addr replace`, so repeating it no longer fails with "File exists", and `remove_ip` of an address that is not there is nothing to do rather than an error, so a replay never drops it from the history.
- Every pod exec goes over WebSocket, as kubectl does, with SPDY as the fallback and `KUBENDT_EXEC_TRANSPORT=spdy` to force it; the Linux actions of a pod in a configure, a replay or a peer replay run as one shell script, one exec per pod instead of one per command, still reported one by one; and the peer replay runs over the neighbours in parallel.
- The heal pass lists each pod's interfaces once instead of running one exec per link endpoint, and the MTU pass reuses what it read. A VyOS pod receives `KUBENDT_LINK_MTU` in its environment and sets it at boot, and the pass asks a guest before committing, so a deploy or a restart no longer commits in the guest.

### Fixed

- A set, its undo and the same set again (`set_ip` and `remove_ip`, `set_default_route` and `remove_default_route`, `link_up` and `link_down`, static routes, DNS, bridge, NAT and OSPF) skipped the third as a duplicate because a replay re-stamps the history. Every action now declares the state it sets or clears, the history keeps the last word on each piece of it.
- A restart or a modify puts the declared extra addresses back before replaying the history, so a route through one of them is applied instead of failing and dropping out of the history.
- A traceroute over IPv6 in ICMP mode showed every router as a timeout: BusyBox traceroute only matches UDP probes inside an ICMPv6 Time Exceeded, so the ICMP method sends UDP probes over IPv6.

## [1.5.0] - 2026-10-16

### Added

- Several nodes can be selected on the graph with a box (the wheel zooms while it is open) or with Shift or Ctrl and a click, and moved together.
- Links in the topology JSON take `delay`, `jitter`, `loss` and `bandwidth`. They are applied on both ends once the link is wired, persisted like any operation so a restart replays them, echoed by `GET /network/get-network` and shown in the link panel.
- The traffic control panel creates and edits htb class trees: link rate, and per class its rate, ceil, priority, matches and leaf queue, with the default class marked.
- Shaping requests take `qdisc: htb` with `classes`: a link rate split into classes with guaranteed rate, ceil, priority, flower matches (protocol, addresses, ports, DSCP, VLAN) and a leaf queue, pfifo, fq_codel, sfq or a netem with impairments. The read-back returns the tree with its classes, matches and leaves, and `tc_fidelity.py` has a QoS case.
- A configure request takes a `links` section that shapes a link by one of its ends: the peer comes from the topology, `symmetric` repeats the shaping on it, `reverse` puts a different one there and `remove` clears both. The panel offers to shape the other end too, and the link menu opens it for both ends.
- A shaping request combines delay, jitter, loss, duplicate, corrupt, reorder and a rate in one netem, with a jitter distribution and a Gilbert-Elliott loss model as options. The panel edits all of them and offers presets, and `tc_fidelity.py` measures the composite and the models.
- `GET /pods/tc/{namespace}/{pod}` returns the traffic control state of every interface of a pod. The Links tab uses it and marks the shaped interfaces.
- Every action is validated before it runs: a missing or malformed field fails the action with the field named, and nothing reaches the pod.
- A `custom` action takes an optional `executor`: `kubectl` by default, `ssh_qemu` for a shell in a VyOS guest or `vyos_ssh_cli` for an op-mode command there.
- A deploy that times out on a node that never gets Ready quotes its readiness probe, so an image without `ip` reads as such.
- `GET /network/export-conf/{namespace}` returns the configuration applied in a namespace as a configure request, and the dashboard downloads it with Export network conf, ready for Load network conf.
- The namespace bar shows the pods and links of the topology on one side of the name and the vCPU and MiB the namespace uses on the other.
- `GET /pods/counters/{namespace}/{pod}` returns the kernel counters of every interface of a pod. The interface detail of the node panel and both ends of the link panel show rates, packets, errors and drops, read every 5 s while open.
- View logs in the node menu and a button in the node panel open a floating viewer that follows the log, with filter, timestamps, the previous instance after a restart and download. `GET /pods/logs/{namespace}/{pod}` returns it as text and the WebSocket variant streams it.
- `GET /iperf/run/{namespace}/{pod}` runs an iperf3 throughput test from a pod toward an address of the namespace, TCP or UDP, with the server started by the backend on the destination node or one the lab already runs, and the WebSocket variant streams it interval by interval.
- New iperf (throughput test) window opens from Tools in the node menu: destination node or address, the server started by KubeNDT or one already running, TCP or UDP, duration, rate, streams and reverse, with one bar per second as it runs, the end totals and a JSON export.
- `doc/DRIVERS.md` explains how to write a driver: the minimum per node type, every optional interface and what happens when it is missing, where a tool runs, and the tests a new driver has to pass.
- Throughput tests run on VyOS nodes too, as source or as server: the image build puts a static iperf3 3.21 in the guest, since VyOS ships 3.12, and the driver declares it.

### Changed

- Every traffic control action runs as one `tc -b` batch instead of one exec per command, so an apply costs one round trip whatever its size and the action's commands are reported as a single batch.
- The traffic control panel is per node: it lists the node's interfaces with their shaping state and edits the one picked. It opens from the node's context menu too, and the link menu and the Links tab open it on that interface.
- Traffic control state is read from tc's JSON output in canonical units, with a `shaped` flag and the settings the old parser ignored: netem rate, reorder, Gilbert-Elliott loss and child qdiscs. A `tc` without `-j` is read through the toolbox container.
- Actions are declared once in a registry next to their capability. `GET /drivers` lists each action's parameters with the JSON names the API takes, their kind and whether they are required, instead of the Go method names.
- Drivers name the executor their actions run through and map guest interface names when they differ from the pod, instead of planning every action by hand. `GET /drivers` reports `vyos_api_apply` for VyOS. The configure request's action entry is now fully described in Swagger.
- The configure request, the replay after a restart, the peer replay and the link attributes run actions through one pipeline. An action stops at its first failing command and the configure summary counts actions, not commands.
- The node and link menus of the graph group the shells, the tools, the captures and the traffic control entries into submenus, so each menu stays at five or six rows.
- Capture, traceroute and traffic control share one `toolbox-*` ephemeral container per pod, reused whichever tool injected it, and `KUBENDT_TOOLBOX_IMAGE` names its image. The slim image moves to `deploy/custom_images/toolbox` and ships every tool the toolbox runs.
- Where a tool runs on a node is declared by its driver: a guest VM driver implements `GuestToolProvider` with the wrapper into the guest and the tools its image ships, the trace and the throughput test go through one runner, and the pod listing and `GET /drivers` publish `tools`.
- A driver declares the devices its pods need and the shells its nodes offer (`DeviceProvider`, `ShellProvider`). The pod listing publishes `shells`, the node menu builds its Shell group from it, and the shell endpoint takes any declared mode.
- Pods carry the `kubendt/runtime` label only. The `kubendt/qemu` label is no longer set, and the backend reads the runtime in one place, still understanding that label on pods from older versions. A double-click on a node opens the driver's own console when it has one.
- The VyOS disk build pins the rolling release the image is validated with instead of taking the newest because rollings from 2026.09 on rename the interfaces at boot before the per-pod configuration lands.

### Fixed

- Traffic control rejects a malformed or out-of-range value instead of dropping it and reporting success. The error names the field and the accepted range.
- Re-applying shaping starts from a clean root qdisc. `tc qdisc replace` kept the parameters a new request did not mention, so a `corrupt` set once survived later updates.
- `del_qdisc` on an interface without shaping succeeds instead of failing with "Cannot delete qdisc with handle of zero".
- A `tbf` request only needs `rate`. `burst` and `latency` default to `32kbit` and `50ms`. A request without `tcparams` or with an unsupported qdisc says so instead of "unrecognized or unsupported action".
- A replayed `custom` action runs through kubectl, as it did the first time, instead of the driver's default executor.
- An action that fails because the image lacks a program names the program, instead of the raw exec error.
- A configure request that names a pod twice, by hand or through a `links` entry, runs its actions as one job in request order, instead of two jobs at once persisted in whichever order they landed.
- The traffic control panel reports the backend's error when an action is rejected instead of saying it was applied, keeps tc units for rate and burst instead of whole Mbit and mislabeled kilobytes, and shows an interface on the kernel default qdisc as unshaped.
- Opening traffic control on a node whose image has no `tc` is faster the first time: a toolbox left by capture or trace is used directly, and a new one is awaited every 200 ms instead of every second.
- Two links between the same two nodes are both returned by `GET /network/get-network` and drawn side by side on the graph. The read deduplicated links by node pair, so a second parallel link never reached the graph or the export, and the graph drew the two on top of each other.
- The chip that counts a multi-node selection sits under the search row of the graph instead of over it.

## [1.4.0] - 2026-09-26

### Added

- Nodes accept an optional `terminationGracePeriodSeconds` in the topology JSON. The platform default is now 2 s instead of the Kubernetes 30 s, since emulated nodes are stateless and the usual `sleep infinity` entrypoint ignores SIGTERM, so every operation that recreates a pod finishes sooner. Set it higher per node for workloads that need an orderly shutdown.
- Deploy, modify and restart responses carry a `timeline` block next to `took_time`: the Kubernetes lifecycle stamps of every pod the operation created or recreated, plus `backend_ms` with the backend's own phases. See `doc/TIMING.md`.
- Nodes accept `nodeSelector` and `nodeName` in the topology JSON, validated against the cluster before anything is created (a `400` names the problem instead of pods left Pending). `nodeName` also fixes whether Meshnet realizes each link as a veth pair or a VXLAN tunnel. `GET /network/get-network` echoes both, and `terminationGracePeriodSeconds`, so an exported topology imports back identically.
- New `GET /network/links/{namespace}` lists every link as it exists on the cluster, with the worker behind each endpoint and its `realization` (`veth`, `vxlan`, `external` or `pending`). The link panel in the dashboard shows it.
- `doc/FAILURE_MODEL.md` describes what KubeNDT survives and what it does not: where each piece of state lives, how interrupted operations and stale locks are recovered, which recreations replay covers, and what stays manual.
- `experiments/`: scripts that measure the platform through its public API (deployment, configure, in-place modification, restart, replay, link fidelity, traffic control, VM routers, footprint, telemetry) plus the end-to-end regression over the six examples. They need a cluster and are not part of CI. See `experiments/README.md`.
- Restart and modify responses report the re-application of neighbour state apart from the replay, as `took_time.peer_replay` and `timeline.backend_ms.peer_replay`. `replay` covers the recreated pods' own history only.

### Changed

- The default readiness probe runs immediately and every 2 s instead of after 5 s and every 5 s, so a container that starts in a second is Ready in about a second. Its failure threshold is raised to 30. Drivers with their own probe (VyOS) are unchanged.
- Deploy, modify and restart watch pod events instead of polling every 5 s, and the fixed settle sleeps after them are gone. The interface validation re-checks a link that looks broken a few times over 3 s before restarting a pod.
- The post-operation interface check and repair is called the heal pass everywhere instead of "reconciliation". No behavior change. `took_time.reconciliation` keeps its key and equals `timeline.backend_ms.heal`.
- Ready means configurable for nodes whose control daemon starts after the container: the VyOS probe also waits for the guest's HTTP API, and Open vSwitch nodes are probed with `ovs-vsctl show`. VyOS becomes Ready a few seconds later than before.
- Example images are pinned to version tags. FRR moves to `quay.io/frrouting/frr:10.7.1`, `ubuntu:20.04` and `mongo:6.0` are replaced by current releases, and the capture and traffic-control helper is pinned to `nicolaka/netshoot:v0.16`. Images that publish no version tags keep their moving tag.
- FRR nodes in the examples start through the image's own init (`watchfrr` under `tini`). The startup command installs `iptables`, which the NAT actions need, and enables `ospfd`.
- FRR nodes are Ready only when zebra and every daemon enabled in `/etc/frr/daemons` answer on their vty socket.
- Deploy validation reads the namespace's Topology objects once instead of one per pod, so the validation phase takes about 0.3 s whatever the size.
- The cluster deployment guide puts the kubelet's `allowedUnsafeSysctls` in the KubeletConfiguration that kubeadm manages instead of a hand edit of `/var/lib/kubelet/config.yaml`, which a minor upgrade rewrites.
- The samples of the import, modify and configure dialogs, the node tables of the example READMEs and the Swagger examples use the pinned images and the current FRR startup. The six example READMEs share one structure, and the VyOS and all-features READMEs describe what their configurations actually do.

### Fixed

- Restarting a pod left its neighbours half configured: Meshnet recreates the neighbour's interface with the pod, and the bridge port, qdiscs, addresses and routes on it were lost. Every recreation KubeNDT orchestrates now re-applies the persisted operations that touch the recreated interface on each neighbour and refreshes the TC redirect of guest-VM neighbours. Replay also retries a failing command for about 12 s before pruning it from the history, and the OVS `setup_bridge` and `add_port` actions use `ovs-vsctl --may-exist`.
- Operation history left behind by a deleted namespace could make configure skip actions in a new namespace with the same name and replay them onto its pods. An action now counts as already applied only when its row is newer than the target pod, and rows older than the Namespace object are dropped.
- Adding a link between two existing pods took about 20 s and reported no restarted pod, because the wiring was left to the heal pass. The endpoint is now recreated in the modify's own restart phase and listed in `restarted_pods` with its timeline.
- The Kubernetes client allows 100 requests per second with a burst of 200 instead of client-go's default 5 and 10, which queued every per-pod call on large topologies. `KUBENDT_K8S_QPS` and `KUBENDT_K8S_BURST` tune it.
- The `add_static_route` action ran `ip route add`. It now runs `ip route replace`, like the default route already did, so re-applying is a no-op and a new next hop for the same prefix wins.

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

[Unreleased]: https://github.com/emigcm98/kubendt/compare/v1.7.0...HEAD
[1.7.0]: https://github.com/emigcm98/kubendt/compare/v1.6.0...v1.7.0
[1.6.0]: https://github.com/emigcm98/kubendt/compare/v1.5.0...v1.6.0
[1.5.0]: https://github.com/emigcm98/kubendt/compare/v1.4.0...v1.5.0
[1.4.0]: https://github.com/emigcm98/kubendt/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/emigcm98/kubendt/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/emigcm98/kubendt/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/emigcm98/kubendt/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/emigcm98/kubendt/releases/tag/v1.0.0
