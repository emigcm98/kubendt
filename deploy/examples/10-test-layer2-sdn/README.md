# 10-test-layer2-sdn

One site at layer 2: three switches in a triangle with two VLANs across their trunks, spanning tree breaking the loop, and the two ways to route between the VLANs, a router on a stick and an SVI on a layer 3 switch. Then the second part: the two Open vSwitch switches are handed to a Faucet controller over a management network, which runs the same VLANs from a YAML in the file manager, applies an ACL live and keeps the switches forwarding while it restarts.

- VLANs on three switches of two kinds, Open vSwitch and a Linux bridge, with access ports for the hosts and 802.1Q trunks between the switches
- Classic spanning tree across both kinds, a root chosen by priority and the port it blocks read from the State tab
- Inter-VLAN routing twice: FRR subinterfaces on one trunk (router on a stick), then VLAN interfaces on the Linux bridge itself (an SVI), with the hosts' gateway moved from one to the other
- The OVS pair under Faucet: the VLANs from its YAML, the datapaths, ports and learned hosts read from the controller, an ACL that cuts a host applied by a reload, fail mode secure through a controller restart, and the OpenFlow session in a capture on the management link

## Topology Overview

![Topology](../../../doc/images/tests/10-test-layer2-sdn.png)

Nodes deployed:

| Node | Driver | Image | Role |
| --- | --- | --- | --- |
| `h1`, `h3` | `HostDriver` | `alpine:3.24.2` | Hosts in VLAN 10, one behind each OVS switch |
| `h2`, `h4` | `HostDriver` | `alpine:3.24.2` | Hosts in VLAN 20, one behind each OVS switch |
| `sw1`, `sw2` | `OpenVSwitchDriver` | `ghcr.io/emigcm98/kubendt-ovs:3.7.1` | Access switches; `sw1` is the spanning tree root |
| `sw3` | `LinuxSwitchDriver` | `ubuntu:26.04` | A Linux bridge closing the triangle, the layer 3 switch of the SVI step |
| `r1` | `FRRRouterDriver` | `quay.io/frrouting/frr:10.7.1` | The router on a stick, one trunk into `sw1` |
| `mgmt` | `LinuxSwitchDriver` | `ubuntu:26.04` | The management switch, out of band |
| `ctrl` | `FaucetControllerDriver` | `faucet/faucet:1.10.12` | The Faucet controller, in charge of `sw1` and `sw2` in the second part |

VLANs and addresses:

| VLAN | Subnet | Hosts | Router on a stick | SVI |
| --- | --- | --- | --- | --- |
| 10 | `10.0.10.0/24` | `h1` (`.11`), `h3` (`.13`) | `r1 eth1.10` (`.1`) | `sw3 br0.10` (`.254`) |
| 20 | `10.0.20.0/24` | `h2` (`.12`), `h4` (`.14`) | `r1 eth1.20` (`.1`) | `sw3 br0.20` (`.254`) |

Links:

| Link | Ends | Carries |
| --- | --- | --- |
| `h1-sw1`, `h2-sw1` | `h1 eth1`, `h2 eth1` to `sw1 eth1`, `eth2` | Access, VLAN 10 and 20 |
| `h3-sw2`, `h4-sw2` | `h3 eth1`, `h4 eth1` to `sw2 eth1`, `eth2` | Access, VLAN 10 and 20 |
| `trunk-sw1-sw2` | `sw1 eth3` to `sw2 eth3` | Trunk, VLANs 10 and 20 |
| `trunk-sw2-sw3` | `sw2 eth4` to `sw3 eth1` | Trunk, VLANs 10 and 20; the side spanning tree blocks |
| `trunk-sw3-sw1` | `sw3 eth2` to `sw1 eth4` | Trunk, VLANs 10 and 20 |
| `r1-sw1` | `r1 eth1` to `sw1 eth5` | Trunk, the router on a stick |
| `mgmt-sw1`, `mgmt-sw2`, `mgmt-ctrl` | `sw1 eth9` (`10.0.100.11`), `sw2 eth9` (`10.0.100.12`), `ctrl eth1` (`10.0.100.1`) to `mgmt` | The management network, outside the bridges |

The three switches form a loop: `sw1` to `sw2`, `sw2` to `sw3`, `sw3` to `sw1`. Spanning tree runs on all three with `sw1` as root (priority 4096) and `sw2` next (8192), and the trunk costs set by hand (10 on every trunk port, 100 on `sw3`'s toward `sw2`), so the port `sw3` has toward `sw2` is the one left blocking whatever the pods' placement, and the hosts of VLAN 10 reach each other over `sw1-sw2` directly.

## Files In This Folder

- `topology-network-test-layer2-sdn.json`: the ten nodes and eleven links, the management network included
- `network_conf_switches.json`: the bridges of the three switches and the management switch, spanning tree on the triangle with the priorities
- `network_conf_vlans.json`: access ports for the hosts, the trunks between the switches and toward the router
- `network_conf_router.json`: the two subinterfaces of `r1` and the hosts' default route toward them
- `network_conf_svi.json`: the two VLAN interfaces on `sw3`'s bridge and the hosts' default route moved to them
- `faucet.yaml`: the controller's configuration, the two datapaths with the VLANs of the first part on the same ports
- `faucet-acl.yaml`: the same with an ACL on `sw1`'s first port that drops `h1`'s IPv4
- `network_conf_sdn.json`: spanning tree and the port VLANs off the OVS pair, their datapath ids, fail mode and controller, and `faucet.yaml` applied on `ctrl`
- `network_conf_acl.json`: `faucet-acl.yaml` applied on `ctrl`

Apply them in that order. After the first two the hosts of a VLAN see each other and nothing of the other VLAN; the third routes between the VLANs through `r1`; the fourth does the same through `sw3` instead. The second part uploads the two YAML files to the file manager first, then `network_conf_sdn.json` hands the OVS pair to Faucet and `network_conf_acl.json` cuts `h1`.

## Notable Characteristics

- Spanning tree runs in its classic flavour on the three switches (`stp_enable` with `mode: stp`), since the Linux bridge speaks 802.1D only and Open vSwitch then meets it on the same path costs. On OVS alone `rstp` is the default and converges in a second instead of thirty.
- The root is chosen, not left to the lowest MAC: `stp_set_bridge_priority` 4096 on `sw1` and 8192 on `sw2` fixes which port blocks, so the State tab reads the same on every deploy. The path costs are set too (`stp_set_port`): the kernel and OVS take them from the link speed, 2 on a veth that reports 10 Gbit/s and 100 on a VXLAN device that reports none, which would move the blocked port with the placement of the pods.
- `sw3` is a plain `LinuxSwitchDriver`, like `mgmt`: the Linux switch carries the layer 3 and subinterface capabilities, so giving its bridge VLAN interfaces with addresses makes it a layer 3 switch without changing the driver.
- VLAN filtering on the Linux bridge comes with its first rule: `vlan_set_trunk` on `sw3 eth1` turns `vlan_filtering` on, and the bridge device itself is a port like the others, which is what the SVI step uses: `vlan_set_trunk` on `br0` lets the tagged frames of 10 and 20 reach the bridge's own interfaces `br0.10` and `br0.20`.
- The hosts keep one default route; the router and the SVI steps each `set_default_route` to their gateway, so the second replaces the first and a trace shows which box routes.
- The management network is ordinary links with addresses on the switch pods' `eth9`, left out of the bridges, and it is where the OpenFlow session runs, so a capture on `mgmt-ctrl` shows it.
- Faucet runs no spanning tree, so the triangle's third side is closed by configuration: `sw2`'s port toward `sw3` is not in `faucet.yaml`, and an OpenFlow switch drops what its controller said nothing about. `sw3` keeps its classic spanning tree and reaches the rest through `sw1` only.
- The OVS ports' own VLAN settings are cleared before the handover (`vlan_clear_port`), as spanning tree is: under a controller the switch does nothing by itself, the VLANs are the `native_vlan` and `tagged_vlans` of the YAML. Its `interfaces` are OpenFlow port numbers, which the OVS driver takes from the interface name, `eth3` is port 3, so a neighbour's restart does not renumber them. **Datapath ports** on `ctrl` shows them.
- The controller's configuration is a file of the namespace's file manager: `controller_apply_config` reads it when it runs, writes it into the pod and Faucet reloads on a signal, so a restart of `ctrl` replays the last file applied. The datapath ids are set by hand (`set_datapath_id`) so the YAML can name them.
- `set_controller` takes several targets separated by spaces, so a bridge can keep a second controller. This example uses one.

## Step-By-Step (UI)

### 1. Create namespace

Create a namespace (e.g. `layer2-sdn` or any name you prefer).

### 2. Import topology

- Go to the namespace graph view.
- Click **Import topology**.
- Select `topology-network-test-layer2-sdn.json`.
- Wait until the ten nodes are running (about half a minute).

### 3. Bridges and spanning tree

- Click **Load network conf** and select `network_conf_switches.json`.
- Wait half a minute, the listening and learning states of classic STP.
- Open the panel of `sw3`, tab **State**, section **Spanning tree ports**: `eth2` is the root port and `eth1` is `blocking`. On `sw1`, **Spanning tree bridges** shows it as root.

### 4. VLANs

- Load `network_conf_vlans.json`.
- Panel of `sw1`, **State**, **VLAN ports**: `eth1` access 10, `eth2` access 20, `eth3` to `eth5` trunks of 10 and 20.
- From `h1` (VLAN 10), reach `h3` behind the other switch, and not `h2` on the same switch:

  ```bash
  ping -c 3 10.0.10.13
  ping -c 3 10.0.20.12
  ```

- Capture on `sw1 eth3` (the trunk) from the link's menu while `h1` pings `h3`: the frames carry an 802.1Q tag with VLAN 10. On `sw1 eth1`, the access port, they carry none.
- **Bridge FDB** on `sw1` lists each host's MAC with its VLAN, `h3`'s and `h4`'s on the trunk `eth3`.

### 5. Router on a stick

- Load `network_conf_router.json`.
- Panel of `r1`, tab **State**, **Routes**: `10.0.10.0/24` connected on `eth1.10` and `10.0.20.0/24` on `eth1.20`, the two subinterfaces. They are not links, so the Links tab does not list them; `ip addr` in the terminal shows them with their addresses.
- From `h1`, reach `h2` in the other VLAN, and see the router on the way:

  ```bash
  ping -c 3 10.0.20.12
  traceroute -n 10.0.20.14
  ```

  The first hop is `10.0.10.1`, `r1`. The frames go up the trunk `sw1 eth5` tagged 10 and come back tagged 20.

### 6. An SVI instead

- Load `network_conf_svi.json`.
- Panel of `sw3`, **State**: **Routes** shows `10.0.10.0/24` on `br0.10` and `10.0.20.0/24` on `br0.20`, the bridge's own VLAN interfaces, and **VLAN ports** now lists `br0` itself as a trunk.
- The same trace from `h1` now shows `10.0.10.254`: `sw3` routes, `r1` is idle though still there.

### 7. Hand the OVS pair to Faucet

- Open **File Manager** and upload `faucet.yaml` and `faucet-acl.yaml` to the namespace.
- Load `network_conf_sdn.json`: spanning tree and the port VLANs come off `sw1` and `sw2`, each gets its datapath id, fail mode `secure` and the controller at `10.0.100.1`, and `ctrl` applies `faucet.yaml`.
- Panel of `sw1`, **State**, **OpenFlow bridges**: controller `tcp:10.0.100.1:6653`, connected `yes`, DPID `0000000000000001`. **OpenFlow flows** lists the tables Faucet installed, `push_vlan` and `pop_vlan` among their actions.
- Panel of `ctrl`, **State**: **Datapaths** shows `sw1` and `sw2` up, **Datapath ports** their ports, and **Learned hosts** fills as the hosts talk. From `h1`, the same pings as before:

  ```bash
  ping -c 3 10.0.10.13
  ping -c 3 10.0.20.12
  ```

  The VLANs and the SVI on `sw3` work the same, now by Faucet's flows on `sw1` and `sw2`.

- Capture on the link `mgmt-ctrl` from its menu: the OpenFlow session on port 6653, packet-in and flow-mod messages while a host starts talking.

### 8. An ACL, live

- Load `network_conf_acl.json`: `ctrl` applies `faucet-acl.yaml`, the same file with an ACL on `sw1`'s port 1 that drops IPv4 from `10.0.10.11`. From `h1`, `ping -c 3 10.0.10.13` fails while `h2`'s pings go on. Faucet reloaded, no switch was touched.
- On `ctrl`'s panel, **Apply controller configuration** with `faucet.yaml` lifts it.

## Experiments

- **The storm.** Take `sw3` out of spanning tree (`stp_disable` on `br0` through **Load network conf** or the panel), then from `h1` ask for an address nobody has, `ping -c 1 10.0.10.99`, and watch the counters of any trunk from the interface menu: that one ARP request goes round the triangle forever and the counters climb by tens of thousands a second (the hosts' own IPv6 multicast often starts it before you do). `stp_enable` again and it stops once the ports are back in listening. A few seconds are enough, the loop runs at the speed of the links.
- **Failover.** Disable `sw1 eth4` (the `sw3-sw1` trunk) from the interface menu: `sw3`'s blocked port toward `sw2` takes over after the forward delay, and `h1` still reaches `h2` through the SVI, now over `sw2`. Enable it back and the tree returns to its first shape.
- **Restart a switch.** Restart `sw1` from its menu: its bridge, VLANs and spanning tree are replayed, the root comes back where it was, and the pings pass again some ten seconds after. Under Faucet the replay carries the datapath id, the fail mode and the controller instead, and the controller pushes its flows again once the switch connects.
- **The controller goes away.** Restart `ctrl` from its menu while `h1` pings `h3`: the pings go on. Fail mode `secure` keeps the flows the switches have, they only stop learning until the controller is back; **OpenFlow bridges** reads connected `no` meanwhile and `yes` once Faucet listens again with the last YAML replayed. Set the fail mode to `standalone` on `sw1` (**Set fail mode** on its panel) and restart `ctrl` again: without a controller the switch falls back to a flat learning switch, `h1` still reaches `h3` but untagged, and the SVI on `sw3` no longer answers since its trunk expects tags.

## Troubleshooting

- The hosts of a VLAN do not see each other right after the VLAN step: spanning tree is still in listening or learning on a port of the path, thirty seconds in classic STP. The State tab of the switch shows the port's state.
- Both VLANs see each other after the VLAN step: a trunk is missing a VLAN or an access port got the wrong tag; **VLAN ports** on each switch shows what each port carries.
- `traceroute` from a host stops at the first hop: the default route points at a gateway of the other step, or `r1` has no subinterface for that VLAN; `ip route` on the host and **Routes** on the router tell which.
- `br0.10` on `sw3` gets no traffic: `br0` itself is not a trunk of VLAN 10, the `vlan_set_trunk` on the bridge device in `network_conf_svi.json` is what lets the tagged frames reach it.
- **Datapaths** on `ctrl` shows a switch down or missing: its datapath id is not the one in the YAML (`set_datapath_id` wrote `0x1` and `0x2`), or the switch cannot reach `10.0.100.1` over the management link. Faucet says why in the pod's logs.
- `controller_apply_config` fails with "not valid YAML": the file in the file manager has a tab or an indentation slip. Faucet keeps the previous one.
- The hosts lose each other for a few seconds right after `network_conf_sdn.json`: Faucet installs its tables when the switches connect, and the hosts are learned again as they talk.

> Last reviewed: 7 Nov 2026
