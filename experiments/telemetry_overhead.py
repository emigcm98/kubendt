#!/usr/bin/env python3
"""What the telemetry and topology endpoints cost.

Against a deployed namespace, first measures the latency of every endpoint the
dashboard polls (topology, per-namespace interfaces, namespace and per-pod metrics,
summary, cluster status), --iterations times each. Then runs --clients concurrent
pollers for --duration seconds at --period seconds per cycle, each cycle calling the
set of endpoints a dashboard session calls, and reports request rate, latency
percentiles and the backend's CPU and memory over the window (--backend-pid or
--backend-container), next to an idle baseline of the same length taken first.
Note which endpoints exec into pods: /namespaces/ips does, once per pod."""
import json
import statistics
import threading
import time

import common as C
import footprint as F

ENDPOINTS = {
    "get-network": lambda c, ns, pod: c.api("GET", f"/network/get-network/{ns}"),
    "links": lambda c, ns, pod: c.api("GET", f"/network/links/{ns}"),
    "pods": lambda c, ns, pod: c.api("GET", f"/pods/{ns}"),
    "ns-ips (exec per pod)": lambda c, ns, pod: c.ns_ips(ns),
    "ns-metrics": lambda c, ns, pod: c.ns_metrics(ns),
    "pod-metrics": lambda c, ns, pod: c.pod_metrics(ns, pod),
    "ns-summary": lambda c, ns, pod: c.ns_summary(ns),
    "cluster-status": lambda c, ns, pod: c.cluster_status(),
}
DASHBOARD_CYCLE = ["get-network", "ns-ips (exec per pod)", "pods", "ns-metrics", "pod-metrics", "cluster-status"]


def pct(values, q):
    if not values:
        return None
    vals = sorted(values)
    k = min(len(vals) - 1, max(0, int(round(q / 100 * (len(vals) - 1)))))
    return vals[k]


def backend_window(args, seconds, label, rec):
    """Sample the backend for a window and return CPU % and RSS."""
    p0 = F.proc_sample(args.backend_pid) if args.backend_pid else None
    d0 = F.docker_sample(args.backend_container) if args.backend_container else None
    t0 = time.time()
    return {"p0": p0, "d0": d0, "t0": t0}


def backend_close(args, win):
    out = {}
    if args.backend_pid and win["p0"]:
        p1 = F.proc_sample(args.backend_pid)
        if p1:
            out["backend_cpu_pct"] = round((p1["cpu_s"] - win["p0"]["cpu_s"]) / max(time.time() - win["t0"], 1) * 100, 2)
            out["backend_rss_mib"] = round(p1["rss_mib"], 1)
    if args.backend_container:
        d1 = F.docker_sample(args.backend_container)
        if d1:
            out["backend_cpu_pct"] = d1["cpu_pct"]
            out["backend_mem"] = d1["mem"]
    return out


def main():
    ap = C.base_parser(__doc__)
    ap.add_argument("--namespace", required=True)
    ap.add_argument("--pod", help="pod for the per-pod endpoints (default: first pod)")
    ap.add_argument("--iterations", type=int, default=20)
    ap.add_argument("--clients", type=int, nargs="+", default=[1, 5, 10])
    ap.add_argument("--period", type=float, default=2.0, help="seconds per dashboard cycle per client (the real UI uses 10 to 60)")
    ap.add_argument("--duration", type=float, default=60.0)
    ap.add_argument("--backend-pid", type=int)
    ap.add_argument("--backend-container")
    args = ap.parse_args()
    client = C.client_from(args)
    rec = C.Recorder("telemetry_overhead", client, args.out, args)
    ns = args.namespace
    pods = [p.get("name") or p.get("pod") for p in client.pods(ns)]
    if not pods:
        raise SystemExit(f"no pods in {ns}")
    pod = args.pod or pods[0]
    rec.note(f"{len(pods)} pods in {ns}, per-pod endpoints use {pod}")

    for name, fn in ENDPOINTS.items():
        lat, codes = [], {}
        for _ in range(args.iterations):
            t0 = time.time()
            st, r = fn(client, ns, pod)
            lat.append((time.time() - t0) * 1000)
            codes[st] = codes.get(st, 0) + 1
        row = rec.add(part="latency", endpoint=name, pods=len(pods), n=len(lat), mean_ms=round(statistics.mean(lat), 1), p50_ms=round(pct(lat, 50), 1),
                      p95_ms=round(pct(lat, 95), 1), max_ms=round(max(lat), 1), codes=json.dumps(codes))
        C.log(f"{name}: mean {row['mean_ms']} ms, p50 {row['p50_ms']}, p95 {row['p95_ms']}, max {row['max_ms']} ms, codes {codes}")

    # idle baseline for the backend
    win = backend_window(args, args.duration, "idle", rec)
    time.sleep(min(args.duration, 30))
    idle = backend_close(args, win)
    rec.add(part="load", clients=0, **idle)
    rec.note(f"idle backend: {idle}")

    for n_clients in args.clients:
        lats, count, errors = [], [0], [0]
        stop = time.time() + args.duration
        lock = threading.Lock()

        def poller():
            c = C.Client(args.url, None, args.token)
            c.cookie = client.cookie
            while time.time() < stop:
                t_cycle = time.time()
                for name in DASHBOARD_CYCLE:
                    t0 = time.time()
                    st, r = ENDPOINTS[name](c, ns, pod)
                    with lock:
                        lats.append((time.time() - t0) * 1000)
                        count[0] += 1
                        if st != 200:
                            errors[0] += 1
                rest = args.period - (time.time() - t_cycle)
                if rest > 0:
                    time.sleep(rest)

        win = backend_window(args, args.duration, f"{n_clients} clients", rec)
        threads = [threading.Thread(target=poller, daemon=True) for _ in range(n_clients)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        b = backend_close(args, win)
        row = rec.add(part="load", clients=n_clients, pods=len(pods), period_s=args.period, duration_s=args.duration, requests=count[0],
                      req_per_s=round(count[0] / args.duration, 2), errors=errors[0], mean_ms=round(statistics.mean(lats), 1) if lats else None,
                      p50_ms=round(pct(lats, 50), 1) if lats else None, p95_ms=round(pct(lats, 95), 1) if lats else None, max_ms=round(max(lats), 1) if lats else None, **b)
        C.log(f"{n_clients} clients: {row['req_per_s']} req/s, p50 {row['p50_ms']} ms, p95 {row['p95_ms']} ms, errors {errors[0]}, backend {b}")
        time.sleep(5)

    rec.finish({"latency": [r for r in rec.rows if r.get("part") == "latency"], "load": [r for r in rec.rows if r.get("part") == "load"]})
    C.table([r for r in rec.rows if r.get("part") == "latency"], ["endpoint", "mean_ms", "p50_ms", "p95_ms", "max_ms"], f"endpoint latency, {len(pods)} pods")
    C.table([r for r in rec.rows if r.get("part") == "load"], ["clients", "req_per_s", "p50_ms", "p95_ms", "errors", "backend_cpu_pct", "backend_rss_mib", "backend_mem"],
            f"dashboard-like polling, period {args.period} s, {args.duration} s")


if __name__ == "__main__":
    main()
