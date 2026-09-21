#!/usr/bin/env python3
"""Does the traffic-control capability produce what was configured?

Two pods with one link (same worker unless --cross). After a baseline without
qdisc, the script applies through the configure API a netem delay, a netem loss and
a tbf rate for each value given, measures the effect from the other end (ping RTT
over the baseline, ping loss over --loss-count packets, iperf3 TCP throughput) and
reads the qdisc back through GET /pods/tc. Each row says whether the measurement
lies within the declared tolerance: delay ±(tolerance% + 1 ms), loss within three
binomial standard deviations, rate ±tolerance%."""
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
    return {k: tp.get(k) for k in ("qdisc", "delay", "loss", "rate") if tp.get(k)}


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
        rb = readback(client, ns, a, "eth1")
        rec.note(f"run {run}: qdisc after del_qdisc: {rb or 'none'}")

    if not args.keep:
        client.drop_namespace(ns)
    rows = [r for r in rec.rows if r.get("kind") in ("delay", "loss", "rate")]
    summary = {}
    for kind in ("delay", "loss", "rate"):
        for cfg in sorted({r["configured"] for r in rows if r["kind"] == kind}):
            sel = [r for r in rows if r["kind"] == kind and r["configured"] == cfg]
            key = {"delay": "measured_added_ms", "loss": "measured_loss_pct", "rate": "measured_mbps"}[kind]
            summary[f"{kind} {cfg}"] = {"measured": C.summarize([r.get(key) for r in sel]), "error_pct": C.summarize([r.get("error_pct") for r in sel]),
                                        "within_tolerance": sum(1 for r in sel if r.get("within")), "n": len(sel)}
    rec.finish(summary)
    C.table([{"case": k, "measured": C.mean_std([r.get({"delay": "measured_added_ms", "loss": "measured_loss_pct", "rate": "measured_mbps"}[k.split()[0]])
                                                  for r in rows if f"{r['kind']} {r['configured']}" == k], 2),
              "error (%)": C.mean_std([r.get("error_pct") for r in rows if f"{r['kind']} {r['configured']}" == k], 1),
              "within tolerance": f"{v['within_tolerance']}/{v['n']}"} for k, v in summary.items()],
            ["case", "measured", "error (%)", "within tolerance"], f"traffic control fidelity (delay in added ms, loss in %, rate in Mbit/s), {args.runs} runs")


if __name__ == "__main__":
    main()
