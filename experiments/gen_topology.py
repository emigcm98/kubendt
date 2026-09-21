#!/usr/bin/env python3
"""Synthetic topologies for the measurement scripts. Importable (the scripts build
theirs in memory) and a CLI to write the JSON files other tools consume.

Shapes:
  sparse  N nodes in two StatefulSet groups (node, newnode) and N links: a random
          spanning tree plus random extra edges, seeded, so the graph is connected,
          has average degree 2 and is the same on every run and every platform.
  ring    N nodes, N links, degree 2 everywhere.
  line    N nodes, N-1 links.
Every link gets its own /24 so ping works right after deploy, no configure needed."""
import argparse
import json
import os
import random

from common import HOST_IMAGE, SLEEP


def host_node(name, image=HOST_IMAGE, replicas=1, command=None, driver=None, **extra):
    node = {"name": name, "image": image, "type": "host", "replicas": replicas,
            "commands": list(command if command is not None else SLEEP)}
    if driver:
        node["driver"] = driver
    node.update({k: v for k, v in extra.items() if v is not None})
    return node


def link_ips(i):
    """One /24 per link, deterministic in the link index."""
    return f"10.{100 + i // 250}.{i % 250}.1/24", f"10.{100 + i // 250}.{i % 250}.2/24"


class IfaceCounter:
    def __init__(self):
        self.n = {}

    def next(self, pod):
        self.n[pod] = self.n.get(pod, 0) + 1
        return f"eth{self.n[pod]}"


def build_links(pairs):
    ifc = IfaceCounter()
    links = []
    for i, (a, b) in enumerate(pairs):
        ia, ib = link_ips(i)
        links.append({"node": a, "localIntf": ifc.next(a), "localIp": ia,
                      "peerNode": b, "peerIntf": ifc.next(b), "peerIp": ib})
    return links


def sparse_pairs(names, seed=1):
    rnd = random.Random(seed)
    order = names[:]
    rnd.shuffle(order)
    pairs, have = [], set()
    for i in range(1, len(order)):           # spanning tree: each node hangs off an earlier one
        a, b = order[i], order[rnd.randrange(i)]
        pairs.append((a, b))
        have.add(frozenset((a, b)))
    while len(pairs) < len(names):           # extra edges up to N links
        a, b = rnd.sample(names, 2)
        if frozenset((a, b)) not in have:
            pairs.append((a, b))
            have.add(frozenset((a, b)))
    return pairs


def sparse(n, image=HOST_IMAGE, command=None, seed=1, groups=("node", "newnode"), **node_extra):
    per = [n // len(groups) + (1 if i < n % len(groups) else 0) for i in range(len(groups))]
    nodes = [host_node(g, image, per[i], command, **node_extra) for i, g in enumerate(groups) if per[i]]
    names = [f"{g}-{k}" for i, g in enumerate(groups) for k in range(per[i])]
    return {"nodes": nodes, "links": build_links(sparse_pairs(names, seed))}


def ring(n, image=HOST_IMAGE, command=None, group="n", **node_extra):
    names = [f"{group}-{i}" for i in range(n)]
    pairs = [(names[i], names[(i + 1) % n]) for i in range(n)] if n > 2 else [(names[0], names[1])]
    return {"nodes": [host_node(group, image, n, command, **node_extra)], "links": build_links(pairs)}


def line(n, image=HOST_IMAGE, command=None, group="n", **node_extra):
    names = [f"{group}-{i}" for i in range(n)]
    return {"nodes": [host_node(group, image, n, command, **node_extra)],
            "links": build_links([(names[i], names[i + 1]) for i in range(n - 1)])}


def pair(image, a="a", b="b", node_a=None, node_b=None, command=None, ip_a="10.0.0.1/24", ip_b="10.0.0.2/24"):
    """Two single-replica nodes with one link, optionally pinned to workers."""
    return {"nodes": [host_node(a, image, 1, command, nodeName=node_a), host_node(b, image, 1, command, nodeName=node_b)],
            "links": [{"node": f"{a}-0", "localIntf": "eth1", "localIp": ip_a, "peerNode": f"{b}-0", "peerIntf": "eth1", "peerIp": ip_b}]}


def pod_names(topology):
    return [f"{n['name']}-{i}" for n in topology["nodes"] for i in range(n.get("replicas", 1))]


SHAPES = {"sparse": sparse, "ring": ring, "line": line}

if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--shape", choices=SHAPES, default="sparse")
    ap.add_argument("--sizes", type=int, nargs="+", default=[8, 16, 32, 64, 128])
    ap.add_argument("--image", default=HOST_IMAGE)
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--out", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "topologies"))
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)
    for n in a.sizes:
        kw = {"seed": a.seed} if a.shape == "sparse" else {}
        topo = SHAPES[a.shape](n, image=a.image, **kw)
        path = os.path.join(a.out, f"{a.shape}_{n}.json")
        with open(path, "w") as f:
            json.dump(topo, f, indent=1)
        degs = {}
        for l in topo["links"]:
            degs[l["node"]] = degs.get(l["node"], 0) + 1
            degs[l["peerNode"]] = degs.get(l["peerNode"], 0) + 1
        print(f"{path}: {len(pod_names(topo))} pods, {len(topo['links'])} links, degree {min(degs.values())}..{max(degs.values())}")
