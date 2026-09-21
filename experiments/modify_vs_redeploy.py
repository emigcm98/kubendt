#!/usr/bin/env python3
"""In-place modification against full redeployment, measured as disruption.

On a ring of --nodes hosts, every run performs the operations an operator does on a
live topology (add node, delete node, add link, delete link, scale up, scale down,
restart), each through the API, and then the baseline: clear the namespace and
deploy the same ring again. For each operation it records completion time, which
pods the platform recreated, how long each of them was unavailable (from the
timeline) and, while the operation runs, a continuous ping between two pods that
are not involved, to show whether the rest of the twin kept working. --grace 30
repeats it with the Kubernetes default grace period."""
import json
import time

import common as C
import gen_topology as G


def link_between(topo, a, b):
    for l in topo["links"]:
        if {l["node"], l["peerNode"]} == {a, b}:
            return l
    return None


def unavailability(resp, recreated):
    """Per-pod unavailable window in seconds from the timeline: delete issued to Ready
    seen for recreated pods, created to Ready for new ones."""
    out = {}
    for p in (resp.get("timeline") or {}).get("pods") or []:
        o, k = p.get("observed_ms") or {}, p.get("kubernetes") or {}
        if o.get("delete_issued") is not None and o.get("ready_seen") is not None:
            out[p["pod"]] = round((o["ready_seen"] - o["delete_issued"]) / 1000, 3)
        elif k.get("created") and k.get("ready"):
            out[p["pod"]] = C.secs(k["created"], k["ready"])
    return out


def main():
    ap = C.base_parser(__doc__)
    ap.add_argument("--nodes", type=int, default=20)
    ap.add_argument("--grace", type=int, default=0, help="terminationGracePeriodSeconds for the ring (0 = platform default)")
    ap.add_argument("--image", default=C.HOST_IMAGE)
    ap.add_argument("--namespace", default="exp-modify")
    ap.add_argument("--ping-interval", type=float, default=0.2)
    args = ap.parse_args()
    client = C.client_from(args)
    rec = C.Recorder("modify_vs_redeploy", client, args.out, args)
    ns, n = args.namespace, args.nodes
    extra = {"terminationGracePeriodSeconds": args.grace} if args.grace else {}
    ring = G.ring(n, image=args.image, **extra)

    if n < 6:
        raise SystemExit("--nodes must be at least 6 so the continuity pair stays out of every operation")
    # The last two pods of the ring carry the continuity ping; no operation touches them.
    watch_a, watch_b = f"n-{n - 2}", f"n-{n - 1}"
    lk = link_between(ring, watch_a, watch_b)
    target_ip = (lk["peerIp"] if lk["peerNode"] == watch_b else lk["localIp"]).split("/")[0]
    extra_link = {"node": "x-0", "localIntf": "eth1", "localIp": "10.200.0.1/24", "peerNode": "n-0", "peerIntf": "eth3", "peerIp": "10.200.0.2/24"}
    cross_link = {"node": "n-2", "localIntf": "eth3", "localIp": "10.200.1.1/24", "peerNode": f"n-{n // 2}", "peerIntf": "eth3", "peerIp": "10.200.1.2/24"}
    scale_link = {"node": f"n-{n}", "localIntf": "eth1", "localIp": "10.200.2.1/24", "peerNode": "n-1", "peerIntf": "eth3", "peerIp": "10.200.2.2/24"}
    ops = [
        ("add_node", lambda: client.modify(ns, {"add": {"nodes": [G.host_node("x", args.image, **extra)], "links": [extra_link]}})),
        ("del_node", lambda: client.modify(ns, {"delete": {"nodes": ["x"]}})),
        ("add_link", lambda: client.modify(ns, {"add": {"links": [cross_link]}})),
        ("del_link", lambda: client.modify(ns, {"delete": {"links": [cross_link]}})),
        ("scale_up", lambda: client.modify(ns, {"scale": [{"name": "n", "replicas": n + 1}], "add": {"links": [scale_link]}})),
        ("scale_down", lambda: client.modify(ns, {"scale": [{"name": "n", "replicas": n}]})),
        ("restart", lambda: client.restart(ns, "n-3")),
    ]

    client.fresh_namespace(ns)
    st, r, wall = client.deploy(ns, ring)
    C.run_or_die(st, r, "deploy")
    rec.note(f"ring of {n} deployed in {wall:.1f} s, grace {args.grace or 'default'}; continuity ping {watch_a} -> {watch_b} {target_ip}")

    for run in range(1, args.runs + 1):
        for name, op in ops:
            before = C.pod_uids(ns)
            pid = C.start_ping(ns, watch_a, target_ip, args.ping_interval)
            time.sleep(1)
            st, r, wall = op()
            time.sleep(1)
            ping = C.stop_ping(ns, watch_a, pid, args.ping_interval) if pid else {}
            after = C.pod_uids(ns)
            recreated = sorted(p for p in after if p in before and after[p] != before[p])
            created = sorted(p for p in after if p not in before)
            deleted = sorted(p for p in before if p not in after)
            if st != 200 or not isinstance(r, dict):
                rec.note(f"run {run} {name}: failed {st} {json.dumps(r)[:200]}")
                rec.add(run=run, op=name, ok=False)
                continue
            un = unavailability(r, recreated)
            row = rec.add(run=run, op=name, ok=True, wall_s=round(wall, 3), took_total_s=C.seconds((r.get("took_time") or {}).get("total")),
                          restarted_pods=",".join(r.get("restarted_pods") or []), recreated=",".join(recreated), created=",".join(created),
                          deleted=",".join(deleted), pods_recreated=len(recreated), pods_created=len(created), pods_deleted=len(deleted),
                          pods_total=len(after), unavailable_max_s=max(un.values()) if un else 0.0, unavailable=json.dumps(un),
                          ping_replies=ping.get("replies"), ping_sent=ping.get("sent"), ping_loss_pct=ping.get("loss_pct"),
                          ping_max_gap_s=ping.get("max_gap_s"))
            rec.save(f"{name}_{run}.json", r)
            C.log(f"run {run} {name}: {row['wall_s']} s, recreated {recreated or '-'}, created {created or '-'}, deleted {deleted or '-'}, "
                  f"unavailable max {row['unavailable_max_s']} s, continuity loss {ping.get('loss_pct')}% (max gap {ping.get('max_gap_s')} s)")
            if not C.wait_pods_ready(ns, timeout=300):
                rec.note(f"run {run} {name}: pods not all Ready afterwards")
        # Baseline: throw the twin away and deploy it again. Every pod is down for the
        # whole operation, so there is nothing to ping. Time until the ring is usable.
        before = C.pod_uids(ns)
        t0 = time.time()
        st, r, wall_clear = client.timed("DELETE", f"/network/clear-topology/{ns}")
        st2, r2, wall_deploy = client.deploy(ns, ring)
        total = time.time() - t0
        ok = st == 200 and st2 == 200
        rc, out = C.kexec(ns, watch_a, f"ping -c 2 -W 2 {target_ip}")
        rec.add(run=run, op="redeploy", ok=ok and rc == 0, wall_s=round(total, 3), clear_s=round(wall_clear, 3), deploy_s=round(wall_deploy, 3),
                pods_recreated=len(before), pods_total=len(before), unavailable_max_s=round(total, 3), ping_loss_pct=100.0,
                restarted_pods="all", recreated="all")
        C.log(f"run {run} redeploy: clear {wall_clear:.1f} s + deploy {wall_deploy:.1f} s = {total:.1f} s, all {len(before)} pods down, ring pings again: {rc == 0}")

    if not args.keep:
        client.drop_namespace(ns)
    names = [o[0] for o in ops] + ["redeploy"]
    summary = {name: {k: C.summarize([r.get(k) for r in rec.rows if r.get("op") == name and r.get("ok")])
                      for k in ("wall_s", "pods_recreated", "unavailable_max_s", "ping_loss_pct", "ping_max_gap_s")} for name in names}
    rec.finish(summary)
    C.table([{"operation": name,
              "time (s)": C.mean_std([r["wall_s"] for r in rec.rows if r.get("op") == name and r.get("ok")]),
              "pods recreated": C.mean_std([r["pods_recreated"] for r in rec.rows if r.get("op") == name and r.get("ok")], 1),
              "max unavailable (s)": C.mean_std([r["unavailable_max_s"] for r in rec.rows if r.get("op") == name and r.get("ok")]),
              "loss elsewhere (%)": C.mean_std([r.get("ping_loss_pct") for r in rec.rows if r.get("op") == name and r.get("ok")], 1),
              "max gap (s)": C.mean_std([r.get("ping_max_gap_s") for r in rec.rows if r.get("op") == name and r.get("ok")], 1)}
             for name in names], ["operation", "time (s)", "pods recreated", "max unavailable (s)", "loss elsewhere (%)", "max gap (s)"],
            f"ring of {n} nodes, grace {args.grace or 'default'}, {args.runs} runs")


if __name__ == "__main__":
    main()
