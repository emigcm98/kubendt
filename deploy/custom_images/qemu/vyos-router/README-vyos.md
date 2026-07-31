# QEMU-Based Network Nodes

This guide explains how to create custom QEMU-based network nodes from scratch. The focus is on VyOS, but the techniques apply to any Linux-based network OS.

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Complete VyOS Router Walkthrough](#complete-vyos-router-walkthrough)
- [Step-by-Step: Building Your Own Router](#step-by-step-building-your-own-router)
- [Advanced: Networking Deep Dive](#advanced-networking-deep-dive)
- [Entrypoint Environment Variables](#entrypoint-environment-variables)
- [KubeNDT Driver Integration](#kubendt-driver-integration)
- [Deployment](#deployment)
- [Troubleshooting](#troubleshooting)

---

## Architecture Overview

### How QEMU Nodes Work in KubeNDT

```
┌─────────────────────────────────────────────────────────────────┐
│  Kubernetes Pod (driver runtime: qemu)                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Pod Network Namespace (Linux kernel network stack)             │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ Primary CNI interface (flannel):                          │  │
│  │   eth0 (cluster IP, passed through to the guest)          │  │
│  │                                                           │  │
│  │ Meshnet-created interfaces:                               │  │
│  │   eth1 (from Meshnet topology)                            │  │
│  │   eth2 (from Meshnet topology)                            │  │
│  │   eth3 (from Meshnet topology)                            │  │
│  │                                                           │  │
│  │ TAP devices (created by entrypoint.sh):                       │  │
│  │   tap0 (connects to QEMU)                                 │  │
│  │   tap1 (connects to QEMU)                                 │  │
│  │   tap2 (connects to QEMU)                                 │  │
│  │   tap3 (connects to QEMU)                                 │  │
│  │                                                           │  │
│  │ TC redirect rules (traffic control):                      │  │
│  │   eth0 redirect to tap0 (L2 passthrough, cluster)         │  │
│  │   eth1 redirect to tap1 (L2 passthrough)                  │  │
│  │   eth2 redirect to tap2 (L2 passthrough)                  │  │
│  │   eth3 redirect to tap3 (L2 passthrough)                  │  │
│  └───────────────────────────────────────────────────────────┘  │
│         │                  │                  │                 │
│         ↓                  ↓                  ↓                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  QEMU Process (vmlinuz + rootfs from qcow2)              │   │
│  │  ┌────────────────────────────────────────────────────┐  │   │
│  │  │  VyOS Guest OS                                     │  │   │
│  │  │                                                    │  │   │
│  │  │  Guest Network Stack:                              │  │   │
│  │  │    eth0 ← TAP0 ← eth0 (pod, cluster IP + def gw)   │  │   │
│  │  │    eth1 ← TAP1 ← eth1 (pod)                        │  │   │
│  │  │    eth2 ← TAP2 ← eth2 (pod)                        │  │   │
│  │  │    eth3 ← TAP3 ← eth3 (pod)                        │  │   │
│  │  │    eth999 (internal mgmt, QEMU slirp               │  │   │
│  │  │            192.168.255.0/24, ssh_qemu only)        │  │   │
│  │  │                                                    │  │   │
│  │  │  /config/config.boot (applied on startup)          │  │   │
│  │  │    - IP addresses                                  │  │   │
│  │  │    - Routing config                                │  │   │
│  │  │    - Protocol daemons (OSPF, BGP, etc.)            │  │   │
│  │  │                                                    │  │   │
│  │  └────────────────────────────────────────────────────┘  │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### Key Concepts

1. **Meshnet CNI**: Creates pod interfaces (eth1, eth2, eth3, etc.) based on Topology CRDs.
2. **TAP Devices**: Virtual network interface in user space that bridges pod namespace ↔ QEMU.
3. **TC Redirect**: Traffic Control rule that redirects L2 frames from pod eth → tap → QEMU.
4. **MAC identity mapping**: `entrypoint.sh` reads each pod-side veth MAC and passes it verbatim to QEMU via `-device virtio-net-pci,mac=...`, so guest `ethN` and pod `ethN` carry the **same MAC**. This keeps VyOS' `hw-id` pinning consistent with what udev sees.
5. **Preconfig Script**: VyOS hook (`vyos-preconfig-bootup.script`) that runs on boot and copies the per-pod `config.boot` from the seed CD-ROM into `/opt/vyatta/etc/config/`. Injected into the qcow2 at image build time, see [Phase 3](#phase-3-create-dockerfile).
6. **config.boot**: VyOS configuration file generated per-pod by `entrypoint.sh` and delivered to the guest as a seed CD-ROM (carries IPs, hostname, SSH keys, i.e. anything that depends on the specific pod).
7. **SMBIOS Type 11 netmap + custom udev rule**: a MAC→ifname table passed to the guest via QEMU `-smbios type=11,value=...` so a build-time udev rule (`64-kubendt-net.rules`) can rename data NICs to the desired name _before_ VyOS' own naming rule runs. Required for non-consecutive names (`eth10`, holes after a delete, etc.). See [Interface Naming Inside The Guest](#interface-naming-inside-the-guest).
8. **Cluster passthrough (eth0)**: the pod's primary CNI interface is passed through like any data NIC (tap0 + TC redirect, same MAC). Its cluster IP and default route move into the VyOS config, so the guest is reachable at the pod IP and can `enable_snat eth0` like the native-container routers. The slirp NIC stays as a loopback-only backend channel, renamed to `eth999`. See [Cluster Passthrough (eth0)](#cluster-passthrough-eth0).

---

## Complete VyOS Router Walkthrough

This section walks through building a VyOS router from complete scratch.

### Phase 1: Create Base VyOS QCOW2 Image

> [!TIP] This whole phase is automated by [`build-vyos-qcow2.sh`](build-vyos-qcow2.sh). VyOS publishes rolling releases as ISO only (there is no qcow2 asset to download), so the interactive installer has to run once, and the script does it for you. It resolves the release tag via the GitHub API, downloads the ISO (cached in `./iso-cache/`, and an ISO already sitting in the current directory or next to the script is reused instead of re-downloaded), then boots it inside a throwaway container and answers every `install image` prompt over the serial console with expect. The result is the same virgin `vyos.qcow2` this walkthrough produces by hand.
>
> ```bash
> ./build-vyos-qcow2.sh                            # latest rolling → ./vyos.qcow2
> ./build-vyos-qcow2.sh --list                     # what's available, newest first
> ./build-vyos-qcow2.sh 2026.07.29-0032-rolling    # pin a specific release
> ./build-vyos-qcow2.sh -o /tmp/vyos.qcow2 -s 20   # custom output path / disk size (GB)
> ./build-vyos-qcow2.sh --engine docker            # force a container engine
> ```
>
> The build host only needs `curl`, a container engine (**podman or docker**) and ~2 GB of free disk (600 MB ISO + ~700 MB qcow2 + the builder image). QEMU, expect and qemu-utils are installed inside the container, never on the host. With no flags the script probes both engines and picks the first that can actually use `/dev/kvm` (~5 min install). If neither can, it falls back to TCG emulation (30+ min) with a warning. The usual reasons for missing KVM are a user that is not in the `kvm` group, or rootless podman running on the `runc` OCI runtime, since `keep-groups` needs `crun` (rootful docker is unaffected, which is why the probe usually lands there).
>
> The manual steps below remain as the reference for what the script does, and as the place to look if a future VyOS release rewords an installer prompt and the expect dialog needs updating.

#### Step 1.1: Download VyOS ISO

```bash
# Create working directory
mkdir -p ~/vyos-build
cd ~/vyos-build

# Download any VyOS rolling release
wget https://github.com/vyos/vyos-nightly-build/releases/download/2026.07.29-0032-rolling/vyos-2026.07.29-0032-rolling-generic-amd64.iso

# Verify checksum (optional)
sha256sum vyos-2026.07.29-0032-rolling-generic-amd64.iso
```

#### Step 1.2: Create QCOW2 Disk

```bash
# Create 10GB disk image in QCOW2 format. The Dockerfile's customizer stage
# expects this exact filename in the build context.
qemu-img create -f qcow2 vyos.qcow2 10G

# Verify creation
ls -lh vyos.qcow2
# Output: -rw-r--r-- 1 user user 193K vyos.qcow2
```

The initial size is 193KB (just metadata). Actual space is allocated as needed.

#### Step 1.3: Boot and Install VyOS

```bash
# Launch QEMU with the ISO and disk
qemu-system-x86_64 \
  -enable-kvm \
  -m 2048 \
  -smp 2 \
  -drive file=vyos.qcow2,if=virtio \
  -drive file=vyos-2026.07.29-0032-rolling-generic-amd64.iso,media=cdrom \
  -nographic \
  -serial mon:stdio
```

**Inside QEMU Console:**

Login with the default credentials (`vyos` / `vyos`) and run `install image`. Accept the defaults, only the password prompt requires input. The full interactive sequence:

```text
vyos@vyos:~$ install image
Welcome to VyOS installation!
This command will install VyOS to your permanent storage.
Would you like to continue? [y/N] y
What would you like to name this image? (Default: 2026.07.29-0032-rolling) <Enter>
Please enter a password for the "vyos" user: vyos
WARNING: Default password used. Consider changing it on next login.
Please confirm password for the "vyos" user: vyos
What console should be used by default? (K: KVM, S: Serial)? (Default: S) <Enter>
Probing disks
1 disk(s) found
The following disks were found:
Drive: /dev/vda (10.0 GB)
Which one should be used for installation? (Default: /dev/vda) <Enter>
Installation will delete all data on the drive. Continue? [y/N] y
Searching for data from previous installations
No previous installation found
Would you like to use all the free space on the drive? [Y/n] y
Creating partition table...
The following config files are available for boot:
  1: /opt/vyatta/etc/config/config.boot
  2: /opt/vyatta/etc/config.boot.default
Which file would you like as boot config? (Default: 1) <Enter>
```

Once installation completes, shut the VM down (`poweroff` from inside, or `Ctrl-A X` on the QEMU monitor). The resulting `vyos.qcow2` is your **virgin** image, no further manual edits are needed.

> [!IMPORTANT] Everything that used to be added by hand to this qcow2, the preconfig hook, the udev rule for non-sequential interface names, the helper script, is now injected automatically by `customize-qcow2.sh` (a `guestfish` wrapper) during the Docker build (see [Phase 3](#phase-3-create-dockerfile)). Drop `vyos.qcow2` straight into the build context and the customizer stage will produce the final image.

#### Step 1.4: (Removed) Preconfig Script Injection

This step was previously a manual `vi /config/scripts/vyos-preconfig-bootup.script` inside the running VM. It is no longer needed: Stage 1 of the multi-stage Dockerfile runs [`customize-qcow2.sh`](customize-qcow2.sh) which uses `guestfish` to upload [`vyos-preconfig-bootup.script`](vyos-preconfig-bootup.script) into the qcow2 at `/opt/vyatta/etc/config/scripts/vyos-preconfig-bootup.script` automatically. Skip ahead to [Phase 3](#phase-3-create-dockerfile).

#### Phase 2: Create Launch Script

The `entrypoint.sh` script is the heart of the operation. It:

1. Generates a fresh per-pod management SSH keypair and detects pod data interfaces (eth1, eth2, …), reading each veth's MAC.
2. Captures `eth0`'s cluster IP, MAC and default gateway for the cluster passthrough, then hands them over to the guest (see [Cluster Passthrough (eth0)](#cluster-passthrough-eth0)).
3. Creates a TAP device per interface (tap0 for `eth0`, tapN for data NICs) and wires it to the pod-side veth via `tc` ingress redirects (L2 passthrough).
4. Generates the per-pod `config.boot` from `startup.cfg.tpl` (hostname, cluster `eth0` block + default route, mgmt IP/SSH key, dataplane `ethN { hw-id <MAC> address <IP> … }` blocks).
5. Wraps the generated `config.boot` in a `cidata`-labelled ISO image (seed CD-ROM) for `vyos-preconfig-bootup.script` to mount and apply at first boot.
6. Emits the MAC→ifname table to the guest via QEMU SMBIOS Type 11 (one `-smbios type=11,value=kubendt-netmap:<MAC>=<ifname>` arg per NIC, including `eth0` and the slirp NIC's rename to `eth999`), so the build-time udev rule can rename NICs to non-sequential names like `eth10` before VyOS' own naming rule runs.
7. Launches QEMU with the customised qcow2, the seed CD-ROM, and one `virtio-net-pci` device per passthrough interface using the pod-side MAC verbatim.

The full file (`entrypoint.sh`) sits next to this README, it's the canonical reference for environment variables and the exact flow. The header block:

```bash
# =============================================================================
# KubeNDT QEMU Launcher for VyOS
# =============================================================================
# This script orchestrates:
# 1. Detection of pod data interfaces (eth1, eth2, ...)
# 2. Creation of TAP devices + TC ingress redirects for L2 passthrough
# 3. Dynamic config.boot generation from startup.cfg.tpl
# 4. Seed CD-ROM build (cidata-labelled ISO carrying config.boot)
# 5. MAC -> ifname netmap exposed via SMBIOS Type 11 for early-boot udev rename
# 6. QEMU VM launch with KVM acceleration
#
# Variables (settable via environment):
#   IMAGE             Path to QCOW2 disk image (default: /vyos.qcow2)
#   RAM_MB            Guest RAM in MB (default: 1024)
#   QEMU_BIN          QEMU binary path (default: qemu-system-x86_64)
#   NIC_MODEL         virtio-net-pci or e1000 (default: virtio-net-pci)
#   BOOTCFG_TEMPLATE  Path to config.boot template (default: /startup.cfg.tpl)
# =============================================================================
```

---

### Phase 3: Create Dockerfile

The build is split into **two stages** so libguestfs only lives in the build environment, not in the final runtime image:

- **Stage 1 (`customizer`)** runs [`customize-qcow2.sh`](customize-qcow2.sh) inside a Debian builder. The script uses `guestfish` (from `libguestfs-tools`) to inject three files into the qcow2:
  - `vyos-preconfig-bootup.script` → `/opt/vyatta/etc/config/scripts/` (the old Phase 1.4 step)
  - `64-kubendt-net.rules` → `/etc/udev/rules.d/` (interface-naming fix, see [Interface Naming Inside The Guest](#interface-naming-inside-the-guest))
  - `kubendt-net-name` → `/usr/local/bin/` (helper called by the udev rule)

  It also applies two small patches to the stock image. The GRUB boot-menu timeout goes to 0 (5 seconds of dead time per pod start, and the file that holds it moved to `grub.cfg.d/20-vyos-defaults-autoload.cfg` in recent releases, so both locations are handled). And the `hw-id` lines are stripped from the stock `config.boot`, because at udev coldplug they take priority over the SMBIOS netmap and would steal `eth0` from the cluster passthrough (see the [troubleshooting entry](#interface-ends-up-as-eth4-or-any-unexpected-name-inside-vyos)).

- **Stage 2** is the actual runtime image: only QEMU + a handful of tooling, with the customised qcow2 `COPY --from=customizer`. No libguestfs.

> **Why guestfish and not `virt-customize`:** the higher-level `virt-customize` always invokes `inspect-os` first, and libguestfs' inspector does not recognise VyOS' SquashFS+overlayfs layout (the real rootfs lives inside `/boot/<version>/<version>.squashfs`, and nothing identifies the OS at the top of any partition). It aborts with `no operating systems were found in the guest image` before it can run any `--upload`. `guestfish` is the lower-level tool, no inspection, you tell it which partition to mount and where to write. `customize-qcow2.sh` is a thin wrapper that auto-detects the per-release version directory and runs the three uploads.

```dockerfile
# syntax=docker/dockerfile:1.6

# ---------- Stage 1: customise a virgin VyOS qcow2 -------------------------
FROM debian:bookworm-slim AS customizer

RUN apt-get update && apt-get install -y --no-install-recommends \
        libguestfs-tools \
        linux-image-amd64 \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && chmod 0644 /boot/vmlinuz-* || true

ENV LIBGUESTFS_BACKEND=direct

WORKDIR /work
COPY vyos.qcow2                      /work/vyos.qcow2
COPY 64-kubendt-net.rules            /work/64-kubendt-net.rules
COPY kubendt-net-name                /work/kubendt-net-name
COPY vyos-preconfig-bootup.script    /work/vyos-preconfig-bootup.script
COPY customize-qcow2.sh              /work/customize-qcow2.sh

RUN chmod +x /work/customize-qcow2.sh \
    && /work/customize-qcow2.sh /work/vyos.qcow2 \
    && qemu-img info /work/vyos.qcow2

# ---------- Stage 2: runtime image ----------------------------------------
FROM ubuntu:22.04

RUN apt update && apt install -y \
    qemu-system-x86 \
    qemu-utils \
    iproute2 \
    iputils-ping \
    iptables \
    sed \
    gawk \
    coreutils \
    genisoimage \
    openssh-client \
    whois \
    && rm -rf /var/lib/apt/lists/*

COPY --from=customizer /work/vyos.qcow2 /vyos.qcow2
COPY entrypoint.sh                     /entrypoint.sh
COPY startup.cfg.tpl                   /startup.cfg.tpl

RUN chmod +x /entrypoint.sh

CMD ["/entrypoint.sh"]
```

**Build prerequisites:**

- `libguestfs` runs an internal microVM. Without `/dev/kvm` exposed to the builder (the default in container build environments), `LIBGUESTFS_BACKEND=direct` forces a TCG fallback, slower but doesn't need a privileged build.
- Debian ships the kernel image as `0600`. The `chmod 0644 /boot/vmlinuz-*` line covers libguestfs' most common silent failure on container builders.
- The script writes files to `/boot/<version>/rw/` on `/dev/sda3`, which is the upper layer of the overlayfs VyOS mounts as `/` at runtime. `<version>` is detected at build time so the same script keeps working across VyOS releases.

---

### Phase 4: Build the Image

```bash
# Build directory with all files
mkdir -p kubendt-vyos-qemu
cd kubendt-vyos-qemu

# Virgin VyOS qcow2 (from Phase 1)
cp /path/to/vyos.qcow2                        ./

# Repo assets injected at build time
cp /path/to/Dockerfile.kubendt                ./Dockerfile.kubendt
cp /path/to/entrypoint.sh                     ./
cp /path/to/startup.cfg.tpl                   ./
cp /path/to/vyos-preconfig-bootup.script      ./
cp /path/to/64-kubendt-net.rules              ./
cp /path/to/kubendt-net-name                  ./
cp /path/to/customize-qcow2.sh                ./

# Build (customize-qcow2.sh runs in Stage 1, runtime image is produced in Stage 2)
podman build -t vyos-router:dev -f Dockerfile.kubendt .

# Verify image
podman image ls | grep vyos

# (Optional) Save as tar for offline distribution
podman save -o vyos-router.tar vyos-router:dev
ls -lh vyos-router.tar
```

---

## Step-by-Step: Building Your Own Router

This is a quick reference for building a custom network router image:

### 1. Prepare Base Image

```bash
# Download OS ISO
wget https://example.com/os.iso

# Create disk
qemu-img create -f qcow2 router.qcow2 10G

# Boot and install (interactive, follow OS prompts)
qemu-system-x86_64 -enable-kvm -m 2048 \
  -drive file=router.qcow2,if=virtio \
  -drive file=os.iso,media=cdrom \
  -nographic -serial mon:stdio

# Install networking tools, daemons, etc.
# Then poweroff and compress
qemu-img convert -c -O qcow2 router.qcow2 router-compressed.qcow2
```

### 2. Create Preconfig Script

Write a script that:

- Mounts the ISO (from entrypoint.sh)
- Copy the boot config template
- Applies customizations (MACs, IPs)

### 3. Create entrypoint.sh

Adapt the VyOS `entrypoint.sh` for your OS:

- Change paths (/config vs /etc)
- Adjust interface detection
- Config file format (VyOS uses vyatta-style, others use /etc/network)

### 4. Create Dockerfile

```dockerfile
FROM [your base OS]
RUN apt install qemu-system-x86 iproute2 mkisofs
COPY router.qcow2 /router.qcow2
COPY entrypoint.sh /entrypoint.sh
COPY preconfig.sh /preconfig.sh
CMD ["/entrypoint.sh"]
```

### 5. Build and Test

```bash
docker build -t myorg/qemu-router:1.0 .
docker push myorg/qemu-router:1.0
```

---

## Advanced: Networking Deep Dive

### TC Redirect Internals

The TC (Traffic Control) redirect is crucial for L2 passthrough:

```bash
# When you do this:
tc filter add dev eth1 ingress \
  u32 match u8 0 0 \
  action mirred egress redirect dev tap1

# What happens:
# 1. All packets arriving at eth1 (ingress) are matched by "u8 0 0" (always true)
# 2. "mirred egress redirect" intercepts the packet
# 3. Packet is sent out (egress) on tap1 instead of normal processing
# 4. TAP device feeds it to QEMU userspace
# 5. QEMU guest OS receives packet on eth1

# For the reverse:
tc filter add dev tap1 ingress \
  u32 match u8 0 0 \
  action mirred egress redirect dev eth1

# Packets generated by guest OS exit tap1
# Are redirected back to eth1
# Go back to peer pods via Meshnet
```

### MAC Address Mapping

Pod-side and guest-side share the **same MAC** for every data NIC, because `entrypoint.sh` reads each pod-side veth MAC via `/sys/class/net/<iface>/address` and passes it verbatim to QEMU:

```bash
guest_mac=$(get_iface_mac "${eth_if}")     # MAC from the pod-side veth
...
QEMU_ARGS+=(
  -netdev "tap,id=p${tap_idx},ifname=${tap_if},script=no,downscript=no"
  -device "${NIC_MODEL},netdev=p${tap_idx},mac=${guest_mac},..."
)
```

This identity mapping is important for two reasons:

1. **TC redirect is L2-transparent.** Frames generated by the guest exit `tapN`, are mirrored back to the pod `ethN` by the ingress filter, and reach peer pods unchanged. Since both sides carry the same MAC, peers see the address they expect.
2. **VyOS' `hw-id` pinning works.** `entrypoint.sh` writes `ethernet ethN { hw-id <MAC> ... }` in the per-pod `config.boot`, and the build-time udev rule (`64-kubendt-net.rules`) keys its rename on the same MAC. Both refer to the **pod-side MAC**, which is exactly what the guest virtio-net device ends up with, so the chain stays consistent.

The only NIC whose MAC is invented is the internal management one (`eth999`, MAC `52:54:00:12:34:56`), because it lives on QEMU's user-mode slirp network and never crosses to the pod side. Every other NIC, including the `eth0` cluster passthrough, carries the pod-side MAC verbatim.

### Cluster Passthrough (eth0)

The pod's primary CNI interface (`eth0`, flannel) is passed through to the guest with the same tap+TC mechanism as the data NICs, so the guest, not the container, owns the pod's cluster identity.

1. `entrypoint.sh` captures `eth0`'s MAC, IPv4 and default gateway **before** touching anything (deleting the only IPv4 would purge the default route).
2. The generated `config.boot` gets an `ethernet eth0 { address <podIP> hw-id <podMAC> ... }` block plus a static default route via the pod's own gateway.
3. The address and route are removed from the pod side and `eth0` is wired to `tap0`. The container keeps only loopback, which is all that `kubectl exec` and the `ssh_qemu` hostfwd need.

What this buys

- The router is **reachable at the pod's cluster IP** from other pods. MACs are identical on both sides, so ARP and conntrack behave as if the guest were the pod, and a Kubernetes `Service` in front works too.
- `enable_snat eth0` works like on the native-container routers, so a VyOS node can be the twin's internet gateway. The backend guard allows exactly `enable_snat`/`disable_snat` on `eth0` and rejects everything else.
- Guest DNS/NTP use a real kernel path out of `eth0` instead of slirp's userspace NAT.

PCI slot layout matters here. The guest kernel names NICs in slot order, so the passthrough NIC sits at `0x05` (kernel `eth0`, rename no-op), data NICs keep `0x06+`, and the slirp NIC moves to `0x1e` so its rename to `eth999` never collides. Without the passthrough (no `eth0` or no IPv4 in the pod), slirp takes `0x05` as before and the guest has no `eth0`.

On the security side, the guest's sshd still binds `listen-address 192.168.255.15` (slirp IP), so the management key is not exposed on the cluster network. SSH at the cluster IP is a deliberate user action (`set service ssh listen-address <podIP>`), not a side effect.

### Interface Naming Inside The Guest

VyOS' built-in interface-naming udev rule (`/etc/udev/rules.d/65-vyos-net.rules`) looks up each NIC's MAC in `/opt/vyatta/etc/config/config.boot` at coldplug time and renames it to whatever the config says (`ethernet eth10 { hw-id ... }` → rename to `eth10`). The catch in our setup: at coldplug time the per-pod `config.boot` is **still on the seed CD-ROM**, `vyos-preconfig-bootup.script` copies it to disk later, after udev has already run.

The consequence is that during coldplug, VyOS' rule only sees the management NIC's MAC mapping in the stock `config.boot`. For every data NIC it falls back to `vyos_net_name`'s biosdevname-style fallback, which always returns sequential names (`eth1, eth2, eth3, …`) by PCI scan order. So:

- A topology with `eth1, eth2, eth3` works by sheer coincidence, pod-side and biosdevname-fallback names happen to match.
- A topology with `eth10` (or any hole / non-consecutive name) is renamed by the kernel to `eth4` (the next sequential). When VyOS later commits the config.boot, `interfaces ethernet eth10` fails with `Interface "eth10" does not exist`.

**Fix** (baked into the qcow2 by the customizer stage of the Dockerfile):

1. **`/etc/udev/rules.d/64-kubendt-net.rules`** runs _before_ VyOS' own rule (lower numeric prefix). It calls a small helper and, if the MAC has a desired name in the netmap, sets `ENV{VYOS_IFNAME}=<name>`.
2. **`/usr/local/bin/kubendt-net-name`** reads the MAC→ifname table from `/sys/firmware/dmi/entries/11-*/raw` (SMBIOS Type 11 "OEM Strings"). The table is provided per-pod by `entrypoint.sh` via one `-smbios type=11,value=kubendt-netmap:<MAC>=<ifname>` per data NIC, and is exposed by the kernel's DMI driver in early boot, well before any udev rule fires.
3. **VyOS' rule then takes its "predefined" branch**: it calls `vyos_net_name <kernel_name> <MAC> <VYOS_IFNAME>` with three arguments, which bypasses the biosdevname fallback and renames the NIC directly to the predef name.

```text
QEMU host                       │ Guest (VyOS)
─────────────────────────────── │ ─────────────────────────────────────────
for each data NIC:              │
  -smbios type=11,              │   /sys/firmware/dmi/entries/11-0/raw
    value=kubendt-netmap:       │            ↑
       <MAC>=<ifname>           │            │ kubendt-net-name
                                │            │   (parses with tr '\0' '\n')
                                │            │            ↑
                                │            │   64-kubendt-net.rules
                                │            │   sets ENV{VYOS_IFNAME}=eth10
                                │            ↓
                                │   65-vyos-net.rules: calls vyos_net_name
                                │   with predef → kernel renames to eth10
                                │            ↓
                                │   later: vyos-preconfig copies config.boot
                                │   from /dev/sr0, VyOS commits, eth10 exists
                                │   → success
```

If the helper finds no entry for a given MAC (e.g. the mgmt NIC, whose MAC is already in the stock `config.boot`), `VYOS_IFNAME` stays empty and VyOS' rule runs unchanged, so the fix is strictly additive, with no regression for already-working configurations.

> **Why SMBIOS Type 11 and not QEMU `-fw_cfg`:** the first iteration of this fix used `-fw_cfg name=opt/kubendt/netmap,file=...` because it's the obvious "early-boot key/value" channel in QEMU. It does not work against VyOS, because VyOS' custom kernel builds with `CONFIG_FW_CFG_SYSFS` **unset**, `/sys/firmware/qemu_fw_cfg/` never appears and the `qemu_fw_cfg` module isn't shipped either. SMBIOS Type 11 only needs `CONFIG_DMI=y`, which is ubiquitous (and confirmed `y` in `/proc/config.gz` on the VyOS guest), and is parsed by the kernel core, not by a module. No additional dependencies in the guest other than `tr`, `awk` and `grep`, all present in a base VyOS install.

### Config.boot Generation

The config.boot template is built dynamically:

```bash
# entrypoint.sh detects pod interfaces:
get_data_ifaces() {
  # Returns: eth1, eth2, eth3, ...
}

# For each interface, generates:
# interfaces {
#     ethernet eth1 {
#         address "10.0.0.1/24"  # (if WITH_IPS=true)
#     }
# }

# Why dynamic?
# 1. Pod interfaces depend on Meshnet topology
# 2. IP addresses are from network config
# 3. Number of interfaces is variable (1-N replicas)
```

### SSH and `disable-host-validation`

The `startup.cfg.tpl` includes `disable-host-validation` in the SSH service block:

```
service {
    ssh {
        disable-host-validation
        ...
    }
}
```

This disables reverse-DNS lookups on incoming SSH connections. Without it, VyOS sshd performs a PTR lookup for the client IP (typically the QEMU slirp address `10.0.2.2`) on every SSH connection. If the router has no working upstream DNS (e.g. it routes through another router with no internet access), this lookup times out after ~20-25 seconds, making every KubeNDT backend call to the guest appear to hang for that duration.

---

## Entrypoint Environment Variables

All runtime behaviour of `entrypoint.sh` can be tuned without rebuilding the image by setting environment variables in the pod spec (or `topology-network.json` `env` field).

| Variable | Default | Description |
| --- | --- | --- |
| `IMAGE` | `/vyos.qcow2` | Path to the VyOS QCOW2 disk image inside the container. |
| `RAM_MB` | `1024` | Memory allocated to the QEMU VM (MiB). |
| `CPU_CORES` | `1` | vCPUs given to the QEMU VM. Bump to e.g. `2`–`4` to speed up boot and configd commits; keep it at or below the pod's CPU limit. |
| `QEMU_BIN` | `qemu-system-x86_64` | QEMU binary to use. |
| `NIC_MODEL` | `virtio-net-pci` | QEMU NIC model for all dataplane interfaces. |
| `BOOTCFG_TEMPLATE` | `/startup.cfg.tpl` | Path to the `config.boot` Jinja-like template inside the container. |
| `USERNAME` | `vyos` | VyOS login username created in the boot config. |
| `PASSWORD` | `vyos` | VyOS login password (plain-text; hashed by the entrypoint via `mkpasswd`). |
| `NAMESERVERS` | `1.1.1.1,8.8.8.8` | Comma-separated list of DNS nameservers written into the boot config. |
| `SSH_FORWARD_PORT` | `2222` | Host port (on `127.0.0.1`) forwarded to the VyOS guest SSH port 22 via QEMU user-mode networking. Must be unique per pod on the same node. |
| `HTTPS_FORWARD_PORT` | `8443` | Host port (on `127.0.0.1`) forwarded to the VyOS guest HTTP API port 443. Used by the `vyos_api` wrapper. |
| `MGMT_NET` | `192.168.255.0/24` | QEMU user-mode management network prefix. |
| `MGMT_GATEWAY` | `192.168.255.2` | Gateway IP within the management network (assigned by QEMU slirp). |
| `MGMT_VM_IP` | `192.168.255.15/24` | IP assigned to the VyOS guest on the management network. |
| `WORKDIR` | `/run/vyos-seed` | Working directory for generated seed files. |

### Commonly tuned variables

- **`RAM_MB`**, increase to `2048` or more for heavyweight workloads or BGP full-table scenarios.
- **`CPU_CORES`**, increase to `2`–`4` for faster boot and configd commits.
- **`PASSWORD`**, change to a non-default value in production labs to prevent unauthorized serial/SSH access.

---

## KubeNDT Driver Integration

### Runtime declaration

`VyOSRouterDriver` implements `drivers_meta.RuntimeProvider` and returns `RuntimeQEMU`. That single declaration is what tells the rest of the backend that VyOS pods need the QEMU pod spec (`/dev/kvm` device, privileged mode, the `kubendt-internal-iface-counts` ConfigMap mount, serial shell as default). Users never write `qemu: true` in topology JSON. The runtime is a property of the driver, not of the topology.

The same value also propagates to the pod's `kubendt/runtime` and `kubendt/qemu` labels at StatefulSet creation time, and is surfaced via the `runtime` field of `GET /pods/{namespace}` for the frontend.

### Interface-name constraints (deploy/modify validation)

`VyOSRouterDriver` declares `InterfaceNameConstraints` in the backend driver registry:

| Property   | Value                                                        |
| ---------- | ------------------------------------------------------------ |
| `Pattern`  | `^eth\d+$`                                                   |
| `Reserved` | `eth0` (cluster passthrough), `eth999` (internal management) |

These constraints are enforced **at the API boundary** by both `DeployNetwork` and `ModifyNetwork`. A topology with `localIntf: "miau"` on a VyOS endpoint, or with `eth0` (which would clash with the cluster passthrough NIC), is rejected with `400 Bad Request` before any pods or topology CRDs are touched. Example error:

```
link[3].localIntf on node "router-0": name "miau" does not match pattern ^eth\d+$ (e.g. eth1, eth10, eth42)
link[5].peerIntf on node "router-1": name "eth0" is reserved by driver VyOSRouterDriver
```

This sits on top of the cross-driver Linux kernel rules (IFNAMSIZ ≤ 15, no `/`, `:` or whitespace) that apply to every driver. Drivers without an explicit `InterfaceNameConstrainer` implementation (e.g. host/switch/FRR/Linux router) only enforce the kernel rules.

### Executors

The KubeNDT backend talks to VyOS pods through the guest's **HTTP API** for the hot path and keeps SSH as the rescue path:

| Executor | Name | Use |
| --- | --- | --- |
| `vyos_api` | Raw API endpoint | `POST /retrieve` (full config as JSON, feeds interface/NAT/internet reads), `POST /show` |
| `vyos_api_apply` | Configure batches | driver CLI lines converted to `/configure` JSON ops, one atomic commit per batch against configd's persistent session |
| `vyos_ssh_cli` | Op-mode via SSH | ad-hoc `show ...` queries and custom actions |
| `vyos_ssh_apply` | Configure via SSH | legacy/rescue path (`vbash` herestring), no longer used by the action pipeline |

The API executors route through `vyos_api`, a curl wrapper inside the pod that POSTs to the guest over a second loopback hostfwd (`127.0.0.1:8443 → guest 443`). The API is enabled in the seed `config.boot` with a per-pod key generated by `entrypoint.sh` (like the SSH keypair, nothing secret is baked into the image), and it listens only on the internal management IP, never on the cluster network. One `/retrieve` call replaces the old `show interfaces` + `show nat source rules` op-mode pair (~1.8 s each), which is what made `GET /namespaces/ips` slow on VyOS-heavy topologies.

The SSH executors route through `ssh_qemu`, the wrapper over the loopback SSH hostfwd. Two details are worth knowing.

- **Per-pod keypair**. The management SSH key is generated by `entrypoint.sh` on every pod start (`ssh-keygen -t ed25519`). The public half rides the seed `config.boot`.
- **Connection multiplexing**. The wrapper sets `ControlMaster=auto` with `ControlPersist=300`, so the first call establishes the session and subsequent ones reuse it, skipping the SSH handshake. Stale sockets fall back to a direct connection.

### Action batching

When the `network_conf.json` for a pod triggers multiple configure-mode actions in sequence (e.g. `set_default_route` + `enable_snat`), the backend merges them into a **single `vyos_api_apply` invocation**, one atomic `POST /configure` request (and therefore one commit) for the whole batch. This avoids redundant commit overhead.

### Available `network_conf.json` actions

| Action type | Description |
| --- | --- |
| `link_up` / `link_down` | Enable / disable an interface |
| `set_ip` / `remove_ip` / `replace_ip` | Manage interface IP addresses |
| `set_default_route` / `remove_default_route` | Default gateway |
| `add_static_route` / `remove_static_route` | Static routes |
| `add_dns_nameserver` / `remove_dns_nameserver` | DNS server |
| `add_dns_search` / `remove_dns_search` | DNS search domain |
| `enable_snat` | Configure MASQUERADE (SNAT) on the given interface |
| `disable_snat` | Remove MASQUERADE rule |
| `enable_dnat` | Configure static DNAT port-forward |
| `disable_dnat` | Remove DNAT port-forward |

Example `network_conf.json` snippet for a router with internet access on `eth3`:

```json
{
  "pod": "router-0",
  "actions": [
    { "type": "replace_ip", "iface": "eth3", "cidr": "10.208.11.106/16" },
    { "type": "remove_default_route" },
    { "type": "set_default_route", "gateway": "10.208.0.1" },
    { "type": "add_dns_nameserver", "dns_server": "8.8.8.8" },
    { "type": "enable_snat", "iface": "eth3" },
    {
      "type": "add_static_route",
      "dst_cidr": "10.0.2.0/24",
      "gateway": "10.0.10.2",
      "device": "eth1"
    }
  ]
}
```

### NAT capability

The VyOS driver implements the `NATCapable` interface. This means:

- `enable_snat` / `disable_snat` configure `nat source` rules (MASQUERADE) via `vyos_api_apply`.
- `enable_dnat` / `disable_dnat` configure `nat destination` rules (port-forward) via `vyos_api_apply`.
- The backend's namespace IP poller reads the `nat source` rules from the cached `/retrieve` config JSON to detect whether a pod has internet access, and populates the `internet` field shown in the UI.

Rule numbers are derived deterministically from the interface name to avoid collisions and stay idempotent across re-runs.

### Interface state in the UI

The backend's `GetEffectiveInterfaceStates` parses the `show interfaces` output (specifically the `U/D` state column) and returns a per-interface `up/down` state. This is used by the frontend to display a green/red dot next to each interface label in the topology graph.

---

## Troubleshooting

### TAP Device Issues

```bash
# Check TAP devices created:
ip tuntap show
ip link show | grep tap

# Verify TC rules:
tc filter show dev eth1 ingress
tc filter show dev tap1 ingress

# Test connectivity:
# (from guest console) ping peer
```

### Config.boot Not Applied

```bash
# (from inside the pod) Check the seed ISO was generated by entrypoint.sh:
ls -lh /run/vyos-seed/seed.iso
cat /run/vyos-seed/config.boot

# (from the guest console / ssh_qemu) Verify the seed CD-ROM was mounted:
mount | grep -E 'sr0|cidata'

# Verify the preconfig script ran (matches the '[preconfig]' tag emitted
# by /opt/vyatta/etc/config/scripts/vyos-preconfig-bootup.script):
sudo journalctl -b | grep -F '[preconfig]'
# Expected: a single line of the form
#   vyos-router[...]: [preconfig] Applied /opt/vyatta/etc/config/config.boot from /dev/sr0

# Confirm the per-pod config.boot landed on disk:
cat /opt/vyatta/etc/config/config.boot
```

### Interface ends up as `eth4` (or any unexpected name) inside VyOS

If `show interfaces` inside the guest lists a NIC as `eth4` with no IP and Admin Down when the topology declared something different (e.g. `eth10`), the SMBIOS netmap path isn't taking effect. Check:

```bash
# Inside the pod, confirm the netmap entries were emitted as QEMU args:
kubectl logs <pod> | grep -E 'kubendt-netmap|SMBIOS'
# Should show lines like:
#   [INFO] Building MAC→ifname netmap (SMBIOS Type 11):
#   [INFO]     kubendt-netmap:e2:64:94:2b:41:20=eth1
#   [INFO]     kubendt-netmap:7a:c2:64:c5:6a:d8=eth10

# And the -smbios args ended up on the QEMU cmdline:
kubectl exec <pod> -- sh -c "ps -o args -A | grep qemu-system | tr ' ' '\\n' | grep kubendt-netmap"

# Inside the guest VM:
ssh_qemu "ls /sys/firmware/dmi/entries/ | grep '^11-'"
# Expected: 11-0  (Type 11 'OEM Strings' entry)

ssh_qemu "sudo tr '\\0' '\\n' < /sys/firmware/dmi/entries/11-0/raw | grep '^kubendt-netmap:'"
# Expected: one line per data NIC.

# Confirm both files are present in the qcow2:
ssh_qemu "ls -l /etc/udev/rules.d/64-kubendt-net.rules \
                /usr/local/bin/kubendt-net-name"

# Confirm the predef branch fired during boot:
ssh_qemu "sudo journalctl -b | grep 'predefined interface name'"
# Expected: one line per data NIC, e.g.
#   vyos_net_name[...]: predefined interface name for 'e6' is 'eth10'

# The full coldplug rename decisions (per NIC: hw-id lookup, predef, final
# name) are logged by vyos_net_name to:
ssh_qemu "sudo cat /run/udev/log/vyatta-net-name.coldplug"
# If a line says "use hw-id ... in config mapped to ..." the STOCK config.boot
# inside the qcow2 still contains hw-id entries; they take priority over the
# netmap predef and shift every rename. customize-qcow2.sh strips them at
# build time. Rebuild the image if they reappear.
# If SSH is down (commit failed), reach the guest over the serial console:
#   kubectl attach -it <pod>    # login with vyos / $PASSWORD
```

If the rules file or helper are missing inside the qcow2, the build's customizer stage failed silently, re-run with `--no-cache` and verify `customize-qcow2.sh` ran without errors (look for its `"Injecting kubendt assets into ... detected VyOS version dir: ..."` lines in the build log).

If the files are present but `/sys/firmware/dmi/entries/11-0/raw` is missing, QEMU was launched without the `-smbios type=11,...` args, check the kubectl logs for the `"Building MAC→ifname netmap"` section of the entrypoint.

If `/sys/firmware/dmi/entries/11-0/raw` exists and contains the strings, but the rename still doesn't happen, run `udevadm test` for one of the data interfaces inside the guest to see exactly which rule fires and what `vyos_net_name` is called with.

### SSH latency / 20-25s delay on backend calls

If KubeNDT operations on a VyOS pod take 20-25 seconds, the cause is likely VyOS sshd performing a reverse-DNS lookup on the connecting IP that times out (common when the router has no internet access or no working DNS).

**Fix**: ensure `disable-host-validation` is present in `startup.cfg.tpl` under `service { ssh { ... } }`. This is already included in the current template.

### QEMU Not Starting

```bash
# Check logs:
kubectl logs pod-name -c container-name

# Verify KVM available:
cat /proc/cpuinfo | grep kvm
ls -la /dev/kvm

# Ensure privileged mode:
kubectl get pod -o yaml | grep privileged

# Check device access:
kubectl exec pod -- ls -la /dev/kvm /dev/net/tun
```

## Summary

Building a QEMU-based network node involves:

1. **Creating the virgin image**: OS installation in QCOW2 (Phase 1), no manual edits inside the VM
2. **Build-time customization**: Stage 1 of the multi-stage Dockerfile runs `customize-qcow2.sh`, a `guestfish` wrapper that detects the VyOS version dir and injects the preconfig hook, the `64-kubendt-net.rules` udev rule, and the `kubendt-net-name` helper into `/boot/<version>/rw/` (the overlayfs upper layer) inside the qcow2
3. **Per-pod config**: `entrypoint.sh` generates `config.boot` from the actual pod interfaces (including the `eth0` cluster block and the pod's default route) and ships it via a seed CD-ROM, and also emits a MAC→ifname netmap to the guest via QEMU `-smbios type=11` (OEM Strings)
4. **Early-boot interface rename**: the build-time udev rule consumes the SMBIOS netmap (`/sys/firmware/dmi/entries/11-0/raw`) so non-sequential names (`eth10`, etc.) are renamed correctly _before_ VyOS' own rule runs, see [Interface Naming Inside The Guest](#interface-naming-inside-the-guest)
5. **TAP + TC**: pod eth ↔ TAP ↔ QEMU userspace ↔ guest eth, for the data NICs _and_ the `eth0` cluster passthrough, so the guest owns the pod's cluster IP and can masquerade twin traffic out of it
6. **API-level validation**: the VyOS driver declares an `InterfaceNameConstraints` of `^eth\d+$` with `eth0` and `eth999` reserved. The backend rejects invalid names at deploy/modify time
7. **Containerization**: Stage 2 of the Dockerfile is a slim runtime image with QEMU, the customized qcow2, and the launcher script
8. **Deployment**: the `VyOSRouterDriver` declares QEMU as its runtime via `drivers_meta.RuntimeProvider`, so KubeNDT applies the QEMU pod spec (KVM device, privileged mode, iface-counts ConfigMap mount, serial shell) automatically. No `qemu` flag in the topology JSON.

The result is a full-featured network OS running inside Kubernetes, with full control over interfaces, routing, NAT, and protocols.
