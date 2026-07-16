# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Traceroute from any L3-capable node toward an IP or hostname, with every hop
  resolved to a topology node so the path can be followed on the graph. The
  probe runs in a shared ephemeral debug container, so the source image needs
  no traceroute of its own.
- Two ways to run a trace. A live WebSocket stream emits each hop as it arrives
  (starting, resolving, tracing, done), and a REST endpoint returns the whole
  run as a single JSON document for scripting and automation.
- Selectable probe method (ICMP, UDP or TCP SYN to port 80) and a metrics mode
  built on mtr. Metrics mode runs a configurable number of cycles and reports
  per-hop loss, average, best, worst, last, standard deviation (jitter),
  geometric mean and packets sent.
- Per-hop detail beyond plain traceroute. Each hop carries its resolved node
  and ingress interface, a kind (resolved L3, external IP or timeout), the ICMP
  unreachable flag when a router drops the probe, and the pod path it crossed,
  marking whether a segment is a real link or an overlay tunnel such as GTP-U.
  Runtime-applied IPs and tunnel endpoints (interfaces like `ogstun` or
  `uesimtun0`) are recognized, not only addresses declared in the topology.
- A final outcome for the run (delivered, unreachable or unreached), with early
  stop on black holes and explicit unreachable replies.
- Traceroute control panel in the UI, opened from a node's context menu. It
  lets you pick the destination by typing or choosing a topology node, select
  method and mode, and adjust the metrics cycles. A packet animation walks the
  path hop by hop over the graph, drawing tunnels and external exits
  differently and marking where the packet is delivered or dropped. Playback
  controls (play, pause, step, scrub) let you replay the traced path, and the
  full result can be downloaded as JSON.

## [1.0.0] - 2026-07-09

Initial public release. KubeNDT deploys and operates virtual network topologies
on Kubernetes, defined declaratively and materialized as Kubernetes-native
resources over the Meshnet CNI.

### Added

- Declarative topologies deployed as StatefulSets over Meshnet CNI, with
  in-place add, remove and scaling of nodes and links and external uplinks to
  the host network.
- Driver and capability system covering hosts, routers (Linux, FRR, VyOS) and
  switches (Linux bridge, OVS), with L2/L3, DNS, traffic control, NAT and OSPF.
- Persisted driver operation history, replayed automatically on pod restart.
- Interactive network graph with status colors, drag, zoom, minimap, search and
  saved layouts, per-node info panel, interactive shell and metrics.
- Live packet capture on any pod interface, with BPF filter, pcap export and
  per-packet dissection.
- Per-namespace file manager with zip import and export, mounted into pods as
  ConfigMaps or Secrets.
- Kubernetes integration for kubeconfig and context selection, cluster status
  and per-node detail.
- Admin password login with browser sessions plus `kdt_` API tokens, and an
  option to disable auth for trusted networks.
- Health, readiness and version endpoints, and Swagger docs at `/swagger`.
- Container images published to GHCR and a Docker Compose deployment.

[Unreleased]: https://github.com/emigcm98/kubendt/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/emigcm98/kubendt/releases/tag/v1.0.0
