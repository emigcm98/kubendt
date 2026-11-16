"""Real 5G stack on KubeNDT: Open5GS core + UERANSIM gNBs and UEs, as nodes of type `gnb` and `ue`.

Derived from KubeNDT's example 5 (5-test-full-open5gs), with three changes:
  * arm64 images (Open5GS built from herlesupreeth/docker_open5gs, UERANSIM 3.2.6);
  * one flat core network: every network function, the UPF and the N2/N3 side of the gNBs hang off one
    switch, no external uplink, no routers, no WebUI;
  * S gNBs and M UEs, every UE pre-wired to every gNB with its own link (the radio links). A gNB's radio
    address lives on its loopback and each UE routes to it over the link to that gNB, so shaping or
    cutting a link is shaping or cutting that UE's radio link to that gNB.

Usage: g5_stack.py --gnbs 2 --ues 2            deploy and verify registration + PDU session + ping
"""
import argparse
import io
import json
import re
import shutil
import sys
import time
import zipfile
from pathlib import Path

KR = Path.home() / "kubendt-radio"
sys.path.insert(0, str(KR / "src" / "kubendt" / "experiments"))
import common as C  # noqa: E402

EX5 = KR / "src" / "kubendt" / "deploy" / "examples" / "5-test-full-open5gs"
OPEN5GS_IMAGE = "docker_open5gs:arm64"
UERANSIM_IMAGE = "gradiant/ueransim:3.2.6"
NS = "radio-5g"
UPF_IP = "10.5.0.20"
UE_POOL_GW = "192.168.10.1"
MCC, MNC = "999", "30"
KEY, OPC = "465B5CE8B199B49FAA5F0A2EE238A6BC", "E8ED289DEBA952E4283B54E88E6183CA"

GNB_YAML = """mcc: '${MCC}'
mnc: '${MNC}'
nci: '${NCI}'
idLength: 32
tac: ${TAC}

linkIp: ${LINK_IP}
ngapIp: ${N2_IP}
gtpIp: ${N2_IP}
gtpAdvertiseIp: ${N2_IP}

amfConfigs:
  - address: ${AMF_IP}
    port: 38412

slices:
  - sst: 0x1

ignoreStreamIds: true
"""

GNB_INIT = """#!/bin/bash
# One gNB per replica: the index in the pod name gives it its cell identity and its radio address.
IDX=${HOSTNAME##*-}
echo "[gnb] waiting for the N2/N3 address on eth1"
while true; do
    N2_IP=$(ip -4 addr show eth1 2>/dev/null | awk '/inet /{print $2}' | cut -d/ -f1 | head -1)
    [ -n "$N2_IP" ] && break
    sleep 1
done
export N2_IP
export LINK_IP="10.70.${IDX}.1"
ip addr add ${LINK_IP}/32 dev lo 2>/dev/null
export NCI=$(printf '0x%09x' $(( (IDX + 1) * 16 )))
echo "[gnb] index $IDX  N2/N3 $N2_IP  radio $LINK_IP  nci $NCI  amf $AMF_IP"
envsubst < /mnt/gnb/gnb.yaml > /tmp/gnb.yaml
until ping -c1 -W1 "$AMF_IP" > /dev/null 2>&1; do sleep 2; done
while true; do
    nr-gnb -c /tmp/gnb.yaml
    echo "[gnb] nr-gnb exited, restarting in 5 s"
    sleep 5
done
"""

UE_YAML_HEAD = """supi: 'imsi-${MCC}${MNC}${MSISDN}'
mcc: '${MCC}'
mnc: '${MNC}'
key: '${KEY}'
op: '${OP}'
opType: 'OPC'
amf: '8000'
imei: '356938035643803'
imeiSv: '4370816125816151'
"""

UE_YAML_TAIL = """uacAic:
  mps: false
  mcs: false
uacAcc:
  normalClass: 0
  class11: false
  class12: false
  class13: false
  class14: false
  class15: false
sessions:
  - type: 'IPv4'
    apn: 'internet'
    slice:
      sst: 0x1
configured-nssai:
  - sst: 0x1
default-nssai:
  - sst: 0x1
integrity:
  IA1: true
  IA2: true
  IA3: true
ciphering:
  EA1: true
  EA2: true
  EA3: true
integrityMaxRate:
  uplink: 'full'
  downlink: 'full'
"""

UE_INIT = """#!/bin/bash
# One UE per replica: the index in the pod name gives it its IMSI. It is wired to every gNB; the route to
# a gNB's radio address goes over the link to that gNB.
IDX=${HOSTNAME##*-}
export MSISDN=$(printf '%010d' $(( IDX + 1 )))
echo "[ue] index $IDX  imsi ${MCC}${MNC}${MSISDN}  gNBs ${NUM_GNBS}"
for s in $(seq 0 $(( NUM_GNBS - 1 ))); do
    IF="eth$(( s + 1 ))"
    until ip -4 addr show "$IF" 2>/dev/null | grep -q "inet "; do sleep 1; done
    ip route replace "10.70.${s}.1/32" via "10.$(( 60 + s )).${IDX}.1" dev "$IF"
done
{
    envsubst < /mnt/ue/ue_head.yaml
    echo "gnbSearchList:"
    for s in $(seq 0 $(( NUM_GNBS - 1 ))); do echo "  - 10.70.${s}.1"; done
    cat /mnt/ue/ue_tail.yaml
} > /tmp/ue.yaml
while true; do
    nr-ue -c /tmp/ue.yaml
    echo "[ue] nr-ue exited, restarting in 5 s"
    sleep 5
done
"""


def build_zip(gnbs):
    """The namespace files: example 5's Open5GS files plus our gNB and UE ones."""
    src = KR / "fiveg" / "files"
    if src.exists():
        shutil.rmtree(src)
    with zipfile.ZipFile(EX5 / "open5gs_files.zip") as z:
        z.extractall(src)
    for d in ("webui", "gnb", "ue"):
        shutil.rmtree(src / d, ignore_errors=True)
    (src / "gnb").mkdir()
    (src / "ue").mkdir()
    (src / "gnb" / "gnb.yaml").write_text(GNB_YAML)
    (src / "gnb" / "gnb_init.sh").write_text(GNB_INIT)
    (src / "ue" / "ue_head.yaml").write_text(UE_YAML_HEAD)
    (src / "ue" / "ue_tail.yaml").write_text(UE_YAML_TAIL)
    (src / "ue" / "ue_init.sh").write_text(UE_INIT)
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as z:
        for f in sorted(src.rglob("*")):
            if f.is_file():
                z.write(f, f.relative_to(src).as_posix())
    out = KR / "fiveg" / "files.zip"
    out.write_bytes(buf.getvalue())
    return out


def topology(gnbs, ues):
    ex = json.loads((EX5 / "topology-network-test-big.json").read_text())
    keep = ("amf", "smf", "nrf", "scp", "ausf", "udm", "udr", "pcf", "udsf", "upf")
    nodes, links = [], []
    for n in ex["nodes"]:
        if n["name"] not in keep:
            continue
        n = json.loads(json.dumps(n))
        if "docker_open5gs" in n["image"]:
            n["image"] = OPEN5GS_IMAGE
        env = n.get("env", {})
        if "UPF_IP" in env:
            env["UPF_IP"] = UPF_IP
        if "UPF_ADVERTISE_IP" in env:
            env["UPF_ADVERTISE_IP"] = UPF_IP
        if n["name"] == "amf":
            env["MAX_NUM_UE"] = "64"
        nodes.append(n)
    nodes.append(dict(name="sw", image="alpine:3.24.2", type="switch", replicas=1))
    port = 1
    for l in ex["links"]:
        if l["node"] in keep and l["node"] != "upf" and l["peerNode"] == "switch-0":
            links.append(dict(node=l["node"], localIntf="eth1", localIp=l["localIp"], peerNode="sw-0",
                              peerIntf=f"eth{port}", peerIp=""))
            port += 1
    links.append(dict(node="upf", localIntf="eth1", localIp=f"{UPF_IP}/24", peerNode="sw-0", peerIntf=f"eth{port}", peerIp=""))
    port += 1
    nodes.append(dict(name="gnb", image=UERANSIM_IMAGE, type="gnb", replicas=gnbs,
                      commands=["sh", "-c", "bash /mnt/gnb/gnb_init.sh"],
                      env=dict(AMF_IP="10.5.0.10", MCC=MCC, MNC=MNC, TAC="1"),
                      mounts=[dict(file="gnb/gnb.yaml", mountTo="/mnt/gnb/gnb.yaml"),
                              dict(file="gnb/gnb_init.sh", mountTo="/mnt/gnb/gnb_init.sh")]))
    nodes.append(dict(name="ue", image=UERANSIM_IMAGE, type="ue", replicas=ues,
                      commands=["sh", "-c", "bash /mnt/ue/ue_init.sh"],
                      env=dict(MCC=MCC, MNC=MNC, KEY=KEY, OP=OPC, NUM_GNBS=str(gnbs)),
                      mounts=[dict(file="ue/ue_head.yaml", mountTo="/mnt/ue/ue_head.yaml"),
                              dict(file="ue/ue_tail.yaml", mountTo="/mnt/ue/ue_tail.yaml"),
                              dict(file="ue/ue_init.sh", mountTo="/mnt/ue/ue_init.sh")],
                      devices=[dict(path="/dev/net/tun")]))
    for s in range(gnbs):
        links.append(dict(node=f"gnb-{s}", localIntf="eth1", localIp=f"10.5.0.{30 + s}/24", peerNode="sw-0",
                          peerIntf=f"eth{port}", peerIp="", name="N2-N3"))
        port += 1
    for k in range(ues):
        for s in range(gnbs):
            links.append(dict(node=f"ue-{k}", localIntf=f"eth{s + 1}", localIp=f"10.{60 + s}.{k}.2/24",
                              peerNode=f"gnb-{s}", peerIntf=f"eth{k + 2}", peerIp=f"10.{60 + s}.{k}.1/24", name="radio"))
    return dict(nodes=nodes, links=links), port - 1


def subscriber_cmd(k):
    imsi = f"{MCC}{MNC}{k + 1:010d}"
    doc = ('{imsi:"%s",subscribed_rau_tau_timer:12,network_access_mode:0,subscriber_status:0,operator_determined_barring:0,'
           'access_restriction_data:32,msisdn:[],imeisv:[],mme_host:[],mme_realm:[],purge_flag:[],'
           'security:{k:"%s",amf:"8000",op:null,opc:"%s"},'
           'slice:[{sst:1,default_indicator:true,session:[{name:"internet",type:3,qos:{index:9,arp:{priority_level:8,'
           'pre_emption_capability:1,pre_emption_vulnerability:1}},ambr:{downlink:{value:1,unit:3},uplink:{value:1,unit:3}},pcc_rule:[]}]}],'
           'ambr:{downlink:{value:1,unit:3},uplink:{value:1,unit:3}},schema_version:1,__v:0}') % (imsi, KEY, OPC)
    return f"mongosh open5gs --quiet --eval 'db.subscribers.deleteOne({{imsi:\"{imsi}\"}}); db.subscribers.insertOne({doc})'"


def client_():
    pw = (KR / ".kubendt_password").read_text().strip()
    return C.Client("http://127.0.0.1:8088/api", password=pw)


def ue_status(k):
    rc, out = C.kexec(NS, f"ue-{k}", "ip -4 -o addr show uesimtun0 2>/dev/null | awk '{print $4}'")
    ip = out.strip().split("/")[0] if rc == 0 and out.strip() else None
    rc, st = C.kexec(NS, f"ue-{k}", f"nr-cli imsi-{MCC}{MNC}{k + 1:010d} -e status 2>/dev/null | egrep 'cm-state|rm-state|mm-state|current-cell|camped' | tr -s ' ' | tr '\\n' ';'")
    return ip, st.strip()


def deploy(a):
    client = client_()
    res = dict(version=client.version(), gnbs=a.gnbs, ues=a.ues)
    client.fresh_namespace(NS)
    ok_, r = client.import_zip(NS, str(build_zip(a.gnbs)))
    if not ok_:
        raise SystemExit(f"file import failed: {str(r)[:300]}")
    topo, sw_ports = topology(a.gnbs, a.ues)
    (KR / "fiveg" / "topology.json").write_text(json.dumps(topo, indent=1))
    st, r, wall = client.deploy(NS, topo)
    C.run_or_die(st, r, "deploy")
    res["deploy_s"] = wall
    res["pods"] = len(C.pods_json(NS))
    res["links"] = len(topo["links"])
    C.log(f"deployed {res['pods']} pods, {res['links']} links in {wall:.1f} s")
    targets = [dict(pod="sw-0", actions=[dict(type="setup_bridge", bridge="br0", ifaces=[f"eth{i}" for i in range(1, sw_ports + 1)])]),
               dict(pod="udsf-0", actions=[dict(type="custom", command=subscriber_cmd(k)) for k in range(a.ues)])]
    st, r, _ = client.configure(NS, dict(targets=targets))
    if st != 200 or r.get("failures"):
        raise SystemExit("configure failed: " + json.dumps(r)[:800])
    t0 = time.time()
    ok = {}
    while time.time() - t0 < a.wait and len(ok) < a.ues:
        time.sleep(5)
        for k in range(a.ues):
            if k not in ok:
                ip, st_ = ue_status(k)
                if ip:
                    ok[k] = dict(ip=ip, after_s=round(time.time() - t0, 1), status=st_)
                    C.log(f"ue-{k} has a PDU session: {ip} after {ok[k]['after_s']} s; {st_}")
    res["pdu_sessions"] = ok
    res["registered"] = len(ok)
    for k in ok:
        rc, out = C.kexec(NS, f"ue-{k}", f"ping -c 5 -i 0.2 -W 1 -q -I uesimtun0 {UE_POOL_GW}")
        ok[k]["ping_upf"] = out.splitlines()[-2:] if out else None
        C.log(f"ue-{k} ping through the 5G tunnel: {ok[k]['ping_upf']}")
    for name in ("amf-0", "gnb-0", "ue-0"):
        rc, out = C.kexec(NS, name, "true")
    rc, out, _ = C.kubectl(["-n", NS, "logs", "gnb-0", "--tail", "12"])
    res["gnb0_log_tail"] = out.splitlines()[-12:]
    rc, out, _ = C.kubectl(["-n", NS, "logs", "ue-0", "--tail", "15"])
    res["ue0_log_tail"] = out.splitlines()[-15:]
    (KR / "results" / "g5_deploy.json").write_text(json.dumps(res, indent=1))
    print(json.dumps({k: v for k, v in res.items() if not k.endswith("_tail")}, indent=1))
    print("--- gnb-0\n" + "\n".join(res["gnb0_log_tail"]))
    print("--- ue-0\n" + "\n".join(res["ue0_log_tail"]))
    print("G5_DEPLOY_DONE", flush=True)


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--gnbs", type=int, default=2)
    ap.add_argument("--ues", type=int, default=2)
    ap.add_argument("--wait", type=int, default=240)
    deploy(ap.parse_args())
