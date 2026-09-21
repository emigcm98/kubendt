#!/usr/bin/env python3
"""Steady-state resource footprint of a running twin and of KubeNDT itself.

Samples GET /namespaces/metrics/{ns} every --interval seconds, --samples times, and
reports per-pod and whole-namespace CPU and memory from the same samples, so the
per-pod rows add up to the total by construction. The KubeNDT side is measured
alongside: the backend process (--backend-pid, from /proc) or container
(--backend-container, docker stats), and the in-cluster helpers it depends on
(metrics-server, Meshnet) through kubectl top. Deploy the topology first, or pass
--topology (and --configure) to deploy one for the measurement."""
import json
import os
import subprocess
import time

import common as C


def proc_sample(pid):
    try:
        with open(f"/proc/{pid}/stat") as f:
            parts = f.read().split(")")[-1].split()
        ticks = os.sysconf("SC_CLK_TCK")
        cpu_s = (int(parts[11]) + int(parts[12])) / ticks
        with open(f"/proc/{pid}/status") as f:
            rss = next((int(l.split()[1]) / 1024 for l in f if l.startswith("VmRSS")), None)
        return {"cpu_s": cpu_s, "rss_mib": rss}
    except (OSError, IndexError, ValueError):
        return None


def docker_sample(name):
    p = subprocess.run(["docker", "stats", "--no-stream", "--format", "{{json .}}", name], capture_output=True, text=True)
    if p.returncode != 0:
        return None
    j = json.loads(p.stdout.strip().splitlines()[0])
    mem = j.get("MemUsage", "").split("/")[0].strip()
    return {"cpu_pct": float(j.get("CPUPerc", "0%").rstrip("%")), "mem": mem}


def helper_pods():
    """Pods of the components KubeNDT relies on, by namespace, for kubectl top."""
    out = {}
    for ns in ("kube-system", "meshnet"):
        for pod, (cpu, mem) in C.top_pods(ns).items():
            if any(k in pod for k in ("metrics-server", "meshnet")):
                out[f"{ns}/{pod}"] = (cpu, mem)
    return out


def main():
    ap = C.base_parser(__doc__)
    ap.add_argument("--namespace", required=True)
    ap.add_argument("--topology", help="deploy this topology into the namespace first")
    ap.add_argument("--configure", help="apply this network_conf.json after deploying")
    ap.add_argument("--samples", type=int, default=8)
    ap.add_argument("--interval", type=float, default=15.0, help="seconds between samples (metrics-server resolution is 15 s)")
    ap.add_argument("--backend-pid", type=int, help="local backend process to sample from /proc")
    ap.add_argument("--backend-container", help="docker container name of the backend (compose deployment)")
    ap.add_argument("--frontend-container", help="docker container name of the frontend")
    ap.add_argument("--settle", type=float, default=60.0, help="seconds to wait after deploying before sampling")
    args = ap.parse_args()
    client = C.client_from(args)
    rec = C.Recorder("footprint", client, args.out, args)
    ns = args.namespace

    if args.topology:
        client.fresh_namespace(ns)
        st, r, wall = client.deploy(ns, json.load(open(args.topology)))
        C.run_or_die(st, r, "deploy")
        rec.note(f"deployed {args.topology} in {wall:.1f} s")
        if args.configure:
            st, r, wall = client.configure(ns, json.load(open(args.configure)))
            rec.note(f"configured in {wall:.1f} s, failures {r.get('failures') if isinstance(r, dict) else '?'}")
        C.log(f"settling {args.settle} s")
        time.sleep(args.settle)

    pods_info = {p.get("name") or p.get("pod"): p for p in client.pods(ns)}
    proc0 = proc_sample(args.backend_pid) if args.backend_pid else None
    t_start = time.time()
    for i in range(1, args.samples + 1):
        st, m = client.ns_metrics(ns)
        if st != 200 or not isinstance(m, dict) or not m.get("available", True):
            rec.note(f"sample {i}: metrics unavailable ({st})")
            time.sleep(args.interval)
            continue
        pods = m.get("pods") or []
        total_cpu = sum(p.get("cpu_milli", 0) for p in pods)
        total_mem = round(sum(p.get("memory_bytes", 0) for p in pods) / 1024 / 1024, 2)
        row = dict(sample=i, kind="namespace", pods=len(pods), cpu_m=total_cpu, mem_mib=total_mem,
                   api_total_cpu_m=m.get("total_cpu_milli"), api_total_mem_mib=m.get("total_memory_mib"))
        for p in pods:
            info = pods_info.get(p["pod"], {})
            rec.add(sample=i, kind="pod", pod=p["pod"], cpu_m=p.get("cpu_milli"), mem_mib=round(p.get("memory_bytes", 0) / 1024 / 1024, 2),
                    driver=info.get("driver") or info.get("labels", {}).get("kubendt/driver"), runtime=info.get("runtime") or info.get("labels", {}).get("kubendt/runtime"))
        if args.backend_pid:
            s = proc_sample(args.backend_pid)
            if s and proc0:
                row.update(backend_rss_mib=round(s["rss_mib"], 1), backend_cpu_pct=round((s["cpu_s"] - proc0["cpu_s"]) / max(time.time() - t_start, 1) * 100, 2))
        if args.backend_container:
            s = docker_sample(args.backend_container)
            if s:
                row.update(backend_cpu_pct=s["cpu_pct"], backend_mem=s["mem"])
        if args.frontend_container:
            s = docker_sample(args.frontend_container)
            if s:
                row.update(frontend_cpu_pct=s["cpu_pct"], frontend_mem=s["mem"])
        helpers = helper_pods()
        row["helpers"] = json.dumps({k: {"cpu_m": v[0], "mem_mib": round(v[1], 1)} for k, v in helpers.items()})
        row["nodes"] = json.dumps(C.top_nodes())
        rec.add(**row)
        C.log(f"sample {i}: {len(pods)} pods, {total_cpu} m, {total_mem} MiB" + (f", backend {row.get('backend_rss_mib')} MiB rss {row.get('backend_cpu_pct')}% cpu" if args.backend_pid else "")
              + f", helpers {row['helpers']}")
        if i < args.samples:
            time.sleep(args.interval)

    if args.topology and not args.keep:
        client.drop_namespace(ns)
    pod_rows = [r for r in rec.rows if r.get("kind") == "pod"]
    ns_rows = [r for r in rec.rows if r.get("kind") == "namespace"]
    per_pod = {}
    for p in sorted({r["pod"] for r in pod_rows}):
        sel = [r for r in pod_rows if r["pod"] == p]
        per_pod[p] = {"cpu_m": C.summarize([r["cpu_m"] for r in sel]), "mem_mib": C.summarize([r["mem_mib"] for r in sel]),
                      "driver": sel[0].get("driver"), "runtime": sel[0].get("runtime")}
    summary = {"pods": per_pod, "namespace": {"cpu_m": C.summarize([r["cpu_m"] for r in ns_rows]), "mem_mib": C.summarize([r["mem_mib"] for r in ns_rows])},
               "backend": {"rss_mib": C.summarize([r.get("backend_rss_mib") for r in ns_rows]), "cpu_pct": C.summarize([r.get("backend_cpu_pct") for r in ns_rows])}}
    rec.finish(summary)
    C.table([{"pod": p, "driver": v["driver"] or "", "runtime": v["runtime"] or "", "cpu (m)": C.mean_std([r["cpu_m"] for r in pod_rows if r["pod"] == p], 1),
              "mem (MiB)": C.mean_std([r["mem_mib"] for r in pod_rows if r["pod"] == p], 1)} for p, v in per_pod.items()]
            + [{"pod": "namespace total", "driver": "", "runtime": "", "cpu (m)": C.mean_std([r["cpu_m"] for r in ns_rows], 1), "mem (MiB)": C.mean_std([r["mem_mib"] for r in ns_rows], 1)}],
            ["pod", "driver", "runtime", "cpu (m)", "mem (MiB)"], f"steady-state footprint of {ns}, {len(ns_rows)} samples every {args.interval} s")
    if args.backend_pid or args.backend_container:
        print(f"\nbackend: rss {C.mean_std([r.get('backend_rss_mib') for r in ns_rows], 1)} MiB, cpu {C.mean_std([r.get('backend_cpu_pct') for r in ns_rows], 2)} %")


if __name__ == "__main__":
    main()
