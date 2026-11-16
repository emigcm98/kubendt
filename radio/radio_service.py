"""Radio service: the GPU-side companion of KubeNDT for radio zones.

Runs on the host that has the GPU. KubeNDT's backend forwards /radio/* here (KUBENDT_RADIO_URL), under its
own authentication, so this service has none and must listen only where the backend can reach it
(the Docker network gateway), never on a public address.

  GET    /health
  GET    /zones                      every zone
  POST   /zones {name, bbox}         create a zone and fetch its OpenStreetMap extract
  GET    /zones/{id}                 metadata, status and statistics
  DELETE /zones/{id}
  PUT    /zones/{id}/sites {sites}   set the sites
  POST   /zones/{id}/sites/auto {count}   propose sites spread over the built-up area
  GET    /zones/{id}/sites/osm       telephony masts mapped in OpenStreetMap inside the zone
  POST   /zones/{id}/build           3D scene, road network and coverage maps (GPU)
  GET    /zones/{id}/coverage.png
  POST   /zones/{id}/activate        the zone the emulation uses
  GET    /active
"""
import argparse
import asyncio
import os
import signal
import json
import shutil
import subprocess
import sys
from pathlib import Path

from aiohttp import web

sys.path.insert(0, str(Path(__file__).parent))
import zone as Z  # noqa: E402

PY = sys.executable
SCRIPT = str(Path(__file__).parent / "zone.py")
JOBS = {}                                   # zone id -> Popen


def err(status, msg):
    return web.json_response({"error": msg}, status=status)


def load(zid):
    if not zid.replace("-", "").isalnum() or not (Z.ZONES / zid / "zone.json").exists():
        return None
    return Z.Zone(zid)


def public(z):
    m = dict(z.meta)
    job = JOBS.get(z.id)
    m["busy"] = bool(job and job.poll() is None)
    # a job that died without writing its status (killed, out of memory)
    if not m["busy"] and m.get("status") in ("fetching", "building"):
        z.save(status="error", step=None, error="the job ended without a result; see job.log in the zone folder")
        m = dict(z.meta, busy=False)
    m["has_coverage"] = (z.dir / "coverage.png").exists() and m.get("status") == "ready"
    m.setdefault("ues", [])
    ns = m.get("namespace")
    if ns:
        ns = dict(ns)
        # the namespace depends on how many gNBs and UEs there are, not on where they are or how they move
        ns["stale"] = ns.get("status") == "ready" and (ns.get("gnbs") != len(m.get("sites", [])) or ns.get("ues") != len(m.get("ues", [])))
        existing = kubendt_namespaces()
        if ns.get("status") == "ready" and existing is not None and ns.get("name") not in existing:
            ns.update(status="error", stale=False, error=f"The namespace {ns.get('name')} no longer exists in KubeNDT: it was deleted outside Radio zones. Create it again, or delete this entry.")
        if ns.get("status") == "deploying" and not m["busy"]:
            ns.update(status="error", error="the deployment ended without a result; see job.log in the zone folder")
        m["namespace"] = ns
    m["emulation"] = emulation_state(z)
    m["active"] = active_id() == z.id
    return m


_NS_CACHE = {"at": 0.0, "names": None}


def kubendt_namespaces():
    """Namespaces KubeNDT has now (cached a few seconds); None when it cannot be asked."""
    import time
    import urllib.request
    if time.time() - _NS_CACHE["at"] < 8:
        return _NS_CACHE["names"]
    names = None
    try:
        token = (Path.home() / "kubendt-radio" / ".kubendt_token").read_text().strip()
        req = urllib.request.Request("http://127.0.0.1:8088/api/namespaces/", headers={"Authorization": f"Bearer {token}"})
        data = json.load(urllib.request.urlopen(req, timeout=4))
        names = {n["name"] for n in (data.get("namespaces") or [])}
    except Exception:
        names = None
    _NS_CACHE.update(at=time.time(), names=names)
    return names


def emulation_state(z):
    """What the emulator last wrote, and whether its process is alive."""
    try:
        st = json.loads((z.dir / "emulation.json").read_text())
    except (OSError, ValueError):
        return dict(state="stopped")
    alive = False
    try:
        cmdline = Path(f"/proc/{int(st.get('pid', 0))}/cmdline").read_bytes()
        alive = b"emulator.py" in cmdline and z.id.encode() in cmdline
    except (OSError, ValueError):
        pass
    st["alive"] = alive
    if not alive and st.get("state") in ("running", "starting"):
        st["state"] = "stopped"
    return st


def emulator_spawn(z, *args):
    log = open(z.dir / "job.log", "a")
    return subprocess.Popen([PY, str(Path(__file__).parent / "emulator.py"), *args], stdout=log, stderr=subprocess.STDOUT,
                            stdin=subprocess.DEVNULL, start_new_session=True)


def any_emulation():
    for d in Z.ZONES.iterdir() if Z.ZONES.exists() else []:
        z = load(d.name)
        if z and emulation_state(z).get("alive"):
            return z.id
    return None


async def put_ues(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    if z.meta.get("status") != "ready":
        return err(409, "build the zone first: the UEs are placed on its road network")
    try:
        body = await request.json()
        ues = await asyncio.get_running_loop().run_in_executor(None, Z.clean_ues, z, body["ues"])
    except (ValueError, KeyError, TypeError) as e:
        return err(400, str(e))
    z.save(ues=ues)
    return web.json_response(public(z))


async def auto_ues(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    if z.meta.get("status") != "ready":
        return err(409, "build the zone first")
    try:
        body = await request.json()
        cars, persons = max(0, int(body.get("cars", 0))), max(0, int(body.get("persons", 0)))
        if cars + persons > 24:
            raise ValueError("at most 24 UEs")
        raw = await asyncio.get_running_loop().run_in_executor(None, Z.auto_ues, z, cars, persons)
        ues = await asyncio.get_running_loop().run_in_executor(None, Z.clean_ues, z, raw)
    except (ValueError, KeyError, TypeError) as e:
        return err(400, str(e))
    return web.json_response(ues)


async def snap_ue(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    if z.meta.get("status") != "ready":
        return err(409, "build the zone first")
    try:
        body = await request.json()
        p = await asyncio.get_running_loop().run_in_executor(None, Z.snap, z, body["lat"], body["lon"], body.get("kind", "person"))
    except (ValueError, KeyError, TypeError) as e:
        return err(400, str(e))
    return web.json_response(p)


async def create_namespace(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    body = await request.json()
    name = str(body.get("name") or f"radio-{z.id}").strip().lower()
    if not (2 <= len(name) <= 40) or not all(ch.isalnum() or ch == "-" for ch in name) or name[0] == "-" or name[-1] == "-":
        return err(400, "the namespace name takes lowercase letters, digits and hyphens")
    if z.meta.get("status") != "ready":
        return err(409, "build the zone first")
    if not z.meta.get("ues"):
        return err(400, "add at least one UE to the zone")
    if public(z)["busy"]:
        return err(409, "the zone is busy")
    if emulation_state(z).get("alive"):
        return err(409, "stop the emulation first")
    for d in Z.ZONES.iterdir():
        other = load(d.name)
        if other and other.id != z.id and (other.meta.get("namespace") or {}).get("name") == name:
            return err(409, f"the namespace {name} belongs to the zone {other.meta['name']}")
    z.save(namespace=dict(name=name, status="deploying", step="queued", error=None))
    JOBS[z.id] = emulator_spawn(z, "deploy", z.id, name)
    return web.json_response(public(z))


async def delete_namespace(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    if public(z)["busy"]:
        return err(409, "the zone is busy")
    if emulation_state(z).get("alive"):
        return err(409, "stop the emulation first")
    proc = emulator_spawn(z, "destroy", z.id)
    await asyncio.get_running_loop().run_in_executor(None, proc.wait)
    z = load(z.id)
    return web.json_response(public(z))


async def start_emulation(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    _NS_CACHE["at"] = 0.0
    ns = public(z).get("namespace")
    if not ns or ns.get("status") != "ready":
        return err(409, (ns or {}).get("error") or "create the namespace of the zone first")
    if ns.get("stale"):
        return err(409, "the sites or the UEs changed: regenerate the namespace first")
    running = any_emulation()
    try:
        takeover = bool((await request.json()).get("takeover"))
    except Exception:
        takeover = False
    if running and running != z.id and takeover:
        other = load(running)
        os.kill(int(emulation_state(other)["pid"]), signal.SIGTERM)
        for _ in range(60):
            await asyncio.sleep(0.5)
            if not emulation_state(other).get("alive"):
                break
        running = any_emulation()
    if running:
        other = load(running)
        return web.json_response({"error": "an emulation is already running" + ("" if running == z.id else f" in the zone {other.meta['name']}: one at a time on the GPU"),
                                  "code": "EMULATION_BUSY", "zone": running, "zone_name": other.meta["name"]}, status=409)
    (z.dir / "emulation.json").unlink(missing_ok=True)
    proc = emulator_spawn(z, "run", z.id)
    (z.dir / "emulation.json").write_text(json.dumps(dict(pid=proc.pid, state="starting", namespace=ns["name"], step="starting")))
    return web.json_response(public(z))


async def stop_emulation(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    st = emulation_state(z)
    if st.get("alive"):
        import os
        import signal
        os.kill(int(st["pid"]), signal.SIGTERM)
        for _ in range(60):
            await asyncio.sleep(0.5)
            if not emulation_state(z).get("alive"):
                break
    return web.json_response(public(load(z.id)))


async def by_namespace(request):
    """The zone a namespace was generated from, for the namespace page."""
    name = request.match_info["ns"]
    for d in sorted(Z.ZONES.iterdir()) if Z.ZONES.exists() else []:
        z = load(d.name)
        if z and (z.meta.get("namespace") or {}).get("name") == name:
            return web.json_response(public(z))
    return web.json_response(None)


def active_id():
    f = Z.ZONES / "active.json"
    try:
        return json.loads(f.read_text()).get("id")
    except (OSError, ValueError):
        return None


def spawn(z, cmd):
    log = open(z.dir / "job.log", "a")
    JOBS[z.id] = subprocess.Popen([PY, SCRIPT, cmd, z.id], stdout=log, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL)


def clean_sites(z, sites):
    s, w, n, e = z.meta["bbox"]
    if not isinstance(sites, list) or len(sites) > 40:
        raise ValueError("sites must be a list of at most 40 sites")
    out = []
    for i, x in enumerate(sites):
        lat, lon = float(x["lat"]), float(x["lon"])
        if not (s <= lat <= n and w <= lon <= e):
            raise ValueError(f"site {i + 1} is outside the zone")
        az = [float(a) % 360.0 for a in (x.get("azimuths") or [0.0, 120.0, 240.0])]
        if not 1 <= len(az) <= 6:
            raise ValueError(f"site {i + 1}: between 1 and 6 sectors")
        h = x.get("height_m")
        h = None if h in (None, "", 0) else float(h)
        if h is not None and not 2.0 <= h <= 200.0:
            raise ValueError(f"site {i + 1}: height between 2 and 200 m, or empty for the rooftop")
        tilt, power = float(x.get("downtilt_deg", 9.0)), float(x.get("power_dbm", 44.0))
        if not 0 <= tilt <= 45 or not 0 <= power <= 60:
            raise ValueError(f"site {i + 1}: downtilt 0-45 degrees, power 0-60 dBm")
        name = "".join(c for c in str(x.get("name") or f"site-{i}") if c.isalnum() or c in "-_ .")[:40] or f"site-{i}"
        out.append(dict(name=name, lat=lat, lon=lon, height_m=h, azimuths=az, downtilt_deg=tilt, power_dbm=power,
                        source=str(x.get("source") or "manual")[:80]))
    return out


async def zones(request):
    out = []
    if Z.ZONES.exists():
        for d in sorted(Z.ZONES.iterdir()):
            z = load(d.name)
            if z:
                out.append(public(z))
    return web.json_response(out)


async def create(request):
    try:
        body = await request.json()
        zid = Z.create(str(body["name"])[:60], body["bbox"])
    except (ValueError, KeyError, TypeError) as e:
        return err(400, str(e))
    z = Z.Zone(zid)
    z.save(status="fetching", step="descargando OpenStreetMap")
    spawn(z, "fetch")
    return web.json_response(public(z))


async def get(request):
    z = load(request.match_info["id"])
    return web.json_response(public(z)) if z else err(404, "zone not found")


async def delete(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    if public(z)["busy"]:
        return err(409, "the zone is busy")
    if z.meta.get("namespace"):
        return err(409, "delete the namespace of the zone first")
    shutil.rmtree(z.dir)
    return web.json_response({"deleted": z.id})


async def put_sites(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    try:
        sites = clean_sites(z, (await request.json())["sites"])
    except (ValueError, KeyError, TypeError) as e:
        return err(400, str(e))
    # changing the sites invalidates the coverage maps
    # the UEs stay: they are re-snapped to the road network when the zone is rebuilt
    z.save(sites=sites, status="fetched" if z.meta.get("status") == "ready" else z.meta.get("status"))
    return web.json_response(public(z))


async def auto_sites(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    if not (z.dir / "zone.osm").exists():
        return err(409, "the map of the zone is not downloaded yet")
    try:
        count = max(1, min(20, int((await request.json()).get("count", 3))))
        sites = await asyncio.get_running_loop().run_in_executor(None, Z.auto_sites, z, count)
    except ValueError as e:
        return err(400, str(e))
    return web.json_response(sites)


async def osm_sites(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    try:
        found = await asyncio.get_running_loop().run_in_executor(None, lambda: Z.osm_sites(*z.meta["bbox"]))
    except Exception as e:
        return err(502, str(e))
    return web.json_response([dict(name=f["name"], lat=f["lat"], lon=f["lon"], height_m=f["height_m"], azimuths=[0.0, 120.0, 240.0],
                                   downtilt_deg=9.0, power_dbm=44.0, source=f["source"], operator=f.get("operator")) for f in found])


async def build(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    if public(z)["busy"]:
        return err(409, "the zone is busy")
    if not (z.dir / "zone.osm").exists():
        return err(409, "the map of the zone is not downloaded yet")
    if not z.meta.get("sites"):
        return err(400, "place at least one site before building")
    if any(j.poll() is None for j in JOBS.values()):
        return err(409, "another zone is being built; one at a time on the GPU")
    z.save(status="building", step="queued", error=None)
    spawn(z, "build")
    return web.json_response(public(z))


async def coverage(request):
    z = load(request.match_info["id"])
    if not z or not (z.dir / "coverage.png").exists():
        return err(404, "no coverage map")
    return web.FileResponse(z.dir / "coverage.png", headers={"Cache-Control": "no-store"})


async def activate(request):
    z = load(request.match_info["id"])
    if not z:
        return err(404, "zone not found")
    if z.meta.get("status") != "ready":
        return err(409, "build the zone first")
    (Z.ZONES / "active.json").write_text(json.dumps({"id": z.id}))
    return web.json_response(public(z))


async def active(request):
    return web.json_response({"id": active_id()})


async def health(request):
    return web.json_response({"service": "kubendt-radio", "zones": str(Z.ZONES), "max_side_m": Z.MAX_SIDE_M})


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default="172.19.0.1")
    ap.add_argument("--port", type=int, default=8770)
    a = ap.parse_args()
    Z.ZONES.mkdir(exist_ok=True)
    app = web.Application(client_max_size=2 * 1024 * 1024)
    app.add_routes([web.get("/health", health), web.get("/zones", zones), web.post("/zones", create), web.get("/active", active),
                    web.get("/zones/{id}", get), web.delete("/zones/{id}", delete), web.put("/zones/{id}/sites", put_sites),
                    web.post("/zones/{id}/sites/auto", auto_sites), web.get("/zones/{id}/sites/osm", osm_sites),
                    web.post("/zones/{id}/build", build), web.get("/zones/{id}/coverage.png", coverage),
                    web.post("/zones/{id}/activate", activate),
                    web.put("/zones/{id}/ues", put_ues), web.post("/zones/{id}/ues/auto", auto_ues), web.post("/zones/{id}/ues/snap", snap_ue),
                    web.post("/zones/{id}/namespace", create_namespace), web.delete("/zones/{id}/namespace", delete_namespace),
                    web.post("/zones/{id}/emulation/start", start_emulation), web.post("/zones/{id}/emulation/stop", stop_emulation),
                    web.get("/namespaces/{ns}", by_namespace)])
    web.run_app(app, host=a.host, port=a.port, print=lambda *_: None)
