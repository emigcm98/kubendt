"""Shared pieces for the experiment scripts: API client, kubectl helpers, timeline
math and result recording. Every script imports this and nothing else in the tree."""
import argparse
import csv
import datetime as dt
import json
import os
import re
import socket
import statistics
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
EXAMPLES = os.path.join(REPO, "deploy", "examples")
DEFAULT_OUT = os.path.join(HERE, "results")

# Images the synthetic topologies use. alpine ships a busybox `ip`, so the default
# readiness probe passes without installing anything and the pod is Ready as soon
# as the container runs. netshoot brings ping, iperf3 and tc for the data-plane runs.
HOST_IMAGE = "alpine:3.24.2"
TOOLS_IMAGE = "nicolaka/netshoot:v0.16"
FRR_IMAGE = "quay.io/frrouting/frr:10.7.1"
FRR_COMMAND = [
    "sh", "-c",
    "apk add --no-cache iptables && sed -i 's/^ospfd=no/ospfd=yes/' /etc/frr/daemons && "
    "touch /etc/frr/frr.conf /etc/frr/vtysh.conf && chown frr:frr /etc/frr/frr.conf /etc/frr/vtysh.conf && "
    "exec /sbin/tini -- /usr/lib/frr/docker-start",
]
SLEEP = ["sleep", "infinity"]


def log(msg):
    print(time.strftime("%H:%M:%S") + " " + msg, flush=True)


# ---------------------------------------------------------------- API client
class Client:
    """Thin wrapper over the REST API. Auth is a login cookie (KUBENDT_PASSWORD) or a
    bearer token (KUBENDT_TOKEN); with KUBENDT_AUTH_DISABLED on the backend neither is needed."""

    def __init__(self, base=None, password=None, token=None):
        self.base = (base or os.environ.get("KUBENDT_URL", "http://localhost:8080")).rstrip("/")
        self.cookie = None
        self.token = token or os.environ.get("KUBENDT_TOKEN")
        password = password or os.environ.get("KUBENDT_PASSWORD")
        if password and not self.token:
            self.login(password)

    def login(self, password):
        req = urllib.request.Request(self.base + "/auth/login", data=json.dumps({"password": password}).encode(), method="POST")
        req.add_header("Content-Type", "application/json")
        with urllib.request.urlopen(req, timeout=30) as r:
            cookies = r.headers.get_all("Set-Cookie") or []
        if not cookies:
            raise SystemExit("login returned no cookie")
        self.cookie = "; ".join(c.split(";", 1)[0] for c in cookies)

    def api(self, method, path, body=None, raw=None, ctype="application/json", timeout=900):
        data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
        req = urllib.request.Request(self.base + path, data=data, method=method)
        if data is not None:
            req.add_header("Content-Type", ctype)
        if self.cookie:
            req.add_header("Cookie", self.cookie)
        if self.token:
            req.add_header("Authorization", "Bearer " + self.token)
        try:
            with urllib.request.urlopen(req, timeout=timeout) as r:
                txt = r.read().decode()
                return r.status, (json.loads(txt) if txt.strip().startswith(("{", "[")) else txt)
        except urllib.error.HTTPError as e:
            txt = e.read().decode()
            try:
                return e.code, json.loads(txt)
            except ValueError:
                return e.code, txt
        except urllib.error.URLError as e:
            raise SystemExit(f"cannot reach {self.base}: {e.reason}")

    def timed(self, method, path, **kw):
        """Same as api() plus the client-side wall time in seconds."""
        t0 = time.time()
        st, r = self.api(method, path, **kw)
        return st, r, time.time() - t0

    # -- namespaces and topology
    def version(self):
        return self.api("GET", "/version")[1]

    def namespaces(self):
        st, r = self.api("GET", "/namespaces/")
        return {n["name"] for n in (r.get("namespaces") or [])} if st == 200 and isinstance(r, dict) else set()

    def create_namespace(self, ns):
        return self.api("POST", "/namespaces/", body={"namespace": ns})

    def delete_namespace(self, ns):
        return self.api("DELETE", f"/namespaces/{ns}")

    def clear_topology(self, ns):
        return self.api("DELETE", f"/network/clear-topology/{ns}")

    def fresh_namespace(self, ns):
        """Leave ns empty and enabled: clear it if it exists, create it otherwise."""
        if ns in self.namespaces():
            st, r = self.clear_topology(ns)
            if st != 200:
                raise SystemExit(f"clear-topology {ns}: {st} {r}")
        else:
            st, r = self.create_namespace(ns)
            if st != 200:
                raise SystemExit(f"create namespace {ns}: {st} {r}")

    def drop_namespace(self, ns):
        self.clear_topology(ns)
        self.delete_namespace(ns)

    def deploy(self, ns, topology):
        return self.timed("POST", f"/network/deploy-network/{ns}", body=topology)

    def modify(self, ns, body):
        return self.timed("POST", f"/network/modify-network/{ns}", body=body)

    def configure(self, ns, body):
        return self.timed("POST", f"/network/configure/{ns}", body=body)

    def restart(self, ns, pod):
        return self.timed("PATCH", f"/pods/restart/{ns}/{pod}")

    def get_network(self, ns):
        return self.api("GET", f"/network/get-network/{ns}")[1]

    def links(self, ns):
        st, r = self.api("GET", f"/network/links/{ns}")
        return (r.get("links") or []) if st == 200 else []

    def pods(self, ns):
        st, r = self.api("GET", f"/pods/{ns}")
        return (r.get("pods") or []) if st == 200 else []

    def history(self, ns, pod=None):
        path = f"/drivers/history/{ns}" + (f"/{pod}" if pod else "")
        return self.api("GET", path)[1]

    def qdisc(self, ns, pod, iface):
        return self.api("GET", f"/pods/tc/{ns}/{pod}/{iface}")[1]

    # -- telemetry endpoints the dashboard polls
    def ns_metrics(self, ns):
        return self.api("GET", f"/namespaces/metrics/{ns}")

    def pod_metrics(self, ns, pod):
        return self.api("GET", f"/pods/metrics/{ns}/{pod}")

    def ns_ips(self, ns):
        return self.api("GET", f"/namespaces/ips/{ns}")

    def ns_summary(self, ns):
        return self.api("GET", f"/namespaces/summary/{ns}")

    def cluster_status(self):
        return self.api("GET", "/cluster/status")

    # -- files
    def upload(self, ns, relpath, localfile):
        body, ct = multipart({"path": relpath}, os.path.basename(localfile), open(localfile, "rb").read())
        st, r = self.api("POST", f"/files/{ns}/", raw=body, ctype=ct)
        return st in (200, 201), r

    def import_zip(self, ns, zipfile):
        body, ct = multipart({}, os.path.basename(zipfile), open(zipfile, "rb").read())
        st, r = self.api("POST", f"/file-ops/{ns}/import", raw=body, ctype=ct)
        return st == 200, r


def multipart(fields, filename, content):
    bnd = "----kdt" + uuid.uuid4().hex
    parts = []
    for k, v in fields.items():
        parts.append(f"--{bnd}\r\nContent-Disposition: form-data; name=\"{k}\"\r\n\r\n{v}\r\n".encode())
    parts.append(f"--{bnd}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{filename}\"\r\n"
                 f"Content-Type: application/octet-stream\r\n\r\n".encode())
    parts.append(content)
    parts.append(f"\r\n--{bnd}--\r\n".encode())
    return b"".join(parts), f"multipart/form-data; boundary={bnd}"


def seconds(s):
    """'12.34s' from took_time to float. None when missing."""
    if s is None:
        return None
    if isinstance(s, (int, float)):
        return float(s)
    m = re.match(r"^\s*([0-9.]+)\s*s", str(s))
    return float(m.group(1)) if m else None


# ------------------------------------------------------------------- kubectl
CONTEXT = os.environ.get("KUBECTL_CONTEXT")


def kubectl(args, timeout=120, input_text=None):
    cmd = ["kubectl"] + (["--context", CONTEXT] if CONTEXT else []) + list(args)
    p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, input=input_text)
    return p.returncode, p.stdout, p.stderr


def kubectl_json(args, timeout=120):
    rc, out, err = kubectl(list(args) + ["-o", "json"], timeout)
    if rc != 0:
        raise RuntimeError(f"kubectl {' '.join(args)}: {err.strip()}")
    return json.loads(out)


def kexec(ns, pod, cmd, timeout=60, container=None):
    """Run a shell command inside a pod. Returns (rc, combined output)."""
    args = ["-n", ns, "exec", pod] + (["-c", container] if container else []) + ["--", "sh", "-c", cmd]
    try:
        rc, out, err = kubectl(args, timeout)
    except subprocess.TimeoutExpired:
        return 124, "timeout"
    # Once a pod carries an ephemeral toolbox container, kubectl prefixes every exec
    # with a "Defaulted container ..." notice on stderr. Not part of the command output.
    err = "\n".join(l for l in err.splitlines() if not l.startswith("Defaulted container"))
    return rc, (out + err).strip()


def pods_json(ns):
    return kubectl_json(["-n", ns, "get", "pods"]).get("items", [])


def pod_stamps(p):
    """Lifecycle stamps Kubernetes wrote on a Pod object, same fields as timeline.kubernetes."""
    cond = {c["type"]: c for c in p.get("status", {}).get("conditions", [])}
    cs = (p.get("status", {}).get("containerStatuses") or [{}])[0]
    running = (cs.get("state") or {}).get("running") or {}
    return {
        "pod": p["metadata"]["name"],
        "uid": p["metadata"]["uid"],
        "node": p.get("spec", {}).get("nodeName"),
        "created": p["metadata"].get("creationTimestamp"),
        "scheduled": cond.get("PodScheduled", {}).get("lastTransitionTime"),
        "sandbox_ready": cond.get("PodReadyToStartContainers", {}).get("lastTransitionTime"),
        "container_started": running.get("startedAt"),
        "ready": cond.get("Ready", {}).get("lastTransitionTime") if cond.get("Ready", {}).get("status") == "True" else None,
        "is_ready": cond.get("Ready", {}).get("status") == "True",
        "image": cs.get("image"),
        "image_id": cs.get("imageID"),
        "restarts": cs.get("restartCount", 0),
    }


def pod_uids(ns):
    return {p["metadata"]["name"]: p["metadata"]["uid"] for p in pods_json(ns)}


def wait_pods_ready(ns, names=None, timeout=600, every=2):
    t0 = time.time()
    while time.time() - t0 < timeout:
        items = {p["metadata"]["name"]: p for p in pods_json(ns)}
        want = names or list(items)
        if want and all(n in items and pod_stamps(items[n])["is_ready"] and not items[n]["metadata"].get("deletionTimestamp") for n in want):
            return True
        time.sleep(every)
    return False


def workers():
    """Worker node names, control plane excluded."""
    nodes = kubectl_json(["get", "nodes"]).get("items", [])
    out = []
    for n in nodes:
        labels = n["metadata"].get("labels", {})
        if "node-role.kubernetes.io/control-plane" in labels or "node-role.kubernetes.io/master" in labels:
            continue
        out.append(n["metadata"]["name"])
    return sorted(out)


def top_pods(ns):
    """kubectl top for a namespace: {pod: (cpu_millicores, memory_mib)}. Empty if metrics are unavailable."""
    rc, out, _ = kubectl(["-n", ns, "top", "pods", "--no-headers"])
    res = {}
    if rc != 0:
        return res
    for line in out.splitlines():
        parts = line.split()
        if len(parts) >= 3:
            res[parts[0]] = (parse_cpu_m(parts[1]), parse_mem_mi(parts[2]))
    return res


def top_nodes():
    rc, out, _ = kubectl(["top", "nodes", "--no-headers"])
    res = {}
    if rc != 0:
        return res
    for line in out.splitlines():
        parts = line.split()
        if len(parts) >= 5:
            res[parts[0]] = {"cpu_m": parse_cpu_m(parts[1]), "cpu_pct": parts[2].rstrip("%"), "mem_mi": parse_mem_mi(parts[3]), "mem_pct": parts[4].rstrip("%")}
    return res


def parse_cpu_m(s):
    s = s.strip()
    if s.endswith("m"):
        return int(s[:-1])
    if s.endswith("n"):
        return int(s[:-1]) / 1e6
    return float(s) * 1000


def parse_mem_mi(s):
    s = s.strip()
    units = {"Ki": 1 / 1024, "Mi": 1, "Gi": 1024, "M": 1, "G": 1024}
    for u, f in units.items():
        if s.endswith(u):
            return float(s[: -len(u)]) * f
    return float(s) / (1024 * 1024)


def pod_cpu_usec(ns, pod, container=None):
    """Cumulative CPU time of the pod's cgroup in microseconds (cgroup v2, v1 fallback).
    Two samples around a run give the CPU the pod burned during it, at full resolution,
    which kubectl top (15 s window) cannot."""
    rc, out = kexec(ns, pod, "cat /sys/fs/cgroup/cpu.stat 2>/dev/null || cat /sys/fs/cgroup/cpu/cpuacct.usage 2>/dev/null || cat /sys/fs/cgroup/cpuacct/cpuacct.usage", container=container)
    if rc != 0:
        return None
    m = re.search(r"usage_usec\s+(\d+)", out)
    if m:
        return int(m.group(1))
    m = re.match(r"^\s*(\d+)\s*$", out)
    return int(m.group(1)) / 1000 if m else None


# ------------------------------------------------------ background ping in a pod
SEQ_RE = re.compile(r"(?:icmp_)?seq=(\d+)")


def start_ping(ns, pod, target, interval=0.2):
    """Start a ping inside the pod and return its PID. Works with busybox and iputils."""
    rc, out = kexec(ns, pod, f"rm -f /tmp/kdt-ping.log; ping -i {interval} {target} > /tmp/kdt-ping.log 2>&1 & echo $!")
    pid = out.strip().splitlines()[-1] if out.strip() else ""
    return pid if pid.isdigit() else None


def stop_ping(ns, pod, pid, interval=0.2):
    """Stop the ping and summarize: packets sent and received, loss, longest gap in seconds."""
    kexec(ns, pod, f"kill -INT {pid} 2>/dev/null; sleep 0.5")
    rc, out = kexec(ns, pod, "cat /tmp/kdt-ping.log")
    seqs = [int(x) for x in SEQ_RE.findall(out)]
    sent = received = None
    m = re.search(r"(\d+) packets transmitted, (\d+) (?:packets )?received", out)
    if m:
        sent, received = int(m.group(1)), int(m.group(2))
    gaps = []
    if seqs:
        prev = seqs[0]
        for s in seqs[1:]:
            if s - prev > 1:
                gaps.append((s - prev - 1) * interval)
            prev = s
    res = {"sent": sent, "received": received, "replies": len(seqs), "max_gap_s": round(max(gaps), 2) if gaps else 0.0, "gaps": len(gaps)}
    if sent:
        res["loss_pct"] = round(100.0 * (sent - (received if received is not None else len(seqs))) / sent, 2)
    return res


# --------------------------------------------------------------- time helpers
def ts(s):
    if not s:
        return None
    s = s.replace("Z", "+00:00")
    return dt.datetime.fromisoformat(s)


def secs(a, b):
    """Seconds from stamp a to stamp b (RFC3339 strings). None if either is missing."""
    ta, tb = ts(a), ts(b)
    if ta is None or tb is None:
        return None
    return (tb - ta).total_seconds()


def spread(stamps):
    """max - min in seconds over a list of RFC3339 strings, ignoring missing ones."""
    vals = [ts(s) for s in stamps if s]
    return (max(vals) - min(vals)).total_seconds() if vals else None


def pod_phases(entry):
    """Per-pod phase durations from a timeline pod entry (or a pod_stamps dict).
    Kubernetes stamps have 1 s resolution, observed_ms are backend milliseconds."""
    k = entry.get("kubernetes", entry)
    o = entry.get("observed_ms", {}) or {}
    ph = {"pod": entry.get("pod")}
    ph["scheduling_s"] = secs(k.get("created"), k.get("scheduled"))
    ph["sandbox_cni_s"] = secs(k.get("scheduled"), k.get("sandbox_ready"))
    ph["container_start_s"] = secs(k.get("sandbox_ready"), k.get("container_started"))
    ph["scheduled_to_started_s"] = secs(k.get("scheduled"), k.get("container_started"))
    ph["readiness_s"] = secs(k.get("container_started"), k.get("ready"))
    ph["created_to_ready_s"] = secs(k.get("created"), k.get("ready"))
    if o.get("delete_issued") is not None and o.get("old_pod_gone") is not None:
        ph["termination_s"] = (o["old_pod_gone"] - o["delete_issued"]) / 1000
    if o.get("ready_seen") is not None:
        ph["ready_seen_s"] = o["ready_seen"] / 1000
        if o.get("container_started") is not None:
            ph["started_to_ready_seen_s"] = (o["ready_seen"] - o["container_started"]) / 1000
    return ph


def timeline_summary(tl):
    """Whole-operation figures from a timeline block: creation and readiness spread,
    critical path pod, detection lag of the backend."""
    pods = tl.get("pods") or []
    if not pods:
        return {}
    ks = [p.get("kubernetes", {}) for p in pods]
    out = {
        "pods": len(pods),
        "created_spread_s": spread([k.get("created") for k in ks]),
        "scheduled_spread_s": spread([k.get("scheduled") for k in ks]),
        "sandbox_spread_s": spread([k.get("sandbox_ready") for k in ks]),
        "ready_spread_s": spread([k.get("ready") for k in ks]),
        "created_to_last_ready_s": secs(min((k["created"] for k in ks if k.get("created")), default=None),
                                        max((k["ready"] for k in ks if k.get("ready")), default=None)),
    }
    last = max(pods, key=lambda p: ts(p.get("kubernetes", {}).get("ready")) or dt.datetime.min.replace(tzinfo=dt.timezone.utc))
    out["critical_pod"] = last.get("pod")
    seen = [p.get("observed_ms", {}).get("ready_seen") for p in pods if p.get("observed_ms", {}).get("ready_seen") is not None]
    out["last_ready_seen_s"] = max(seen) / 1000 if seen else None
    return out


# -------------------------------------------------------------- statistics
def summarize(values):
    vals = [v for v in values if v is not None]
    if not vals:
        return {"n": 0}
    return {
        "n": len(vals),
        "mean": round(statistics.mean(vals), 3),
        "std": round(statistics.stdev(vals), 3) if len(vals) > 1 else 0.0,
        "min": round(min(vals), 3),
        "max": round(max(vals), 3),
        "median": round(statistics.median(vals), 3),
    }


def affine_fit(xs, ys):
    """Least squares y = a + b x. Returns (a, b, r2)."""
    n = len(xs)
    if n < 2:
        return None
    mx, my = sum(xs) / n, sum(ys) / n
    sxx = sum((x - mx) ** 2 for x in xs)
    if sxx == 0:
        return None
    b = sum((x - mx) * (y - my) for x, y in zip(xs, ys)) / sxx
    a = my - b * mx
    ss_res = sum((y - (a + b * x)) ** 2 for x, y in zip(xs, ys))
    ss_tot = sum((y - my) ** 2 for y in ys)
    r2 = 1 - ss_res / ss_tot if ss_tot else 1.0
    return a, b, r2


def mean_std(values, digits=2):
    s = summarize(values)
    if not s.get("n"):
        return "-"
    return f"{s['mean']:.{digits}f} ± {s['std']:.{digits}f}" if s["n"] > 1 else f"{s['mean']:.{digits}f}"


def table(rows, cols, title=None):
    """Print rows (dicts) as an aligned text table."""
    if title:
        print("\n" + title)
    widths = {c: max(len(c), *(len(str(r.get(c, ""))) for r in rows)) for c in cols}
    print("  ".join(c.ljust(widths[c]) for c in cols))
    print("  ".join("-" * widths[c] for c in cols))
    for r in rows:
        print("  ".join(str(r.get(c, "")).ljust(widths[c]) for c in cols))


# ---------------------------------------------------------------- recording
def git_rev():
    try:
        return subprocess.run(["git", "-C", REPO, "rev-parse", "--short", "HEAD"], capture_output=True, text=True).stdout.strip()
    except OSError:
        return None


SECRET_ARGS = ("password", "token")


def redact_secrets(args_dict, argv):
    """meta.json travels with the results, so the credentials given on the command line must not be in it."""
    if args_dict is not None:
        args_dict = {k: ("<redacted>" if k in SECRET_ARGS and v else v) for k, v in args_dict.items()}
    out, hide_next = [], False
    for a in argv:
        if hide_next:
            out.append("<redacted>")
            hide_next = False
        elif a in {f"--{k}" for k in SECRET_ARGS}:
            out.append(a)
            hide_next = True
        elif any(a.startswith(f"--{k}=") for k in SECRET_ARGS):
            out.append(a.split("=", 1)[0] + "=<redacted>")
        else:
            out.append(a)
    return args_dict, out


class Recorder:
    """One directory per run under results/<experiment>/<timestamp>/ with meta.json,
    rows.jsonl (appended as the run goes, so a crash keeps what was measured), rows.csv
    at the end, and notes.log."""

    def __init__(self, experiment, client=None, out_dir=None, args=None):
        stamp = time.strftime("%Y%m%d-%H%M%S")
        self.dir = os.path.join(out_dir or os.environ.get("KUBENDT_EXPERIMENTS_OUT", DEFAULT_OUT), experiment, stamp)
        os.makedirs(self.dir, exist_ok=True)
        self.rows = []
        self._jsonl = open(os.path.join(self.dir, "rows.jsonl"), "a")
        self._notes = open(os.path.join(self.dir, "notes.log"), "a")
        safe_args, safe_argv = redact_secrets(vars(args) if args else None, sys.argv)
        meta = {
            "experiment": experiment,
            "started_at": dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds"),
            "argv": safe_argv,
            "args": safe_args,
            "kubendt_repo_commit": git_rev(),
            "kubectl_context": CONTEXT,
            "host": socket.gethostname(),
        }
        if client is not None:
            try:
                meta["backend_version"] = client.version()
                meta["backend_url"] = client.base
            except SystemExit as e:
                meta["backend_version"] = f"unavailable: {e}"
        try:
            nodes = kubectl_json(["get", "nodes"]).get("items", [])
            meta["cluster_nodes"] = [{
                "name": n["metadata"]["name"],
                "kubelet": n["status"]["nodeInfo"]["kubeletVersion"],
                "runtime": n["status"]["nodeInfo"]["containerRuntimeVersion"],
                "kernel": n["status"]["nodeInfo"]["kernelVersion"],
                "os": n["status"]["nodeInfo"]["osImage"],
                "cpu": n["status"]["allocatable"].get("cpu"),
                "memory": n["status"]["allocatable"].get("memory"),
                "roles": sorted(k.split("/", 1)[1] for k in n["metadata"].get("labels", {}) if k.startswith("node-role.kubernetes.io/")),
            } for n in nodes]
        except (RuntimeError, OSError, KeyError) as e:
            meta["cluster_nodes"] = f"unavailable: {e}"
        self.meta = meta
        self.save("meta.json", meta)
        log(f"results in {self.dir}")

    def add(self, **row):
        row.setdefault("t", round(time.time(), 3))
        self.rows.append(row)
        self._jsonl.write(json.dumps(row, default=str) + "\n")
        self._jsonl.flush()
        return row

    def note(self, msg):
        log(msg)
        self._notes.write(time.strftime("%H:%M:%S ") + msg + "\n")
        self._notes.flush()

    def save(self, name, obj):
        with open(os.path.join(self.dir, name), "w") as f:
            json.dump(obj, f, indent=1, default=str)

    def finish(self, summary=None):
        fields = []
        for r in self.rows:
            for k in r:
                if k not in fields:
                    fields.append(k)
        if self.rows:
            with open(os.path.join(self.dir, "rows.csv"), "w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=fields)
                w.writeheader()
                for r in self.rows:
                    w.writerow({k: (json.dumps(v) if isinstance(v, (dict, list)) else v) for k, v in r.items()})
        if summary is not None:
            self.save("summary.json", summary)
        self.meta["finished_at"] = dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")
        self.save("meta.json", self.meta)
        log(f"done, {len(self.rows)} rows in {self.dir}")
        return self.dir


# ------------------------------------------------------------------ CLI base
def base_parser(description):
    p = argparse.ArgumentParser(description=description, formatter_class=argparse.ArgumentDefaultsHelpFormatter)
    p.add_argument("--url", default=os.environ.get("KUBENDT_URL", "http://localhost:8080"), help="backend URL")
    p.add_argument("--password", default=os.environ.get("KUBENDT_PASSWORD"), help="admin password (login cookie)")
    p.add_argument("--token", default=os.environ.get("KUBENDT_TOKEN"), help="API token instead of a password")
    p.add_argument("--out", default=None, help="results root (default experiments/results)")
    p.add_argument("--runs", type=int, default=10, help="repetitions per case")
    p.add_argument("--keep", action="store_true", help="leave the namespace deployed at the end")
    return p


def client_from(args):
    return Client(args.url, args.password, args.token)


def run_or_die(st, r, what):
    if st != 200:
        raise SystemExit(f"{what} failed: {st} {json.dumps(r)[:400] if not isinstance(r, str) else r[:400]}")
    return r


def retry(fn, timeout, every=3):
    """fn -> (ok, detail). Poll until ok or timeout. Returns (ok, detail, waited_s)."""
    t0 = time.time()
    ok, det = False, ""
    while True:
        try:
            ok, det = fn()
        except Exception as e:  # noqa: BLE001 - the check itself is the thing under test
            ok, det = False, f"exception: {e}"
        if ok or time.time() - t0 > timeout:
            break
        time.sleep(every)
    return ok, det, round(time.time() - t0, 1)
