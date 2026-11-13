// SPDX-FileCopyrightText: 2018 Nate Foster
// SPDX-License-Identifier: Apache-2.0
/* -*- P4_16 -*- */
/* mcast.p4 with the switch resolving its hosts: it answers the ARP requests
 * for the gateway addresses it owns, learns the MAC seen on every port in a
 * register, forwards with the learned MAC instead of a parameter, and when a
 * packet is bound for a host port whose MAC it has not learned yet, it turns
 * that packet into the ARP request for the host, so the reply teaches it and
 * the next packet goes through. Adapted for KubeNDT's example 11. */
#include <core.p4>
#include <v1model.p4>

const bit<16> TYPE_IPV4 = 0x800;
const bit<16> TYPE_ARP  = 0x806;
const bit<16> ARP_REQUEST = 1;
const bit<16> ARP_REPLY   = 2;
const bit<8>  PROTO_TCP = 6;
const bit<8>  PROTO_UDP = 17;

/*************************************************************************
*********************** H E A D E R S  ***********************************
*************************************************************************/

typedef bit<9>  egressSpec_t;
typedef bit<48> macAddr_t;
typedef bit<32> ip4Addr_t;

header ethernet_t {
    macAddr_t dstAddr;
    macAddr_t srcAddr;
    bit<16>   etherType;
}

header ipv4_t {
    bit<4>    version;
    bit<4>    ihl;
    bit<8>    diffserv;
    bit<16>   totalLen;
    bit<16>   identification;
    bit<3>    flags;
    bit<13>   fragOffset;
    bit<8>    ttl;
    bit<8>    protocol;
    bit<16>   hdrChecksum;
    ip4Addr_t srcAddr;
    ip4Addr_t dstAddr;
}

header arp_t {
    bit<16>   htype;
    bit<16>   ptype;
    bit<8>    hlen;
    bit<8>    plen;
    bit<16>   oper;
    macAddr_t sha;
    ip4Addr_t spa;
    macAddr_t tha;
    ip4Addr_t tpa;
}

header tcp_t {
    bit<16> srcPort;
    bit<16> dstPort;
    bit<32> seqNo;
    bit<32> ackNo;
    bit<4>  dataOffset;
    bit<4>  res;
    bit<8>  flags;
    bit<16> window;
    bit<16> checksum;
    bit<16> urgentPtr;
}

header udp_t {
    bit<16> srcPort;
    bit<16> dstPort;
    bit<16> length_;
    bit<16> checksum;
}

struct metadata {
    bit<16>   ecmp_group;
    bit<16>   ecmp_select;
    bit<16>   srcPort;
    bit<16>   dstPort;
    macAddr_t next_mac;
    macAddr_t switch_mac;
    ip4Addr_t asked;
    bit<1>    ask;
    ip4Addr_t gw_ip;
    macAddr_t gw_mac;
}

struct headers {
    ethernet_t   ethernet;
    arp_t        arp;
    ipv4_t       ipv4;
    tcp_t        tcp;
    udp_t        udp;
}

/*************************************************************************
*********************** P A R S E R  ***********************************
*************************************************************************/

parser MyParser(packet_in packet,
                out headers hdr,
                inout metadata meta,
                inout standard_metadata_t standard_metadata) {

    state start {
        transition parse_ethernet;
    }

    state parse_ethernet {
        packet.extract(hdr.ethernet);
        transition select(hdr.ethernet.etherType) {
            TYPE_IPV4: parse_ipv4;
            TYPE_ARP: parse_arp;
            default: accept;
        }
    }
    state parse_arp {
        packet.extract(hdr.arp);
        transition accept;
    }

    state parse_ipv4 {
        packet.extract(hdr.ipv4);
        transition select(hdr.ipv4.protocol) {
            PROTO_TCP: parse_tcp;
            PROTO_UDP: parse_udp;
            default: accept;
        }
    }

    state parse_tcp {
        packet.extract(hdr.tcp);
        meta.srcPort = hdr.tcp.srcPort;
        meta.dstPort = hdr.tcp.dstPort;
        transition accept;
    }

    state parse_udp {
        packet.extract(hdr.udp);
        meta.srcPort = hdr.udp.srcPort;
        meta.dstPort = hdr.udp.dstPort;
        transition accept;
    }
}

/*************************************************************************
************   C H E C K S U M    V E R I F I C A T I O N   *************
*************************************************************************/

control MyVerifyChecksum(inout headers hdr, inout metadata meta) {
    apply {  }
}


/*************************************************************************
**************  I N G R E S S   P R O C E S S I N G   *******************
*************************************************************************/

control MyIngress(inout headers hdr,
                  inout metadata meta,
                  inout standard_metadata_t standard_metadata) {
    direct_counter(CounterType.packets_and_bytes) lpm_counter;
    direct_counter(CounterType.packets_and_bytes) arp_counter;
    /* The source MAC last seen on each port in an ARP or IPv4 frame: a
     * host's address once it has said anything. Other frames are left out,
     * the pod's own kernel sends IPv6 neighbour discovery out of the ports
     * and the switch sees it as if it came in. */
    register<macAddr_t>(512) learned_mac;

    action drop() {
        mark_to_drop(standard_metadata);
    }

    /* The switch's own MAC, the source of everything it forwards: the
     * default action of a table with no key, set with its parameter. */
    action set_switch_mac(macAddr_t mac) {
        meta.switch_mac = mac;
    }

    table switch_mac {
        actions = {
            set_switch_mac;
        }
        default_action = set_switch_mac(0);
    }

    /* The next hop's MAC is the one learned on the port, no parameter. */
    action ipv4_forward(egressSpec_t port) {
        standard_metadata.egress_spec = port;
        learned_mac.read(meta.next_mac, (bit<32>)port);
        hdr.ethernet.srcAddr = meta.switch_mac;
        hdr.ethernet.dstAddr = meta.next_mac;
        hdr.ipv4.ttl = hdr.ipv4.ttl - 1;
    }

    /* The request for a gateway address the switch owns is turned into the
     * reply and sent back out of the port it came in on. */
    action arp_reply(macAddr_t mac) {
        meta.asked = hdr.arp.tpa;
        hdr.arp.oper = ARP_REPLY;
        hdr.arp.tha = hdr.arp.sha;
        hdr.arp.tpa = hdr.arp.spa;
        hdr.arp.sha = mac;
        hdr.arp.spa = meta.asked;
        hdr.ethernet.dstAddr = hdr.ethernet.srcAddr;
        hdr.ethernet.srcAddr = mac;
        standard_metadata.egress_spec = standard_metadata.ingress_port;
    }

    table arp_responder {
        key = {
            hdr.arp.tpa: exact;
        }
        actions = {
            arp_reply;
            drop;
        }
        size = 64;
        default_action = drop();
        counters = arp_counter;
    }

    /* A host port and the gateway the switch speaks as on it, to ask for a
     * host it has not learned yet. */
    action arp_ask(ip4Addr_t gw_ip, macAddr_t gw_mac) {
        meta.ask = 1;
        meta.gw_ip = gw_ip;
        meta.gw_mac = gw_mac;
    }

    table port_gateway {
        key = {
            standard_metadata.egress_spec: exact;
        }
        actions = {
            arp_ask;
            NoAction;
        }
        size = 64;
        default_action = NoAction();
    }

    /* The group has `count` next hops, numbered from 0 in ecmp_nhop; the
     * hash keeps every packet of a flow on the same one. */
    action set_ecmp_group(bit<16> group, bit<16> count) {
        meta.ecmp_group = group;
        hash(meta.ecmp_select,
             HashAlgorithm.crc16,
             (bit<16>)0,
             { hdr.ipv4.srcAddr,
               hdr.ipv4.dstAddr,
               hdr.ipv4.protocol,
               meta.srcPort,
               meta.dstPort },
             count);
    }

    /* The replication engine copies the packet to every port of the group;
     * the copies keep the multicast MAC and the TTL and carry the switch as
     * their source, so the next switch learns it, not the host. */
    action set_multicast_group(bit<16> group) {
        standard_metadata.mcast_grp = group;
        hdr.ethernet.srcAddr = meta.switch_mac;
    }

    table ipv4_lpm {
        key = {
            hdr.ipv4.dstAddr: lpm;
        }
        actions = {
            ipv4_forward;
            set_ecmp_group;
            set_multicast_group;
            drop;
            NoAction;
        }
        size = 1024;
        default_action = drop();
        counters = lpm_counter;
    }

    table ecmp_nhop {
        key = {
            meta.ecmp_group:  exact;
            meta.ecmp_select: exact;
        }
        actions = {
            ipv4_forward;
            drop;
        }
        size = 64;
        default_action = drop();
    }

    apply {
        switch_mac.apply();
        if (hdr.arp.isValid() || hdr.ipv4.isValid()) {
            learned_mac.write((bit<32>)standard_metadata.ingress_port, hdr.ethernet.srcAddr);
        }
        meta.ask = 0;
        if (hdr.arp.isValid()) {
            if (hdr.arp.oper == ARP_REQUEST) {
                arp_responder.apply();
            } else {
                drop();
            }
        } else if (hdr.ipv4.isValid()) {
            switch (ipv4_lpm.apply().action_run) {
                set_ecmp_group: { ecmp_nhop.apply(); }
            }
            /* Bound for a port with nothing learned: on a host port the
             * packet becomes the ARP request for its destination, from the
             * gateway of that port; a switch port does not mind the MAC. */
            if (hdr.ethernet.dstAddr == 0) {
                port_gateway.apply();
                if (meta.ask == 1) {
                    meta.asked = hdr.ipv4.dstAddr;
                    hdr.tcp.setInvalid();
                    hdr.udp.setInvalid();
                    hdr.ipv4.setInvalid();
                    hdr.arp.setValid();
                    hdr.arp.htype = 1;
                    hdr.arp.ptype = TYPE_IPV4;
                    hdr.arp.hlen = 6;
                    hdr.arp.plen = 4;
                    hdr.arp.oper = ARP_REQUEST;
                    hdr.arp.sha = meta.gw_mac;
                    hdr.arp.spa = meta.gw_ip;
                    hdr.arp.tha = 0;
                    hdr.arp.tpa = meta.asked;
                    hdr.ethernet.srcAddr = meta.gw_mac;
                    hdr.ethernet.dstAddr = 0xffffffffffff;
                    hdr.ethernet.etherType = TYPE_ARP;
                    truncate((bit<32>)42);
                }
            }
        }
    }
}

/*************************************************************************
****************  E G R E S S   P R O C E S S I N G   *******************
*************************************************************************/

control MyEgress(inout headers hdr,
                 inout metadata meta,
                 inout standard_metadata_t standard_metadata) {
    /* A replicated copy (egress_rid set by the engine) bound for the port it
     * came in on is dropped, so a group may hold every port of the switch. */
    apply {
        if (standard_metadata.egress_rid != 0 &&
            standard_metadata.egress_port == standard_metadata.ingress_port) {
            mark_to_drop(standard_metadata);
        }
    }
}

/*************************************************************************
*************   C H E C K S U M    C O M P U T A T I O N   **************
*************************************************************************/

control MyComputeChecksum(inout headers  hdr, inout metadata meta) {
     apply {
        update_checksum(
        hdr.ipv4.isValid(),
            { hdr.ipv4.version,
              hdr.ipv4.ihl,
              hdr.ipv4.diffserv,
              hdr.ipv4.totalLen,
              hdr.ipv4.identification,
              hdr.ipv4.flags,
              hdr.ipv4.fragOffset,
              hdr.ipv4.ttl,
              hdr.ipv4.protocol,
              hdr.ipv4.srcAddr,
              hdr.ipv4.dstAddr },
            hdr.ipv4.hdrChecksum,
            HashAlgorithm.csum16);
    }
}

/*************************************************************************
***********************  D E P A R S E R  *******************************
*************************************************************************/

control MyDeparser(packet_out packet, in headers hdr) {
    apply {
        packet.emit(hdr.ethernet);
        packet.emit(hdr.arp);
        packet.emit(hdr.ipv4);
        packet.emit(hdr.tcp);
        packet.emit(hdr.udp);
    }
}

/*************************************************************************
***********************  S W I T C H  *******************************
*************************************************************************/

V1Switch(
MyParser(),
MyVerifyChecksum(),
MyIngress(),
MyEgress(),
MyComputeChecksum(),
MyDeparser()
) main;
