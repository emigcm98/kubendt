#!/usr/bin/env python3
"""Deployment time as a function of topology size, with the per-pod timeline.

Default mode deploys synthetic topologies (see gen_topology.py) of the given sizes,
--runs times each, and records for every run the client wall time, the backend's
took_time and backend_ms phases, and what Kubernetes wrote on the pods: creation
and scheduling spread (are pods created concurrently?), sandbox/CNI stagger, the
critical-path pod, and the lag between a pod being Ready and the backend seeing it.
Pod stamps are read with kubectl too, so the Kubernetes-side figures come out even
against a backend without the timeline block.

--topology FILE switches to a single fixed topology (a use case, for instance),
optionally with --files DIR / --zip FILE to upload mounts first and --configure
FILE to time the configuration step as well.

At the end it prints mean ± std per size and a least-squares affine fit
t(n) = a + b n over the per-size means."""
import json
import os
import time

import common as C
import gen_topology as G


def deploy_once(client, ns, topo, rec, tag, run, conf=None):
    client.fresh_namespace(ns)
    pods_before = C.pod_uids(ns)
    st, r, wall = client.deploy(ns, topo)
    if st != 200:
        rec.note(f"[{tag} run {run}] deploy failed: {st} {json.dumps(r)[:300]}")
        rec.add(case=tag, run=run, ok=False, error=json.dumps(r)[:300])
        client.clear_topology(ns)
        return None
    took = r.get("took_time") or {}
    tl = r.get("timeline") or {}
    bm = tl.get("backend_ms") or {}
    pods = C.pods_json(ns)
    stamps = [C.pod_stamps(p) for p in pods if p["metadata"]["uid"] not in pods_before.values()]
    k8s = {
        "pods": len(stamps),
        "created_spread_s": C.spread([s["created"] for s in stamps]),
        "scheduled_spread_s": C.spread([s["scheduled"] for s in stamps]),
        "sandbox_spread_s": C.spread([s["sandbox_ready"] for s in stamps]),
        "started_spread_s": C.spread([s["container_started"] for s in stamps]),
        "ready_spread_s": C.spread([s["ready"] for s in stamps]),
        "created_to_last_ready_s": C.secs(min((s["created"] for s in stamps if s["created"]), default=None),
                                          max((s["ready"] for s in stamps if s["ready"]), default=None)),
        "workers": sorted({s["node"] for s in stamps if s["node"]}),
        "image_ids": sorted({s["image_id"] for s in stamps if s["image_id"]}),
    }
    tls = C.timeline_summary(tl) if tl else {}
    row = dict(case=tag, run=run, ok=True, pods=len(G.pod_names(topo)),
               links=len(topo["links"]), wall_s=round(wall, 3),
               took_total_s=C.seconds(took.get("total")), took_resource_creation_s=C.seconds(took.get("resource_creation")),
               took_node_running_s=C.seconds(took.get("node_running")), took_heal_s=C.seconds(took.get("reconciliation")),
               backend_validation_ms=bm.get("validation"), backend_resource_creation_ms=bm.get("resource_creation"),
               backend_wait_ready_ms=bm.get("wait_ready"), backend_heal_ms=bm.get("heal"), backend_total_ms=bm.get("total"),
               k8s_created_spread_s=k8s["created_spread_s"], k8s_scheduled_spread_s=k8s["scheduled_spread_s"],
               k8s_sandbox_spread_s=k8s["sandbox_spread_s"], k8s_started_spread_s=k8s["started_spread_s"],
               k8s_ready_spread_s=k8s["ready_spread_s"], k8s_created_to_last_ready_s=k8s["created_to_last_ready_s"],
               critical_pod=tls.get("critical_pod"), last_ready_seen_s=tls.get("last_ready_seen_s"),
               workers=",".join(k8s["workers"]), image_ids=";".join(k8s["image_ids"]),
               warnings=len(r.get("warnings") or []))
    # Platform overhead: what the request took beyond the Kubernetes-side critical path
    # (first pod created to last pod Ready). Includes validation, resource creation
    # calls, detection lag and the heal pass.
    if k8s["created_to_last_ready_s"] is not None:
        row["overhead_s"] = round(wall - k8s["created_to_last_ready_s"], 3)
    if conf:
        st2, r2, wall2 = client.configure(ns, conf)
        ct = (r2.get("took_time") or {}) if isinstance(r2, dict) else {}
        row.update(configure_ok=(st2 == 200 and isinstance(r2, dict) and r2.get("failures") == 0),
                   configure_wall_s=round(wall2, 3), configure_total_s=C.seconds(ct.get("total")),
                   configure_seq_s=C.seconds(ct.get("sequential_equivalent")),
                   configure_actions=(r2.get("successes") if isinstance(r2, dict) else None))
    rec.add(**row)
    rec.save(f"timeline_{tag}_{run}.json", {"response_took_time": took, "timeline": tl, "pods": stamps,
                                             "phases": [C.pod_phases(p) for p in (tl.get("pods") or [])] or [C.pod_phases(s) for s in stamps]})
    st, r, wall = client.timed("DELETE", f"/network/clear-topology/{ns}")
    row["clear_wall_s"] = round(wall, 3)
    C.log(f"[{tag} run {run}] wall {row['wall_s']} s, backend total {row['took_total_s']} s, "
          f"k8s created→last Ready {row['k8s_created_to_last_ready_s']} s, created spread {row['k8s_created_spread_s']} s, "
          f"critical {row.get('critical_pod')}, clear {row['clear_wall_s']} s")
    return row


def main():
    ap = C.base_parser(__doc__)
    ap.add_argument("--sizes", type=int, nargs="+", default=[8, 16, 32, 64, 128])
    ap.add_argument("--shape", choices=list(G.SHAPES), default="sparse")
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--image", default=C.HOST_IMAGE)
    ap.add_argument("--topology", help="fixed topology JSON instead of synthetic sizes")
    ap.add_argument("--files", help="directory of files to upload before deploying (use-case mounts)")
    ap.add_argument("--zip", help="archive to import into the namespace file manager first")
    ap.add_argument("--configure", help="network_conf.json to apply and time after each deploy")
    ap.add_argument("--namespace", default="exp-deploy")
    ap.add_argument("--warmup", type=int, default=1, help="unrecorded deploys of the largest case first (image pull)")
    args = ap.parse_args()
    client = C.client_from(args)
    rec = C.Recorder("deploy_scaling", client, args.out, args)
    ns = args.namespace

    cases = []
    if args.topology:
        topo = json.load(open(args.topology))
        cases.append((os.path.splitext(os.path.basename(args.topology))[0], topo))
    else:
        for n in args.sizes:
            kw = {"seed": args.seed} if args.shape == "sparse" else {}
            cases.append((f"{args.shape}_{n}", G.SHAPES[args.shape](n, image=args.image, **kw)))
    conf = json.load(open(args.configure)) if args.configure else None

    client.fresh_namespace(ns)
    if args.files:
        for root, _, files in os.walk(args.files):
            for f in files:
                path = os.path.join(root, f)
                ok, r = client.upload(ns, os.path.relpath(path, args.files), path)
                if not ok:
                    rec.note(f"upload {f} failed: {r}")
    if args.zip:
        ok, r = client.import_zip(ns, args.zip)
        if not ok:
            rec.note(f"import {args.zip} failed: {r}")

    for _ in range(args.warmup):
        tag, topo = max(cases, key=lambda c: len(G.pod_names(c[1])))
        C.log(f"warmup deploy of {tag}")
        client.fresh_namespace(ns)
        st, r, wall = client.deploy(ns, topo)
        rec.note(f"warmup {tag}: {st} in {wall:.1f} s")
        client.clear_topology(ns)

    for tag, topo in cases:
        for run in range(1, args.runs + 1):
            deploy_once(client, ns, topo, rec, tag, run, conf)

    if not args.keep:
        client.drop_namespace(ns)

    # summary and affine fit over per-size means
    summary, xs, ys = {}, [], []
    for tag, topo in cases:
        rows = [r for r in rec.rows if r.get("case") == tag and r.get("ok")]
        s = {k: C.summarize([r.get(k) for r in rows]) for k in
             ("wall_s", "took_total_s", "took_resource_creation_s", "took_node_running_s", "took_heal_s", "overhead_s",
              "k8s_created_spread_s", "k8s_scheduled_spread_s", "k8s_sandbox_spread_s", "k8s_ready_spread_s",
              "k8s_created_to_last_ready_s", "clear_wall_s", "configure_total_s", "configure_seq_s")}
        s["pods"] = len(G.pod_names(topo))
        summary[tag] = s
        if rows:
            xs.append(s["pods"])
            ys.append(s["wall_s"]["mean"])
    fit = C.affine_fit(xs, ys) if len(xs) >= 2 else None
    if fit:
        summary["affine_fit_wall"] = {"fixed_s": round(fit[0], 2), "per_node_s": round(fit[1], 3), "r2": round(fit[2], 4)}
    rec.finish(summary)

    C.table([{"case": t, "pods": summary[t]["pods"], "wall": C.mean_std([r["wall_s"] for r in rec.rows if r.get("case") == t and r.get("ok")]),
              "resource_creation": C.mean_std([r["took_resource_creation_s"] for r in rec.rows if r.get("case") == t and r.get("ok")]),
              "node_running": C.mean_std([r["took_node_running_s"] for r in rec.rows if r.get("case") == t and r.get("ok")]),
              "heal": C.mean_std([r["took_heal_s"] for r in rec.rows if r.get("case") == t and r.get("ok")]),
              "k8s path": C.mean_std([r["k8s_created_to_last_ready_s"] for r in rec.rows if r.get("case") == t and r.get("ok")]),
              "overhead": C.mean_std([r.get("overhead_s") for r in rec.rows if r.get("case") == t and r.get("ok")]),
              "created spread": C.mean_std([r["k8s_created_spread_s"] for r in rec.rows if r.get("case") == t and r.get("ok")])}
             for t, _ in cases],
            ["case", "pods", "wall", "resource_creation", "node_running", "heal", "k8s path", "overhead", "created spread"],
            "deploy time (s, mean ± std)")
    if fit:
        print(f"\naffine fit of mean wall time: t(n) = {fit[0]:.2f} + {fit[1]:.3f} n  (R² = {fit[2]:.4f})")


if __name__ == "__main__":
    main()
