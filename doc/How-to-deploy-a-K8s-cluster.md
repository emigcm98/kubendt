# Cluster Bootstrap Guide for KubeNDT

This document provides a practical and formal procedure to prepare a Kubernetes cluster for KubeNDT in four scenarios:

1. Single-node lab with Minikube
2. Multi-node local cluster with kind (one control-plane, two workers)
3. Two-node production-style cluster with kubeadm
4. Single-node k3s

The same Meshnet installation method is used in all scenarios. Each option below tells you when to install it, always after the cluster is created (see §4.2, §5.3, §6.5 and §7.2), and on k3s with one patch. For reference, the method is always:

```bash
git clone https://github.com/networkop/meshnet-cni.git
cd meshnet-cni/
kubectl apply -k manifests/base
```

## 1. Scope and Assumptions

This guide focuses on cluster preparation. KubeNDT application startup is included at the end.

KubeNDT runs inside the cluster it manages or outside it, with the compose. Outside, the backend reads the kubeconfig mounted from the host and starts on its current context, so the active kubectl context on your machine is the cluster that KubeNDT will manage.

## 2. Common Requirements

- Linux host (or Linux VMs for kubeadm)
- Kubernetes 1.25+ (ephemeral containers, which the toolbox needs; the rest of what the backend uses is older, the table in `doc/DEPLOYMENT.md` says what and since when); 1.30 or newer recommended, since pod execs go over WebSocket there and an older API server falls back to SPDY, about a third slower per exec
- kubectl configured and working
- Docker and Docker Compose, to run KubeNDT outside the cluster
- Git (to clone meshnet-cni)
- Minimum recommended lab resources: 4 vCPU, 8 GB RAM
- One of the following, depending on the option you pick:
  - [Minikube](https://minikube.sigs.k8s.io/docs/start/) (Option 1)
  - [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) (Option 2)
  - [kubeadm](https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/install-kubeadm/) (Option 3)
  - [k3s](https://docs.k3s.io/quick-start) (Option 4)

## 3. Mandatory Validation Checks (All Options)

Run these checks after cluster creation and after Meshnet installation:

```bash
kubectl get nodes -o wide
kubectl get pods -A
kubectl get ds -A | grep -i meshnet
kubectl get crd | grep -E 'topologies.networkop.co.uk|network-attachment-definitions.k8s.cni.cncf.io'
kubectl api-resources | grep -i topology
```

Expected outcome:

- All nodes are Ready
- Meshnet workloads are running
- Topology CRD is registered and discoverable

## 4. Option 1: Single Node with Minikube

Use this option for quick functional validation of KubeNDT.

### 4.1 Create cluster

```bash
minikube start --driver=docker --cpus=4 --memory=8192 --disk-size=30g \
  --extra-config=kubelet.allowed-unsafe-sysctls="net.ipv4.ip_forward,net.ipv6.conf.all.forwarding"
kubectl config current-context
kubectl get nodes -o wide
```

Verify current context is minikube before proceeding.

> **Why `--extra-config=kubelet.allowed-unsafe-sysctls`?** KubeNDT router and switch pods set `net.ipv4.ip_forward` and `net.ipv6.conf.all.forwarding` via pod security context `sysctls`, so they forward both families. Without this flag the kubelet will reject those pods with `SysctlForbidden`.

### 4.2 Install Meshnet (required)

```bash
git clone https://github.com/networkop/meshnet-cni.git
cd meshnet-cni/
kubectl apply -k manifests/base
```

Then validate:

```bash
kubectl get pods -A | grep -i meshnet
kubectl get ds -A | grep -i meshnet
kubectl get crd | grep -i topologies.networkop.co.uk
```

### 4.3 Ready state

When the checks pass, the cluster is ready for KubeNDT, installed in it or run outside it (§9).

For KubeNDT outside the cluster, the compose's backend has to reach the API server, which listens on minikube's own Docker network. Add `--embed-certs --apiserver-ips=<host IP> --listen-address=0.0.0.0` to the `minikube start` of §4.1, then point the kubeconfig at the port Docker publishes on the host. Run it after each `minikube start`, which writes minikube's entry in the kubeconfig again.

```bash
kubectl config set-cluster minikube --server=https://<host IP>:$(docker port minikube 8443 | cut -d: -f2)
```

`--embed-certs` puts the credentials in the kubeconfig the compose mounts, which otherwise names files under `~/.minikube`.

> Optional: to see per-pod CPU and RAM in the UI, install metrics-server (see §11).

## 5. Option 2: Multi-node cluster with kind

Use this option for reproducible multi-node local testing. The example below creates one control-plane node and two worker nodes. A multi-worker cluster is recommended over a single node because it lets you validate that KubeNDT topologies work **inter-node**: meshnet stitches links between pods that the scheduler places on different workers, so this layout exercises the cross-node data plane that a single-node cluster cannot.

> You can add more workers by appending additional `- role: worker` blocks with the same `kubeadmConfigPatches`. Two workers is the minimum to observe inter-node behaviour.

### 5.1 Create cluster definition

Create file kind-cluster.yaml:

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: kubendt
networking:
  # Bind the API server to a routable host IP instead of 127.0.0.1 so the
  # kubeconfig KubeNDT mounts can reach the cluster from its container, and so
  # the API server certificate is valid for that address. kind adds this
  # address to the cert SANs automatically.
  apiServerAddress: "192.168.1.50" # replace with your host IP (see note)
  apiServerPort: 6443
nodes:
  - role: control-plane
    kubeadmConfigPatches:
      - |
        kind: InitConfiguration
        nodeRegistration:
          kubeletExtraArgs:
            allowed-unsafe-sysctls: "net.ipv4.ip_forward,net.ipv6.conf.all.forwarding"
  - role: worker
    kubeadmConfigPatches:
      - |
        kind: JoinConfiguration
        nodeRegistration:
          kubeletExtraArgs:
            allowed-unsafe-sysctls: "net.ipv4.ip_forward,net.ipv6.conf.all.forwarding"
  - role: worker
    kubeadmConfigPatches:
      - |
        kind: JoinConfiguration
        nodeRegistration:
          kubeletExtraArgs:
            allowed-unsafe-sysctls: "net.ipv4.ip_forward,net.ipv6.conf.all.forwarding"
```

> The `kubeletExtraArgs` entries are required for KubeNDT router and switch nodes, which set `net.ipv4.ip_forward` and `net.ipv6.conf.all.forwarding` as pod-level sysctls. Without them the kubelet rejects those pods with `SysctlForbidden`.

> Replace `apiServerAddress` with your host's LAN IP (find it with `hostname -I` or `ip -4 addr`). Using `127.0.0.1` here makes the generated kubeconfig unreachable from the KubeNDT backend container and produces a certificate that is only valid for localhost.

### 5.2 Create cluster

```bash
kind create cluster --config kind-cluster.yaml
kubectl cluster-info --context kind-kubendt
kubectl config use-context kind-kubendt
kubectl get nodes -o wide
```

### 5.3 Install Meshnet (required)

```bash
git clone https://github.com/networkop/meshnet-cni.git
cd meshnet-cni/
kubectl apply -k manifests/base
```

Then validate:

```bash
kubectl get nodes -o wide
kubectl get pods -A | grep -i meshnet
kubectl get ds -A | grep -i meshnet
kubectl get crd | grep -i topologies.networkop.co.uk
```

### 5.4 Ready state

When all nodes (control-plane and both workers) are Ready and Meshnet is healthy, the cluster is ready for KubeNDT, installed in it or run outside it (§9). The `apiServerAddress` of §5.1 is only needed outside it, for the compose.

> Optional: to see per-pod CPU and RAM in the UI, install metrics-server (see §11).

## 6. Option 3: Two Nodes with kubeadm

Use this option for a production-style setup with one control-plane node and one worker node.

This section assumes Ubuntu/Debian-like systems. Unlike Minikube and kind, kubeadm installs no pod network, so this option adds one (flannel, §6.4) before Meshnet.

### 6.1 Prepare both nodes

Run on both control-plane and worker:

```bash
sudo swapoff -a
sudo sed -i '/ swap / s/^/#/' /etc/fstab

cat <<'EOF' | sudo tee /etc/modules-load.d/k8s.conf
overlay
br_netfilter
EOF
sudo modprobe overlay
sudo modprobe br_netfilter

cat <<'EOF' | sudo tee /etc/sysctl.d/k8s.conf
net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1
EOF
sudo sysctl --system
```

Install containerd:

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl gpg apt-transport-https containerd
sudo mkdir -p /etc/containerd
containerd config default | sudo tee /etc/containerd/config.toml >/dev/null
sudo sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
sudo systemctl restart containerd
sudo systemctl enable containerd
```

Install kubeadm, kubelet, kubectl. Pick the minor you want on pkgs.k8s.io, 1.31 or newer: the `kubeadm.yaml` of §6.1b is `v1beta4`, which an older kubeadm does not read.

```bash
sudo mkdir -p /etc/apt/keyrings
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.37/deb/Release.key | sudo gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.37/deb/ /' | sudo tee /etc/apt/sources.list.d/kubernetes.list
sudo apt-get update
sudo apt-get install -y kubelet kubeadm kubectl
sudo apt-mark hold kubelet kubeadm kubectl
```

### 6.1b Allow unsafe sysctls, cluster wide

KubeNDT router and switch pods set `net.ipv4.ip_forward` and `net.ipv6.conf.all.forwarding` through the pod security context. The kubelet must allow both or those pods stay in `SysctlForbidden`.

Put the allowlist in the kubelet configuration that kubeadm manages, not in `/var/lib/kubelet/config.yaml` by hand. kubeadm regenerates that file from the `kubelet-config` ConfigMap on every `kubeadm upgrade node`, so a hand edit is lost with the next minor upgrade.

Pass a configuration file to `kubeadm init` with the cluster's pod subnet and a `KubeletConfiguration` document:

```yaml
# kubeadm.yaml
apiVersion: kubeadm.k8s.io/v1beta4
kind: InitConfiguration
---
apiVersion: kubeadm.k8s.io/v1beta4
kind: ClusterConfiguration
networking:
  podSubnet: 10.244.0.0/16
---
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
allowedUnsafeSysctls:
  - net.ipv4.ip_forward
  - net.ipv6.conf.all.forwarding
```

The pod subnet is the one flannel's manifest expects (§6.4). Nodes that join afterwards download this configuration from the cluster, so nothing else is needed on the workers. Section 6.2 uses this file.

### 6.2 Initialize control-plane

Run only on control-plane, with the file from 6.1b:

```bash
sudo kubeadm init --config kubeadm.yaml
```

Configure kubeconfig for your user on control-plane:

```bash
mkdir -p $HOME/.kube
sudo cp /etc/kubernetes/admin.conf $HOME/.kube/config
sudo chown $(id -u):$(id -g) $HOME/.kube/config
kubectl get nodes -o wide
```

The node shows `NotReady` until the pod network of §6.4 is in. That is expected.

### 6.3 Join worker

On control-plane, print join command:

```bash
kubeadm token create --print-join-command
```

Run that full join command on worker. Then verify from control-plane:

```bash
kubectl get nodes -o wide
```

### 6.4 Install the pod network (flannel)

Meshnet is a chained CNI: it wires the lab links and leaves `eth0` and the cluster network to a primary plugin. Without one every node stays `NotReady` and the Meshnet pods never schedule. flannel is what the reference clusters run and its manifest uses the subnet set in §6.1b. Any other primary CNI works as long as it goes in before Meshnet.

```bash
kubectl apply -f https://github.com/flannel-io/flannel/releases/latest/download/kube-flannel.yml
kubectl -n kube-flannel rollout status ds/kube-flannel-ds
kubectl get nodes -o wide
```

Both nodes must be Ready before going on.

### 6.5 Install Meshnet (required)

From the machine that has admin kubeconfig for this cluster:

```bash
git clone https://github.com/networkop/meshnet-cni.git
cd meshnet-cni/
kubectl apply -k manifests/base
```

If your environment also requires NetworkAttachmentDefinition CRD, apply:

```bash
kubectl apply -f deploy/others/network-attachment-definition-crd.yaml
```

Validate:

```bash
kubectl get nodes -o wide
kubectl get pods -A
kubectl get ds -A | grep -i meshnet
kubectl get crd | grep -E 'topologies.networkop.co.uk|network-attachment-definitions.k8s.cni.cncf.io'
```

### 6.6 Ready state

When both nodes are Ready and Meshnet resources are healthy, the cluster is ready for KubeNDT.

> Optional: to see per-pod CPU and RAM in the UI, install metrics-server (see §11).

## 7. Option 4: Single node with k3s

A one-node cluster on the host itself, with k3s's own containerd and flannel.

### 7.1 Create cluster

```bash
curl -sfL https://get.k3s.io | sudo INSTALL_K3S_EXEC="server --disable=traefik --write-kubeconfig-mode=644 \
  --kubelet-arg=allowed-unsafe-sysctls=net.ipv4.ip_forward,net.ipv6.conf.all.forwarding" sh -
mkdir -p ~/.kube && cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
kubectl config set-cluster default --server=https://<host IP>:6443
kubectl get nodes -o wide
```

> `allowed-unsafe-sysctls` is the same allowance as in the other options. `--disable=traefik` keeps ports 80 and 443 of the host free, since k3s's bundled Traefik takes them and the compose serves the UI on 80. The kubeconfig k3s writes names `127.0.0.1`, which a container cannot reach, and the host's address is already in the API server's certificate, so `set-cluster` points it there for the compose.

### 7.2 Install Meshnet (required)

k3s keeps its CNI configuration and plugins under `/var/lib/rancher/k3s`, while Meshnet's DaemonSet mounts `/etc/cni/net.d` and `/opt/cni/bin`. Without the patch below the DaemonSet rolls out, but its configuration never reaches k3s's CNI directory, so Meshnet is not in the chain.

```bash
git clone https://github.com/networkop/meshnet-cni.git
cd meshnet-cni/
kubectl apply -k manifests/base
kubectl -n meshnet patch ds meshnet -p '{"spec":{"template":{"spec":{"volumes":[{"name":"cni-bin","hostPath":{"path":"/var/lib/rancher/k3s/data/cni"}},{"name":"cni-cfg","hostPath":{"path":"/var/lib/rancher/k3s/agent/etc/cni/net.d"}}]}}}}'
kubectl -n meshnet rollout status ds/meshnet
sudo ls /var/lib/rancher/k3s/agent/etc/cni/net.d   # 00-meshnet.conflist, then 10-flannel.conflist
```

### 7.3 Ready state

When the node is Ready and `00-meshnet.conflist` is in k3s's CNI directory, the cluster is ready for KubeNDT, installed in it or run outside it (§9). An image built locally, such as the VyOS one, goes into k3s's containerd with `sudo k3s ctr images import <file>.tar`.

## 8. Operational Troubleshooting

```bash
kubectl config current-context
kubectl get nodes -o wide
kubectl get pods -A
kubectl get ds -A | grep -i meshnet
kubectl describe pod -n <namespace> <pod>
kubectl logs -n kube-system -l app=meshnet-controller
```

Most common root causes:

- Wrong kubeconfig context selected
- Nodes `NotReady` on a kubeadm cluster, no pod network installed (§6.4)
- Meshnet not running on all nodes
- On k3s, Meshnet running but out of the CNI chain, its DaemonSet not patched (§7.2)
- Missing CRDs required by networking components
- Insufficient CPU/RAM in local environments
- **`SysctlForbidden` on switch/router pods**, the kubelet does not allow setting unsafe sysctls. See below.

### SysctlForbidden, switch or router pods stuck at 0/1

Symptom:

```
NAME    READY   STATUS            RESTARTS   AGE
ovs-0   0/1     SysctlForbidden   0          0s
sw-0    0/1     SysctlForbidden   0          0s
```

Cause: KubeNDT router and switch pods set `net.ipv4.ip_forward` and `net.ipv6.conf.all.forwarding` as pod-level sysctls and the kubelet is not configured to allow them. An allowlist with only the first one ran every version before 1.6.0 and refuses these pods since, so an older cluster needs the second entry.

Fix for an existing kubeadm cluster: add the allowlist to the cluster's kubelet configuration and let kubeadm write it to every node, so it also survives upgrades.

```bash
kubectl -n kube-system edit configmap kubelet-config
```

Add under the `KubeletConfiguration` document:

```yaml
allowedUnsafeSysctls:
  - net.ipv4.ip_forward
  - net.ipv6.conf.all.forwarding
```

Then on every node, control plane included:

```bash
sudo kubeadm upgrade node phase kubelet-config
sudo systemctl restart kubelet
```

Verify the kubelet restarted cleanly and the key is in place:

```bash
sudo systemctl status kubelet
grep -A2 allowedUnsafeSysctls /var/lib/kubelet/config.yaml
kubectl get nodes -o wide
```

After any `kubeadm upgrade`, run that `grep` again on each node. If the allowlist was only in the file, the upgrade dropped it and the pods come back `SysctlForbidden`.

Then delete the failing pods so they are recreated with the updated policy:

```bash
kubectl delete pod -n <namespace> ovs-0 sw-0
```

For new clusters, see the `allowedUnsafeSysctls` steps in each option above (§4.1, §5.1, §6.1b, §7.1).

## 9. Start KubeNDT after cluster bootstrap

KubeNDT runs inside the cluster or outside it, and [DEPLOYMENT.md](DEPLOYMENT.md#in-the-cluster-or-outside-it) compares the two.

- **In the cluster**, follow [Deployment in the cluster](DEPLOYMENT.md#deployment-in-the-cluster): a Secret with the admin password and `kubectl apply -k`. The UI is then at http://localhost:8080 through `kubectl -n kubendt-system port-forward svc/kubendt-frontend 8080:80`, or on port 30080 of any node with `deploy/k8s/optional/nodeport.yaml`.
- **Outside it**, run `docker compose up -d` from `deploy/compose` and open http://localhost. The backend reads `~/.kube/config` and starts on its current context, so its container has to reach the API server at the address that kubeconfig names: kind with the `apiServerAddress` of §5.1, minikube as in §4.3, k3s as in §7.1.

Verify the backend through the UI's proxy:

```bash
curl http://localhost:8080/api/readyz   # in the cluster, through the port-forward
curl http://localhost/api/readyz        # with the compose
```

The API docs are at `/api/swagger/index.html` once signed in.

## 10. Recommended minimal smoke check

After startup, validate end-to-end control-plane connectivity:

1. Create one namespace from the UI.
2. Import example `deploy/examples/1-test-small/topology-network-test-small.json`.
3. Confirm pods reach Running/Ready.
4. Open one pod shell and run:

```bash
ip a
```

If these steps pass, the cluster bootstrap is considered successful for KubeNDT lab usage.

## 11. Optional: Install metrics-server (pod CPU/RAM monitoring)

KubeNDT can display real-time CPU and RAM usage per pod in the node info panel. This feature requires **metrics-server** to be running in the cluster. If it is not installed, the UI will show `n/a` with a tooltip indicating the missing component.

### 11.1 Standard installation (kubeadm / kind / most clusters)

```bash
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
```

Verify it is running:

```bash
kubectl get deployment metrics-server -n kube-system
kubectl top nodes
```

### 11.2 Minikube

Minikube ships metrics-server as an addon. Enable it with:

```bash
minikube addons enable metrics-server
```

Verify:

```bash
minikube addons list | grep metrics-server
kubectl top pods -A
```

### 11.3 Insecure TLS (self-signed certificates)

If metrics-server fails to start due to TLS certificate errors (common in kubeadm clusters with self-signed certs), patch the deployment to skip TLS verification:

```bash
kubectl patch deployment metrics-server -n kube-system \
  --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
```

Check that the pods are ready after patching:

```bash
kubectl rollout status deployment/metrics-server -n kube-system
```

### 11.4 Validation

Once metrics-server is running, wait 60 seconds for the first scrape cycle, then verify:

```bash
kubectl top pod -n <your-kubendt-namespace>
```

The CPU and RAM values should now appear in the KubeNDT pod info panel (Summary tab) and update every 5 seconds automatically while the panel is open.

## 12. Link MTU and jumbo frames

KubeNDT sets the same MTU on every link of a namespace once it is wired, 1450 by default. The reason is the dataplane Meshnet builds: a link whose two pods share a worker is a veth pair (1500 by default), a link across workers is a VXLAN tunnel whose MTU is the worker NIC's minus 50 (1450 on a 1500 underlay), and the kernel refuses to raise a VXLAN above that. Mixed values hurt silently: a Linux bridge drops frames larger than its egress port without ICMP, so TCP stalls on full-size segments and OSPF neighbours with different MTUs stay in ExStart. One value for the whole namespace keeps a lab independent of where its pods landed, and 1450 is the largest value that works on every placement of a 1500 underlay.

Set `KUBENDT_LINK_MTU` on the backend when your cluster carries more or less than that. A value a worker cannot carry fails the deploy, modify or restart with the link, the worker and the value named; if you leave the default and it does not fit (an underlay smaller than 1500, or an IPv6 underlay where VXLAN costs 70 bytes), the backend lowers it to what fits and says so in the response's warnings.

Jumbo frames: any underlay of 1550 bytes or more lets VXLAN carry 1500, and a 9000 underlay lets it carry 8950. Every hop has to allow it: the physical NIC and the switch ports between the workers, the bridge or bond the workers hang from when they are VMs (and the virtual NIC of each VM), the worker's NIC inside the OS, and any overlay the cluster CNI runs on top (flannel's `flannel.1` follows the NIC's MTU after a restart of its pods). Check the chain end to end with a DF ping between two workers before changing anything in KubeNDT:

```bash
ping -c 3 -M do -s 8972 <other worker>   # 8972 = 9000 - 28
```

Then set `KUBENDT_LINK_MTU=8950` and redeploy. Most public clouds do not allow jumbo between nodes and some carry less than 1500 to begin with; the default and the warning handle those.
