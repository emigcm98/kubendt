# 2-test-frr-ospf

FRR + OSPF end-to-end scenario to validate dynamic routing workflows in KubeNDT:

- Topology import and deployment
- Driver-based network configuration
- FRR daemon configuration from the UI
- OSPF adjacency between routers
- L3 connectivity across multiple routed subnets

## Topology Overview

![Topology](../../../doc/images/tests/2-test-frr-ospf.png)

Nodes deployed:

| Node | Driver | Image | Replicas | Role |
| --- | --- | --- | --- | --- |
| `host` | `BasicHostDriver` | `alpine:3.24.2` | 4 | Alpine hosts, two per LAN |
| `web` | `BasicHostDriver` | `nginxdemos/hello:0.4` | 1 | Web server on its own LAN behind `router2` |
| `router1` | `FRRRouterDriver` | `quay.io/frrouting/frr:10.7.1` | 1 | Edge router: OSPF, SNAT on `eth0` for upstream access |
| `router2` | `FRRRouterDriver` | `quay.io/frrouting/frr:10.7.1` | 1 | Internal router: OSPF, default route via `router1` |
| `switch` | `LinuxSwitchDriver` | `ubuntu:26.04` | 3 | Linux bridge switches, one per LAN |

Nodes without a `driver` in the topology JSON get the default of their type: `BasicHostDriver` for hosts and `LinuxSwitchDriver` for switches.

Network segments:

| Segment | Subnet | Purpose |
| --- | --- | --- |
| LAN 1 | `192.168.1.0/24` | `host-0` and `host-1` behind `switch-0`; `router1 eth1` (`.1`) is the gateway |
| Transit | `10.0.0.0/30` | Point-to-point link between `router1 eth2` (`.1`) and `router2 eth2` (`.2`) |
| LAN 2 | `192.168.2.0/24` | `host-2` and `host-3` behind `switch-1`; `router2 eth1` (`.1`) is the gateway |
| Web LAN | `192.168.3.0/24` | `web-0` (`.10`) behind `switch-2`; `router2 eth3` (`.1`) is the gateway |

## Files In This Folder

- `topology-network-test-frr-ospf.json`: network inventory and links
- `network_conf.json`: post-deploy actions (host addresses and default routes, bridge setup, SNAT, and OSPF configuration)

## Notable Characteristics

- Both routers use the `FRRRouterDriver` and run `quay.io/frrouting/frr:10.7.1` through the image's own init (`watchfrr` under `tini`). The startup command installs `iptables`, which `enable_snat` needs and the image does not ship, and enables `ospfd`. The hostname is the pod name, and a router is Ready only when zebra and the enabled daemons answer on their vty socket.
- `router1` enables `SNAT` on `eth0`, acting as the upstream edge router. This enables internet access for all devices through the CNI.
- `router2` installs a default route via `10.0.0.1`.
- `switch-*` nodes are configured with Linux bridges using `setup_bridge` actions.
- `network_conf.json` replaces the `eth1` address of every `host-*` (`replace_ip`) before setting its default route, so the addresses used in the connectivity checks below are the ones it assigns, not the ones in the topology file.
- OSPF is configured declaratively via `ospf_add_network` driver actions in `network_conf.json`.

## Step-By-Step (UI)

### 1. Create namespace

Create a namespace (e.g. `frr-ospf` or any name you prefer).

### 2. Import topology

- Go to the namespace graph view.
- Click **Import topology**.
- Select `topology-network-test-frr-ospf.json`.
- Wait until all nodes are running and visible.

### 3. Apply network configuration (including OSPF)

- Click **Load network conf**.
- Select `network_conf.json`.
- Confirm successful actions in the result dialog. This applies IP config, bridges, SNAT, and OSPF configuration on both routers in one step.

### 4. Validate OSPF adjacency and learned routes

- Open a vtysh console on `router1` by selecting the node and clicking the blue shell button, or open a shell and run:

  ```bash
  vtysh -c "show ip ospf neighbor"
  ```

  Expected: neighbor relationship with `router2` in `Full` state.

- On `router1`, check learned routes:

  ```bash
  vtysh -c "show ip route"
  ```

  Expected OSPF-learned routes for `192.168.2.0/24` and `192.168.3.0/24`.

- On `router2`, check learned routes:

  ```bash
  vtysh -c "show ip route"
  ```

  Expected OSPF-learned route for `192.168.1.0/24`.

### 5. Validate end-to-end connectivity

- From `host-0` to `host-1` (same subnet):

  ```bash
  ping -c 3 192.168.1.52
  ```

- From `host-0` to `host-2` (across both routers):

  ```bash
  ping -c 3 192.168.2.51
  ```

- From `host-1` to `web-0`:

  ```bash
  ping -c 3 192.168.3.10
  ```

- From `host-1`, verify application reachability:

  ```bash
  wget -qO- http://192.168.3.10
  ```

  Expected output from the `nginxdemos/hello` page.

## Troubleshooting

- If `show ip ospf neighbor` is empty, verify both routers have the OSPF actions applied and that the `10.0.0.0/30` link is up.
- If inter-subnet ping fails, verify `router1` and `router2` learned OSPF routes with `vtysh -c "show ip route"`.
- If same-subnet ping fails, verify `br0` exists on the corresponding `switch-*` node and includes all expected interfaces.
- If `web-0` is unreachable, verify `router2` announces `192.168.3.0/24` (its `ospf_add_network` actions in `network_conf.json`) and that `web-0` has the default route `192.168.3.1`.
- If outbound internet access is required from downstream networks, verify `router1-0` has `SNAT` enabled on `eth0` and `router2` keeps its default route via `10.0.0.1`.
