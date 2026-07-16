# Cluster Bootstrap Guide for KubeNDT

This document provides a practical and formal procedure to prepare a Kubernetes cluster for KubeNDT in three scenarios:

1. Single-node lab with Minikube
2. Multi-node local cluster with kind (one control-plane, two workers)
3. Two-node production-style cluster with kubeadm

The same Meshnet installation method is used in all scenarios. Each option below tells you when to install it, always after the
cluster is created (see §4.2, §5.3 and §6.4). For reference, the method is
always:

```bash
git clone https://github.com/networkop/meshnet-cni.git
cd meshnet-cni/
kubectl apply -k manifests/base
```

## 1. Scope and Assumptions

This guide focuses on cluster preparation. KubeNDT application startup is included at the end.

KubeNDT backend reads the kubeconfig mounted from the host, so the active kubectl context on your machine is the cluster that KubeNDT will manage.

## 2. Common Requirements

- Linux host (or Linux VMs for kubeadm)
- Kubernetes 1.20+
- kubectl configured and working
- Docker and Docker Compose (for running KubeNDT services)
- Git (to clone meshnet-cni)
- Minimum recommended lab resources: 4 vCPU, 8 GB RAM
- One of the following, depending on the option you pick:
  - [Minikube](https://minikube.sigs.k8s.io/docs/start/) (Option 1)
  - [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) (Option 2)
  - [kubeadm](https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/install-kubeadm/) (Option 3)

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

> **Why `--extra-config=kubelet.allowed-unsafe-sysctls`?**
> Some KubeNDT node types (Linux switches, OVS nodes) set `net.ipv4.ip_forward` and `net.ipv6.conf.all.forwarding` via pod security context `sysctls`. Without this flag the kubelet will reject those pods with `SysctlForbidden`.

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

When the checks pass, the cluster is ready for KubeNDT.

> Optional: to see per-pod CPU and RAM in the UI, install metrics-server (see §10).

## 5. Option 2: Multi-node cluster with kind

Use this option for reproducible multi-node local testing. The example below
creates one control-plane node and two worker nodes. A multi-worker cluster is
recommended over a single node because it lets you validate that KubeNDT
topologies work **inter-node**: meshnet stitches links between pods that the
scheduler places on different workers, so this layout exercises the cross-node
data plane that a single-node cluster cannot.

> You can add more workers by appending additional `- role: worker` blocks with
> the same `kubeadmConfigPatches`. Two workers is the minimum to observe
> inter-node behaviour.

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
  apiServerAddress: "192.168.1.50"   # replace with your host IP (see note)
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

> The `kubeletExtraArgs` entries are required for KubeNDT switch and router nodes that set `net.ipv4.ip_forward` / `net.ipv6.conf.all.forwarding` as pod-level sysctls. Without them the kubelet rejects those pods with `SysctlForbidden`.

> Replace `apiServerAddress` with your host's LAN IP (find it with
> `hostname -I` or `ip -4 addr`). Using `127.0.0.1` here makes the generated
> kubeconfig unreachable from the KubeNDT backend container and produces a
> certificate that is only valid for localhost.

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

When all nodes (control-plane and both workers) are Ready and Meshnet is
healthy, the cluster is ready for KubeNDT.

> Optional: to see per-pod CPU and RAM in the UI, install metrics-server (see §10).

## 6. Option 3: Two Nodes with kubeadm

Use this option for a production-style setup with one control-plane node and one worker node.

This section assumes Ubuntu/Debian-like systems.

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

Install kubeadm, kubelet, kubectl:

```bash
sudo mkdir -p /etc/apt/keyrings
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.30/deb/Release.key | sudo gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.30/deb/ /' | sudo tee /etc/apt/sources.list.d/kubernetes.list
sudo apt-get update
sudo apt-get install -y kubelet kubeadm kubectl
sudo apt-mark hold kubelet kubeadm kubectl
```

### 6.1b Allow unsafe sysctls on all nodes

Run on **every node** (control-plane and all workers) before `kubeadm init/join`.
KubeNDT switch and router pods set `net.ipv4.ip_forward` and `net.ipv6.conf.all.forwarding` via pod security context `sysctls`; the kubelet must explicitly permit them or it will reject those pods with `SysctlForbidden`.

Edit `/var/lib/kubelet/config.yaml` and add the following block (if the key does not exist yet, append it at the end of the file):

```yaml
allowedUnsafeSysctls:
  - "net.ipv4.ip_forward"
  - "net.ipv6.conf.all.forwarding"
```

Then restart the kubelet:

```bash
sudo systemctl restart kubelet
```

### 6.2 Initialize control-plane

Run only on control-plane:

```bash
sudo kubeadm init
```

Configure kubeconfig for your user on control-plane:

```bash
mkdir -p $HOME/.kube
sudo cp /etc/kubernetes/admin.conf $HOME/.kube/config
sudo chown $(id -u):$(id -g) $HOME/.kube/config
kubectl get nodes -o wide
```

### 6.3 Join worker

On control-plane, print join command:

```bash
kubeadm token create --print-join-command
```

Run that full join command on worker. Then verify from control-plane:

```bash
kubectl get nodes -o wide
```

### 6.4 Install Meshnet (required)

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

### 6.5 Ready state

When both nodes are Ready and Meshnet resources are healthy, the cluster is ready for KubeNDT.

> Optional: to see per-pod CPU and RAM in the UI, install metrics-server (see §10).

## 7. Operational Troubleshooting

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
- Meshnet not running on all nodes
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

Cause: KubeNDT switch and router pods set `net.ipv4.ip_forward` and `net.ipv6.conf.all.forwarding` as pod-level sysctls. The kubelet must be configured to allow them.

Fix for an existing kubeadm cluster, run on every affected worker node:

Edit `/var/lib/kubelet/config.yaml` and add (or append) the following block:

```yaml
allowedUnsafeSysctls:
  - "net.ipv4.ip_forward"
  - "net.ipv6.conf.all.forwarding"
```

Then restart the kubelet:

```bash
sudo systemctl restart kubelet
```

Verify the kubelet restarted cleanly:

```bash
sudo systemctl status kubelet
kubectl get nodes -o wide
```

Then delete the failing pods so they are recreated with the updated policy:

```bash
kubectl delete pod -n <namespace> ovs-0 sw-0
```

For new clusters, see the `allowedUnsafeSysctls` steps in each option above (§4.1, §5.1, §6.1b).

## 8. Start KubeNDT after cluster bootstrap

Once your cluster passes the validation checks above:

1. Ensure your `kubectl` context points to the target cluster.
2. Start KubeNDT from the repository root.

```bash
docker compose up --build
```

3. Open the UI at `http://localhost:3000` (or through your reverse proxy in production mode).
4. Verify backend health and API docs:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

Swagger UI:

`http://localhost:8080/swagger/index.html`

## 9. Recommended minimal smoke check

After startup, validate end-to-end control-plane connectivity:

1. Create one namespace from the UI.
2. Import example `deploy/examples/1-test-small/topology-network-test-small.json`.
3. Confirm pods reach Running/Ready.
4. Open one pod shell and run:

```bash
ip a
```

If these steps pass, the cluster bootstrap is considered successful for KubeNDT lab usage.

## 10. Optional: Install metrics-server (pod CPU/RAM monitoring)

KubeNDT can display real-time CPU and RAM usage per pod in the node info panel. This feature requires **metrics-server** to be running in the cluster. If it is not installed, the UI will show `n/a` with a tooltip indicating the missing component.

### 10.1 Standard installation (kubeadm / kind / most clusters)

```bash
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
```

Verify it is running:

```bash
kubectl get deployment metrics-server -n kube-system
kubectl top nodes
```

### 10.2 Minikube

Minikube ships metrics-server as an addon. Enable it with:

```bash
minikube addons enable metrics-server
```

Verify:

```bash
minikube addons list | grep metrics-server
kubectl top pods -A
```

### 10.3 Insecure TLS (self-signed certificates)

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

### 10.4 Validation

Once metrics-server is running, wait 60 seconds for the first scrape cycle, then verify:

```bash
kubectl top pod -n <your-kubendt-namespace>
```

The CPU and RAM values should now appear in the KubeNDT pod info panel (Summary tab) and update every 5 seconds automatically while the panel is open.
