#!/usr/bin/env python3
"""Does the state come back after a restart, and how long does replay take?

Part one, verification. Deploys a routed lab (two FRR routers speaking OSPF, a Linux
switch with two hosts behind it, two more hosts) and configures it through the API
with a mixed history: OSPF router ids and networks, a secondary address, a static
route, SNAT, netem and tbf qdiscs, bridge membership, default routes. It snapshots
the observable state of every pod (addresses, routes, FRR running config, NAT rules,
bridge ports, qdiscs read back through the API), restarts each --target through the
API, waits for OSPF to converge, snapshots again and diffs. Neighbours are included
in the diff, since Meshnet recreates their interfaces too. A functional check (pings
across the routers) runs before and after. With --vyos-image a VyOS router joins the
OSPF domain and is restarted as well.

Part two, --depth. On the first router, replaces the history with N mixed actions
(static routes, secondary addresses, OSPF networks) and restarts it --runs times per
depth, recording the replay phase, for the replay time versus history depth curve."""
import json
import re
import time

import common as C
import gen_topology as G

VOLATILE_RE = re.compile(r"\b(valid_lft|preferred_lft|scope|qlen|link/ether|brd|noprefixroute|dynamic|metric \d+)\b.*")
NHID_RE = re.compile(r"\s*nhid \d+")


def topology(args):
    frr = {"image": args.frr_image, "type": "router", "driver": "FRRRouterDriver", "commands": C.FRR_COMMAND}
    sw_cmd = ["sh", "-c", "apk add --no-cache iproute2 >/dev/null 2>&1; sleep infinity"]
    nodes = [
        {"name": "r1", **frr}, {"name": "r2", **frr},
        {"name": "sw", "image": args.host_image, "type": "switch", "commands": sw_cmd},
        G.host_node("h1", args.host_image), G.host_node("h2", args.host_image),
        G.host_node("h3", args.host_image), G.host_node("h4", args.host_image),
    ]
    links = [
        {"node": "r1-0", "localIntf": "eth1", "localIp": "10.1.0.1/24", "peerNode": "sw-0", "peerIntf": "eth1", "peerIp": ""},
        {"node": "h1-0", "localIntf": "eth1", "localIp": "10.1.0.11/24", "peerNode": "sw-0", "peerIntf": "eth2", "peerIp": ""},
        {"node": "h2-0", "localIntf": "eth1", "localIp": "10.1.0.12/24", "peerNode": "sw-0", "peerIntf": "eth3", "peerIp": ""},
        {"node": "r1-0", "localIntf": "eth2", "localIp": "10.3.0.1/24", "peerNode": "h3-0", "peerIntf": "eth1", "peerIp": "10.3.0.2/24"},
        {"node": "r1-0", "localIntf": "eth3", "localIp": "10.9.0.1/24", "peerNode": "r2-0", "peerIntf": "eth1", "peerIp": "10.9.0.2/24"},
        {"node": "r2-0", "localIntf": "eth2", "localIp": "10.4.0.1/24", "peerNode": "h4-0", "peerIntf": "eth1", "peerIp": "10.4.0.2/24"},
    ]
    if args.vyos_image:
        nodes.append({"name": "v1", "image": args.vyos_image, "type": "router", "driver": "VyOSRouterDriver"})
        nodes.append(G.host_node("h5", args.host_image))
        links += [
            {"node": "r1-0", "localIntf": "eth4", "localIp": "10.8.0.1/24", "peerNode": "v1-0", "peerIntf": "eth1", "peerIp": "10.8.0.2/24"},
            {"node": "v1-0", "localIntf": "eth2", "localIp": "10.5.0.1/24", "peerNode": "h5-0", "peerIntf": "eth1", "peerIp": "10.5.0.2/24"},
        ]
    return {"nodes": nodes, "links": links}


def configuration(args):
    r1 = [
        {"type": "ospf_set_router_id", "router_id": "1.1.1.1"},
        {"type": "ospf_add_network", "cidr": "10.1.0.0/24", "ospf_area": "0"},
        {"type": "ospf_add_network", "cidr": "10.3.0.0/24", "ospf_area": "0"},
        {"type": "ospf_add_network", "cidr": "10.9.0.0/24", "ospf_area": "0"},
        {"type": "ospf_passive_default"},
        {"type": "ospf_no_passive", "iface": "eth3"},
        {"type": "set_ip", "iface": "eth1", "cidr": "10.1.0.254/24"},
        {"type": "add_static_route", "dst_cidr": "10.99.0.0/24", "gateway": "10.3.0.2"},
        {"type": "enable_snat", "iface": "eth0"},
        {"type": "add_qdisc", "iface": "eth2", "tcparams": {"qdisc": "netem", "delay": "20ms"}},
    ]
    r2 = [
        {"type": "ospf_set_router_id", "router_id": "2.2.2.2"},
        {"type": "ospf_add_network", "cidr": "10.9.0.0/24", "ospf_area": "0"},
        {"type": "ospf_add_network", "cidr": "10.4.0.0/24", "ospf_area": "0"},
    ]
    targets = [
        {"pod": "r1-0", "actions": r1}, {"pod": "r2-0", "actions": r2},
        {"pod": "sw-0", "actions": [{"type": "setup_bridge", "bridge": "br0", "ifaces": ["eth1", "eth2", "eth3"]}]},
        {"pod": "h1-0", "actions": [{"type": "set_default_route", "gateway": "10.1.0.1"},
                                    {"type": "add_qdisc", "iface": "eth1", "tcparams": {"qdisc": "tbf", "rate": "5mbit", "burst": "32kbit", "latency": "50ms"}}]},
        {"pod": "h2-0", "actions": [{"type": "set_default_route", "gateway": "10.1.0.1"}]},
        {"pod": "h3-0", "actions": [{"type": "set_default_route", "gateway": "10.3.0.1"}]},
        {"pod": "h4-0", "actions": [{"type": "set_default_route", "gateway": "10.4.0.1"}]},
    ]
    if args.vyos_image:
        r1.extend([{"type": "ospf_add_network", "cidr": "10.8.0.0/24", "ospf_area": "0"}, {"type": "ospf_no_passive", "iface": "eth4"},
                   {"type": "ospf_mtu_ignore", "iface": "eth4"}])
        targets.append({"pod": "v1-0", "actions": [
            {"type": "ospf_set_router_id", "router_id": "3.3.3.3"},
            {"type": "ospf_add_network", "cidr": "10.8.0.0/24", "ospf_area": "0"},
            {"type": "ospf_add_network", "cidr": "10.5.0.0/24", "ospf_area": "0"},
            {"type": "ospf_mtu_ignore", "iface": "eth1"}]})
        targets.append({"pod": "h5-0", "actions": [{"type": "set_default_route", "gateway": "10.5.0.1"}]})
    return {"targets": targets}


def norm_lines(out, drop=("eth0", "lo ", "docker", "cni")):
    res = []
    for line in out.splitlines():
        line = NHID_RE.sub("", VOLATILE_RE.sub("", line)).strip()
        line = re.sub(r"^\d+:\s*", "", line)
        if not line or any(d in line for d in drop):
            continue
        res.append(line)
    return sorted(set(res))


def frr_config(out):
    keep = re.compile(r"^(router ospf|\s+(ospf router-id|network|passive-interface|no passive-interface)|ip route|interface eth|\s+ip ospf)")
    return sorted({l.rstrip() for l in out.splitlines() if keep.match(l)})


def snapshot(client, ns, pod, kind, ifaces):
    """Observable state of a pod, normalized so a restart with identical configuration diffs empty."""
    s = {}
    rc, out = C.kexec(ns, pod, "ip -o -4 addr show")
    s["addrs"] = norm_lines(out)
    rc, out = C.kexec(ns, pod, "ip -4 route show")
    s["routes"] = norm_lines(out)
    if kind == "frr":
        rc, out = C.kexec(ns, pod, "vtysh -c 'show running-config'")
        s["frr"] = frr_config(out)
        rc, out = C.kexec(ns, pod, "iptables -t nat -S 2>/dev/null | grep -E 'MASQUERADE|DNAT' | sed 's/ -o eth0//'")
        s["nat"] = norm_lines(out, drop=())
    if kind == "switch":
        rc, out = C.kexec(ns, pod, "for i in /sys/class/net/br0/brif/*; do basename $i; done 2>/dev/null")
        s["bridge_ports"] = sorted(out.split())
    if kind == "vyos":
        rc, out = C.kexec(ns, pod, "ssh_qemu /opt/vyatta/bin/vyatta-op-cmd-wrapper show configuration commands 2>/dev/null | grep -E 'protocols ospf|interfaces ethernet eth[1-9]'", timeout=60)
        s["vyos"] = sorted(set(l.strip() for l in out.splitlines() if l.strip()))
        s["addrs"] = []  # the pod netns only holds taps, the addresses live in the guest
        s["routes"] = []
    for iface in ifaces:
        q = client.qdisc(ns, pod, iface)
        tp = (q or {}).get("tcparams") or {}
        s[f"qdisc_{iface}"] = {k: tp.get(k) for k in ("qdisc", "delay", "loss", "rate") if tp.get(k)}
    return s


def diff(a, b):
    out = {}
    for k in sorted(set(a) | set(b)):
        if a.get(k) != b.get(k):
            out[k] = {"before": a.get(k), "after": b.get(k)}
    return out


def ospf_full(ns, pod, want):
    def f():
        rc, out = C.kexec(ns, pod, "vtysh -c 'show ip ospf neighbor'")
        return out.count("Full") >= want, out[-200:]
    return f


def main():
    ap = C.base_parser(__doc__)
    ap.add_argument("--targets", nargs="+", default=None, help="pods to restart in the verification part")
    ap.add_argument("--depth", type=int, nargs="*", default=[1, 3, 5, 10, 20], help="history depths for part two, empty to skip")
    ap.add_argument("--frr-image", default=C.FRR_IMAGE)
    ap.add_argument("--host-image", default=C.HOST_IMAGE)
    ap.add_argument("--vyos-image", default=None, help="add a VyOS router (e.g. localhost/vyos-router:dev)")
    ap.add_argument("--namespace", default="exp-replay")
    args = ap.parse_args()
    client = C.client_from(args)
    rec = C.Recorder("replay_verify", client, args.out, args)
    ns = args.namespace
    topo, conf = topology(args), configuration(args)
    kinds = {"r1-0": "frr", "r2-0": "frr", "sw-0": "switch", "v1-0": "vyos"}
    ifaces = {"r1-0": ["eth2"], "h1-0": ["eth1"]}  # where a qdisc was configured
    pods = G.pod_names(topo)
    targets = args.targets or ["r1-0", "sw-0", "h1-0"] + (["v1-0"] if args.vyos_image else [])
    peers_of = {}
    for l in topo["links"]:
        peers_of.setdefault(l["node"], set()).add(l["peerNode"])
        peers_of.setdefault(l["peerNode"], set()).add(l["node"])

    client.fresh_namespace(ns)
    st, r, wall = client.deploy(ns, topo)
    C.run_or_die(st, r, "deploy")
    rec.note(f"deployed {len(pods)} pods in {wall:.1f} s")
    if args.vyos_image:
        C.retry(lambda: (C.kexec(ns, "v1-0", "vyos_api retrieve '{\"op\":\"showConfig\",\"path\":[\"system\",\"host-name\"]}' >/dev/null 2>&1 && echo ok", timeout=40)[1] == "ok", ""), 120)
    st, r, wall = client.configure(ns, conf)
    n_actions = sum(len(t["actions"]) for t in conf["targets"])
    rec.note(f"configured {r.get('successes') if isinstance(r, dict) else '?'}/{n_actions} actions in {wall:.1f} s, failures {r.get('failures') if isinstance(r, dict) else '?'}")
    want_full = 2 if args.vyos_image else 1
    ok, det, w = C.retry(ospf_full(ns, "r1-0", want_full), 180)
    rec.note(f"OSPF Full on r1 after {w} s: {ok}")

    def functional():
        checks = {}
        ok, det, w = C.retry(lambda: (C.kexec(ns, "h1-0", "ping -c 2 -W 2 10.3.0.2")[0] == 0, ""), 60)
        checks["h1->h3 via r1 (bridge + router)"] = (ok, w)
        ok, det, w = C.retry(lambda: (C.kexec(ns, "h3-0", "ping -c 2 -W 2 10.4.0.2")[0] == 0, ""), 90)
        checks["h3->h4 via r1,r2 (OSPF)"] = (ok, w)
        if args.vyos_image:
            ok, det, w = C.retry(lambda: (C.kexec(ns, "h3-0", "ping -c 2 -W 2 10.5.0.2")[0] == 0, ""), 120)
            checks["h3->h5 via r1,v1 (OSPF, VM)"] = (ok, w)
        return checks

    base_checks = functional()
    rec.note("functional before: " + ", ".join(f"{k}: {'ok' if v[0] else 'FAIL'} ({v[1]} s)" for k, v in base_checks.items()))
    before = {p: snapshot(client, ns, p, kinds.get(p, "host"), ifaces.get(p, [])) for p in pods}
    rec.save("snapshot_before.json", before)

    for target in targets:
        hist = client.history(ns, target)
        n_hist = len(hist.get("operations") or hist.get("history") or hist) if isinstance(hist, (dict, list)) else None
        st, r, wall = client.restart(ns, target)
        if st != 200:
            rec.note(f"restart {target} failed: {st} {r}")
            rec.add(part="verify", target=target, ok=False)
            continue
        rs, ps = r.get("replay") or {}, r.get("peer_replay") or {}
        bm = (r.get("timeline") or {}).get("backend_ms") or {}
        ok, det, w_ospf = C.retry(ospf_full(ns, "r1-0", want_full), 240)
        checks = functional()
        after = {p: snapshot(client, ns, p, kinds.get(p, "host"), ifaces.get(p, [])) for p in pods}
        diffs = {p: diff(before[p], after[p]) for p in pods if diff(before[p], after[p])}
        row = rec.add(part="verify", target=target, ok=True, wall_s=round(wall, 3), history_ops=n_hist,
                      replayed=rs.get("replayed"), pruned=rs.get("pruned"), replay_total=rs.get("total"),
                      peers=ps.get("peers"), peer_reapplied=ps.get("reapplied"), peer_failed=ps.get("failed"), qemu_rewired=ps.get("qemu_rewired"),
                      replay_s=(bm.get("replay") or 0) / 1000, wait_ready_s=(bm.get("wait_ready") or 0) / 1000,
                      ospf_full_after_s=w_ospf if ok else None, pods_with_diff=len(diffs), diff_pods=",".join(sorted(diffs)),
                      neighbours=",".join(sorted(peers_of.get(target, []))),
                      functional_ok=all(v[0] for v in checks.values()),
                      functional=json.dumps({k: {"ok": v[0], "after_s": v[1]} for k, v in checks.items()}))
        rec.save(f"restart_{target}.json", {"response": r, "diffs": diffs})
        C.log(f"restart {target}: {row['wall_s']} s, replayed {row['replayed']}/{row['replay_total']} (pruned {row['pruned']}), "
              f"peers {row['peers']} reapplied {row['peer_reapplied']}, OSPF Full after {w_ospf} s, "
              f"state diff in {len(diffs)} pod(s) {sorted(diffs) or ''}, functional {'ok' if row['functional_ok'] else 'FAIL'}")
        for p, d in diffs.items():
            for k, v in d.items():
                rec.note(f"  diff {p}.{k}: before {v['before']} after {v['after']}")

    if args.depth:
        rec.note("part two: replay time vs history depth on r1-0 (its history is replaced)")
        for depth in args.depth:
            client.api("DELETE", f"/drivers/history/namespace/{ns}/pod/r1-0")
            st, r, wall = client.restart(ns, "r1-0")  # clean pod, empty history
            actions = []
            for i in range(depth):
                k = i % 3
                if k == 0:
                    actions.append({"type": "add_static_route", "dst_cidr": f"10.{60 + i // 250}.{i % 250}.0/24", "gateway": "10.3.0.2"})
                elif k == 1:
                    actions.append({"type": "set_ip", "iface": "eth2", "cidr": f"10.{70 + i // 250}.{i % 250}.1/24"})
                else:
                    actions.append({"type": "ospf_add_network", "cidr": f"10.{70 + i // 250}.{i % 250}.0/24", "ospf_area": "0"})
            st, r, wall = client.configure(ns, {"targets": [{"pod": "r1-0", "actions": actions}]})
            applied = r.get("successes") if isinstance(r, dict) else None
            for run in range(1, args.runs + 1):
                st, r, wall = client.restart(ns, "r1-0")
                if st != 200:
                    rec.add(part="depth", depth=depth, run=run, ok=False)
                    continue
                rs = r.get("replay") or {}
                bm = (r.get("timeline") or {}).get("backend_ms") or {}
                row = rec.add(part="depth", depth=depth, run=run, ok=rs.get("replayed") == depth, applied=applied, replayed=rs.get("replayed"),
                              pruned=rs.get("pruned"), replay_s=(bm.get("replay") or 0) / 1000, took_replay_s=C.seconds((r.get("took_time") or {}).get("replay")),
                              wall_s=round(wall, 3), per_action_ms=round((bm.get("replay") or 0) / depth, 1))
                C.log(f"depth {depth} run {run}: replay {row['replay_s']} s for {row['replayed']} actions ({row['per_action_ms']} ms/action), restart {row['wall_s']} s")

    if not args.keep:
        client.drop_namespace(ns)
    verify_rows = [r for r in rec.rows if r.get("part") == "verify"]
    depth_rows = [r for r in rec.rows if r.get("part") == "depth" and r.get("ok")]
    summary = {"verify": verify_rows,
               "depth": {d: C.summarize([r["replay_s"] for r in depth_rows if r["depth"] == d]) for d in args.depth}}
    xs = [d for d in args.depth if summary["depth"][d].get("n")]
    fit = C.affine_fit(xs, [summary["depth"][d]["mean"] for d in xs]) if len(xs) >= 2 else None
    if fit:
        summary["depth_fit"] = {"fixed_s": round(fit[0], 3), "per_action_s": round(fit[1], 4), "r2": round(fit[2], 4)}
    rec.finish(summary)
    C.table([{"target": r["target"], "restart (s)": r.get("wall_s"), "replayed": f"{r.get('replayed')}/{r.get('replay_total')}", "pruned": r.get("pruned"),
              "peers reapplied": f"{r.get('peer_reapplied')} on {r.get('peers')}", "OSPF Full after (s)": r.get("ospf_full_after_s"),
              "pods with state diff": r.get("pods_with_diff"), "functional": "ok" if r.get("functional_ok") else "FAIL"} for r in verify_rows],
            ["target", "restart (s)", "replayed", "pruned", "peers reapplied", "OSPF Full after (s)", "pods with state diff", "functional"], "state after restart")
    if depth_rows:
        C.table([{"depth": d, "replay (s)": C.mean_std([r["replay_s"] for r in depth_rows if r["depth"] == d], 3),
                  "ms/action": C.mean_std([r["per_action_ms"] for r in depth_rows if r["depth"] == d], 0)} for d in xs],
                ["depth", "replay (s)", "ms/action"], "replay time vs history depth (mixed actions)")
        if fit:
            print(f"fit: replay(N) = {fit[0]:.3f} + {fit[1]:.4f} N s (R² = {fit[2]:.4f})")


if __name__ == "__main__":
    main()
