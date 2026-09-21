#!/usr/bin/env python3
"""Everything a reader needs to reproduce a measurement, in one JSON.

Records the KubeNDT backend version and repository commit, the Kubernetes server
and node versions with their runtime, kernel, OS and allocatable resources, the
Meshnet and metrics-server images with their digests, and for every image in the
given topology files (or every image running in --namespace): repository, tag, the
digest the workers actually run, compressed size from the registry manifest, the
unpacked size the node reports, the entrypoint and command from the image config,
and the readiness probe the pods carry. Local tool versions (kubectl, kind, docker,
containerlab, kne from KNE_SRC) are included when present.

Node-side digests and unpacked sizes come from each node's status.images, which the
kubelet caps at 50 entries, so run it right after the measurement while the images
are hot, or with --namespace, where the running pods' imageID is authoritative."""
import argparse
import json
import os
import re
import subprocess
import time
import urllib.error
import urllib.request

import common as C

ACCEPT = ", ".join([
    "application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json",
    "application/vnd.docker.distribution.manifest.v2+json", "application/vnd.oci.image.manifest.v1+json"])


def parse_ref(ref):
    """'quay.io/frrouting/frr:10.7.1' -> (registry, repository, tag)."""
    ref = ref.split("@")[0]
    parts = ref.split("/")
    if len(parts) == 1 or ("." not in parts[0] and ":" not in parts[0] and parts[0] != "localhost"):
        registry, path = "registry-1.docker.io", ref if len(parts) > 1 else "library/" + ref
    else:
        registry, path = parts[0], "/".join(parts[1:])
    tag = "latest"
    if ":" in path.split("/")[-1]:
        path, tag = path.rsplit(":", 1)
    return registry, path, tag


def registry_get(url, token=None, accept=None, raw=False):
    req = urllib.request.Request(url, headers={"User-Agent": "kubendt-inventory"})
    if token:
        req.add_header("Authorization", "Bearer " + token)
    if accept:
        req.add_header("Accept", accept)
    with urllib.request.urlopen(req, timeout=30) as r:
        body = r.read()
        return (body if raw else json.loads(body.decode()), dict(r.headers))


def registry_token(registry, repo):
    if registry == "registry-1.docker.io":
        return registry_get(f"https://auth.docker.io/token?service=registry.docker.io&scope=repository:{repo}:pull")[0]["token"]
    if registry == "ghcr.io":
        return registry_get(f"https://ghcr.io/token?scope=repository:{repo}:pull")[0]["token"]
    if registry == "quay.io":
        try:
            return registry_get(f"https://quay.io/v2/auth?service=quay.io&scope=repository:{repo}:pull")[0]["token"]
        except (urllib.error.HTTPError, KeyError):
            return None
    return None


def image_from_registry(ref, want_digest=None):
    """Manifest digest, compressed size (sum of layers) and config (entrypoint, cmd) for the
    amd64 image behind ref. When the nodes report a digest, that manifest is fetched."""
    registry, repo, tag = parse_ref(ref)
    if registry == "localhost":
        return {"registry": "local build", "repository": repo, "tag": tag}
    out = {"registry": registry, "repository": repo, "tag": tag}
    try:
        token = registry_token(registry, repo)
        base = f"https://{registry}/v2/{repo}"
        m, h = registry_get(f"{base}/manifests/{tag}", token, ACCEPT)
        out["tag_digest"] = h.get("Docker-Content-Digest") or h.get("docker-content-digest")
        if "manifests" in m:  # multi-arch index: pick amd64/linux
            entry = next((x for x in m["manifests"] if x.get("platform", {}).get("architecture") == "amd64" and x.get("platform", {}).get("os") == "linux"), m["manifests"][0])
            out["amd64_manifest_digest"] = entry["digest"]
            m, h = registry_get(f"{base}/manifests/{entry['digest']}", token, ACCEPT)
        layers = m.get("layers") or []
        out["compressed_size_mib"] = round(sum(l.get("size", 0) for l in layers) / 1024 / 1024, 1)
        out["layers"] = len(layers)
        cfg_digest = (m.get("config") or {}).get("digest")
        if cfg_digest:
            cfg, _ = registry_get(f"{base}/blobs/{cfg_digest}", token)
            c = cfg.get("config") or {}
            out["entrypoint"] = c.get("Entrypoint")
            out["cmd"] = c.get("Cmd")
            out["created"] = cfg.get("created")
    except (urllib.error.URLError, KeyError, ValueError, StopIteration) as e:
        out["registry_error"] = str(e)[:200]
    return out


def node_images():
    """{image ref: (digest ref, size bytes)} from every node's status.images."""
    out = {}
    for n in C.kubectl_json(["get", "nodes"]).get("items", []):
        for img in n.get("status", {}).get("images", []):
            names = img.get("names") or []
            digests = [x for x in names if "@sha256:" in x]
            tags = [x for x in names if "@sha256:" not in x]
            for t in tags:
                out[t] = {"digest": digests[0] if digests else None, "unpacked_size_mib": round(img.get("sizeBytes", 0) / 1024 / 1024, 1), "node": n["metadata"]["name"]}
    return out


def tool(cmd):
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=20)
        return (p.stdout or p.stderr).strip().splitlines()[0][:120] if (p.stdout or p.stderr).strip() else None
    except (OSError, subprocess.TimeoutExpired):
        return None


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--url", default=os.environ.get("KUBENDT_URL", "http://localhost:8080"))
    ap.add_argument("--password", default=os.environ.get("KUBENDT_PASSWORD"))
    ap.add_argument("--token", default=os.environ.get("KUBENDT_TOKEN"))
    ap.add_argument("--topologies", nargs="*", default=[], help="topology JSON files whose images to describe")
    ap.add_argument("--namespace", help="describe the images and probes of the pods running here")
    ap.add_argument("--out", default=None, help="output file (default results/inventory/<timestamp>.json)")
    args = ap.parse_args()
    client = C.Client(args.url, args.password, args.token)
    inv = {"taken_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"), "kubendt": {}, "cluster": {}, "images": {}, "tools": {}}

    try:
        inv["kubendt"] = {"version_endpoint": client.version(), "repo_commit": C.git_rev(), "backend_url": client.base}
    except SystemExit as e:
        inv["kubendt"] = {"error": str(e), "repo_commit": C.git_rev()}
    rc, out, _ = C.kubectl(["version", "-o", "json"])
    try:
        v = json.loads(out)
        inv["cluster"]["server_version"] = v.get("serverVersion", {}).get("gitVersion")
        inv["cluster"]["kubectl_version"] = v.get("clientVersion", {}).get("gitVersion")
    except ValueError:
        inv["cluster"]["server_version"] = out.strip()[:200]
    inv["cluster"]["context"] = C.CONTEXT or tool(["kubectl", "config", "current-context"])
    nodes = C.kubectl_json(["get", "nodes"]).get("items", [])
    inv["cluster"]["nodes"] = [{
        "name": n["metadata"]["name"], "roles": sorted(k.split("/", 1)[1] for k in n["metadata"].get("labels", {}) if k.startswith("node-role.kubernetes.io/")),
        "kubelet": n["status"]["nodeInfo"]["kubeletVersion"], "runtime": n["status"]["nodeInfo"]["containerRuntimeVersion"],
        "kernel": n["status"]["nodeInfo"]["kernelVersion"], "os": n["status"]["nodeInfo"]["osImage"],
        "capacity": {k: n["status"]["capacity"].get(k) for k in ("cpu", "memory", "pods")},
        "allocatable": {k: n["status"]["allocatable"].get(k) for k in ("cpu", "memory", "pods")},
        "kvm_label": {k: v for k, v in n["metadata"].get("labels", {}).items() if "kvm" in k} or None,
    } for n in nodes]
    inv["cluster"]["daemonsets"] = {}
    for ds in C.kubectl_json(["get", "ds", "-A"]).get("items", []):
        name = f"{ds['metadata']['namespace']}/{ds['metadata']['name']}"
        if any(k in name for k in ("meshnet", "flannel", "kindnet", "calico", "cilium", "kube-proxy")):
            inv["cluster"]["daemonsets"][name] = ds["spec"]["template"]["spec"]["containers"][0]["image"]
    for d in C.kubectl_json(["get", "deploy", "-A"]).get("items", []):
        if "metrics-server" in d["metadata"]["name"]:
            inv["cluster"]["metrics_server"] = d["spec"]["template"]["spec"]["containers"][0]["image"]
    running = {}
    for p in C.kubectl_json(["get", "pods", "-A"]).get("items", []):
        for cs in p.get("status", {}).get("containerStatuses", []) or []:
            if any(k in p["metadata"]["namespace"] for k in ("meshnet", "kube-system")) and any(k in cs.get("name", "") for k in ("meshnet", "metrics-server", "flannel", "kindnet")):
                running[f"{p['metadata']['namespace']}/{cs['name']}"] = cs.get("imageID")
    inv["cluster"]["infra_image_digests"] = running

    refs, probes = {}, {}
    for f in args.topologies:
        for node in json.load(open(f)).get("nodes", []):
            refs.setdefault(node["image"], set()).add(os.path.basename(f))
    if args.namespace:
        for p in C.pods_json(args.namespace):
            for c in p["spec"]["containers"]:
                refs.setdefault(c["image"], set()).add(f"{args.namespace}/{p['metadata']['name']}")
                rp = c.get("readinessProbe")
                if rp:
                    probes[c["image"]] = {"exec": (rp.get("exec") or {}).get("command"), "initialDelaySeconds": rp.get("initialDelaySeconds", 0),
                                          "periodSeconds": rp.get("periodSeconds"), "failureThreshold": rp.get("failureThreshold"), "timeoutSeconds": rp.get("timeoutSeconds")}
                if c.get("command") or c.get("args"):
                    probes.setdefault(c["image"], {})["pod_command"] = (c.get("command") or []) + (c.get("args") or [])
    on_nodes = node_images()
    for ref, used in sorted(refs.items()):
        info = image_from_registry(ref)
        node_ref = on_nodes.get(ref) or on_nodes.get(ref.replace("docker.io/", "")) or on_nodes.get("docker.io/" + ref) or on_nodes.get("docker.io/library/" + ref)
        if node_ref:
            info["node_digest"] = node_ref["digest"]
            info["unpacked_size_mib"] = node_ref["unpacked_size_mib"]
        if ref in probes:
            info["readiness_probe"] = {k: v for k, v in probes[ref].items() if k != "pod_command"}
            if probes[ref].get("pod_command"):
                info["pod_command"] = probes[ref]["pod_command"]
        info["used_by"] = sorted(used)
        inv["images"][ref] = info
        C.log(f"{ref}: digest {info.get('node_digest') or info.get('tag_digest')} compressed {info.get('compressed_size_mib')} MiB unpacked {info.get('unpacked_size_mib')} MiB")

    inv["tools"] = {k: v for k, v in {
        "kubectl": inv["cluster"].get("kubectl_version"), "kind": tool(["kind", "version"]), "docker": tool(["docker", "--version"]),
        "containerlab": tool(["containerlab", "version"]) and re.sub(r"\s+", " ", subprocess.run(["containerlab", "version"], capture_output=True, text=True).stdout).strip()[-200:],
        "kne_source": tool(["git", "-C", os.environ.get("KNE_SRC", "/nonexistent"), "log", "-1", "--format=%h %cs %s"]),
        "python": tool(["python3", "--version"]),
    }.items() if v}

    out = args.out or os.path.join(C.DEFAULT_OUT, "inventory", time.strftime("%Y%m%d-%H%M%S") + ".json")
    os.makedirs(os.path.dirname(out), exist_ok=True)
    with open(out, "w") as f:
        json.dump(inv, f, indent=1, default=str)
    C.log(f"written {out}")
    print(json.dumps({"kubendt": inv["kubendt"], "cluster": {k: v for k, v in inv["cluster"].items() if k != "nodes"}, "nodes": len(inv["cluster"]["nodes"]), "images": len(inv["images"]), "tools": inv["tools"]}, indent=1, default=str))


if __name__ == "__main__":
    main()
