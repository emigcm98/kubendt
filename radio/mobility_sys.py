"""Mobility system for the KubeNDT radio study."""
import random
import sys
from pathlib import Path

import numpy as np

import config
import sumo
import sumolib
from link_rate import LinkConfig, LinkRate
from radio_maps import RadioMaps

LCFG = LinkConfig(bandwidth_hz=20e6, num_prb=51)
INVALID = -1e9


class SumoWorld:
    def __init__(self, n_veh_ue, n_ped_ue, zone, step=0.5, seed=1, background=True, served=None, ue_specs=None):
        import libsumo as traci
        self.traci = traci
        self.step_s = step
        self.rnd = random.Random(seed)
        self.zone = zone
        self._xf, self.rot = zone.sumo_to_scene(), 0.0
        net_file = zone.net_file
        self.net = sumolib.net.readNet(str(net_file))
        self.veh_edges = [e.getID() for e in self.net.getEdges() if e.allows("passenger") and e.getLength() > 25]
        self.ped_edges = [e.getID() for e in self.net.getEdges() if e.allows("pedestrian") and e.getLength() > 15]
        if served is not None:
            ok = lambda e: bool(served(self._edge_mid(e)))
            self.veh_edges = [e for e in self.veh_edges if ok(e)]
            self.ped_edges = [e for e in self.ped_edges if ok(e)]
        routes = zone.routes() if background else []
        cmd = [str(Path(sumo.SUMO_HOME) / "bin" / "sumo"), "-n", str(net_file), "--step-length", str(step),
               "--no-step-log", "--no-warnings", "--time-to-teleport", "120", "--seed", str(seed),
               "--ignore-route-errors", "--collision.action", "none"]
        if routes:
            cmd += ["-r", ",".join(routes)]
        cmd += ["--scale", str(zone.meta.get("traffic_scale", 0.3)),
                "--ignore-junction-blocker", "15", "--max-depart-delay", "20"]
        traci.start(cmd)
        self.ues = []
        for i in range(n_veh_ue):
            self.ues.append(dict(id=f"uev{i}", base=f"uev{i}", gen=0, kind="vehicle", xy=None, yaw=0.0, speed=0.0, edge=None))
        for i in range(n_ped_ue):
            self.ues.append(dict(id=f"uep{i}", base=f"uep{i}", gen=0, kind="pedestrian", xy=None, yaw=0.0, speed=0.0, edge=None))
        if ue_specs is not None:
            for _ in range(int(60 / step)):
                traci.simulationStep()
            self.ues = []
            for i, sp in enumerate(ue_specs):
                kind = "vehicle" if sp["kind"] == "car" else "pedestrian"
                x, y = zone.to_xy(sp["lat"], sp["lon"])
                self.ues.append(dict(id=f"ue{i}", base=f"ue{'v' if kind == 'vehicle' else 'p'}{i}", gen=0, kind=kind,
                                     xy=np.array([float(x), float(y)]), yaw=0.0, speed=0.0, edge=sp.get("edge"),
                                     start_pos=sp.get("pos"), fixed=sp.get("mode") == "fixed"))
                if sp.get("mode") == "route":
                    def point(w):
                        wx, wy = zone.to_xy(w["lat"], w["lon"])
                        return dict(edge=w["edge"], pos=float(w["pos"]), xy=np.array([float(wx), float(wy)]))
                    pts = [point(w) for w in sp["waypoints"]]
                    if sp.get("at_end") == "loop":
                        pts.append(point(sp))
                    self.ues[-1].update(waypoints=pts, wp_i=0, at_end=sp.get("at_end", "stop"), legs_done=0)
        for u in self.ues:
            if not u.get("fixed"):
                self._spawn(u)
        self.respawns = 0
        for _ in range(0 if ue_specs is not None else int(60 / step)):
            traci.simulationStep()
        self.refresh()

    def to_scene(self, xy):
        out = self._xf(xy)
        return out[0] if np.ndim(xy) == 1 else out

    def _edge_mid(self, edge):
        shp = self.net.getEdge(edge).getShape()
        return self.to_scene(shp[len(shp) // 2])

    def _spawn(self, u):
        tr = self.traci
        for _ in range(50):
            u["gen"] += 1
            u["id"] = f"{u['base']}g{u['gen']}"
            try:
                if u["kind"] == "vehicle":
                    edge = u["edge"] or self.rnd.choice(self.veh_edges)
                    rid = f"r_{u['id']}_{self.rnd.randrange(10 ** 9)}"
                    tr.route.add(rid, [edge])
                    start = u.pop("start_pos", None)
                    tr.vehicle.add(u["id"], rid, departLane="best", departSpeed="0",
                                   departPos=("base" if start is None else f"{float(start):.1f}"))
                    self._head(u, edge)
                else:
                    edge = u["edge"] or self.rnd.choice(self.ped_edges)
                    tr.person.add(u["id"], edge, pos=float(u.pop("start_pos", None) or 1.0))
                    if not self._head(u, edge):
                        tr.person.remove(u["id"])
                        u["edge"] = None
                        continue
                u["edge"] = edge
                if u["xy"] is None:
                    u["xy"] = self._edge_mid(edge)
                return
            except tr.TraCIException:
                u["edge"] = None
        self.failed_spawns = getattr(self, "failed_spawns", 0) + 1

    def _head(self, u, from_edge):
        """Send a UE on its way: to its next waypoint if it follows a route, to a random destination otherwise."""
        tr = self.traci
        wps = u.get("waypoints")
        if not wps:
            return self._retarget_vehicle(u, from_edge) if u["kind"] == "vehicle" else self._append_walk(u, from_edge)
        goal = wps[u["wp_i"]]
        if u["kind"] == "vehicle":
            try:
                tr.vehicle.changeTarget(u["id"], goal["edge"])
                return True
            except tr.TraCIException:
                return False
        edges = [from_edge]
        if from_edge != goal["edge"]:
            try:
                stages = tr.simulation.findIntermodalRoute(from_edge, goal["edge"], pType="DEFAULT_PEDTYPE")
            except tr.TraCIException:
                return False
            edges = next((list(st.edges) for st in stages if len(st.edges) >= 1), None)
            if not edges:
                return False
        tr.person.appendWalkingStage(u["id"], edges, arrivalPos=float(goal["pos"]))
        u["walk_end"] = edges[-1]
        return True

    def _follow(self, u, lane_pos):
        """A UE on a route reached its waypoint: on to the next one. After the last it stays there, or, on a."""
        tr = self.traci
        goal = u["waypoints"][u["wp_i"]]
        near = float(np.hypot(*(np.asarray(u["xy"]) - goal["xy"])))
        if u["kind"] == "vehicle":
            arrived = near <= 12.0 or (u.get("edge") == goal["edge"] and lane_pos >= goal["pos"] - 3.0)
        else:
            arrived = near <= 4.0
        if not arrived:
            return
        u["legs_done"] += 1
        u["wp_i"] += 1
        if u["wp_i"] >= len(u["waypoints"]):
            if u["at_end"] == "loop":
                u["wp_i"] = 0
            else:
                try:
                    (tr.vehicle.remove if u["kind"] == "vehicle" else tr.person.remove)(u["id"])
                except tr.TraCIException:
                    pass
                u.update(fixed=True, done=True, speed=0.0, xy=goal["xy"].copy(), wp_i=len(u["waypoints"]) - 1)
                return
        self._head(u, goal["edge"])

    def _retarget_vehicle(self, u, here=None):
        tr = self.traci
        for _ in range(30):
            try:
                tr.vehicle.changeTarget(u["id"], self.rnd.choice(self.veh_edges))
                return True
            except tr.TraCIException:
                continue
        return False

    def _append_walk(self, u, from_edge):
        tr = self.traci
        for _ in range(30):
            to = self.rnd.choice(self.ped_edges)
            if to == from_edge:
                continue
            try:
                stages = tr.simulation.findIntermodalRoute(from_edge, to, pType="DEFAULT_PEDTYPE")
            except tr.TraCIException:
                continue
            for st in stages:
                if len(st.edges) >= 2:
                    tr.person.appendWalkingStage(u["id"], list(st.edges), arrivalPos=-1)
                    u["walk_end"] = st.edges[-1]
                    return True
        return False

    def step(self):
        self.traci.simulationStep()
        self.refresh()

    def refresh(self):
        tr = self.traci
        vehicles = set(tr.vehicle.getIDList())
        persons = set(tr.person.getIDList())
        for u in self.ues:
            if u.get("fixed"):
                continue
            try:
                if u["kind"] == "vehicle":
                    if u["id"] not in vehicles:
                        if u["id"] not in tr.simulation.getPendingVehicles():
                            self.respawns += 1
                            self._spawn(u)
                        continue
                    x, y = tr.vehicle.getPosition(u["id"])
                    if x < INVALID:
                        continue
                    u["xy"] = self.to_scene((x, y))
                    u["yaw"] = np.deg2rad(90.0 - tr.vehicle.getAngle(u["id"])) + self.rot
                    u["speed"] = tr.vehicle.getSpeed(u["id"])
                    edge = tr.vehicle.getRoadID(u["id"])
                    if edge and not edge.startswith(":"):
                        u["edge"] = edge
                    route = tr.vehicle.getRoute(u["id"])
                    if u.get("waypoints"):
                        self._follow(u, tr.vehicle.getLanePosition(u["id"]))
                    elif tr.vehicle.getRouteIndex(u["id"]) >= len(route) - 1:
                        self._retarget_vehicle(u)
                else:
                    if u["id"] not in persons:
                        self.respawns += 1
                        u["edge"] = u.get("walk_end") or u["edge"]
                        self._spawn(u)
                        continue
                    x, y = tr.person.getPosition(u["id"])
                    if x < INVALID:
                        continue
                    u["xy"] = self.to_scene((x, y))
                    u["yaw"] = np.deg2rad(90.0 - tr.person.getAngle(u["id"])) + self.rot
                    u["speed"] = tr.person.getSpeed(u["id"])
                    if u.get("waypoints"):
                        self._follow(u, 0.0)
                    elif tr.person.getRemainingStages(u["id"]) <= 1:
                        self._append_walk(u, u.get("walk_end") or tr.person.getRoadID(u["id"]))
            except tr.TraCIException:
                continue

    def ue_xy(self):
        return np.array([u["xy"] for u in self.ues])

    def vehicles(self):
        """Every vehicle on the road: [x, y, yaw, speed] in scene coordinates, and whether it carries a UE."""
        tr = self.traci
        out, is_ue = [], []
        for v in tr.vehicle.getIDList():
            x, y = tr.vehicle.getPosition(v)
            if x < INVALID:
                continue
            p = self.to_scene((x, y))
            out.append((p[0], p[1], np.deg2rad(90.0 - tr.vehicle.getAngle(v)) + self.rot, tr.vehicle.getSpeed(v)))
            is_ue.append(v.startswith("uev"))
        return np.array(out).reshape(-1, 4), np.array(is_ue, bool)

    def counts(self):
        tr = self.traci
        return dict(vehicles=tr.vehicle.getIDCount(), persons=tr.person.getIDCount(), time_s=tr.simulation.getTime())

    def roads(self, maps):
        """Road centre lines inside the scene, for the viewer."""
        out = []
        for e in self.net.getEdges():
            if e.allows("passenger"):
                out.append([[round(float(a), 1), round(float(b), 1)] for a, b in self.to_scene(e.getShape())])
        return out

    def close(self):
        self.traci.close()


class LiveRadio:
    VEH_ANT_Z = 1.9
    PED_Z = 1.5

    def __init__(self, world, zone, car_pool=60, depth=5, diffraction=True, fused=False):
        import mitsuba as mi
        import sionna.rt as rt
        self.mi, self.rt = mi, rt
        self.world = world
        self.zone = zone
        self.maps = RadioMaps.load(zone.dir / "radio.npz")
        scene_src, freq = zone.scene_file, zone.meta.get("frequency_hz", 3.5e9)
        tx = [(sec["pos"], sec["yaw"], sec["tilt"], sec["power_dbm"]) for sec in zone.sectors()]
        self.tx_power = np.array([t[3] for t in tx], float)
        self.link = LinkRate(LCFG, cache=config.HOME / "cache" / "link_curves.npz")
        scene = rt.load_scene(scene_src)
        scene.frequency = freq
        scene.tx_array = rt.PlanarArray(num_rows=1, num_cols=1, pattern="tr38901", polarization="V")
        scene.rx_array = rt.PlanarArray(num_rows=1, num_cols=1, pattern="iso", polarization="V")
        for i, (pos, yaw, tilt, power) in enumerate(tx):
            scene.add(rt.Transmitter(f"s{i}", position=[float(v) for v in pos],
                                     orientation=[float(yaw), float(tilt), 0.0], power_dbm=float(power)))
        self.rx = []
        for j, u in enumerate(world.ues):
            r = rt.Receiver(f"ue{j}", position=[0.0, 0.0, 1.5])
            scene.add(r)
            self.rx.append(r)
        self.cars = []
        if car_pool:
            mat = rt.ITURadioMaterial("car-metal", "metal", thickness=0.01, color=(0.8, 0.1, 0.1))
            self.cars = [rt.SceneObject(fname=rt.scene.low_poly_car, name=f"car{i}", radio_material=mat)
                         for i in range(car_pool)]
            scene.edit(add=self.cars)
            bb = self.cars[0].mi_mesh.bbox()
            self.car_dims = [float(bb.max.x - bb.min.x), float(bb.max.y - bb.min.y), float(bb.max.z - bb.min.z)]
            self.car_z = float(self.cars[0].position.z[0]) - float(bb.min.z)
        self.scene, self.solver = scene, rt.PathSolver()
        self.kw = dict(max_depth=depth, los=True, specular_reflection=True, diffuse_reflection=False,
                       refraction=False, diffraction=diffraction, synthetic_array=True, seed=1)
        self.cars_used, self.car_updates, self.pose = 0, 0, {}
        self.fused, self._nocars, self.last_delta = fused, False, None
        if fused:
            scene0 = rt.load_scene(scene_src)
            scene0.frequency = freq
            scene0.tx_array, scene0.rx_array = scene.tx_array, scene.rx_array
            for i, (pos, yaw, tilt, power) in enumerate(tx):
                scene0.add(rt.Transmitter(f"s{i}", position=[float(v) for v in pos],
                                          orientation=[float(yaw), float(tilt), 0.0], power_dbm=float(power)))
            self.rx0 = []
            for j, u in enumerate(world.ues):
                r = rt.Receiver(f"ue{j}", position=[0.0, 0.0, 1.5])
                scene0.add(r)
                self.rx0.append(r)
            self.scene0 = scene0

    def place(self, with_cars=True):
        mi = self.mi
        if self.fused:
            self._nocars = not with_cars
            if not with_cars:
                return
        xy = self.world.ue_xy()
        for j, (r, u, p) in enumerate(zip(self.rx, self.world.ues, xy)):
            z = self.VEH_ANT_Z if u["kind"] == "vehicle" else self.PED_Z
            r.position = mi.Point3f(float(p[0]), float(p[1]), z)
            if self.fused:
                self.rx0[j].position = mi.Point3f(float(p[0]), float(p[1]), z)
        if not self.cars:
            return
        veh, _ = self.world.vehicles()
        if with_cars and len(veh) > len(self.cars):
            d = np.min(np.hypot(veh[:, None, 0] - xy[None, :, 0], veh[:, None, 1] - xy[None, :, 1]), axis=1)
            veh = veh[np.argsort(d)[:len(self.cars)]]
        self.cars_used = len(veh) if with_cars else 0
        self.car_updates = 0
        for i, c in enumerate(self.cars):
            if with_cars and i < len(veh):
                x, y, yaw, speed = veh[i]
                pose = (float(x), float(y), float(yaw))
            else:
                pose = None
            last = self.pose.get(i, "new")
            if pose is None:
                if last is not None:
                    c.position = mi.Point3f(0.0, 0.0, -500.0 - 10.0 * i)
                    self.pose[i] = None
                    self.car_updates += 1
                continue
            if last in (None, "new") or abs(pose[2] - last[2]) > 0.035:
                c.orientation = mi.Point3f(pose[2], 0.0, 0.0)
                yaw_set = pose[2]
                self.car_updates += 1
            else:
                yaw_set = last[2]
            if last in (None, "new") or np.hypot(pose[0] - last[0], pose[1] - last[1]) > 0.2:
                c.position = mi.Point3f(pose[0], pose[1], self.car_z)
                self.car_updates += 1
                self.pose[i] = (pose[0], pose[1], yaw_set)
            else:
                self.pose[i] = (last[0], last[1], yaw_set)

    def _trace(self, scene):
        paths = self.solver(scene, **self.kw)
        a, _ = paths.cir(normalize_delays=False, out_type="numpy")
        g = (np.abs(np.asarray(a)) ** 2).sum(axis=(1, 3, 4, 5))
        return 10 * np.log10(np.maximum(g, 1e-30))

    def gain_db(self):
        if not self.fused:
            return self._trace(self.scene)
        if self._nocars:
            return self._fused - self.last_delta
        g1, g0 = self._trace(self.scene), self._trace(self.scene0)
        both, lost = (g1 > -200) & (g0 > -200), (g0 > -200) & (g1 <= -200)
        self.last_delta = np.clip(np.where(both, g1 - g0, np.where(lost, -40.0, 0.0)), -40.0, 20.0)
        self._fused = self.map_gain_db() + self.last_delta
        return self._fused

    def map_gain_db(self):
        """Map gain at each UE. A UE that SUMO puts on a cell the map holds as built (a road hugging a."""
        xy = self.world.ue_xy()
        g = self.maps.gain_db(self.maps.cell_of(xy))
        bad = g.max(1) < -150
        if bad.any():
            cell = float(self.maps.x[1] - self.maps.x[0])
            for dx in (-cell, 0.0, cell):
                for dy in (-cell, 0.0, cell):
                    alt = self.maps.gain_db(self.maps.cell_of(xy[bad] + np.array([dx, dy])))
                    g[bad] = np.maximum(g[bad], alt)
        return g

    def link_state(self, gain_db, serving):
        """RSRP [dBm] per sector, SINR of the serving sector, and the rates it gives [Mbit/s]."""
        c = self.link.config
        n = gain_db.shape[0]
        rsrp = self.tx_power[None, :] + gain_db
        rx_mw = 10 ** (rsrp / 10)
        sig = rx_mw[np.arange(n), serving]
        interf = c.interference_activity * (rx_mw.sum(1) - sig)
        sinr = 10 * np.log10(np.maximum(sig, 1e-30) / (interf + 10 ** (c.noise_dbm(c.ue_noise_figure_db) / 10)))
        users = np.bincount(serving, minlength=gain_db.shape[1])[serving]
        dl = self.link.downlink_capacity_mbps(sinr) / users
        snr_ul = c.ue_power_dbm + gain_db[np.arange(n), serving] - c.noise_dbm(c.bs_noise_figure_db) \
            - c.uplink_interference_margin_db
        full_ul = c.uplink_layers * c.resource_elements_per_s * (1 - c.downlink_fraction) / 1e6
        ul = full_ul * np.interp(snr_ul, self.link.grid, self.link.uplink, left=0.0) / users
        return rsrp, sinr, dl, ul


class A3Manager:
    """Event A3 (TS 38.331): neighbour better than serving by offset + hysteresis for time-to-trigger,."""

    def __init__(self, n, sectors, sector_site, dt, hysteresis_db=3.0, offset_db=0.0, ttt_s=0.48,
                 filter_k=4, qout_db=-8.0, qin_db=-6.0, t310_s=1.0, reestablish_s=1.0):
        self.n, self.dt = n, dt
        self.hys, self.off, self.ttt = hysteresis_db, offset_db, ttt_s
        self.alpha = 0.5 ** (filter_k / 4.0)
        self.qout, self.qin, self.t310, self.reest = qout_db, qin_db, t310_s, reestablish_s
        self.site = np.asarray(sector_site)
        self.filt = None
        self.serving = np.zeros(n, int)
        self.cand = np.full(n, -1)
        self.timer = np.zeros(n)
        self.bad = np.zeros(n)
        self.outage = np.zeros(n)
        self.prev = np.full(n, -1)
        self.since = np.full(n, 1e9)
        self.stats = dict(handovers=0, inter_site=0, intra_site=0, ping_pong=0, rlf=0, outage_steps=0)

    def start(self, rsrp):
        self.filt = rsrp.copy()
        self.serving = rsrp.argmax(1)

    def step(self, rsrp, sinr_of):
        """rsrp [n, sectors] in dBm; sinr_of(serving) -> SINR per UE. Returns (events, in_outage)."""
        rsrp = np.maximum(rsrp, -160.0)
        self.filt = (1 - self.alpha) * self.filt + self.alpha * rsrp
        idx = np.arange(self.n)
        events = []
        self.since += self.dt
        masked = self.filt.copy()
        masked[idx, self.serving] = -np.inf
        best = masked.argmax(1)
        entering = masked[idx, best] > self.filt[idx, self.serving] + self.off + self.hys
        same = entering & (best == self.cand)
        self.timer = np.where(same, self.timer + self.dt, np.where(entering, self.dt, 0.0))
        self.cand = np.where(entering, best, -1)
        for i in np.flatnonzero(entering & (self.timer >= self.ttt - 1e-9) & (self.outage <= 0)):
            self._switch(i, int(best[i]), events, "a3")
        sinr = sinr_of(self.serving)
        self.bad = np.where(sinr < self.qout, self.bad + self.dt, np.where(sinr > self.qin, 0.0, self.bad))
        for i in np.flatnonzero((self.bad >= self.t310 - 1e-9) & (self.outage <= 0)):
            self.outage[i] = self.reest
            self.bad[i] = 0.0
            self.stats["rlf"] += 1
            tgt = int(self.filt[i].argmax())
            if tgt != self.serving[i]:
                self._switch(i, tgt, events, "rlf")
            else:
                events.append(dict(ue=int(i), kind="rlf", frm=int(self.serving[i]), to=tgt))
        in_outage = self.outage > 0
        self.stats["outage_steps"] += int(in_outage.sum())
        self.outage = np.maximum(self.outage - self.dt, 0.0)
        return events, in_outage

    def _switch(self, i, to, events, kind):
        frm = int(self.serving[i])
        inter = bool(self.site[frm] != self.site[to])
        if kind == "a3":
            self.stats["handovers"] += 1
            self.stats["inter_site" if inter else "intra_site"] += 1
            if to == self.prev[i] and self.since[i] <= 5.0:
                self.stats["ping_pong"] += 1
        self.prev[i], self.since[i] = frm, 0.0
        self.serving[i] = to
        self.timer[i], self.cand[i] = 0.0, -1
        events.append(dict(ue=int(i), kind=kind, frm=frm, to=to, inter_site=inter))
