#!/usr/bin/env python3
"""Data plane of a link on the same worker (veth) against one across workers (VXLAN).

Two pods with one link, pinned with nodeName: case "same" puts both on the first
worker, case "cross" one on each. For every run it records the realization the API
reports and what `ip -d link` shows inside the pods, the interface MTU and the
largest DF ping that passes, RTT (ping), packet loss under a fast ping, TCP
throughput and retransmissions (iperf3), UDP loss and jitter at --udp-rate, and the
CPU each pod burned during the TCP run (cgroup counters) plus iperf3's own CPU
figures. With --vyos-image a third case adds a VyOS VM next to the first pod and
reports the MTU and RTT of the QEMU/TAP path."""
import json
import re
import time

import common as C
import gen_topology as G

PING_RE = re.compile(r"= ([0-9.]+)/([0-9.]+)/([0-9.]+)/([0-9.]+) ms")
LOSS_RE = re.compile(r"(\d+) packets transmitted, (\d+) (?:packets )?received.*?([0-9.]+)% packet loss")


def ping(ns, pod, target, count, interval, size=None, df=False, timeout=120):
    opts = f"-c {count} -i {interval} -q" + (f" -s {size}" if size else "") + (" -M do" if df else "")
    rc, out = C.kexec(ns, pod, f"ping {opts} {target}", timeout=timeout)
    m, l = PING_RE.search(out), LOSS_RE.search(out)
    res = {"rc": rc}
    if l:
        res.update(sent=int(l.group(1)), received=int(l.group(2)), loss_pct=float(l.group(3)))
    if m:
        res.update(rtt_min_ms=float(m.group(1)), rtt_avg_ms=float(m.group(2)), rtt_max_ms=float(m.group(3)), rtt_mdev_ms=float(m.group(4)))
    return res


def largest_df_ping(ns, pod, target, sizes=(1472, 1450, 1422, 1400, 1300, 1200, 1000)):
    for s in sizes:
        r = ping(ns, pod, target, 3, 0.2, size=s, df=True)
        if r.get("received"):
            return s + 28  # payload + ICMP/IP headers = IP packet size that passed
    return None


def link_info(ns, pod, iface):
    rc, out = C.kexec(ns, pod, f"ip -d -o link show {iface}")
    mtu = re.search(r"mtu (\d+)", out)
    kind = "vxlan" if " vxlan " in out else ("veth" if " veth " in out else ("tun" if " tun " in out else "?"))
    return int(mtu.group(1)) if mtu else None, kind


def iperf_tcp(ns, client_pod, server_ip, seconds):
    rc, out = C.kexec(ns, client_pod, f"iperf3 -c {server_ip} -t {seconds} -J", timeout=seconds + 60)
    try:
        j = json.loads(out[out.index("{"):])
    except (ValueError, json.JSONDecodeError):
        return {"error": out[-200:]}
    end = j.get("end", {})
    cpu = end.get("cpu_utilization_percent", {})
    return {"tcp_sent_mbps": round(end.get("sum_sent", {}).get("bits_per_second", 0) / 1e6, 1),
            "tcp_recv_mbps": round(end.get("sum_received", {}).get("bits_per_second", 0) / 1e6, 1),
            "tcp_retransmits": end.get("sum_sent", {}).get("retransmits"),
            "iperf_cpu_client_pct": round(cpu.get("host_total", 0), 1), "iperf_cpu_server_pct": round(cpu.get("remote_total", 0), 1)}


def iperf_udp(ns, client_pod, server_ip, seconds, rate):
    rc, out = C.kexec(ns, client_pod, f"iperf3 -c {server_ip} -u -b {rate} -t {seconds} -J", timeout=seconds + 60)
    try:
        j = json.loads(out[out.index("{"):])
    except (ValueError, json.JSONDecodeError):
        return {"error": out[-200:]}
    s = j.get("end", {}).get("sum", {})
    return {"udp_mbps": round(s.get("bits_per_second", 0) / 1e6, 1), "udp_loss_pct": round(s.get("lost_percent", 0), 3),
            "udp_jitter_ms": round(s.get("jitter_ms", 0), 3), "udp_packets": s.get("packets")}


def measure(client, ns, a, b, ip_b, rec, case, run, args):
    mtu_a, kind_a = link_info(ns, a, "eth1")
    mtu_b, kind_b = link_info(ns, b, "eth1")
    api_links = client.links(ns)
    realization = next((l.get("realization") for l in api_links if {l.get("node"), l.get("peerNode")} == {a, b}), None)
    workers = next(((l.get("nodeWorker"), l.get("peerWorker")) for l in api_links if {l.get("node"), l.get("peerNode")} == {a, b}), (None, None))
    row = dict(case=case, run=run, pod_a=a, pod_b=b, worker_a=workers[0], worker_b=workers[1], api_realization=realization,
               link_kind_a=kind_a, link_kind_b=kind_b, mtu_a=mtu_a, mtu_b=mtu_b, largest_df_packet=largest_df_ping(ns, a, ip_b))
    row.update({f"rtt_{k}": v for k, v in ping(ns, a, ip_b, args.ping_count, 0.02).items() if k.startswith("rtt") or k == "loss_pct"})
    fast = ping(ns, a, ip_b, args.loss_count, 0.005, size=1400)
    row.update(loss_sent=fast.get("sent"), loss_received=fast.get("received"), loss_pct=fast.get("loss_pct"))
    cpu_a0, cpu_b0 = C.pod_cpu_usec(ns, a), C.pod_cpu_usec(ns, b)
    t0 = time.time()
    row.update(iperf_tcp(ns, a, ip_b, args.iperf_seconds))
    dur = time.time() - t0
    cpu_a1, cpu_b1 = C.pod_cpu_usec(ns, a), C.pod_cpu_usec(ns, b)
    if None not in (cpu_a0, cpu_a1, cpu_b0, cpu_b1):
        row["pod_cpu_a_pct"] = round((cpu_a1 - cpu_a0) / 1e6 / dur * 100, 1)   # percent of one core over the TCP run
        row["pod_cpu_b_pct"] = round((cpu_b1 - cpu_b0) / 1e6 / dur * 100, 1)
    if args.udp_rate:
        row.update(iperf_udp(ns, a, ip_b, args.iperf_seconds, args.udp_rate))
    top = C.top_nodes()
    row["node_cpu_m"] = json.dumps({w: top[w]["cpu_m"] for w in set(workers) if w in top})
    rec.add(**row)
    C.log(f"[{case} run {run}] {realization} ({kind_a}/{kind_b}) mtu {mtu_a}/{mtu_b}, largest DF packet {row['largest_df_packet']}, "
          f"rtt avg {row.get('rtt_rtt_avg_ms')} ms, loss {row.get('loss_pct')}%, tcp {row.get('tcp_recv_mbps')} Mbit/s "
          f"(retrans {row.get('tcp_retransmits')}), pod cpu {row.get('pod_cpu_a_pct')}/{row.get('pod_cpu_b_pct')}%"
          + (f", udp loss {row.get('udp_loss_pct')}% jitter {row.get('udp_jitter_ms')} ms" if args.udp_rate else ""))
    return row


def main():
    ap = C.base_parser(__doc__)
    ap.add_argument("--workers", nargs=2, default=None, help="two worker names (default: first two workers of the cluster)")
    ap.add_argument("--image", default=C.TOOLS_IMAGE)
    ap.add_argument("--ping-count", type=int, default=200)
    ap.add_argument("--loss-count", type=int, default=2000)
    ap.add_argument("--iperf-seconds", type=int, default=10)
    ap.add_argument("--udp-rate", default="100M", help="iperf3 UDP offered rate, empty to skip")
    ap.add_argument("--vyos-image", default=None, help="also measure the QEMU/TAP path with a VyOS router")
    ap.add_argument("--namespace", default="exp-placement")
    args = ap.parse_args()
    if args.runs == 10:
        args.runs = 5
    client = C.client_from(args)
    rec = C.Recorder("placement_fidelity", client, args.out, args)
    ns = args.namespace
    w1, w2 = args.workers or C.workers()[:2]
    if not w2:
        raise SystemExit("need two workers")
    cases = [("same", w1, w1), ("cross", w1, w2)]

    for case, wa, wb in cases:
        topo = G.pair(args.image, node_a=wa, node_b=wb)
        client.fresh_namespace(ns)
        st, r, wall = client.deploy(ns, topo)
        C.run_or_die(st, r, f"deploy {case}")
        rec.note(f"[{case}] a on {wa}, b on {wb}, deployed in {wall:.1f} s")
        C.kexec(ns, "b-0", "iperf3 -s -D")
        time.sleep(1)
        for run in range(1, args.runs + 1):
            measure(client, ns, "a-0", "b-0", "10.0.0.2", rec, case, run, args)
        client.clear_topology(ns)

    if args.vyos_image:
        topo = {"nodes": [G.host_node("a", args.image, nodeName=w1), {"name": "v", "image": args.vyos_image, "type": "router", "driver": "VyOSRouterDriver", "nodeName": w1}],
                "links": [{"node": "a-0", "localIntf": "eth1", "localIp": "10.0.0.1/24", "peerNode": "v-0", "peerIntf": "eth1", "peerIp": "10.0.0.2/24"}]}
        client.fresh_namespace(ns)
        st, r, wall = client.deploy(ns, topo)
        C.run_or_die(st, r, "deploy vm case")
        rec.note(f"[vm] VyOS deployed in {wall:.1f} s")
        for run in range(1, args.runs + 1):
            mtu_a, kind_a = link_info(ns, "a-0", "eth1")
            rc, out = C.kexec(ns, "v-0", "ip -o link show | grep -E 'tap|eth1' | sed 's/link.*//'")
            tap_mtu = re.search(r"mtu (\d+)", out)
            rc, guest = C.kexec(ns, "v-0", "ssh_qemu ip -o link show eth1", timeout=40)
            guest_mtu = re.search(r"mtu (\d+)", guest)
            p = ping(ns, "a-0", "10.0.0.2", args.ping_count, 0.02)
            row = rec.add(case="vm", run=run, pod_a="a-0", pod_b="v-0", worker_a=w1, worker_b=w1, link_kind_a=kind_a, mtu_a=mtu_a,
                          mtu_tap=int(tap_mtu.group(1)) if tap_mtu else None, mtu_guest=int(guest_mtu.group(1)) if guest_mtu else None,
                          largest_df_packet=largest_df_ping(ns, "a-0", "10.0.0.2"),
                          **{f"rtt_{k}": v for k, v in p.items() if k.startswith("rtt") or k == "loss_pct"})
            C.log(f"[vm run {run}] pod side {kind_a} mtu {mtu_a}, tap mtu {row['mtu_tap']}, guest eth1 mtu {row['mtu_guest']}, "
                  f"largest DF packet {row['largest_df_packet']}, rtt avg {row.get('rtt_rtt_avg_ms')} ms")
        client.clear_topology(ns)

    if not args.keep:
        client.drop_namespace(ns)
    cases_all = [c[0] for c in cases] + (["vm"] if args.vyos_image else [])
    keys = ("mtu_a", "largest_df_packet", "rtt_rtt_avg_ms", "rtt_rtt_min_ms", "rtt_rtt_max_ms", "loss_pct", "tcp_recv_mbps", "tcp_retransmits",
            "udp_loss_pct", "udp_jitter_ms", "pod_cpu_a_pct", "pod_cpu_b_pct", "iperf_cpu_client_pct", "iperf_cpu_server_pct")
    summary = {c: {k: C.summarize([r.get(k) for r in rec.rows if r.get("case") == c]) for k in keys} for c in cases_all}
    rec.finish(summary)
    C.table([{"case": c, "realization": next((r.get("api_realization") or r.get("link_kind_a") for r in rec.rows if r.get("case") == c), ""),
              "mtu": next((r.get("mtu_a") for r in rec.rows if r.get("case") == c), ""),
              "rtt avg (ms)": C.mean_std([r.get("rtt_rtt_avg_ms") for r in rec.rows if r.get("case") == c], 3),
              "loss (%)": C.mean_std([r.get("loss_pct") for r in rec.rows if r.get("case") == c], 2),
              "tcp (Mbit/s)": C.mean_std([r.get("tcp_recv_mbps") for r in rec.rows if r.get("case") == c], 0),
              "retrans": C.mean_std([r.get("tcp_retransmits") for r in rec.rows if r.get("case") == c], 0),
              "udp loss (%)": C.mean_std([r.get("udp_loss_pct") for r in rec.rows if r.get("case") == c], 2),
              "pod cpu a/b (%)": f"{C.mean_std([r.get('pod_cpu_a_pct') for r in rec.rows if r.get('case') == c], 0)} / {C.mean_std([r.get('pod_cpu_b_pct') for r in rec.rows if r.get('case') == c], 0)}"}
             for c in cases_all], ["case", "realization", "mtu", "rtt avg (ms)", "loss (%)", "tcp (Mbit/s)", "retrans", "udp loss (%)", "pod cpu a/b (%)"],
            f"same worker vs cross worker, {args.runs} runs")


if __name__ == "__main__":
    main()
