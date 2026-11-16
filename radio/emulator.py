"""Emulator: a radio zone as a KubeNDT namespace, and the loop that drives its radio links."""
import json
import os
import signal
import sys
import threading
import time
import traceback
from pathlib import Path

import numpy as np

sys.path.insert(0, str(Path(__file__).parent))
import config
import g5_stack as G
import zone as Z

C = G.C
DN_CIDR, DN_GW, SERVER_IP = "10.45.0.0/24", "10.45.0.1", "10.45.0.10"
OPT = {"persist_history": False}
TOOLBOX = config.TOOLBOX_IMAGE


def client():
    return C.Client(config.KUBENDT_URL, token=config.token())


def topology(z):
    sites, ues = z.meta["sites"], z.meta["ues"]
    topo, sw_ports = G.topology(len(sites), len(ues))
    topo["nodes"] += [dict(name="dn", image="alpine:3.24.2", type="switch", replicas=1),
                      dict(name="server", image=TOOLBOX, type="host", replicas=1,
                           commands=["sh", "-c", "iperf3 -s -D; sleep infinity"])]
    topo["links"] += [dict(node="upf", localIntf="eth2", localIp=f"{DN_GW}/24", peerNode="dn-0", peerIntf="eth1", peerIp="", name="N6"),
                      dict(node="server", localIntf="eth1", localIp=f"{SERVER_IP}/24", peerNode="dn-0", peerIntf="eth2", peerIp="")]
    setup = [dict(pod="sw-0", actions=[dict(type="setup_bridge", bridge="br0", ifaces=[f"eth{i}" for i in range(1, sw_ports + 1)])]),
             dict(pod="dn-0", actions=[dict(type="setup_bridge", bridge="br0", ifaces=["eth1", "eth2"])]),
             dict(pod="server-0", actions=[dict(type="set_default_route", gateway=DN_GW)]),
             dict(pod="upf-0", actions=[dict(type="custom", command="sysctl -w net.ipv4.ip_forward=1")]),
             dict(pod="udsf-0", actions=[dict(type="custom", command=G.subscriber_cmd(k)) for k in range(len(ues))])]
    return topo, dict(targets=setup)


def deploy(z, ns):
    G.NS = ns
    c = client()
    info = dict(name=ns, status="deploying", step="creating the namespace", error=None, config=Z.config_hash(z.meta),
                gnbs=len(z.meta["sites"]), ues=len(z.meta["ues"]), template="Open5GS + UERANSIM", server=SERVER_IP, data_network=DN_CIDR)
    z.save(namespace=info)

    def step(text, **extra):
        info.update(step=text, **extra)
        z.save(namespace=info)

    if DN_CIDR not in G.UE_INIT:
        G.UE_INIT = G.UE_INIT.replace("while true; do\n    nr-ue",
                                      f"( while true; do ip route replace {DN_CIDR} dev uesimtun0 2>/dev/null; sleep 2; done ) &\n"
                                      "while true; do\n    nr-ue")
    c.fresh_namespace(ns)
    step("uploading the 5G configuration files")
    ok, r = c.import_zip(ns, str(G.build_zip(len(z.meta["sites"]))))
    if not ok:
        raise RuntimeError(f"file import failed: {str(r)[:300]}")
    topo, setup = topology(z)
    step(f"deploying {sum(n.get('replicas', 1) for n in topo['nodes'])} pods and {len(topo['links'])} links")
    st, r, wall = c.deploy(ns, topo)
    if st != 200:
        raise RuntimeError(f"deploy failed: {st} {json.dumps(r)[:400]}")
    step("configuring the core and the subscribers", deploy_s=round(wall, 1), pods=len(C.pods_json(ns)), links=len(topo["links"]))
    st, r, _ = c.configure(ns, setup)
    if st != 200 or r.get("failures"):
        raise RuntimeError("configure failed: " + json.dumps(r)[:500])
    step("waiting for the UEs to register")
    n, t0, up = len(z.meta["ues"]), time.time(), 0
    while time.time() - t0 < 300:
        up = sum(1 for k in range(n) if G.ue_status(k)[0])
        step(f"waiting for the UEs to register ({up} of {n})", sessions=up)
        if up == n:
            break
        time.sleep(5)
    info.update(status="ready", step=None, sessions=up, created=time.strftime("%Y-%m-%dT%H:%M:%S"), ready_s=round(time.time() - t0 + wall, 1))
    z.save(namespace=info)


def destroy(z):
    ns = (z.meta.get("namespace") or {}).get("name")
    if ns:
        client().drop_namespace(ns)
    z.save(namespace=None)


def radio_actions(k, s, dl, ul, cut):
    def tc(rate):
        t = dict(qdisc="netem", in_place=True, delay="4ms", loss="100%" if cut else "0%")
        if not cut:
            t["rate"] = f"{max(int(rate * 1000), 50)}kbit"
        return t
    return [(f"gnb-{s}", dict(type="add_qdisc", iface=f"eth{k + 2}", tcparams=tc(dl), options=OPT)),
            (f"ue-{k}", dict(type="add_qdisc", iface=f"eth{s + 1}", tcparams=tc(ul), options=OPT))]


def send(c, ns, pairs):
    by = {}
    for pod, act in pairs:
        by.setdefault(pod, []).append(act)
    try:
        st, r, wall = c.configure(ns, dict(targets=[dict(pod=p, actions=a) for p, a in by.items()]))
    except SystemExit as e:
        return False, str(e)
    ok = st == 200 and isinstance(r, dict) and not r.get("failures")
    return ok, (None if ok else json.dumps(r)[:300])


def planned_route(world, u, limit=70):
    """Where a mobile UE is heading: the rest of its SUMO route (a car) or walk (a person), as [lat, lon]."""
    if u.get("fixed"):
        return []
    tr, net = world.traci, world.net
    try:
        if u["kind"] == "vehicle":
            here = tr.vehicle.getPosition(u["id"])
            edges = list(tr.vehicle.getRoute(u["id"]))[max(tr.vehicle.getRouteIndex(u["id"]), 0):]
        else:
            here = tr.person.getPosition(u["id"])
            edges = list(tr.person.getEdges(u["id"]))
            road = tr.person.getRoadID(u["id"])
            if road in edges:
                edges = edges[edges.index(road):]
    except Exception:
        return []
    if here[0] < -1e8:
        return []
    pts = [tuple(here)]
    first = True
    for e in edges[:60]:
        if e.startswith(":"):
            continue
        try:
            shp = [tuple(q) for q in net.getEdge(e).getShape()]
        except KeyError:
            continue
        d = lambda a, b: (a[0] - b[0]) ** 2 + (a[1] - b[1]) ** 2
        if d(pts[-1], shp[-1]) < d(pts[-1], shp[0]):
            shp.reverse()
        if first:
            i = min(range(len(shp)), key=lambda k: d(shp[k], here))
            shp = shp[i + 1:]
            first = False
        pts += shp
    if len(pts) > limit:
        idx = sorted(set([int(round(i * (len(pts) - 1) / (limit - 1))) for i in range(limit)]))
        pts = [pts[i] for i in idx]
    out = []
    for x, y in pts:
        lon, lat = net.convertXY2LonLat(x, y)
        out.append([round(lat, 6), round(lon, 6)])
    return out


def run(z):
    from mobility_sys import A3Manager, LiveRadio, RadioMaps, SumoWorld
    ns = z.meta["namespace"]["name"]
    G.NS = ns
    c = client()
    specs, sites = Z.clean_ues(z, z.meta["ues"]), z.meta["sites"]
    n, S = len(specs), len(sites)
    p = dict(dt=0.5, cars=20, hysteresis_db=3.0, ttt_s=0.96, filter_k=8)
    p.update(z.meta.get("emulation_params") or {})
    status_file = z.dir / "emulation.json"
    started = time.strftime("%Y-%m-%dT%H:%M:%S")

    def write(**state):
        tmp = z.dir / "emulation.json.tmp"
        tmp.write_text(json.dumps(dict(pid=os.getpid(), namespace=ns, started=started, **state)))
        os.replace(tmp, status_file)

    write(state="starting", step="loading the scene and the traffic")
    stop = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stop.set())
    signal.signal(signal.SIGINT, lambda *_: stop.set())

    maps = RadioMaps.load(z.dir / "radio.npz")
    world = SumoWorld(0, 0, z, step=p["dt"], seed=1, ue_specs=specs)
    radio = LiveRadio(world, z, car_pool=int(p["cars"]), depth=3, fused=True)
    radio.place(True)
    g = radio.gain_db()
    a3 = A3Manager(n, maps.num_sectors, maps.sector_site, p["dt"], hysteresis_db=p["hysteresis_db"], ttt_s=p["ttt_s"], filter_k=p["filter_k"])
    a3.start(radio.link_state(g, g.argmax(1))[0])
    site_of = np.asarray(maps.sector_site)
    idx_in_site = []
    seen = {}
    for s in site_of:
        idx_in_site.append(seen.get(int(s), 0))
        seen[int(s)] = idx_in_site[-1] + 1

    session, reach = [None] * n, [None] * n
    misses, restarted_at, restarts = [0] * n, [0.0] * n, [0] * n
    radio_ok = [False] * n
    changed_at = [0.0] * n

    def prober():
        while not stop.is_set():
            for k in range(n):
                if stop.is_set():
                    return
                rc, out = C.kexec(ns, f"ue-{k}", f"ip -4 -o addr show uesimtun0 2>/dev/null | wc -l; ping -c 2 -i 0.2 -W 1 -q -I uesimtun0 {SERVER_IP} 2>&1 | tail -2")
                lines = out.splitlines() if out else []
                session[k] = bool(lines and lines[0].strip() == "1")
                reach[k] = any(" 0% packet loss" in l or " 50% packet loss" in l for l in lines)
                now = time.time()
                steady = radio_ok[k] and now - changed_at[k] > 20 and now - restarted_at[k] > 45
                misses[k] = 0 if reach[k] or not steady else misses[k] + 1
                if misses[k] >= 2:
                    C.kexec(ns, f"ue-{k}", "pkill -x nr-ue || kill $(pidof nr-ue)")
                    misses[k], restarted_at[k] = 0, now
                    restarts[k] += 1
                stop.wait(1.0)

    threading.Thread(target=prober, daemon=True).start()

    hist, recent = [], []
    trail = [[] for _ in range(n)]
    route = [[] for _ in range(n)]
    changes = np.zeros(n, int)
    api_fail, step, t_start = 0, 0, time.time()
    gone = 0
    while not stop.is_set():
        w0 = time.perf_counter()
        world.step()
        w1 = time.perf_counter()
        radio.place(True)
        w2 = time.perf_counter()
        g = radio.gain_db()
        w3 = time.perf_counter()
        rsrp = radio.link_state(g, a3.serving)[0]
        events, outage = a3.step(rsrp, lambda srv: radio.link_state(g, srv)[1])
        _, sinr, dl, ul = radio.link_state(g, a3.serving)
        sim_t = world.counts()["time_s"]
        for k in range(n):
            radio_ok[k] = (not outage[k]) and dl[k] > 0.5 and ul[k] > 0.2
        for e in events:
            if e.get("inter_site"):
                changes[e["ue"]] += 1
                changed_at[e["ue"]] = time.time()
            recent.append(dict(t=round(sim_t, 1), ue=f"ue-{e['ue']}", kind=e["kind"], frm=f"gnb-{int(site_of[e['frm']])}.{idx_in_site[e['frm']]}",
                               to=f"gnb-{int(site_of[e['to']])}.{idx_in_site[e['to']]}", inter_gnb=bool(e.get("inter_site"))))
        recent = recent[-12:]
        pairs = []
        for k in range(n):
            serving = int(site_of[a3.serving[k]])
            for s in range(S):
                fade = bool(outage[k])
                pairs += radio_actions(k, s, 0.0 if fade else float(dl[k]), 0.0 if fade else float(ul[k]), cut=(s != serving))
        w4 = time.perf_counter()
        ok, err = send(c, ns, pairs)
        w5 = time.perf_counter()
        api_fail += 0 if ok else 1
        gone = gone + 1 if (err and "not found in namespace" in err) else 0
        if gone >= 10:
            raise RuntimeError(f"the namespace {ns} no longer exists: it was deleted outside Radio zones. Create it again.")
        hist.append((w1 - w0, w2 - w1, w3 - w2, w5 - w4, w5 - w0))
        hist = hist[-240:]
        med = 1e3 * np.median(np.array(hist), axis=0)
        cnt = world.counts()
        lat, lon = z.to_ll(world.ue_xy()[:, 0], world.ue_xy()[:, 1])
        if step % max(1, int(round(2.0 / p["dt"]))) == 0:
            for k, u in enumerate(world.ues):
                pt = [round(float(lat[k]), 6), round(float(lon[k]), 6)]
                if not trail[k] or trail[k][-1] != pt:
                    trail[k] = (trail[k] + [pt])[-300:]
                route[k] = planned_route(world, u)
        write(state="running", t=round(sim_t, 1), step=step, dt=p["dt"], wall_s=round(time.time() - t_start, 1),
              realtime_factor=round(min(1.0, p["dt"] * 1e3 / max(med[4], 1.0)), 2),
              step_ms=dict(sumo=round(med[0], 1), objects=round(med[1], 1), ray_tracing=round(med[2], 1), kubendt_api=round(med[3], 1), total=round(med[4], 1)),
              traffic=dict(vehicles=cnt["vehicles"], persons=cnt["persons"], car_objects=radio.cars_used),
              a3=dict(a3.stats, hysteresis_db=a3.hys, ttt_s=a3.ttt), api_failures=api_fail, last_api_error=err, events=recent,
              ues=[dict(name=f"ue-{k}", kind=specs[k]["kind"], mode=specs[k]["mode"], lat=float(lat[k]), lon=float(lon[k]),
                        speed=round(float(u["speed"]), 1), gnb=int(site_of[a3.serving[k]]), sector=idx_in_site[a3.serving[k]],
                        sinr=round(float(sinr[k]), 1), dl=round(float(dl[k]), 2), ul=round(float(ul[k]), 2), outage=bool(outage[k]),
                        gnb_changes=int(changes[k]), session=session[k], reaches_server=reach[k], stack_restarts=restarts[k],
                        trail=trail[k], route=route[k], legs_done=u.get("legs_done"), done=bool(u.get("done"))) for k, u in enumerate(world.ues)])
        step += 1
        stop.wait(max(0.0, p["dt"] - (time.perf_counter() - w0)))

    pairs = []
    for k in range(n):
        for s in range(S):
            pairs += [(f"gnb-{s}", dict(type="del_qdisc", iface=f"eth{k + 2}", options=OPT)),
                      (f"ue-{k}", dict(type="del_qdisc", iface=f"eth{s + 1}", options=OPT))]
    send(c, ns, pairs)
    world.close()
    write(state="stopped", steps=step, stopped=time.strftime("%Y-%m-%dT%H:%M:%S"))


if __name__ == "__main__":
    cmd, zone = sys.argv[1], Z.Zone(sys.argv[2])
    try:
        if cmd == "deploy":
            deploy(zone, sys.argv[3])
        elif cmd == "destroy":
            destroy(zone)
        elif cmd == "run":
            run(zone)
    except BaseException as exc:
        msg = f"{type(exc).__name__}: {exc}"
        traceback.print_exc()
        if cmd == "deploy":
            zone.save(namespace=dict(zone.meta.get("namespace") or {}, status="error", step=None, error=msg))
        elif cmd == "run":
            (zone.dir / "emulation.json").write_text(json.dumps(dict(pid=os.getpid(), state="error", error=msg)))
        sys.exit(1)
