#!/usr/bin/env python3
"""Deployment of VM-based nodes next to container-based ones.

Deploys N VyOS routers (QEMU) and, for comparison, N FRR routers (container), each
--runs times, and splits the deploy with the timeline: created, scheduled, sandbox
and CNI, container started, Ready. For a VM the last step is the guest boot plus its
HTTP API coming up, because that is what the VyOS driver's readiness probe checks;
for the FRR container it is the daemons answering on their vty sockets. Both are OSPF
routers, so the comparison is the same network function in two packagings, not the
same image. Also records the memory and CPU each node type shows once Ready."""
import time

import common as C
import gen_topology as G


def topology(n, image, driver, node_type="router", command=None, extra=None):
    node = {"name": "r", "image": image, "type": node_type, "driver": driver, "replicas": n}
    if command:
        node["commands"] = command
    node.update(extra or {})
    # A line r-0 .. r-(n-1) plus a host on r-0, so every router has at least one link
    # and the CNI work per pod is comparable across sizes.
    links = [{"node": f"r-{i}", "localIntf": "eth1" if i == 0 else "eth2", "localIp": f"10.{50 + i}.0.1/24",
              "peerNode": f"r-{i + 1}", "peerIntf": "eth1", "peerIp": f"10.{50 + i}.0.2/24"} for i in range(n - 1)]
    links.append({"node": "r-0", "localIntf": "eth3" if n > 1 else "eth1", "localIp": "10.49.0.1/24",
                  "peerNode": "h-0", "peerIntf": "eth1", "peerIp": "10.49.0.2/24"})
    return {"nodes": [node, G.host_node("h")], "links": links}


def main():
    ap = C.base_parser(__doc__)
    ap.add_argument("--sizes", type=int, nargs="+", default=[1, 2, 4])
    ap.add_argument("--vyos-image", default="localhost/vyos-router:dev")
    ap.add_argument("--frr-image", default=C.FRR_IMAGE)
    ap.add_argument("--node-selector", default=None, help="label=value the VM nodes must land on, e.g. kubendt/kvm=true")
    ap.add_argument("--skip-containers", action="store_true")
    ap.add_argument("--namespace", default="exp-vm")
    args = ap.parse_args()
    if args.runs == 10:
        args.runs = 5
    client = C.client_from(args)
    rec = C.Recorder("vm_scaling", client, args.out, args)
    ns = args.namespace
    extra = {}
    if args.node_selector:
        k, v = args.node_selector.split("=", 1)
        extra["nodeSelector"] = {k: v}
    cases = [("vyos", args.vyos_image, "VyOSRouterDriver", None, extra)]
    if not args.skip_containers:
        cases.append(("frr", args.frr_image, "FRRRouterDriver", C.FRR_COMMAND, {}))

    for kind, image, driver, command, ex in cases:
        for n in args.sizes:
            topo = topology(n, image, driver, command=command, extra=ex)
            for run in range(1, args.runs + 1):
                client.fresh_namespace(ns)
                st, r, wall = client.deploy(ns, topo)
                if st != 200:
                    rec.note(f"[{kind} {n} run {run}] deploy failed: {st} {str(r)[:300]}")
                    rec.add(kind=kind, nodes=n, run=run, ok=False)
                    client.clear_topology(ns)
                    continue
                tl = r.get("timeline") or {}
                bm = tl.get("backend_ms") or {}
                phases = [C.pod_phases(p) for p in tl.get("pods") or []]
                ts = C.timeline_summary(tl)
                time.sleep(20)  # let metrics-server catch up before reading the footprint
                st2, m = client.ns_metrics(ns)
                pods_m = (m.get("pods") or []) if st2 == 200 and isinstance(m, dict) else []
                row = rec.add(kind=kind, nodes=n, run=run, ok=True, wall_s=round(wall, 3), took_total_s=C.seconds((r.get("took_time") or {}).get("total")),
                              resource_creation_ms=bm.get("resource_creation"), wait_ready_ms=bm.get("wait_ready"), heal_ms=bm.get("heal"),
                              created_spread_s=ts.get("created_spread_s"), ready_spread_s=ts.get("ready_spread_s"), critical_pod=ts.get("critical_pod"),
                              scheduling_s_max=max((p.get("scheduling_s") or 0) for p in phases) if phases else None,
                              sandbox_cni_s_max=max((p.get("sandbox_cni_s") or 0) for p in phases) if phases else None,
                              scheduled_to_started_s_max=max((p.get("scheduled_to_started_s") or 0) for p in phases) if phases else None,
                              readiness_s_max=max((p.get("readiness_s") or 0) for p in phases) if phases else None,
                              readiness_s_mean=round(sum((p.get("readiness_s") or 0) for p in phases) / len(phases), 2) if phases else None,
                              cpu_m_total=sum(p.get("cpu_milli", 0) for p in pods_m) if pods_m else None,
                              mem_mib_total=round(sum(p.get("memory_bytes", 0) for p in pods_m) / 1024 / 1024, 1) if pods_m else None,
                              mem_mib_per_node=round(sum(p.get("memory_bytes", 0) for p in pods_m) / 1024 / 1024 / n, 1) if pods_m else None)
                rec.save(f"timeline_{kind}_{n}_{run}.json", {"response": r, "phases": phases})
                C.log(f"[{kind} x{n} run {run}] deploy {row['wall_s']} s: sandbox/CNI max {row['sandbox_cni_s_max']} s, "
                      f"start max {row['scheduled_to_started_s_max']} s, readiness (boot) mean {row['readiness_s_mean']} max {row['readiness_s_max']} s, "
                      f"footprint {row['cpu_m_total']} m / {row['mem_mib_total']} MiB")
                client.clear_topology(ns)
    if not args.keep:
        client.drop_namespace(ns)

    keys = ("wall_s", "resource_creation_ms", "wait_ready_ms", "sandbox_cni_s_max", "scheduled_to_started_s_max", "readiness_s_mean", "readiness_s_max",
            "created_spread_s", "cpu_m_total", "mem_mib_total", "mem_mib_per_node")
    summary = {f"{k} x{n}": {key: C.summarize([r.get(key) for r in rec.rows if r.get("kind") == k and r.get("nodes") == n and r.get("ok")]) for key in keys}
               for k, _, _, _, _ in cases for n in args.sizes}
    rec.finish(summary)
    C.table([{"case": f"{k} x{n}",
              "deploy (s)": C.mean_std([r["wall_s"] for r in rec.rows if r.get("kind") == k and r.get("nodes") == n and r.get("ok")]),
              "sandbox+CNI max (s)": C.mean_std([r["sandbox_cni_s_max"] for r in rec.rows if r.get("kind") == k and r.get("nodes") == n and r.get("ok")], 1),
              "readiness mean (s)": C.mean_std([r["readiness_s_mean"] for r in rec.rows if r.get("kind") == k and r.get("nodes") == n and r.get("ok")], 1),
              "mem/node (MiB)": C.mean_std([r["mem_mib_per_node"] for r in rec.rows if r.get("kind") == k and r.get("nodes") == n and r.get("ok")], 0),
              "cpu total (m)": C.mean_std([r["cpu_m_total"] for r in rec.rows if r.get("kind") == k and r.get("nodes") == n and r.get("ok")], 0)}
             for k, _, _, _, _ in cases for n in args.sizes],
            ["case", "deploy (s)", "sandbox+CNI max (s)", "readiness mean (s)", "mem/node (MiB)", "cpu total (m)"], "VM (VyOS) vs container (FRR) routers")


if __name__ == "__main__":
    main()
