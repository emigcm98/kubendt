#!/usr/bin/env python3
"""Does the traffic-control capability produce what was configured?

Two pods with one link (same worker unless --cross). After a baseline without
qdisc, the script applies through the configure API a netem delay, a netem loss and
a tbf rate for each value given, measures the effect from the other end (ping RTT
over the baseline, ping loss over --loss-count packets, iperf3 TCP throughput) and
reads the qdisc back through GET /pods/tc. Each row says whether the measurement
lies within the declared tolerance: delay ±(tolerance% + 1 ms), loss within three
binomial standard deviations, rate ±tolerance%.

--composite applies the first delay, loss and rate together in one netem and checks
the three at once; the rate is measured with UDP offered above the cap, since TCP
under random loss is bound by the loss, not by the rate. --models checks the jitter
distributions (ping mdev, the RTT standard deviation, against what a uniform or a
normal spread of --jitter gives) and a Gilbert-Elliott loss (average loss
p / (p + r), a bursty loss so the tolerance is wider)."""
import json
import math
import re
import time

import common as C
import gen_topology as G
import placement_fidelity as P


def apply(client, ns, pod, action):
    st, r, wall = client.configure(ns, {"targets": [{"pod": pod, "actions": [action]}]})
    ok = st == 200 and isinstance(r, dict) and r.get("failures") == 0
    return ok, wall, r


def readback(client, ns, pod, iface):
    q = client.qdisc(ns, pod, iface) or {}
    tp = q.get("tcparams") or {}
    rb = {k: tp.get(k) for k in ("qdisc", "delay", "loss", "rate") if tp.get(k)}
    if tp.get("classes"):
        rb["classes"] = len(tp["classes"])
    return rb


def udp_through(ns, a, b, ip_b, seconds, offered_mbit):
    """UDP rate as the server receives it, with the loss it saw. The client's own
    figure divides what arrived by its sending time, which overstates a rate-limited
    link: netem's queue keeps draining after the client stopped and the server
    counts that too. A one-off JSON server on its own port gives the real number."""
    C.kexec(ns, b, "sh -c 'rm -f /tmp/udp.json; (iperf3 -s -1 -p 5202 -J > /tmp/udp.json 2>&1 &)'")
    time.sleep(0.5)
    C.kexec(ns, a, f"iperf3 -c {ip_b} -u -b {offered_mbit}M -t {seconds} -p 5202 -J", timeout=seconds + 60)
    out = ""
    for _ in range(30):
        time.sleep(0.5)
        rc, out = C.kexec(ns, b, "cat /tmp/udp.json")
        if '"end"' in out:
            break
    try:
        j = json.loads(out[out.index("{"):])
    except (ValueError, json.JSONDecodeError):
        return None, None
    end = j.get("end", {})
    # The server fills sum_received for UDP (sum carries only the loss); the
    # rate is bytes over the server's own duration, which includes the drain.
    recv, sm = end.get("sum_received", {}), end.get("sum", {})
    mbps = recv.get("bits_per_second") or 0
    if not mbps and recv.get("seconds"):
        mbps = recv.get("bytes", 0) * 8 / recv["seconds"]
    return round(mbps / 1e6, 2), round(sm.get("lost_percent", 0), 3)


def to_ms(s):
    m = re.match(r"([0-9.]+)\s*(ms|us|s)?", str(s))
    if not m:
        return None
    v, u = float(m.group(1)), m.group(2) or "ms"
    return v * {"ms": 1, "us": 0.001, "s": 1000}[u]


def to_mbit(s):
    m = re.match(r"([0-9.]+)\s*([kmgKMG]?)bit", str(s))
    if not m:
        return None
    return float(m.group(1)) * {"": 1e-6, "k": 1e-3, "K": 1e-3, "m": 1, "M": 1, "g": 1e3, "G": 1e3}[m.group(2)]


def main():
    ap = C.base_parser(__doc__)
    ap.add_argument("--delays", nargs="*", default=["10ms", "50ms", "100ms"])
    ap.add_argument("--losses", nargs="*", default=["1%", "5%", "10%"])
    ap.add_argument("--rates", nargs="*", default=["1mbit", "10mbit", "100mbit"])
    ap.add_argument("--tolerance", type=float, default=10.0, help="percent, for delay and rate")
    ap.add_argument("--ping-count", type=int, default=100)
    ap.add_argument("--loss-count", type=int, default=2000)
    ap.add_argument("--iperf-seconds", type=int, default=10)
    ap.add_argument("--cross", action="store_true", help="pods on different workers (VXLAN) instead of the same one")
    ap.add_argument("--composite", action="store_true", help="also apply delay, loss and rate together in one netem")
    ap.add_argument("--models", action="store_true", help="also check the jitter distributions and a Gilbert-Elliott loss")
    ap.add_argument("--jitter", default="20ms", help="jitter for the distribution cases")
    ap.add_argument("--offload", action="store_true", help="also compare TCP under netem with GSO/TSO/GRO on and off at both ends")
    ap.add_argument("--qos", action="store_true", help="also measure ping RTT under bulk TCP on a 10 mbit link, plain and with an htb priority class")
    ap.add_argument("--image", default=C.TOOLS_IMAGE)
    ap.add_argument("--namespace", default="exp-tc")
    args = ap.parse_args()
    if args.runs == 10:
        args.runs = 3
    client = C.client_from(args)
    rec = C.Recorder("tc_fidelity", client, args.out, args)
    ns, a, b, ip_b = args.namespace, "a-0", "b-0", "10.0.0.2"
    ws = C.workers()
    topo = G.pair(args.image, node_a=ws[0], node_b=(ws[1] if args.cross and len(ws) > 1 else ws[0]))
    client.fresh_namespace(ns)
    st, r, wall = client.deploy(ns, topo)
    C.run_or_die(st, r, "deploy")
    C.kexec(ns, b, "iperf3 -s -D")
    time.sleep(1)
    base = P.ping(ns, a, ip_b, args.ping_count, 0.02)
    base_tcp = P.iperf_tcp(ns, a, ip_b, args.iperf_seconds)
    rec.note(f"baseline: rtt avg {base.get('rtt_avg_ms')} ms, loss {base.get('loss_pct')}%, tcp {base_tcp.get('tcp_recv_mbps')} Mbit/s, "
             f"realization {next((l.get('realization') for l in client.links(ns)), '?')}")
    rec.add(kind="baseline", configured=None, measured_rtt_ms=base.get("rtt_avg_ms"), measured_loss_pct=base.get("loss_pct"),
            measured_mbps=base_tcp.get("tcp_recv_mbps"), readback=json.dumps(readback(client, ns, a, "eth1")))

    for run in range(1, args.runs + 1):
        for d in args.delays:
            ok, wall, r = apply(client, ns, a, {"type": "add_qdisc", "iface": "eth1", "tcparams": {"qdisc": "netem", "delay": d}})
            rb = readback(client, ns, a, "eth1")
            p = P.ping(ns, a, ip_b, args.ping_count, 0.02)
            added = (p.get("rtt_avg_ms") or 0) - (base.get("rtt_avg_ms") or 0)
            want = to_ms(d)
            tol = want * args.tolerance / 100 + 1.0
            row = rec.add(run=run, kind="delay", configured=d, apply_ok=ok, apply_s=round(wall, 3), readback=json.dumps(rb),
                          measured_rtt_ms=p.get("rtt_avg_ms"), measured_added_ms=round(added, 3), expected_ms=want,
                          error_pct=round((added - want) / want * 100, 2) if want else None, within=abs(added - want) <= tol, tolerance_ms=round(tol, 2))
            C.log(f"run {run} delay {d}: added {row['measured_added_ms']} ms (error {row['error_pct']}%), readback {rb}, within tolerance {row['within']}")
            apply(client, ns, a, {"type": "del_qdisc", "iface": "eth1"})
        for l in args.losses:
            ok, wall, r = apply(client, ns, a, {"type": "add_qdisc", "iface": "eth1", "tcparams": {"qdisc": "netem", "loss": l}})
            rb = readback(client, ns, a, "eth1")
            p = P.ping(ns, a, ip_b, args.loss_count, 0.005)
            want = float(l.rstrip("%"))
            n = p.get("sent") or args.loss_count
            sigma = math.sqrt(want / 100 * (1 - want / 100) / n) * 100
            got = p.get("loss_pct")
            row = rec.add(run=run, kind="loss", configured=l, apply_ok=ok, apply_s=round(wall, 3), readback=json.dumps(rb), packets=n,
                          measured_loss_pct=got, expected_pct=want, error_pct=round((got - want) / want * 100, 2) if got is not None else None,
                          within=(got is not None and abs(got - want) <= 3 * sigma), tolerance_pct=round(3 * sigma, 3))
            C.log(f"run {run} loss {l}: measured {got}% over {n} packets (3σ = {row['tolerance_pct']}%), readback {rb}, within {row['within']}")
            apply(client, ns, a, {"type": "del_qdisc", "iface": "eth1"})
        for rate in args.rates:
            mbit = to_mbit(rate)
            burst = f"{max(32, int(mbit * 1000 / 50))}kbit"   # 20 ms worth of tokens, 32 kbit floor as in the UI
            ok, wall, r = apply(client, ns, a, {"type": "add_qdisc", "iface": "eth1", "tcparams": {"qdisc": "tbf", "rate": rate, "burst": burst, "latency": "50ms"}})
            rb = readback(client, ns, a, "eth1")
            t = P.iperf_tcp(ns, a, ip_b, args.iperf_seconds)
            got = t.get("tcp_recv_mbps")
            row = rec.add(run=run, kind="rate", configured=rate, burst=burst, apply_ok=ok, apply_s=round(wall, 3), readback=json.dumps(rb),
                          measured_mbps=got, expected_mbps=mbit, error_pct=round((got - mbit) / mbit * 100, 2) if got is not None else None,
                          within=(got is not None and abs(got - mbit) <= mbit * args.tolerance / 100), retransmits=t.get("tcp_retransmits"))
            C.log(f"run {run} rate {rate} (burst {burst}): measured {got} Mbit/s (error {row['error_pct']}%), readback {rb}, within {row['within']}")
            apply(client, ns, a, {"type": "del_qdisc", "iface": "eth1"})
        if args.composite:
            # The first of each list, or the middle defaults when a list was emptied.
            d, l, rate = (args.delays or ["50ms"])[0], (args.losses or ["2%"])[0], (args.rates or ["10mbit"])[0]
            mbit, want_d, want_l = to_mbit(rate), to_ms(d), float(l.rstrip("%"))
            ok, wall, r = apply(client, ns, a, {"type": "add_qdisc", "iface": "eth1", "tcparams": {"qdisc": "netem", "delay": d, "loss": l, "rate": rate}})
            rb = readback(client, ns, a, "eth1")
            p = P.ping(ns, a, ip_b, args.loss_count, 0.005)
            # UDP offered at 1.5x the cap: what gets through is the cap minus the loss.
            got_m, udp_loss = udp_through(ns, a, b, ip_b, args.iperf_seconds, mbit * 1.5)
            added = (p.get("rtt_avg_ms") or 0) - (base.get("rtt_avg_ms") or 0)
            n = p.get("sent") or args.loss_count
            sigma = math.sqrt(want_l / 100 * (1 - want_l / 100) / n) * 100
            got_l = p.get("loss_pct")
            want_m = mbit * (1 - want_l / 100)
            within = (abs(added - want_d) <= want_d * args.tolerance / 100 + 1.0 and got_l is not None and abs(got_l - want_l) <= 3 * sigma
                      and got_m is not None and abs(got_m - want_m) <= want_m * args.tolerance / 100)
            row = rec.add(run=run, kind="composite", configured=f"{d} {l} {rate}", apply_ok=ok, apply_s=round(wall, 3), readback=json.dumps(rb),
                          measured_added_ms=round(added, 3), expected_ms=want_d, measured_loss_pct=got_l, expected_pct=want_l, packets=n,
                          measured_mbps=got_m, expected_mbps=round(want_m, 2), udp_offered_mbps=round(mbit * 1.5, 2), udp_loss_pct=udp_loss,
                          error_pct=round((got_m - want_m) / want_m * 100, 2) if got_m is not None else None, within=within)
            C.log(f"run {run} composite {d} {l} {rate}: added {row['measured_added_ms']} ms, loss {got_l}%, {got_m} Mbit/s, readback {rb}, within {within}")
            apply(client, ns, a, {"type": "del_qdisc", "iface": "eth1"})
        if args.models:
            # ping's mdev is the standard deviation of the RTT. netem's normal table
            # has sd = jitter, its uniform spread is +-jitter, sd = jitter / sqrt(3).
            j = to_ms(args.jitter)
            for dist, factor in (("uniform", 1 / math.sqrt(3)), ("normal", 1.0)):
                ok, wall, r = apply(client, ns, a, {"type": "add_qdisc", "iface": "eth1", "tcparams": {"qdisc": "netem", "delay": "50ms", "jitter": args.jitter, "distribution": dist}})
                rb = readback(client, ns, a, "eth1")
                p = P.ping(ns, a, ip_b, args.ping_count, 0.02)
                got, want = p.get("rtt_mdev_ms"), factor * j
                tol = want * args.tolerance / 100 + 1.0
                row = rec.add(run=run, kind="jitter", configured=f"{dist} {args.jitter}", apply_ok=ok, apply_s=round(wall, 3), readback=json.dumps(rb),
                              measured_mdev_ms=got, expected_mdev_ms=round(want, 3), error_pct=round((got - want) / want * 100, 2) if got is not None else None,
                              within=(got is not None and abs(got - want) <= tol), tolerance_ms=round(tol, 2))
                C.log(f"run {run} jitter {dist} {args.jitter}: mdev {got} ms, expected {row['expected_mdev_ms']} ms, within {row['within']}")
                apply(client, ns, a, {"type": "del_qdisc", "iface": "eth1"})
            gp, gr = 1.0, 10.0   # p/(p+r): 9.09% average loss in bursts
            ok, wall, r = apply(client, ns, a, {"type": "add_qdisc", "iface": "eth1", "tcparams": {"qdisc": "netem", "gemodel": {"p": f"{gp}%", "r": f"{gr}%"}}})
            rb = readback(client, ns, a, "eth1")
            p = P.ping(ns, a, ip_b, args.loss_count, 0.005)
            want = gp / (gp + gr) * 100
            n = p.get("sent") or args.loss_count
            got = p.get("loss_pct")
            # Bursty loss: the sample holds about n * p bursts, so the relative spread
            # of the measured loss is about 1 / sqrt(n * p), far above the binomial one.
            tol = max(3 * math.sqrt(want / 100 * (1 - want / 100) / n) * 100, want * 3 / math.sqrt(n * gp / 100))
            row = rec.add(run=run, kind="gemodel", configured=f"p {gp}% r {gr}%", apply_ok=ok, apply_s=round(wall, 3), readback=json.dumps(rb), packets=n,
                          measured_loss_pct=got, expected_pct=round(want, 3), error_pct=round((got - want) / want * 100, 2) if got is not None else None,
                          within=(got is not None and abs(got - want) <= tol), tolerance_pct=round(tol, 3))
            C.log(f"run {run} gemodel p {gp}% r {gr}%: measured {got}% over {n} packets, expected {row['expected_pct']}%, within {row['within']}")
            apply(client, ns, a, {"type": "del_qdisc", "iface": "eth1"})
        if args.qos:
            # Bulk TCP and pings share a 10 mbit link. With one queue the pings wait
            # behind the bulk; with htb and ICMP in a higher-priority class they do not.
            def rtt_under_load():
                C.kexec(ns, a, f"sh -c 'iperf3 -c {ip_b} -t {args.iperf_seconds} -p 5201 >/dev/null 2>&1 &'")
                time.sleep(2)
                p = P.ping(ns, a, ip_b, 20, 0.2)
                time.sleep(max(0, args.iperf_seconds - 6))
                return p.get("rtt_avg_ms")
            plain = {"qdisc": "netem", "rate": "10mbit"}
            qos = {"qdisc": "htb", "rate": "10mbit", "classes": [
                {"name": "ping", "rate": "1mbit", "ceil": "10mbit", "prio": 0, "match": [{"proto": "icmp"}]},
                {"name": "bulk", "rate": "9mbit", "ceil": "10mbit", "prio": 1, "default": True, "queue": "pfifo", "limit": 100}]}
            got = {}
            for label, spec in (("plain rate 10mbit", plain), ("htb icmp priority", qos)):
                ok, wall, r = apply(client, ns, a, {"type": "add_qdisc", "iface": "eth1", "tcparams": spec})
                rb = readback(client, ns, a, "eth1")
                got[label] = rtt_under_load()
                rec.add(run=run, kind="qos", configured=label, apply_ok=ok, apply_s=round(wall, 3), readback=json.dumps(rb), measured_rtt_ms=got[label],
                        within=True)
                C.log(f"run {run} qos {label}: ping RTT under bulk load {got[label]} ms, readback {rb}")
                apply(client, ns, a, {"type": "del_qdisc", "iface": "eth1"})
            rec.note(f"run {run}: RTT under load, plain {got['plain rate 10mbit']} ms against htb {got['htb icmp priority']} ms")
        if args.offload:
            # With the offloads on, netem handles 64 KB super-packets: a lost one is a
            # burst of segments and a rate-limited one is serialized as a block. The
            # same netem is measured with TCP with the offloads on, then off at both ends.
            def offloads(state):
                for pod in (a, b):
                    rc, out = C.kexec(ns, pod, f"ethtool -K eth1 tso {state} gso {state} gro {state}")
                    if rc != 0:
                        rec.note(f"ethtool on {pod} failed: {out[-120:]}")
            for mode in ("on", "off"):
                offloads(mode)
                for label, spec in (("loss 1% at 20ms", {"qdisc": "netem", "delay": "20ms", "loss": "1%"}),
                                    ("rate 10mbit at 20ms", {"qdisc": "netem", "delay": "20ms", "rate": "10mbit"})):
                    ok, wall, r = apply(client, ns, a, {"type": "add_qdisc", "iface": "eth1", "tcparams": spec})
                    t = P.iperf_tcp(ns, a, ip_b, args.iperf_seconds)
                    # A comparison, not a pass or fail: the row is informational.
                    row = rec.add(run=run, kind="offload", configured=f"{mode} {label}", apply_ok=ok, apply_s=round(wall, 3),
                                  measured_mbps=t.get("tcp_recv_mbps"), retransmits=t.get("tcp_retransmits"), offloads=mode, within=True)
                    C.log(f"run {run} offloads {mode}, {label}: {row['measured_mbps']} Mbit/s, {row['retransmits']} retransmits")
                    apply(client, ns, a, {"type": "del_qdisc", "iface": "eth1"})
            offloads("on")
        rb = readback(client, ns, a, "eth1")
        rec.note(f"run {run}: qdisc after del_qdisc: {rb or 'none'}")

    if not args.keep:
        client.drop_namespace(ns)
    measure_key = {"delay": "measured_added_ms", "loss": "measured_loss_pct", "rate": "measured_mbps",
                   "composite": "measured_mbps", "jitter": "measured_mdev_ms", "gemodel": "measured_loss_pct", "offload": "measured_mbps",
                   "qos": "measured_rtt_ms"}
    rows = [r for r in rec.rows if r.get("kind") in measure_key]
    summary = {}
    for kind in measure_key:
        for cfg in sorted({r["configured"] for r in rows if r["kind"] == kind}):
            sel = [r for r in rows if r["kind"] == kind and r["configured"] == cfg]
            summary[f"{kind} {cfg}"] = {"measured": C.summarize([r.get(measure_key[kind]) for r in sel]), "error_pct": C.summarize([r.get("error_pct") for r in sel]),
                                        "within_tolerance": sum(1 for r in sel if r.get("within")), "n": len(sel)}
    rec.finish(summary)
    C.table([{"case": k, "measured": C.mean_std([r.get(measure_key[k.split()[0]]) for r in rows if f"{r['kind']} {r['configured']}" == k], 2),
              "error (%)": C.mean_std([r.get("error_pct") for r in rows if f"{r['kind']} {r['configured']}" == k], 1),
              "within tolerance": f"{v['within_tolerance']}/{v['n']}"} for k, v in summary.items()],
            ["case", "measured", "error (%)", "within tolerance"],
            f"traffic control fidelity (delay in added ms, loss in %, rate and composite in Mbit/s, jitter in mdev ms), {args.runs} runs")


if __name__ == "__main__":
    main()
