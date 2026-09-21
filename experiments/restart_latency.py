#!/usr/bin/env python3
"""Where the time of a pod restart goes.

Deploys a small router topology (FRR router with two hosts), gives the router a
history of --depth static routes, then restarts it --runs times through the API and
splits every restart with the timeline: backend preparation, termination of the
old pod, scheduling, sandbox and CNI attachment, container start, readiness,
detection by the backend, replay. --grace sets terminationGracePeriodSeconds on the
nodes, so the same run with 30 shows what the Kubernetes default costs."""
import datetime as dt

import common as C
import gen_topology as G


def topology(grace, image, command):
    extra = {"terminationGracePeriodSeconds": grace} if grace else {}
    return {"nodes": [
        {"name": "r1", "image": image, "type": "router", "driver": "FRRRouterDriver", "commands": command, **extra},
        G.host_node("h1", **extra), G.host_node("h2", **extra)],
        "links": [
            {"node": "r1-0", "localIntf": "eth1", "localIp": "10.0.1.1/24", "peerNode": "h1-0", "peerIntf": "eth1", "peerIp": "10.0.1.2/24"},
            {"node": "r1-0", "localIntf": "eth2", "localIp": "10.0.2.1/24", "peerNode": "h2-0", "peerIntf": "eth1", "peerIp": "10.0.2.2/24"}]}


def decompose(resp):
    """Phase durations in seconds for the restarted pod, from the restart response."""
    tl = resp.get("timeline") or {}
    bm = tl.get("backend_ms") or {}
    pods = tl.get("pods") or []
    if not pods:
        return {}
    p = pods[0]
    ph = C.pod_phases(p)
    k, o = p.get("kubernetes", {}), p.get("observed_ms", {})
    out = {
        "prepare_s": bm.get("prepare", 0) / 1000 if bm.get("prepare") is not None else None,
        "termination_s": ph.get("termination_s"),
        "scheduling_s": ph.get("scheduling_s"),
        "sandbox_cni_s": ph.get("sandbox_cni_s"),
        # A negative value is the kubelet reporting sandbox and container in one sync
        # (doc/TIMING.md); the two cannot be told apart then and only their sum is real.
        "container_start_s": ph.get("container_start_s") if (ph.get("container_start_s") or 0) >= 0 else None,
        "scheduled_to_started_s": ph.get("scheduled_to_started_s"),
        "readiness_s": ph.get("readiness_s"),
        "wait_ready_s": bm.get("wait_ready", 0) / 1000 if bm.get("wait_ready") is not None else None,
        "replay_s": bm.get("replay", 0) / 1000 if bm.get("replay") is not None else None,
        "backend_total_s": bm.get("total", 0) / 1000 if bm.get("total") is not None else None,
    }
    # Detection lag: when the backend saw Ready minus when Kubernetes stamped it. The
    # stamp has 1 s resolution, so small negative values are rounding, not time travel.
    if o.get("ready_seen") is not None and k.get("ready") and tl.get("request_started_at"):
        seen_abs = C.ts(tl["request_started_at"]) + dt.timedelta(milliseconds=o["ready_seen"])
        out["detection_lag_s"] = round((seen_abs - C.ts(k["ready"])).total_seconds(), 3)
    if o.get("old_pod_gone") is not None and o.get("ready_seen") is not None:
        out["gone_to_ready_seen_s"] = (o["ready_seen"] - o["old_pod_gone"]) / 1000
    return out


PHASES = ["prepare_s", "termination_s", "scheduling_s", "sandbox_cni_s", "container_start_s", "scheduled_to_started_s", "readiness_s",
          "detection_lag_s", "replay_s", "backend_total_s", "wall_s"]


def main():
    ap = C.base_parser(__doc__)
    ap.add_argument("--grace", type=int, default=0, help="terminationGracePeriodSeconds for every node (0 = platform default)")
    ap.add_argument("--depth", type=int, default=3, help="static routes in the router's history before restarting")
    ap.add_argument("--image", default=C.FRR_IMAGE)
    ap.add_argument("--target", default="r1-0")
    ap.add_argument("--namespace", default="exp-restart")
    args = ap.parse_args()
    client = C.client_from(args)
    rec = C.Recorder("restart_latency", client, args.out, args)
    ns = args.namespace

    client.fresh_namespace(ns)
    st, r, wall = client.deploy(ns, topology(args.grace, args.image, C.FRR_COMMAND))
    C.run_or_die(st, r, "deploy")
    rec.note(f"deployed in {wall:.1f} s, grace {args.grace or 'default'}")
    if args.depth:
        actions = [{"type": "add_static_route", "dst_cidr": f"10.{200 + i // 250}.{i % 250}.0/24", "gateway": "10.0.2.2"} for i in range(args.depth)]
        st, r, wall = client.configure(ns, {"targets": [{"pod": args.target, "actions": actions}]})
        rec.note(f"history depth {args.depth}: {r.get('successes') if isinstance(r, dict) else r} applied in {wall:.1f} s")

    for run in range(1, args.runs + 1):
        st, r, wall = client.restart(ns, args.target)
        if st != 200:
            rec.note(f"run {run}: restart failed {st} {r}")
            rec.add(run=run, ok=False)
            continue
        d = decompose(r)
        rs, ps = r.get("replay") or {}, r.get("peer_replay") or {}
        row = rec.add(run=run, ok=True, grace=args.grace or "default", depth=args.depth, wall_s=round(wall, 3),
                      took_total_s=C.seconds((r.get("took_time") or {}).get("total")),
                      took_pod_restart_s=C.seconds((r.get("took_time") or {}).get("pod_restart")),
                      replayed=rs.get("replayed"), pruned=rs.get("pruned"), peers=ps.get("peers"), peer_reapplied=ps.get("reapplied"), **d)
        rec.save(f"restart_{run}.json", r)
        C.log(f"run {run}: wall {row['wall_s']} s = prepare {d.get('prepare_s')} + termination {d.get('termination_s')} + "
              f"sched {d.get('scheduling_s')} + sandbox/CNI {d.get('sandbox_cni_s')} + start {d.get('container_start_s')} + "
              f"readiness {d.get('readiness_s')} + detection {d.get('detection_lag_s')} + replay {d.get('replay_s')} "
              f"(replayed {row['replayed']}/{rs.get('total')}, peers {row['peers']})")

    if not args.keep:
        client.drop_namespace(ns)
    ok_rows = [r for r in rec.rows if r.get("ok")]
    summary = {k: C.summarize([r.get(k) for r in ok_rows]) for k in PHASES}
    rec.finish(summary)
    C.table([{"phase": k, "mean ± std (s)": C.mean_std([r.get(k) for r in ok_rows]), "min": summary[k].get("min"), "max": summary[k].get("max")}
             for k in PHASES], ["phase", "mean ± std (s)", "min", "max"],
            f"restart of {args.target}, grace {args.grace or 'default'}, history depth {args.depth}, {len(ok_rows)} runs")
    print("\nphases overlap and Kubernetes stamps have 1 s resolution: read them as a critical path, not a sum (doc/TIMING.md)")


if __name__ == "__main__":
    main()
