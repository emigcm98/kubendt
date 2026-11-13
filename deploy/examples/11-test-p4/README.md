# 11-test-p4

Three P4 switches in a triangle, the topology of the p4lang tutorials, with a host behind each. The switches are BMv2's `simple_switch` on the `kubendt-p4` image and run the tutorials' `basic.p4`: no learning, no routing protocol, every packet goes where a table entry says, and the entries are pushed from KubeNDT. Then the second part on the same deploy: one switch swaps its program live for `ecmp.p4`, where a destination maps to a group of next hops picked by a hash of the flow, and parallel flows spread over its two uplinks.

- `basic.p4` on three switches, the entries from files as the tutorials' `sX-runtime.json`, read back from the State tab and the inspector
- The hosts as the tutorials' Mininet sets them, a fixed MAC, a gateway nobody owns and its MAC as a permanent neighbor, since the program drops ARP
- A capture with the destination MAC rewritten and the TTL taken by each switch, and a path changed by replacing one entry, seen in the interface counters
- A switch restart, its ports, program and entries replayed, and the hosts' MACs and neighbors put back on their recreated ends
- A program swapped live on one switch, its tables empty until the entries follow, and equal-cost multipath measured with iperf

## Topology Overview

![Topology](../../../doc/images/tests/11-test-p4.png)

Nodes deployed:

| Node | Driver | Image | Role |
| --- | --- | --- | --- |
| `h1`, `h2`, `h3` | `HostDriver` | `alpine:3.24.2` | One host behind each switch |
| `s1`, `s2`, `s3` | `P4SwitchDriver` | `ghcr.io/emigcm98/kubendt-p4:1.15.8` | BMv2 `simple_switch`, `basic.p4` on all three, `ecmp.p4` on `s1` in the second part |

Addresses, the tutorials' plan:

| Host | Address | MAC | Gateway (address, MAC) |
| --- | --- | --- | --- |
| `h1` | `10.0.1.1/24` | `08:00:00:00:01:11` | `10.0.1.10`, `08:00:00:00:01:00` |
| `h2` | `10.0.2.2/24` | `08:00:00:00:02:22` | `10.0.2.20`, `08:00:00:00:02:00` |
| `h3` | `10.0.3.3/24` | `08:00:00:00:03:33` | `10.0.3.30`, `08:00:00:00:03:00` |

Links:

| Link | Ends | BMv2 ports |
| --- | --- | --- |
| `h1-s1`, `h2-s2`, `h3-s3` | each host's `eth1` to its switch's `eth1` | port 1 of every switch |
| `s1-s2` | `s1 eth2` to `s2 eth2` | port 2 on both |
| `s1-s3` | `s1 eth3` to `s3 eth2` | port 3 of `s1`, port 2 of `s3` |
| `s2-s3` | `s2 eth3` to `s3 eth3` | port 3 on both |

The gateways do not exist: a host sends everything to its switch, which rewrites the destination MAC to the next hop's and the next switch does not look at it. The MAC a switch "has" (`08:00:00:00:0N:00`) is a convention of the entries, nothing carries it. Each switch's port number is its interface number, so an entry's output port reads from the topology.

## Files In This Folder

- `topology-network-test-p4.json`: the six nodes and six links
- `files/basic.p4`, `files/basic.json`, `files/basic.p4info.txtpb`: the tutorials' program, L3 forwarding by one longest-prefix table, and its compiled form for BMv2
- `files/ecmp.p4`, `files/ecmp.json`, `files/ecmp.p4info.txtpb`: the same program with a second table, `ecmp_nhop`, and an action that hashes the flow onto a group of next hops
- `network_conf_hosts.json`: a fixed MAC, the default route to the fake gateway and the gateway's MAC as a static neighbor, on each host
- `network_conf_switches.json`: the three interfaces of each switch as ports, `basic.json` loaded, `drop` as the default action
- `network_conf_entries.json`: the forwarding entries of the three switches, the tutorials' `s1-runtime.json` to `s3-runtime.json`
- `network_conf_reroute.json`: on `s1`, the entry for `h3` replaced so it leaves toward `s2`
- `network_conf_ecmp.json`: on `s1`, `ecmp.json` loaded and its entries, `h3` behind a group of two next hops
- `files/mcast.p4`, `files/mcast.json`, `files/mcast.p4info.txtpb`: `ecmp.p4` with a multicast action, and a copy never sent back out of its ingress port
- `network_conf_mcast.json`: `mcast.json` on the three switches with their entries, `224.0.0.0/4` to multicast group 1, and the group's ports on each switch, a tree over the triangle
- `files/arp.p4`, `files/arp.json`, `files/arp.p4info.txtpb`: `mcast.p4` with the switch answering ARP for its gateway, learning the hosts' MACs in a register and asking for a host it has not learned yet
- `network_conf_arp.json`: `arp.json` on the three switches with their entries by port alone, the gateway each answers for and the host port each asks on, and the hosts without the static neighbor

Upload the `files/` folder to the file manager first, deploy, then apply the configurations in that order. After the first three the hosts reach each other; the fourth bends one path; the fifth is the second part, the sixth the third and the seventh the fourth.

## Notable Characteristics

- The switch starts empty: `setup_bridge` gives it the pod's interfaces as ports, `load_program` the compiled program (a file of the namespace by name, read when the action runs), and the entries follow. Nothing of this lives in the image, so the same image runs any program.
- `basic.p4` has one table, `ipv4_lpm`, keyed by the destination address with longest-prefix match. Its `ipv4_forward` action takes the next hop's MAC and the output port, writes them into the frame and takes one from the TTL; `drop` is the default, so a destination without an entry goes nowhere, ARP included. That is why the hosts need their gateway's MAC as a static neighbor (`add_static_neighbor`) and a MAC the entries can name (`set_mac`), as the tutorials' Mininet does with `arp -s` and a fixed `mac`.
- The entries are actions like any other: `table_add` with the table, the action, the match key and the action's parameters as `simple_switch_CLI` takes them, kept in the history by table and key, so `table_delete` with the same key clears the add, and a restart replays them in order. `p4_entries` reads them back from the switch, with the handle BMv2 gave each.
- A program swap is `load_program` on a running switch: BMv2 swaps the new program in without dropping the ports, but its tables come up empty, so the entries must be added again. The action clears the old entries from the history, and `network_conf_ecmp.json` carries the new ones in the same request, under a second of outage in all.
- `ecmp.p4` adds `set_ecmp_group(group, count)` as a possible action of `ipv4_lpm`: it hashes source and destination address, protocol and ports (a CRC16, in v1model's `hash`) into a number below `count`, and a second table, `ecmp_nhop`, keyed by the group and that number, picks the next hop. Every packet of a flow hashes alike, so a flow never reorders; different flows spread.
- BMv2 reads and writes its ports through packet sockets, so it forwards whole frames and cannot send one larger than the port's MTU. The driver turns the segmentation and receive offloads off on its ports for that reason, and a port on a link to another worker, a VXLAN tunnel, reads through a veth pair of its own: the worker's NIC merges the TCP segments of a flow arriving through the tunnel into frames of several segments, and the veth, with its offloads off, has the kernel split them again before the switch sees them (`ip -d link` in the switch shows `eth2-tc` and `eth2-p4` next to such a port). A switch forwards a few hundred Mbit/s and some fifty thousand packets per second, software numbers, with two to three cores busy.
- `mcast.p4` adds `set_multicast_group(group)` to `ipv4_lpm`: it hands the packet to BMv2's replication engine, which copies it to every port of the group, each copy going through egress on its own with the group's replication id set. The ports of a group are not in the program: `add_multicast_group` sets them on the running switch (`mc_mgrp_create`, `mc_node_create` and `mc_node_associate` in the CLI), `remove_multicast_group` takes them away, **P4 multicast groups** shows them, and they survive a program swap, so `load_program` leaves them in the history. The egress drops a copy bound for the port it came in on, so a group may hold every port of the switch. The copies are the frame as the host built it, multicast MAC and TTL included.
- `arp.p4` makes the switch resolve its hosts, as a layer 3 switch does. It answers the ARP requests for the gateway address it owns (`arp_responder`, the reply built from the request and sent back out of its port), writes the source MAC of every ARP or IPv4 frame into a register indexed by port (`learned_mac`), and `ipv4_forward` takes the next hop's MAC from that register, so the entries carry only the port, and puts the switch's own MAC as the source, as a router does, which is what the next switch learns on the link between them. A packet for a host port with nothing learned yet becomes the ARP request for its destination, from the gateway `port_gateway` names for that port, and the host's reply teaches the switch. Other frames are left out of the register on purpose: the pod's own kernel sends IPv6 neighbour discovery out of the ports, and the switch reads it as if it had come in.
- The hosts' MACs are fixed by an action, not by Meshnet, which assigns a random one each time it creates an interface. A switch restart recreates the hosts' ends too, and the peer replay re-runs `set_mac` and `add_static_neighbor` on them, so the entries that name those MACs stay valid.

## Step-By-Step (UI)

### 1. Create namespace

Create a namespace (e.g. `p4` or any name you prefer).

### 2. Upload the programs

- Open **File Manager** and upload the six files of `files/` to the namespace, the `.p4` sources included so they can be read next to their compiled form.

### 3. Import topology

- Go to the namespace graph view.
- Click **Import topology**.
- Select `topology-network-test-p4.json`.
- Wait until the six nodes are running (the switches are ready once their CLI port answers).

### 4. Hosts

- Click **Load network conf** and select `network_conf_hosts.json`.
- Panel of `h1`, tab **State**, section **ARP and NDP**: `10.0.1.10` with `08:00:00:00:01:00` as `PERMANENT`. The **Links** tab shows `eth1` with its MAC, `08:00:00:00:01:11`.

### 5. Switches and their program

- Load `network_conf_switches.json`.
- Panel of `s1`, **State**: **P4 ports** lists `eth1` to `eth3` as ports 1 to 3, up; **P4 tables** lists `MyIngress.ipv4_lpm` with its match field, `ipv4.dstAddr (lpm, 32)`; **P4 table entries** is empty, every packet hits the default `drop`.
- From `h1`, `ping -c 2 10.0.2.2` fails.

### 6. The entries

- Load `network_conf_entries.json`.
- **P4 table entries** on `s1`: `10.0.1.1/32` out of port 1 toward `h1`'s MAC, `10.0.2.2/32` out of port 2, `10.0.3.3/32` out of port 3, each with its handle. The switch shows keys and parameters as it stores them, in hex (`0a000101/32`, `080000000111, 01`), and the default action of each table as one more row.
- From `h1`, reach the other two:

  ```bash
  ping -c 3 10.0.2.2
  ping -c 3 10.0.3.3
  ```

  The replies show `ttl=62`: two switches on the way, each took one.

- Capture on the link `s1-s3` from its menu while `h1` pings `h3`: the frames from `h1` carry `08:00:00:00:03:00` as destination, the MAC `s1`'s entry wrote, and a TTL of 63 already.
- **Throughput test** from `h1` to `10.0.3.3`: a few hundred Mbit/s through two software switches.

### 7. Bend a path

- Load `network_conf_reroute.json`: on `s1`, the entry for `10.0.3.3/32` is deleted and added again out of port 2. `h1`'s packets to `h3` now go to `s2`, which forwards them to `s3` by its own entry; `h3`'s replies still come straight back, `s3`'s entry for `h1` did not change.
- Open the counters of `s1 eth2` and `s1 eth3` from the interface menu while `h1` pings `h3`: `eth2` transmits, `eth3` only receives.

### 8. Restart a switch

- Restart `s2` from its menu: its ports, program and entries come back from the history, and **P4 table entries** reads the same three. The hosts' ends `s2` faced were recreated too; `h2`'s MAC and neighbor were put back by the peer replay, so `ping 10.0.2.2` from `h1` passes again a few seconds after.

### 9. ECMP on `s1`, live

- Load `network_conf_ecmp.json`: `s1` loads `ecmp.json` in place of `basic.json` (its ports stay, its tables come up empty) and gets its entries in the same request: `h1` and `h2` as before, and `10.0.3.3/32` to `set_ecmp_group` 1 with two members, port 3 straight to `s3` and port 2 through `s2`, in `ecmp_nhop`.
- **P4 tables** on `s1` now lists `ecmp_nhop` with its two match fields; **P4 table entries** the group's two next hops, keyed by group 1 and index 0 or 1. `s2` and `s3` still run `basic.p4`.
- From `h1`, `ping -c 3 10.0.3.3`: it passes, every echo of the same flow by the same uplink.
- Open the counters of `s1 eth2` and `s1 eth3`, then run the **Throughput test** from `h1` to `10.0.3.3` several times: each run is a new flow with a new source port, and over a few runs both counters climb. With iperf3 in the hosts' toolboxes, `iperf3 -c 10.0.3.3 -P 16` spreads sixteen flows at once.
- Restart `s1`: `ecmp.p4` and its entries are what the history replays, and the group is back.

### 10. Multicast over the triangle

- Load `network_conf_mcast.json`: the three switches load `mcast.json`, `ecmp.p4` with a multicast action, and get their entries again, `s1` the ECMP ones, plus `224.0.0.0/4` to multicast group 1 on each. The group's ports form a tree over the triangle: every port on `s1`, the host and the `s1` side on `s2` and `s3`, the `s2-s3` link left out, since a copy that came back round would circle for ever, nothing takes from a multicast packet's TTL here.
- **P4 multicast groups** on each switch shows group 1 with its ports; **P4 table entries** the `224.0.0.0/4` entry with `set_multicast_group`.
- Open the counters of `h2 eth1` and `h3 eth1`, then from `h1` run `ping -c 5 224.1.1.1`. The ping reports every echo lost, Linux does not answer echo requests sent to a multicast address, but both counters climb by five: `s1` copied each echo to `s2` and `s3`, and each of those to its host. The capture on `h3-s3` shows the echo requests to `224.1.1.1` with the multicast MAC `01:00:5e:01:01:01`.
- The same from `h3`: `h1` and `h2` get the five, `h3` gets nothing back, its own copy dropped at `s3`'s egress.
- Restart `s1`: the group and the entry are back with the program.

### 11. The switch resolves its hosts

- Load `network_conf_arp.json`: the three switches load `arp.json`, `mcast.p4` with ARP, and get their entries again, now with the port alone as the parameter of `ipv4_forward`; each gets its own MAC as the parameter of the default action of `switch_mac`, a table with no key, the gateway address it answers for in `arp_responder` and its host port in `port_gateway`. The hosts drop the static neighbor of their gateway with `remove_static_neighbor`.
- From `h1`, `ping -c 5 10.0.3.3`. The first echo is lost: `s3` had not learned `h3`'s MAC, so it turned that echo into an ARP request for `10.0.3.3` from `10.0.3.30`, `h3` answered, and the rest went through. **ARP and NDP** on `h1` shows `10.0.1.10` as `REACHABLE` now, resolved by `s1`, where it read `PERMANENT` before.
- **P4 registers** on `s3`: `learned_mac` holds `h3`'s MAC at index 1 and `s1`'s at index 2, in hex. **P4 counters**: the ARP requests `arp_responder` answered, and the packets each entry of `ipv4_lpm` forwarded.
- The capture on `h3-s3` during a first ping shows the exchange: no echo request at first, an ARP request from `10.0.3.30`, `h3`'s reply, then the echoes.
- Restart `s1`: program, entries and group come back, its registers start empty, and the first packet toward `h1` asks for it again.

## Bring Your Own Program

The switch runs whatever `p4c` compiles for BMv2's `simple_switch` (the v1model architecture). Compile with the official image, upload the JSON to the namespace (and the source, to read it next to the compiled form), then `load_program` with its name and the entries its tables take:

```bash
podman run --rm -v "$PWD:/w" docker.io/p4lang/p4c:1.2.5.19 \
  p4c-bm2-ss --p4v 16 --p4runtime-files /w/mine.p4info.txtpb -o /w/mine.json /w/mine.p4
```

`docker` takes the same line. The programs here were compiled with that image. The `.p4info.txtpb` is for a P4Runtime controller, the switch does not need it. The table and action names in `table_add` are the ones `p4c` gives, with their control block (`MyIngress.ipv4_lpm`); **P4 tables** lists them once the program is loaded, and a program's counters and registers show under **P4 counters** and **P4 registers**.

## Experiments

- **Drop a host.** `table_delete` on `s1` with `10.0.2.2/32` through **Load network conf**: `h1` loses `h2` while `h3` goes on, and the State tab shows the entry gone. Add it back with the same `table_add` as in `network_conf_entries.json`.
- **A wrong MAC.** Add `h2`'s entry on `s2` with another MAC: the switch forwards, `h2`'s kernel drops the frame as not its own, and the ping fails though every counter climbs. The capture on `h2-s2` shows why.
- **Loop it.** Point `s1`'s entry for `h3` at port 2 and `s2`'s at port 2 too: each switch hands the packet back to the other until the TTL reaches zero, some thirty round trips, visible in the counters of `s1-s2`. Nothing else protects a P4 network from itself.
- **Three ways.** Make `h3` a group of three on `s1` (`set_ecmp_group 1 3` and a third `ecmp_nhop` entry, `1 2`, out of port 1 toward `h1` itself): the flows that hash to it go back to `h1` and die, a third of them. The counters of `s1 eth1` show the share.
- **Close the triangle.** Give `s2`'s group its three ports (`add_multicast_group` with `1 2 3`): one echo from `h1` now comes back round, `s2` to `s3` to `s1` to `s2`, for ever, since no switch takes from its TTL, and every lap leaves a copy on each host. The counters of the three links run away at the switches' speed until `remove_multicast_group` on `s2`, or the group with `1 2` again. The tree of step 10 is what a flooding network needs and what spanning tree computes for a real switch.
- **Who answers for the gateway.** `table_delete` on `s1`'s `arp_responder` entry (`10.0.1.10`): nothing answers `h1`'s next ARP request, so once its neighbor entry ages out the pings from `h1` stop while `h2` and `h3` go on, and **P4 counters** shows the entry gone with its count. Add it back and `h1` resolves again.

## Troubleshooting

- A ping fails right after the entries: a `table_add` was refused, the configure result names the entry and the CLI's reason (an action or table that is not in the program, a key in the wrong form). **P4 table entries** shows what the switch holds.
- A ping fails and the entries are right: the host's MAC is not the one the entry names (the **Links** tab of the host), or the gateway's neighbor is missing (**ARP and NDP** on the host, it must read `PERMANENT`); both are in `network_conf_hosts.json`.
- Everything stops after `network_conf_ecmp.json`: the program swap emptied `s1`'s tables and an entry of the new set was refused; load the file again once fixed, or `network_conf_switches.json` and `network_conf_entries.json` to go back to `basic.p4` on `s1`.
- The flows do not spread: one flow is one path by design; several runs of the throughput test, or `iperf3 -P`, are needed to see both uplinks move.
- The first echo of a ping is lost after `network_conf_arp.json`: by design, that echo became the switch's ARP request for a host it had not learned, and the rest pass. Every echo lost: the host still holds the static neighbor (**ARP and NDP**, `PERMANENT`), or its switch lacks the `arp_responder` entry (**P4 table entries**).
- `ping 224.1.1.1` reports every packet lost: expected, the hosts do not answer echo requests to a multicast address; the counters of their `eth1` and the capture are the evidence. If they do not climb either, **P4 multicast groups** on the switches shows whether the groups are there with the ports of step 10.
- A switch pod never becomes ready: the image could not be pulled (`ghcr.io/emigcm98/kubendt-p4`, public), or its CLI port is not answering; the pod's logs show `simple_switch` starting.

> Last reviewed: 13 Nov 2026
