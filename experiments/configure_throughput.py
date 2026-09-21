#!/usr/bin/env python3
"""Configuration throughput: one action dispatched to every pod at once.

Deploys a synthetic topology per size, then --runs times sends a configure request
with one replace_ip per pod (a distinct subnet every run, so the driver always
performs a real write and the history never skips it as a duplicate). Records the
backend's wall time, the sequential-equivalent time (sum of per-pod durations) and
the resulting speedup, plus the per-pod exec time distribution."""
import json

import common as C
import gen_topology as G


def main():
    ap = C.base_parser(__doc__)
    ap.add_argument("--sizes", type=int, nargs="+", default=[8, 16, 32, 64])
    ap.add_argument("--shape", choices=list(G.SHAPES), default="sparse")
    ap.add_argument("--image", default=C.HOST_IMAGE)
    ap.add_argument("--namespace", default="exp-configure")
    args = ap.parse_args()
    client = C.client_from(args)
    rec = C.Recorder("configure_throughput", client, args.out, args)
    ns = args.namespace

    for n in args.sizes:
        kw = {"seed": 1} if args.shape == "sparse" else {}
        topo = G.SHAPES[args.shape](n, image=args.image, **kw)
        pods = G.pod_names(topo)
        client.fresh_namespace(ns)
        st, r, wall = client.deploy(ns, topo)
        C.run_or_die(st, r, f"deploy {n}")
        C.log(f"[{n}] deployed in {wall:.1f} s")
        for run in range(1, args.runs + 1):
            body = {"targets": [{"pod": p, "actions": [{"type": "replace_ip", "iface": "eth1", "cidr": f"10.{80 + run}.{i % 250}.1/24"}]}
                                for i, p in enumerate(pods)]}
            st, r, wall = client.configure(ns, body)
            if st != 200 or not isinstance(r, dict):
                rec.note(f"[{n} run {run}] configure failed: {st} {json.dumps(r)[:200]}")
                rec.add(pods=n, run=run, ok=False)
                continue
            took = r.get("took_time") or {}
            per_pod = [a.get("pod_took_ms") for a in (r.get("action_results") or []) if a.get("pod_took_ms")]
            s = C.summarize(per_pod)
            row = rec.add(pods=n, run=run, ok=(r.get("failures") == 0), wall_s=round(wall, 3),
                          total_s=C.seconds(took.get("total")), sequential_s=C.seconds(took.get("sequential_equivalent")),
                          speedup=round(r.get("speedup") or 0, 2), successes=r.get("successes"), failures=r.get("failures"),
                          skipped=r.get("skipped"), pod_ms_mean=s.get("mean"), pod_ms_max=s.get("max"), pod_ms_min=s.get("min"))
            C.log(f"[{n} run {run}] total {row['total_s']} s, sequential-equivalent {row['sequential_s']} s, "
                  f"speedup {row['speedup']}x, per-pod {row['pod_ms_min']}..{row['pod_ms_max']} ms, "
                  f"{row['successes']} ok / {row['failures']} failed / {row['skipped']} skipped")
        client.clear_topology(ns)
    if not args.keep:
        client.drop_namespace(ns)

    summary = {}
    for n in args.sizes:
        rows = [r for r in rec.rows if r.get("pods") == n and r.get("ok")]
        summary[n] = {k: C.summarize([r.get(k) for r in rows]) for k in ("total_s", "sequential_s", "speedup", "pod_ms_mean", "pod_ms_max")}
    rec.finish(summary)
    C.table([{"pods": n, "concurrent": C.mean_std([r["total_s"] for r in rec.rows if r.get("pods") == n and r.get("ok")]),
              "sequential-equivalent": C.mean_std([r["sequential_s"] for r in rec.rows if r.get("pods") == n and r.get("ok")]),
              "speedup": C.mean_std([r["speedup"] for r in rec.rows if r.get("pods") == n and r.get("ok")], 1),
              "per-pod ms (mean)": C.mean_std([r["pod_ms_mean"] for r in rec.rows if r.get("pods") == n and r.get("ok")], 0)}
             for n in args.sizes], ["pods", "concurrent", "sequential-equivalent", "speedup", "per-pod ms (mean)"],
            "configure throughput (s, mean ± std)")


if __name__ == "__main__":
    main()
