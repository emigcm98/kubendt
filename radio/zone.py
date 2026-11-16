"""Zones: any area of the world as a radio emulation scenario."""
import json
import math
import re
import subprocess
import sys
import time
import traceback
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
from pathlib import Path

import numpy as np

import config

ZONES = config.ZONES
OVERPASS_MIRRORS = ("https://overpass-api.de/api/interpreter", "https://overpass.kumi.systems/api/interpreter",
                    "https://overpass.private.coffee/api/interpreter")
UA = "kubendt-radio-zones/1.0"
R_EARTH = 6378137.0
MAX_SIDE_M = 3000.0
CELL_M = 5.0
UE_HEIGHT_M = 1.5
DEFAULT_HEIGHT = {"church": 20, "cathedral": 30, "chapel": 10, "garage": 3, "garages": 3, "shed": 3, "roof": 4, "hut": 3,
                  "house": 7, "detached": 7, "semidetached_house": 7, "terrace": 8, "bungalow": 4, "apartments": 18,
                  "residential": 15, "university": 14, "school": 10, "college": 12, "industrial": 9, "warehouse": 9,
                  "retail": 8, "commercial": 15, "office": 20, "hospital": 18, "hotel": 20, "kiosk": 3, "service": 3}


class Zone:
    def __init__(self, zone_id):
        self.id = zone_id
        self.dir = ZONES / zone_id
        self.meta = json.loads((self.dir / "zone.json").read_text())
        s, w, n, e = self.meta["bbox"]
        self.lat0, self.lon0 = (s + n) / 2, (w + e) / 2
        self.kx = R_EARTH * math.cos(math.radians(self.lat0)) * math.pi / 180.0
        self.ky = R_EARTH * math.pi / 180.0

    def save(self, **changes):
        self.meta.update(changes)
        tmp = self.dir / "zone.json.tmp"
        tmp.write_text(json.dumps(self.meta, indent=1))
        tmp.replace(self.dir / "zone.json")

    def to_xy(self, lat, lon):
        return (np.asarray(lon) - self.lon0) * self.kx, (np.asarray(lat) - self.lat0) * self.ky

    def to_ll(self, x, y):
        return self.lat0 + np.asarray(y) / self.ky, self.lon0 + np.asarray(x) / self.kx

    @property
    def extent(self):
        s, w, n, e = self.meta["bbox"]
        x0, y0 = self.to_xy(s, w)
        x1, y1 = self.to_xy(n, e)
        return float(x0), float(y0), float(x1), float(y1)

    @property
    def scene_file(self):
        return str(self.dir / "scene.xml")

    @property
    def net_file(self):
        return str(self.dir / "net.xml")

    def routes(self):
        return [str(self.dir / f) for f in ("veh.rou.xml", "ped.rou.xml") if (self.dir / f).exists()]

    def sumo_to_scene(self):
        """Function mapping SUMO network coordinates [n, 2] to scene coordinates."""
        import sumolib
        net = sumolib.net.readNet(self.net_file)
        ox, oy = net.getLocationOffset()
        proj = net.getGeoProj()

        def f(xy):
            xy = np.asarray(xy, float).reshape(-1, 2)
            lon, lat = proj(xy[:, 0] - ox, xy[:, 1] - oy, inverse=True)
            x, y = self.to_xy(np.asarray(lat), np.asarray(lon))
            return np.stack([x, y], axis=1)
        return f

    def sectors(self):
        """Per sector: position [x, y, z], yaw [rad, from +x counter-clockwise], downtilt [rad], power, site index."""
        out = []
        for i, s in enumerate(self.meta.get("sites", [])):
            for az in s["azimuths"]:
                out.append(dict(pos=s["xyz"], yaw=math.radians(90.0 - az), tilt=math.radians(s.get("downtilt_deg", 9.0)),
                                power_dbm=s.get("power_dbm", 44.0), site=i, azimuth_deg=az))
        return out


def create(name, bbox):
    s, w, n, e = [float(v) for v in bbox]
    if not (-90 <= s < n <= 90 and -180 <= w < e <= 180):
        raise ValueError("bbox must be [south, west, north, east] in degrees")
    side_y = (n - s) * R_EARTH * math.pi / 180
    side_x = (e - w) * R_EARTH * math.cos(math.radians((s + n) / 2)) * math.pi / 180
    if max(side_x, side_y) > MAX_SIDE_M:
        raise ValueError(f"the area is {side_x:.0f} x {side_y:.0f} m; the limit is {MAX_SIDE_M:.0f} m per side")
    if min(side_x, side_y) < 150:
        raise ValueError("the area is too small (150 m per side at least)")
    zid = re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-")[:40] or "zone"
    base, k = zid, 2
    while (ZONES / zid).exists():
        zid, k = f"{base}-{k}", k + 1
    (ZONES / zid).mkdir(parents=True)
    meta = dict(id=zid, name=name, bbox=[s, w, n, e], size_m=[round(side_x), round(side_y)], sites=[], status="created",
                frequency_hz=3.5e9, created=time.strftime("%Y-%m-%dT%H:%M:%S"))
    (ZONES / zid / "zone.json").write_text(json.dumps(meta, indent=1))
    return zid


def overpass(query, timeout=240):
    """Overpass answers 429/502/504 when busy: retry, then try the public mirrors."""
    data = urllib.parse.urlencode({"data": query}).encode()
    last = None
    for attempt in range(6):
        url = OVERPASS_MIRRORS[attempt % len(OVERPASS_MIRRORS)]
        try:
            req = urllib.request.Request(url, data=data, headers={"User-Agent": UA})
            with urllib.request.urlopen(req, timeout=timeout) as r:
                return r.read()
        except Exception as exc:
            last = exc
            time.sleep(3 + 3 * attempt)
    raise RuntimeError(f"OpenStreetMap (Overpass) does not answer: {last}")


def fetch(z):
    z.save(status="fetching", step="downloading OpenStreetMap", error=None)
    s, w, n, e = z.meta["bbox"]
    raw = overpass(f"[out:xml][timeout:180];(node({s},{w},{n},{e});way({s},{w},{n},{e});relation({s},{w},{n},{e}););out body;>;out skel qt;")
    (z.dir / "zone.osm").write_bytes(raw)
    b = parse_buildings(z)
    roads = sum(1 for _ in re.finditer(rb'<tag k="highway"', raw))
    z.save(status="fetched", step=None, stats=dict(osm_mb=round(len(raw) / 1e6, 1), buildings=len(b), highway_ways=roads,
                                                    buildings_with_height=sum(1 for x in b if x["height_source"] != "default")))


def _height(tags):
    for key in ("height", "building:height"):
        m = re.match(r"\s*([0-9.]+)", tags.get(key, ""))
        if m:
            return max(float(m.group(1)), 2.5), "height"
    m = re.match(r"\s*([0-9.]+)", tags.get("building:levels", ""))
    if m:
        return max(float(m.group(1)) * 3.2 + 1.0, 2.5), "levels"
    kind = tags.get("building", tags.get("building:part", "yes"))
    return float(DEFAULT_HEIGHT.get(kind, 12.0)), "default"


def parse_buildings(z):
    """Building footprints of the extract: [{xy [n, 2], height, name}]."""
    nodes, ways, out, rel_outer = {}, {}, [], []
    for ev, el in ET.iterparse(str(z.dir / "zone.osm"), events=("end",)):
        if el.tag == "node":
            nodes[el.get("id")] = (float(el.get("lat")), float(el.get("lon")))
        elif el.tag == "way":
            ways[el.get("id")] = ([n.get("ref") for n in el.findall("nd")], {t.get("k"): t.get("v") for t in el.findall("tag")})
        elif el.tag == "relation":
            tags = {t.get("k"): t.get("v") for t in el.findall("tag")}
            if "building" in tags:
                for m in el.findall("member"):
                    if m.get("type") == "way" and m.get("role") in ("outer", ""):
                        rel_outer.append((m.get("ref"), tags))
        if el.tag in ("node", "way", "relation"):
            el.clear()
    cand = [(nds, tags) for nds, tags in ways.values() if "building" in tags or "building:part" in tags]
    cand += [(ways[ref][0], tags) for ref, tags in rel_outer if ref in ways]
    for nds, tags in cand:
        if len(nds) < 4 or nds[0] != nds[-1] or tags.get("building") in ("no",) or tags.get("location") == "underground":
            continue
        ll = np.array([nodes[n] for n in nds[:-1] if n in nodes])
        if len(ll) < 3:
            continue
        x, y = z.to_xy(ll[:, 0], ll[:, 1])
        xy = np.stack([x, y], axis=1)
        area = 0.5 * float(np.sum(xy[:, 0] * np.roll(xy[:, 1], -1) - np.roll(xy[:, 0], -1) * xy[:, 1]))
        if abs(area) < 4.0:
            continue
        if area < 0:
            xy = xy[::-1]
        h, src = _height(tags)
        out.append(dict(xy=xy, height=h, height_source=src, name=tags.get("name")))
    return out


def osm_sites(s, w, n, e):
    """Mobile telephony masts and antennas mapped in OpenStreetMap inside the box."""
    b = f"({s},{w},{n},{e})"
    q = ("[out:json][timeout:60];("
         f'nwr["communication:mobile_phone"]{b};'
         f'nwr["man_made"~"^(mast|tower)$"]["tower:type"="communication"]{b};'
         f'nwr["man_made"="antenna"]{b};'
         f'nwr["telecom"~"antenna|mobile"]{b};'
         f'nwr["man_made"="mast"]["mast:type"="communication"]{b};'
         ");out center tags;")
    data = json.loads(overpass(q, timeout=90))
    out = []
    for el in data.get("elements", []):
        lat = el.get("lat", el.get("center", {}).get("lat"))
        lon = el.get("lon", el.get("center", {}).get("lon"))
        if lat is None:
            continue
        t = el.get("tags", {})
        m = re.match(r"\s*([0-9.]+)", t.get("height", ""))
        out.append(dict(lat=lat, lon=lon, name=t.get("name") or t.get("operator") or f"osm-{el['id']}",
                        operator=t.get("operator"), height_m=float(m.group(1)) if m else None,
                        source=f"OpenStreetMap {el['type']}/{el['id']}", tags={k: v for k, v in t.items() if k in (
                            "man_made", "tower:type", "communication:mobile_phone", "operator", "height", "telecom", "location")}))
    return out


def _write_ply(path, vertices, faces):
    v = np.asarray(vertices, "<f4")
    f = np.asarray(faces, "<i4")
    rec = np.empty(len(f), dtype=[("n", "u1"), ("i", "<i4", 3)])
    rec["n"], rec["i"] = 3, f
    with open(path, "wb") as fh:
        fh.write((f"ply\nformat binary_little_endian 1.0\nelement vertex {len(v)}\nproperty float x\nproperty float y\n"
                  f"property float z\nelement face {len(f)}\nproperty list uchar int vertex_index\nend_header\n").encode())
        fh.write(v.tobytes())
        fh.write(rec.tobytes())


def build_scene(z):
    import mapbox_earcut as earcut
    buildings = parse_buildings(z)
    V, F, base = [], [], 0
    for b in buildings:
        xy, h, n = b["xy"], b["height"], len(b["xy"])
        bottom = np.column_stack([xy, np.zeros(n)])
        top = np.column_stack([xy, np.full(n, h)])
        i = np.arange(n)
        j = (i + 1) % n
        walls = np.concatenate([np.column_stack([i, j, j + n]), np.column_stack([i, j + n, i + n])])
        roof = earcut.triangulate_float32(xy.astype(np.float32), np.array([n], np.uint32)).reshape(-1, 3) + n
        V.append(np.concatenate([bottom, top]))
        F.append(np.concatenate([walls, roof]) + base)
        base += 2 * n
    (z.dir / "meshes").mkdir(exist_ok=True)
    if V:
        _write_ply(z.dir / "meshes" / "buildings.ply", np.concatenate(V), np.concatenate(F))
    x0, y0, x1, y1 = z.extent
    m = 150.0
    _write_ply(z.dir / "meshes" / "ground.ply", [[x0 - m, y0 - m, 0], [x1 + m, y0 - m, 0], [x1 + m, y1 + m, 0], [x0 - m, y1 + m, 0]],
               [[0, 1, 2], [0, 2, 3]])
    shapes = ""
    if V:
        shapes += ('\t<shape type="ply" id="mesh-buildings">\n\t\t<string name="filename" value="meshes/buildings.ply"/>\n'
                   '\t\t<boolean name="face_normals" value="true"/>\n\t\t<ref id="concrete" name="bsdf"/>\n\t</shape>\n')
    shapes += ('\t<shape type="ply" id="mesh-ground">\n\t\t<string name="filename" value="meshes/ground.ply"/>\n'
               '\t\t<boolean name="face_normals" value="true"/>\n\t\t<ref id="medium_dry_ground" name="bsdf"/>\n\t</shape>\n')
    (z.dir / "scene.xml").write_text(
        '<scene version="2.1.0">\n'
        '\t<bsdf type="itu-radio-material" id="concrete">\n\t\t<string name="type" value="concrete"/>\n\t\t<float name="thickness" value="0.3"/>\n\t</bsdf>\n'
        '\t<bsdf type="itu-radio-material" id="medium_dry_ground">\n\t\t<string name="type" value="medium_dry_ground"/>\n\t\t<float name="thickness" value="1.0"/>\n\t</bsdf>\n'
        + shapes + '</scene>\n')
    return dict(buildings=len(buildings), triangles=int(sum(len(f) for f in F)) + 2,
                buildings_with_height=sum(1 for b in buildings if b["height_source"] != "default"))


def build_roads(z):
    import sumo
    home = Path(sumo.SUMO_HOME)
    typemaps = home / "data" / "typemap"
    subprocess.run([str(home / "bin" / "netconvert"), "--osm-files", str(z.dir / "zone.osm"), "-o", z.net_file,
                    "--type-files", ",".join(str(typemaps / f) for f in ("osmNetconvert.typ.xml", "osmNetconvertUrbanDe.typ.xml",
                                                                          "osmNetconvertPedestrians.typ.xml")),
                    "--proj.utm", "--geometry.remove", "--roundabouts.guess", "--ramps.guess", "--junctions.join",
                    "--tls.guess-signals", "--tls.discard-simple", "--tls.join", "--tls.default-type", "actuated",
                    "--sidewalks.guess", "--crossings.guess", "--walkingareas", "--remove-edges.isolated",
                    "--keep-edges.by-vclass", "passenger,pedestrian,bus,tram,bicycle", "--no-turnarounds.except-deadend",
                    "--no-warnings"], check=True, capture_output=True)
    import sumolib
    net = sumolib.net.readNet(z.net_file)
    edges = net.getEdges()
    veh_len = sum(e.getLength() for e in edges if e.allows("passenger"))
    veh_period = max(0.5, min(20.0, 150.0 * 120.0 / max(veh_len, 1.0)))
    rt_py = str(home / "tools" / "randomTrips.py")
    common = [sys.executable, rt_py, "-n", z.net_file, "-b", "0", "-e", "3600", "--validate", "--seed", "3"]
    stats = dict(edges=len(edges), junctions=len(net.getNodes()), traffic_lights=len(net.getTrafficLights()),
                 road_km=round(veh_len / 1000, 2))
    for label, extra, out in (("veh", ["-p", f"{veh_period:.2f}", "--vehicle-class", "passenger", "--prefix", "bg", "--fringe-factor", "3",
                                       "--min-distance", "200"], "veh"),
                              ("ped", ["-p", f"{max(0.5, veh_period * 0.7):.2f}", "--pedestrians", "--prefix", "bp", "--min-distance", "100"], "ped")):
        r = subprocess.run(common + extra + ["-o", str(z.dir / f"{out}.trips.xml"), "-r", str(z.dir / f"{out}.rou.xml")],
                           capture_output=True, text=True)
        stats[f"{label}_trips_ok"] = r.returncode == 0 and (z.dir / f"{out}.rou.xml").exists()
    return stats


def build_radio(z):
    import mitsuba as mi
    import sionna.rt as rt
    import mitsuba as mi
    from scipy import ndimage
    from radio_maps import RadioMaps, largest_component

    scene = rt.load_scene(z.scene_file)
    scene.frequency = z.meta.get("frequency_hz", 3.5e9)
    x0, y0, x1, y1 = z.extent
    nx, ny = int(math.ceil((x1 - x0) / CELL_M)), int(math.ceil((y1 - y0) / CELL_M))
    size = np.array([nx * CELL_M, ny * CELL_M])
    centre = np.array([(x0 + x1) / 2, (y0 + y1) / 2])
    x = centre[0] - size[0] / 2 + CELL_M / 2 + CELL_M * np.arange(nx)
    y = centre[1] - size[1] / 2 + CELL_M / 2 + CELL_M * np.arange(ny)
    gx, gy = np.meshgrid(x, y)
    top = 400.0
    origin = mi.Point3f(gx.ravel().astype(np.float32), gy.ravel().astype(np.float32), np.full(gx.size, top, np.float32))
    hit = scene.mi_scene.ray_intersect(mi.Ray3f(origin, mi.Vector3f(0, 0, -1)))
    height = np.where(np.array(hit.is_valid()), top - np.array(hit.t), 0.0).reshape(gx.shape)
    built = height > 3.0
    radius = int(round(60.0 / CELL_M))
    yy, xx = np.ogrid[-radius:radius + 1, -radius:radius + 1]
    near_buildings = ndimage.binary_dilation(built, structure=(yy ** 2 + xx ** 2) <= radius ** 2) if built.any() else np.ones_like(built)
    outdoor = largest_component((height < UE_HEIGHT_M - 0.5) & near_buildings)

    sites = z.meta["sites"]
    if not sites:
        raise ValueError("the zone has no sites")
    for s in sites:
        sx, sy = z.to_xy(s["lat"], s["lon"])
        sx, sy = float(sx), float(sy)
        if s.get("height_m"):
            zc, how = float(s["height_m"]), "given height"
        else:
            near = (np.hypot(gx - sx, gy - sy) <= 30.0) & built & (height <= 80.0)
            if near.any():
                iy, ix = np.unravel_index(np.argmax(np.where(near, height, -1.0)), height.shape)
                sx, sy, zc, how = float(x[ix]), float(y[iy]), float(height[iy, ix]) + 6.0, "highest rooftop within 30 m + 6 m mast"
            else:
                zc, how = 25.0, "25 m mast on the ground"
        s["xyz"], s["placement"] = [sx, sy, zc], how
        s.setdefault("azimuths", [0.0, 120.0, 240.0])
    z.save(sites=sites)

    scene.tx_array = rt.PlanarArray(num_rows=1, num_cols=1, pattern="tr38901", polarization="V")
    scene.rx_array = rt.PlanarArray(num_rows=1, num_cols=1, pattern="iso", polarization="V")
    secs = z.sectors()
    for i, s in enumerate(secs):
        scene.add(rt.Transmitter(f"sector{i}", position=[float(v) for v in s["pos"]],
                                 orientation=[float(s["yaw"]), float(s["tilt"]), 0.0], power_dbm=float(s["power_dbm"])))
    t0 = time.perf_counter()
    rm = rt.RadioMapSolver()(scene, center=mi.Point3f(float(centre[0]), float(centre[1]), UE_HEIGHT_M),
                             orientation=mi.Point3f(0, 0, 0), size=mi.Point2f(float(size[0]), float(size[1])),
                             cell_size=mi.Point2f(CELL_M, CELL_M), samples_per_tx=int(z.meta.get("samples_per_tx", 10_000_000)),
                             max_depth=6, los=True, specular_reflection=True, diffuse_reflection=True, refraction=False,
                             diffraction=True, seed=1)
    gain = np.array(rm.path_gain)
    solve_s = time.perf_counter() - t0
    served = outdoor & (gain.max(0) >= 10.0 ** (-118.0 / 10.0))
    walk = largest_component(served) if served.any() else served
    RadioMaps(gain, x, y, height, walk, np.array([s["pos"] for s in secs], float), np.array([s["site"] for s in secs]),
              dict(zone=z.id)).save(z.dir / "radio.npz")

    from matplotlib import cm
    from PIL import Image
    best = 10 * np.log10(np.maximum(gain.max(0), 1e-30))
    lo, hi = -130.0, -70.0
    rgba = (cm.turbo(np.clip((best - lo) / (hi - lo), 0, 1)) * 255).astype(np.uint8)
    rgba[..., 3] = np.where(built, 0, np.where(best < lo, 40, 170))
    Image.fromarray(rgba[::-1]).save(z.dir / "coverage.png")
    s_, w_ = z.to_ll(x[0] - CELL_M / 2, y[0] - CELL_M / 2)[0], z.to_ll(x[0] - CELL_M / 2, y[0] - CELL_M / 2)[1]
    n_, e_ = z.to_ll(x[-1] + CELL_M / 2, y[-1] + CELL_M / 2)[0], z.to_ll(x[-1] + CELL_M / 2, y[-1] + CELL_M / 2)[1]
    street = (height < UE_HEIGHT_M - 0.5)
    return dict(sectors=len(secs), grid=[int(nx), int(ny)], radio_map_s=round(solve_s, 2),
                street_cells=int(street.sum()), served_fraction_of_streets=round(float((street & (gain.max(0) >= 10 ** -11.8)).sum() / max(street.sum(), 1)), 3),
                walkable_cells=int(walk.sum()), coverage_bounds=[[float(s_), float(w_)], [float(n_), float(e_)]],
                legend_db=[lo, hi])


def auto_sites(z, count):
    """Spread `count` sites over the built-up area (k-means of the building centroids)."""
    b = parse_buildings(z)
    if not b:
        raise ValueError("no buildings in the area")
    pts = np.array([x["xy"].mean(0) for x in b])
    w = np.array([abs(0.5 * np.sum(x["xy"][:, 0] * np.roll(x["xy"][:, 1], -1) - np.roll(x["xy"][:, 0], -1) * x["xy"][:, 1])) for x in b])
    x0, y0, x1, y1 = z.extent
    inside = (pts[:, 0] > x0) & (pts[:, 0] < x1) & (pts[:, 1] > y0) & (pts[:, 1] < y1)
    pts, w = pts[inside], w[inside]
    rng = np.random.default_rng(1)
    c = pts[rng.choice(len(pts), size=min(count, len(pts)), replace=False, p=w / w.sum())]
    for _ in range(40):
        lab = np.argmin(((pts[:, None, :] - c[None]) ** 2).sum(-1), axis=1)
        c = np.array([np.average(pts[lab == k], axis=0, weights=w[lab == k]) if (lab == k).any() else c[k] for k in range(len(c))])
    out = []
    for k, p in enumerate(c):
        near = pts[np.argmin(((pts - p) ** 2).sum(-1))]
        lat, lon = z.to_ll(near[0], near[1])
        out.append(dict(name=f"site-{k}", lat=float(lat), lon=float(lon), height_m=None, azimuths=[0.0, 120.0, 240.0],
                        downtilt_deg=9.0, power_dbm=44.0, source="auto-placed"))
    return out


def build(z):
    t0 = time.time()
    stats = dict(z.meta.get("stats", {}))
    z.save(status="building", step="3D scene from the buildings", error=None)
    stats["scene"] = build_scene(z)
    z.save(step="road network and SUMO traffic", stats=stats)
    stats["roads"] = build_roads(z)
    z.save(step="coverage maps with Sionna RT", stats=stats)
    stats["radio"] = build_radio(z)
    stats["build_s"] = round(time.time() - t0, 1)
    z.save(status="ready", step=None, stats=stats, built=time.strftime("%Y-%m-%dT%H:%M:%S"))


_NETS = {}
VCLASS = {"car": "passenger", "person": "pedestrian"}


def _net(z):
    import sumolib
    key = (z.id, (z.dir / "net.xml").stat().st_mtime)
    if key not in _NETS:
        _NETS.clear()
        _NETS[key] = sumolib.net.readNet(z.net_file)
    return _NETS[key]


def snap(z, lat, lon, kind):
    """The nearest point of a street a car (carriageway) or a person (pavement, footpath) may use."""
    import sumolib
    if kind not in VCLASS:
        raise ValueError("kind must be car or person")
    net = _net(z)
    x, y = net.convertLonLat2XY(float(lon), float(lat))
    best = None
    for radius in (25, 60, 150, 400):
        cand = [(e, d) for e, d in net.getNeighboringEdges(x, y, radius) if e.allows(VCLASS[kind]) and e.getLength() > 8.0]
        if cand:
            best = min(cand, key=lambda t: t[1])[0]
            break
    if best is None:
        raise ValueError("no street within 400 m of that point")
    off, dist = sumolib.geomhelper.polygonOffsetAndDistanceToPoint((x, y), best.getShape())
    off = min(max(off, 1.0), max(best.getLength() - 1.0, 1.0))
    px, py = sumolib.geomhelper.positionAtShapeOffset(best.getShape(), off)
    lon2, lat2 = net.convertXY2LonLat(px, py)
    return dict(lat=float(lat2), lon=float(lon2), edge=best.getID(), pos=round(float(off), 1), moved_m=round(float(dist), 1))


def clean_ues(z, ues):
    """Validated UEs of a zone, each snapped to a street: [{name, kind, mode, lat, lon, edge, pos}]."""
    if not isinstance(ues, list) or len(ues) > 24:
        raise ValueError("at most 24 UEs")
    out = []
    for i, u in enumerate(ues):
        kind, mode = u.get("kind", "person"), u.get("mode", "mobile")
        if kind not in VCLASS or mode not in ("mobile", "fixed", "route"):
            raise ValueError(f"UE {i + 1}: kind is car or person, mode is mobile, fixed or route")
        s_, w_, n_, e_ = z.meta["bbox"]
        if not (s_ <= float(u["lat"]) <= n_ and w_ <= float(u["lon"]) <= e_):
            raise ValueError(f"UE {i + 1} is outside the zone")
        p = snap(z, u["lat"], u["lon"], kind)
        out.append(dict(name=f"ue-{i}", kind=kind, mode=mode, lat=p["lat"], lon=p["lon"], edge=p["edge"], pos=p["pos"],
                        source=str(u.get("source") or "manual")[:40]))
        if mode == "route":
            wps = u.get("waypoints") or []
            if not 1 <= len(wps) <= 12:
                raise ValueError(f"UE {i + 1}: a route takes between 1 and 12 destinations")
            snapped = []
            for j, w in enumerate(wps):
                if not (s_ <= float(w["lat"]) <= n_ and w_ <= float(w["lon"]) <= e_):
                    raise ValueError(f"UE {i + 1}: destination {j + 1} is outside the zone")
                q = snap(z, w["lat"], w["lon"], kind)
                snapped.append(dict(lat=q["lat"], lon=q["lon"], edge=q["edge"], pos=q["pos"]))
            at_end = u.get("at_end", "stop")
            if at_end not in ("stop", "loop"):
                raise ValueError(f"UE {i + 1}: at the end of its route a UE stops or loops")
            out[-1].update(waypoints=snapped, at_end=at_end)
    return out


def auto_ues(z, cars, persons, seed=None):
    """UEs spread over the streets, preferring the ones the sites serve."""
    net = _net(z)
    rng = np.random.default_rng(seed)
    x0, y0, x1, y1 = z.extent
    xf = z.sumo_to_scene()
    served = None
    if (z.dir / "radio.npz").exists():
        from radio_maps import RadioMaps
        maps = RadioMaps.load(z.dir / "radio.npz")
        served = lambda p: bool(maps.outdoor.ravel()[maps.cell_of(np.asarray(p))])
    out = []
    for kind, count in (("car", int(cars)), ("person", int(persons))):
        edges = [e for e in net.getEdges() if e.allows(VCLASS[kind]) and e.getLength() > 15.0]
        mids = xf([e.getShape()[len(e.getShape()) // 2] for e in edges]) if edges else np.zeros((0, 2))
        ok = [(e, m) for e, m in zip(edges, mids) if x0 + 30 < m[0] < x1 - 30 and y0 + 30 < m[1] < y1 - 30]
        good = [(e, m) for e, m in ok if served is None or served(m)] or ok
        if count and not good:
            raise ValueError(f"no street for a {kind} in the zone")
        w = np.array([e.getLength() for e, _ in good])
        for i in rng.choice(len(good), size=count, replace=count > len(good), p=w / w.sum()) if count else []:
            e = good[int(i)][0]
            off = float(rng.uniform(1.0, max(e.getLength() - 1.0, 1.5)))
            import sumolib
            px, py = sumolib.geomhelper.positionAtShapeOffset(e.getShape(), off)
            lon, lat = net.convertXY2LonLat(px, py)
            out.append(dict(kind=kind, mode="mobile", lat=float(lat), lon=float(lon), source="auto-placed"))
    return out


def config_hash(meta):
    """What a namespace is generated from: when it changes, the namespace is out of date."""
    import hashlib
    sites = [[s["name"], round(s["lat"], 6), round(s["lon"], 6)] for s in meta.get("sites", [])]
    ues = [[u["kind"], u["mode"], round(u["lat"], 6), round(u["lon"], 6)] for u in meta.get("ues", [])]
    return hashlib.sha1(json.dumps([sites, ues]).encode()).hexdigest()[:12]


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "sites-osm":
        print(json.dumps(osm_sites(*[float(v) for v in sys.argv[2:6]])))
        sys.exit(0)
    zone = Zone(sys.argv[2])
    try:
        {"fetch": fetch, "build": build}[cmd](zone)
        print(json.dumps(zone.meta.get("stats"), indent=1))
    except Exception as exc:
        zone.save(status="error", step=None, error=f"{type(exc).__name__}: {exc}")
        traceback.print_exc()
        sys.exit(1)
