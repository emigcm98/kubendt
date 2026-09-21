#!/usr/bin/env python3
"""End-to-end regression over the six shipped examples, following their READMEs.

Each example is deployed into the namespace its README uses, configured, checked
(pings, OSPF adjacencies, DNS, HTTP, 5G registration, mounts), restarted through the
API to exercise replay, and torn down. Results go to results/regression/<stamp>/
with one row per check. It is the gate before tagging a release and it needs a
cluster, so it is not part of CI.

  python3 regression.py            # all six
  python3 regression.py 2 3        # a subset
  KEEP=5 ...                       # leave example 5 deployed
  SKIP_RAW=1 ...                   # skip the raw kubectl delete check in example 3

Example 5 (Open5GS) applies network_conf.json and then subscriber_conf.json, and
never restarts amf-0 (the core keeps no UE state, a restarted AMF strands the UE)."""
import glob
import json
import os
import subprocess
import sys
import time

import common as C

EX = C.EXAMPLES
client = None


def log(msg):
    C.log(msg)


def api(method, path, body=None, raw=None, ctype="application/json", timeout=900):
    return client.api(method, path, body=body, raw=raw, ctype=ctype, timeout=timeout)


def upload(ns, relpath, localfile):
    return client.upload(ns, relpath, localfile)


def import_zip(ns, zipfile):
    return client.import_zip(ns, zipfile)


def kexec(ns, pod, cmd, timeout=60):
    return C.kexec(ns, pod, cmd, timeout=timeout)


def retry(fn, timeout, every=5, label=""):
    return C.retry(fn, timeout, every)


def ping(ns, pod, ip, count=3):
    def f():
        rc, out = kexec(ns, pod, f"ping -c {count} -W 2 {ip}")
        return rc == 0 and " 0% packet loss" in out, out.splitlines()[-2:] if out else out
    return f

def contains(ns, pod, cmd, needle):
    def f():
        rc, out = kexec(ns, pod, cmd)
        return needle in out, out[-300:]
    return f

# ---------- result bookkeeping ----------
class Example:
    def __init__(self, name, ns):
        self.name, self.ns, self.checks, self.timings, self.notes = name, ns, [], {}, []
        self.t0 = time.time()
    def check(self, label, ok, detail="", waited=None):
        self.checks.append({"check": label, "ok": bool(ok), "detail": str(detail)[:400], "waited_s": waited})
        log(f"  [{'OK ' if ok else 'FAIL'}] {label}" + (f" ({waited}s)" if waited else "") + ("" if ok else f" :: {str(detail)[:200]}"))
    def note(self, msg): self.notes.append(msg); log(f"  note: {msg}")
    def timing(self, key, value): self.timings[key] = value; log(f"  {key}: {value}")

def deploy(ex, topo_path):
    st, r = api("POST", f"/network/deploy-network/{ex.ns}", body=json.load(open(topo_path)))
    ok = st == 200
    ex.check("deploy", ok, r if not ok else "")
    if ok:
        ex.timing("deploy.took_time", r.get("took_time")); ex.timing("deploy.backend_ms", r.get("timeline", {}).get("backend_ms"))
        if r.get("warnings"): ex.note(f"deploy warnings: {r['warnings']}")
    return ok

def configure(ex, conf_path, label="configure"):
    st, r = api("POST", f"/network/configure/{ex.ns}", body=json.load(open(conf_path)))
    fails = [a for a in (r.get("action_results") or []) if a.get("status") not in ("ok", "success", "OK", "SUCCESS", "done", "applied")] if isinstance(r, dict) else []
    failures = r.get("failures", None) if isinstance(r, dict) else None
    ok = st == 200 and (failures == 0)
    ex.check(label, ok, "" if ok else (f"failures={failures} " + "; ".join(f"{a.get('pod')}/{a.get('action')}: {a.get('error','')[:80]}" for a in fails[:6])))
    if isinstance(r, dict): ex.timing(f"{label}.took_time", r.get("took_time"))
    return ok

def modify(ex, path, label):
    st, r = api("POST", f"/network/modify-network/{ex.ns}", body=json.load(open(path)))
    ok = st == 200
    ex.check(label, ok, r if not ok else "")
    if ok: ex.timing(f"{label}.took_time", r.get("took_time"))
    return ok

def restart(ex, pod):
    st, r = api("PATCH", f"/pods/restart/{ex.ns}/{pod}")
    ok = st == 200
    ex.check(f"restart {pod}", ok, r if not ok else "")
    if ok:
        ex.timing(f"restart.{pod}.took_time", r.get("took_time")); ex.timing(f"restart.{pod}.replay", r.get("replay")); ex.timing(f"restart.{pod}.peer_replay", r.get("peer_replay"))
    return ok

def setup(ex):
    st, r = api("GET", "/namespaces/")
    existing = {n["name"] for n in (r.get("namespaces") or [])} if st == 200 and isinstance(r, dict) else set()
    if ex.ns in existing:
        st, r = api("DELETE", f"/network/clear-topology/{ex.ns}")
        ex.check(f"namespace {ex.ns} already exists, topology cleared", st == 200, r if st != 200 else "")
        return
    st, r = api("POST", "/namespaces/", body={"namespace": ex.ns})
    ex.check("create namespace", st == 200, r if st != 200 else "")

def teardown(ex):
    if ex.name.split("-")[0] in os.environ.get("KEEP", "").split(","):
        ex.note(f"namespace {ex.ns} left deployed on request"); ex.timing("total_s", round(time.time() - ex.t0, 1)); return
    st, r = api("DELETE", f"/network/clear-topology/{ex.ns}")
    ex.check("clear topology", st == 200, r if st != 200 else "")
    if st == 200: ex.timing("clear.took_time", r.get("took_time"))
    st, r = api("DELETE", f"/namespaces/{ex.ns}")
    ex.check("delete namespace", st == 200, r if st != 200 else "")
    ex.timing("total_s", round(time.time() - ex.t0, 1))

def upload_dir(ex, files_dir):
    for f in sorted(glob.glob(f"{files_dir}/**/*", recursive=True)):
        if os.path.isfile(f):
            rel = os.path.relpath(f, files_dir)
            ok, r = upload(ex.ns, rel, f)
            ex.check(f"upload {rel}", ok, r if not ok else "")

# ---------- examples ----------
def ex1():
    d = f"{EX}/1-test-small"; ex = Example("1-test-small", "small"); setup(ex)
    upload_dir(ex, f"{d}/files")
    if deploy(ex, f"{d}/topology-network-test-small.json"):
        configure(ex, f"{d}/network_conf.json")
        for ip, what in (("10.0.1.12", "pc-1 same LAN"), ("10.0.2.11", "pc-2 via r1"), ("10.0.3.11", "server via ovs")):
            ok, det, w = retry(ping(ex.ns, "pc-0", ip), 60); ex.check(f"pc-0 ping {ip} ({what})", ok, det, w)
        rc, out = kexec(ex.ns, "pc-0", "cat /mnt/test.sh"); ex.check("mount test.sh visible in pc-0", rc == 0 and out, out[:80])
        rc, out = kexec(ex.ns, "server-0", "cat /etc/kubendt/secret.env"); ex.check("secret mount visible in server-0", rc == 0 and out, out[:80])
        if restart(ex, "r1-0"):
            ok, det, w = retry(ping(ex.ns, "pc-0", "10.0.2.11"), 90); ex.check("pc-0 ping 10.0.2.11 after r1 restart (replay)", ok, det, w)
    teardown(ex); return ex

def ex2():
    d = f"{EX}/2-test-frr-ospf"; ex = Example("2-test-frr-ospf", "frr-ospf"); setup(ex)
    if deploy(ex, f"{d}/topology-network-test-frr-ospf.json"):
        configure(ex, f"{d}/network_conf.json")
        ok, det, w = retry(contains(ex.ns, "router1-0", 'vtysh -c "show ip ospf neighbor"', "Full"), 120); ex.check("router1 OSPF neighbor Full", ok, det, w)
        for ip in ("192.168.1.52", "192.168.2.51", "192.168.3.10"):
            ok, det, w = retry(ping(ex.ns, "host-0", ip), 60); ex.check(f"host-0 ping {ip}", ok, det, w)
        if restart(ex, "router1-0"):
            ok, det, w = retry(contains(ex.ns, "router1-0", 'vtysh -c "show ip ospf neighbor"', "Full"), 150); ex.check("router1 OSPF Full after restart (replay)", ok, det, w)
            ok, det, w = retry(ping(ex.ns, "host-0", "192.168.2.51"), 60); ex.check("host-0 ping 192.168.2.51 after restart", ok, det, w)
    teardown(ex); return ex

def pods_ready(ns, pod, timeout):
    def f():
        p = subprocess.run(["kubectl"] + (["--context", C.CONTEXT] if C.CONTEXT else []) + ["-n", ns, "get", "pod", pod, "-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}{' '}{.metadata.deletionTimestamp}"], capture_output=True, text=True)
        out = p.stdout.strip()
        return out.startswith("True") and out.endswith("True"), out
    return retry(f, timeout, every=3)

def ex3():
    d = f"{EX}/3-test-modify-ospf"; ex = Example("3-test-modify-ospf", "modify-ospf"); setup(ex)
    if deploy(ex, f"{d}/topology-network-test-modify-ospf.json"):
        wait_vyos_api(ex, ["vyos-router-0"])
        configure(ex, f"{d}/network_conf.json")
        ok, det, w = retry(contains(ex.ns, "frr-router-0", 'vtysh -c "show ip ospf neighbor"', "Full"), 300); ex.check("frr-router OSPF Full with vyos-router", ok, det, w)
        ok, det, w = retry(ping(ex.ns, "alpine-host-0", "10.0.1.11"), 60); ex.check("alpine-host-0 ping ubuntu-host (cross LAN)", ok, det, w)
        if modify(ex, f"{d}/modify-phase2-scaleup.json", "phase2 scale-up"):
            configure(ex, f"{d}/network_conf-phase2.json", "phase2 configure")
            ok, det, w = retry(ping(ex.ns, "alpine-host-1", "10.0.1.11"), 60); ex.check("alpine-host-1 ping 10.0.1.11", ok, det, w)
        if modify(ex, f"{d}/modify-phase3-add.json", "phase3 add node"):
            configure(ex, f"{d}/network_conf-phase3.json", "phase3 configure")
            ok, det, w = retry(ping(ex.ns, "debian-host-0", "10.0.2.11"), 60); ex.check("debian-host-0 ping 10.0.2.11", ok, det, w)
        if modify(ex, f"{d}/modify-phase4-scaledown.json", "phase4 scale-down"):
            ok, det, w = retry(contains(ex.ns, "alpine-host-0", "ip route", "default via 10.0.2.1"), 30); ex.check("alpine-host-0 keeps its default route after scale-down", ok, det, w)
            ok, det, w = retry(ping(ex.ns, "alpine-host-0", "10.0.1.11"), 60); ex.check("alpine-host-0 still reaches 10.0.1.11 after scale-down", ok, det, w)
        modify(ex, f"{d}/modify-phase5-delete.json", "phase5 delete node")
        p = subprocess.run(["kubectl"] + (["--context", C.CONTEXT] if C.CONTEXT else []) + ["-n", ex.ns, "get", "pods", "-o", "name"], capture_output=True, text=True)
        names = sorted(x.split("/")[1] for x in p.stdout.split())
        ex.check("base topology has 6 pods again", len(names) == 6, names)
        if restart(ex, "frr-router-0"):
            ok, det, w = retry(contains(ex.ns, "frr-router-0", "ip a show eth1", "10.0.1.1/24"), 60); ex.check("frr eth1 IP replayed after API restart", ok, det, w)
            ok, det, w = retry(contains(ex.ns, "frr-router-0", 'vtysh -c "show ip ospf neighbor"', "Full"), 180); ex.check("frr OSPF Full after API restart", ok, det, w)
        # Optional claim test: a raw kubectl delete must not replay (documented behavior).
        ok = False
        if not os.environ.get("SKIP_RAW"):
            subprocess.run(["kubectl"] + (["--context", C.CONTEXT] if C.CONTEXT else []) + ["-n", ex.ns, "delete", "pod", "frr-router-0", "--wait=false"], capture_output=True)
            ok, det, w = pods_ready(ex.ns, "frr-router-0", 180); ex.check("frr-router-0 Ready after raw kubectl delete", ok, det, w)
        if ok:
            time.sleep(5)
            rc, out = kexec(ex.ns, "frr-router-0", 'vtysh -c "show running-config"')
            replayed = "network 10.0.1.0/24 area" in out
            # Documented behavior: only KubeNDT-driven recreations replay, so the OSPF config must be gone here.
            ex.check("raw kubectl delete does NOT replay (documented, OSPF config absent)", not replayed, out[-200:])
            rc, out2 = kexec(ex.ns, "frr-router-0", "ip a show eth1")
            ex.note(f"after raw delete: CRD address {'present' if '10.0.1.1/24' in out2 else 'missing'}, OSPF config {'present' if replayed else 'missing'}")
            if not replayed:
                ex.note("raw kubectl delete gives no replay, only API-driven restarts do (as documented now)")
                if restart(ex, "frr-router-0"):
                    ok, det, w = retry(contains(ex.ns, "frr-router-0", 'vtysh -c "show running-config"', "network 10.0.1.0/24 area"), 60); ex.check("frr OSPF config back after API restart", ok, det, w)
        if restart(ex, "vyos-router-0"):
            ok, det, w = retry(contains(ex.ns, "frr-router-0", 'vtysh -c "show ip ospf neighbor"', "Full"), 300); ex.check("OSPF Full again after VyOS API restart (VyOS replay)", ok, det, w)
    teardown(ex); return ex

def ex4():
    d = f"{EX}/4-test-medium-allfeatures"; ex = Example("4-test-medium-allfeatures", "allfeatures"); setup(ex)
    upload_dir(ex, f"{d}/files")
    if deploy(ex, f"{d}/topology-network-test-allfeatures.json"):
        configure(ex, f"{d}/network_conf.json")
        def two_full():
            rc, out = kexec(ex.ns, "edge-router-0", 'vtysh -c "show ip ospf neighbor"')
            return out.count("Full") >= 2, out[-300:]
        ok, det, w = retry(two_full, 180); ex.check("edge-router two OSPF neighbors Full", ok, det, w)
        ok, det, w = retry(ping(ex.ns, "user-0", "192.168.10.10"), 90); ex.check("user-0 ping iperf-server-1 192.168.10.10 (via OSPF)", ok, det, w)
        ok, det, w = retry(contains(ex.ns, "user-0", "nslookup web-internal 2>&1", "Address"), 90); ex.check("user-0 nslookup web-internal", ok, det, w)
        ok, det, w = retry(contains(ex.ns, "user-0", "wget -qO- http://web-internal.kubendt.local 2>&1", "<"), 60); ex.check("user-0 wget web-internal (mounted page)", ok, det, w)
        ok, det, w = retry(contains(ex.ns, "user-0", "wget -qO- http://web-public.kubendt.local 2>&1", "<"), 60); ex.check("user-0 wget web-public", ok, det, w)
        if restart(ex, "room-router-0"):
            ok, det, w = retry(ping(ex.ns, "user-0", "192.168.10.10"), 150); ex.check("user-0 ping 192.168.10.10 after room-router restart (replay)", ok, det, w)
    teardown(ex); return ex

def ex5():
    d = f"{EX}/5-test-full-open5gs"; ex = Example("5-test-full-open5gs", "open5gs"); setup(ex)
    ok, r = import_zip(ex.ns, f"{d}/open5gs_files.zip"); ex.check("import open5gs_files.zip", ok, r if not ok else "")
    if deploy(ex, f"{d}/topology-network-test-big.json"):
        configure(ex, f"{d}/network_conf.json")
        for pod, ip, what in (("amf-0", "10.5.0.12", "NRF"), ("amf-0", "10.5.0.18", "MongoDB"), ("smf-0", "10.5.1.10", "UPF N4")):
            ok, det, w = retry(ping(ex.ns, pod, ip), 60); ex.check(f"{pod} ping {ip} ({what})", ok, det, w)
        ok, det, w = retry(contains(ex.ns, "upf-0", "ip link show ogstun", "UP"), 90); ex.check("upf-0 ogstun UP", ok, det, w)
        ok, det, w = retry(contains(ex.ns, "nrf-0", "curl -s --http2-prior-knowledge http://10.5.0.12:7777/nnrf-nfm/v1/nf-instances", "nf-instances/"), 120); ex.check("NRF lists registered NF instances", ok, det, w)
        configure(ex, f"{d}/subscriber_conf.json", "subscriber_conf (README step 11)")
        ok, det, w = retry(contains(ex.ns, "ue-0", "ip addr show uesimtun0 2>&1", "inet "), 300); ex.check("ue-0 uesimtun0 has an IP (5G registration + PDU session)", ok, det, w)
        if ok:
            ok, det, w = retry(contains(ex.ns, "ue-0", "ip route", "uesimtun0"), 30); ex.check("ue-0 default route via uesimtun0", ok, det, w)
        # Restart a node without 5G session state. Restarting amf-0 drops the NGAP
        # association and strands the UE with a tunnel the gNB no longer knows.
        if restart(ex, "webui-0"):
            ok, det, w = retry(ping(ex.ns, "webui-0", "10.5.3.1"), 90); ex.check("webui-0 ping its gateway after restart (replay)", ok, det, w)
    teardown(ex); return ex

def wait_vyos_api(ex, pods, timeout=120):
    """Measures the gap between Ready and the guest HTTP API answering. With the
    API-aware readiness probe this must be ~0 s, since Ready already implies it."""
    for pod in pods:
        def f(pod=pod):
            rc, out = kexec(ex.ns, pod, "vyos_api retrieve '{\"op\":\"showConfig\",\"path\":[\"system\",\"host-name\"]}' >/dev/null 2>&1 && echo API_OK", timeout=40)
            return "API_OK" in out, out[-120:]
        ok, det, w = retry(f, timeout, every=3)
        ex.check(f"{pod} VyOS HTTP API answers right at Ready (gap should be ~0 s)", ok and w < 3, f"gap {w}s {det}", w)

def vyos_op(ns, pod, cmd, needle):
    def f():
        rc, out = kexec(ns, pod, f"ssh_qemu /opt/vyatta/bin/vyatta-op-cmd-wrapper {cmd}", timeout=40)
        return needle in out, out[-300:]
    return f

def ex6():
    d = f"{EX}/6-test-vyos"; ex = Example("6-test-vyos", "vyos"); setup(ex)
    upload_dir(ex, f"{d}/files")
    if deploy(ex, f"{d}/topology-network-test-vyos.json"):
        wait_vyos_api(ex, ["router-0", "router-1"])
        configure(ex, f"{d}/network_conf.json")
        ok, det, w = retry(vyos_op(ex.ns, "router-0", "show ip ospf neighbor", "Full"), 240); ex.check("router-0 OSPF Full with router-1", ok, det, w)
        for ip in ("10.0.1.10", "10.0.2.10", "10.0.4.10"):
            ok, det, w = retry(ping(ex.ns, "host-0", ip), 90); ex.check(f"host-0 ping {ip}", ok, det, w)
        ok, det, w = retry(contains(ex.ns, "host-0", "wget -qO- http://10.0.4.10 2>&1", "<"), 60); ex.check("host-0 wget web-server (mounted index.html)", ok, det, w)
        if restart(ex, "router-1"):
            ok, det, w = retry(ping(ex.ns, "host-0", "10.0.2.10"), 300); ex.check("host-0 ping 10.0.2.10 after router-1 restart (VyOS replay)", ok, det, w)
            ok, det, w = retry(vyos_op(ex.ns, "router-0", "show ip ospf neighbor", "Full"), 120); ex.check("router-0 OSPF Full again after router-1 restart", ok, det, w)
    teardown(ex); return ex


ALL = {"1": ex1, "2": ex2, "3": ex3, "4": ex4, "5": ex5, "6": ex6}


def main():
    global client
    ap = C.base_parser(__doc__)
    ap.add_argument("examples", nargs="*", help="example numbers to run (default all)")
    args = ap.parse_args()
    client = C.client_from(args)
    rec = C.Recorder("regression", client, args.out, args)
    results = []
    for k in args.examples or list(ALL):
        log(f"===== example {k} =====")
        try:
            results.append(ALL[k]())
        except Exception as e:  # noqa: BLE001 - one broken example must not stop the others
            log(f"  !! exception in example {k}: {e}")
    for r in results:
        for c in r.checks:
            rec.add(example=r.name, **c)
        rec.save(f"{r.name}.json", {"checks": r.checks, "timings": r.timings, "notes": r.notes})
    rec.finish({r.name: {"ok": sum(c["ok"] for c in r.checks), "checks": len(r.checks), "total_s": r.timings.get("total_s")} for r in results})
    print("\n===== SUMMARY =====")
    failed = 0
    for r in results:
        n_ok = sum(c["ok"] for c in r.checks)
        failed += len(r.checks) - n_ok
        print(f"{r.name}: {n_ok}/{len(r.checks)} checks ok, {r.timings.get('total_s')} s")
        for c in r.checks:
            if not c["ok"]:
                print(f"   FAIL {c['check']}: {c['detail'][:160]}")
        for n in r.notes:
            print(f"   note: {n}")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
