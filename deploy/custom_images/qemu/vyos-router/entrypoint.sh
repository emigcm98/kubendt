#!/usr/bin/env bash
set -euo pipefail

IMAGE="${IMAGE:-/vyos.qcow2}"
BOOTCFG_TEMPLATE="${BOOTCFG_TEMPLATE:-/startup.cfg.tpl}"
RAM_MB="${RAM_MB:-1024}"
QEMU_BIN="${QEMU_BIN:-qemu-system-x86_64}"
NIC_MODEL="${NIC_MODEL:-virtio-net-pci}"

SSH_FORWARD_PORT="${SSH_FORWARD_PORT:-2222}"
SSH_PRIVATE_KEY="${SSH_PRIVATE_KEY:-/ssh_key}"
SSH_PUBLIC_KEY="${SSH_PUBLIC_KEY:-/ssh_key.pub}"

# Management network for QEMU user-mode networking
MGMT_NET="${MGMT_NET:-192.168.255.0/24}"
MGMT_GATEWAY="${MGMT_GATEWAY:-192.168.255.2}"
MGMT_VM_IP="${MGMT_VM_IP:-192.168.255.15/24}"

# Base system config values
USERNAME="${USERNAME:-vyos}"
PASSWORD="${PASSWORD:-vyos}"
NAMESERVERS="${NAMESERVERS:-1.1.1.1,8.8.8.8}"
ENCRYPTED_PASSWORD=""

# StatefulSet guarantees that the pod's hostname equals the pod name
# (e.g. "router-0"). Use it as the canonical identifier rather than an
# explicit env var with a misleading "router" default, pods aren't
# named "router", they're "router-N".
POD_NAME="$(hostname)"

WORKDIR="${WORKDIR:-/run/vyos-seed}"
SEED_DIR="${SEED_DIR:-${WORKDIR}/seed}"
SEED_ISO="${SEED_ISO:-${WORKDIR}/seed.iso}"
FINAL_BOOTCFG="${FINAL_BOOTCFG:-${WORKDIR}/config.boot}"

# If true, move dataplane IPv4 addresses from pod ethX to guest ethX
MOVE_POD_IPS_TO_GUEST="${MOVE_POD_IPS_TO_GUEST:-true}"

# Seconds to wait for dataplane interfaces to be available in the pod
IFACE_SETTLE_SLEEP="${IFACE_SETTLE_SLEEP:-5}"

write_ssh_private_key() {
  mkdir -p "$(dirname "${SSH_PRIVATE_KEY}")"

  cat > "${SSH_PRIVATE_KEY}" <<'EOF'
-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACCdDV9sg3z3URmhPzzlKtZ8gPwXStoOCqwTtIyfCbXzjQAAAIhZTR1GWU0d
RgAAAAtzc2gtZWQyNTUxOQAAACCdDV9sg3z3URmhPzzlKtZ8gPwXStoOCqwTtIyfCbXzjQ
AAAEDfo/nmfVkNSr40B9ngPrVrji4yKoWNhiRhB865wd8ag50NX2yDfPdRGaE/POUq1nyA
/BdK2g4KrBO0jJ8JtfONAAAAAAECAwQF
-----END OPENSSH PRIVATE KEY-----
EOF

  chmod 600 "${SSH_PRIVATE_KEY}"

  ssh-keygen -y -f "${SSH_PRIVATE_KEY}" > "${SSH_PUBLIC_KEY}"
  chmod 644 "${SSH_PUBLIC_KEY}"

  echo "[INFO] Wrote management SSH private key to ${SSH_PRIVATE_KEY}"
  echo "[INFO] Wrote management SSH public key to ${SSH_PUBLIC_KEY}"
}

cleanup() {
  set +e

  for dev in $(ip -o link show | awk -F': ' '{print $2}' | sed 's/@.*//' | grep -E '^tap[0-9]+$' || true); do
    tc qdisc del dev "$dev" ingress 2>/dev/null || true
    ip link del "$dev" 2>/dev/null || true
  done

  while IFS= read -r dev; do
    [[ -z "$dev" ]] && continue
    tc qdisc del dev "$dev" ingress 2>/dev/null || true
  done < <(get_data_ifaces || true)
}
trap cleanup EXIT

get_data_ifaces() {
  # Return all data interfaces: everything except lo, eth0, tap*, tun*, veth*, and other system interfaces.
  ip -o link show \
    | awk -F': ' '{print $2}' \
    | sed 's/@.*//' \
    | grep -Ev '^(lo$|eth0$|tap[0-9]|tun[0-9]|veth|docker|cni|flannel|dummy)' \
    | sort -V
}

get_iface_mac() {
  local iface="$1"
  cat "/sys/class/net/${iface}/address"
}

get_iface_ipv4() {
  local iface="$1"
  ip -4 -o addr show dev "$iface" | awk '{print $4}' | head -n1
}

remove_iface_ipv4() {
  local iface="$1"
  local addr="$2"

  [[ -z "${addr}" ]] && return 0

  echo "[INFO] Removing IPv4 ${addr} from pod ${iface}"
  ip addr del "${addr}" dev "${iface}" 2>/dev/null || true
}

wire_iface_to_tap() {
  local eth_if="$1"
  local tap_if="$2"

  ip tuntap add dev "$tap_if" mode tap
  ip link set "$tap_if" up
  ip link set "$eth_if" up || true

  ip link set "$eth_if" promisc on || true
  ip link set "$tap_if" promisc on || true

  tc qdisc add dev "$eth_if" ingress 2>/dev/null || true
  tc filter add dev "$eth_if" parent ffff: protocol all u32 \
    match u8 0 0 action mirred egress redirect dev "$tap_if" 2>/dev/null || true

  tc qdisc add dev "$tap_if" ingress 2>/dev/null || true
  tc filter add dev "$tap_if" parent ffff: protocol all u32 \
    match u8 0 0 action mirred egress redirect dev "$eth_if" 2>/dev/null || true
}

qemu_pci_addr_for_iface() {
  local idx="$1"
  printf '0x%x' "$((idx + 5))"
}

generate_nameservers_block() {
  local output_file="$1"
  : > "${output_file}"

  local ns
  IFS=',' read -ra ns_array <<< "${NAMESERVERS}"
  for ns in "${ns_array[@]}"; do
    ns="$(echo "${ns}" | xargs)"
    [[ -n "${ns}" ]] && echo "    name-server \"${ns}\"" >> "${output_file}"
  done
}

generate_interfaces_block() {
  local output_file="$1"
  : > "${output_file}"

  local eth_if guest_mac guest_ipv4

  while IFS= read -r eth_if; do
    [[ -z "${eth_if}" ]] && continue
    guest_mac="$(get_iface_mac "${eth_if}")"
    guest_ipv4="$(get_iface_ipv4 "${eth_if}")"

    {
      echo "    ethernet ${eth_if} {"
      if [[ -n "${guest_ipv4}" ]]; then
        echo "        address \"${guest_ipv4}\""
      fi
      echo "        description \"KubeNDT ${eth_if}\""
      echo "        hw-id \"${guest_mac}\""
      echo "        offload {"
      echo "            gro"
      echo "            gso"
      echo "            sg"
      echo "            tso"
      echo "        }"
      echo "    }"
    } >> "${output_file}"

    if [[ "${MOVE_POD_IPS_TO_GUEST}" == "true" && -n "${guest_ipv4}" ]]; then
      remove_iface_ipv4 "${eth_if}" "${guest_ipv4}"
    fi
  done < <(get_data_ifaces)
}

generate_boot_config() {
  local interfaces_tmp="$1"
  local nameservers_tmp="$2"

  if [[ ! -f "${BOOTCFG_TEMPLATE}" ]]; then
    echo "[ERROR] Missing template: ${BOOTCFG_TEMPLATE}" >&2
    exit 1
  fi

  if [[ ! -f "${SSH_PUBLIC_KEY}" ]]; then
    echo "[ERROR] Missing SSH public key: ${SSH_PUBLIC_KEY}" >&2
    exit 1
  fi

  mkdir -p "$(dirname "${FINAL_BOOTCFG}")"

  local ssh_pubkey
  local hostname_short
  local mgmt_vm_ip_no_cidr

  ssh_pubkey="$(awk '{print $2}' "${SSH_PUBLIC_KEY}")"
  hostname_short="$(hostname -s)"
  mgmt_vm_ip_no_cidr="${MGMT_VM_IP%%/*}"

  sed \
    -e "/^[[:space:]]*__INTERFACES_BLOCK__[[:space:]]*$/r ${interfaces_tmp}" \
    -e '/^[[:space:]]*__INTERFACES_BLOCK__[[:space:]]*$/d' \
    -e "/^[[:space:]]*__NAMESERVERS_BLOCK__[[:space:]]*$/r ${nameservers_tmp}" \
    -e '/^[[:space:]]*__NAMESERVERS_BLOCK__[[:space:]]*$/d' \
    -e "s|__HOSTNAME__|${hostname_short//|/\\|}|g" \
    -e "s|__USER__|${USERNAME//|/\\|}|g" \
    -e "s|__ENCRYPTED_PASSWORD__|${ENCRYPTED_PASSWORD//|/\\|}|g" \
    -e "s|__SSH_PUBLIC_KEY__|${ssh_pubkey//|/\\|}|g" \
    -e "s|__MGMT_VM_IP__|${MGMT_VM_IP//|/\\|}|g" \
    -e "s|__MGMT_VM_IP_NO_CIDR__|${mgmt_vm_ip_no_cidr//|/\\|}|g" \
    -e "s|__MGMT_GATEWAY__|${MGMT_GATEWAY//|/\\|}|g" \
    "${BOOTCFG_TEMPLATE}" \
    > "${FINAL_BOOTCFG}"

  echo "[INFO] Generated VyOS config.boot at ${FINAL_BOOTCFG}"
}

generate_seed_iso() {
  mkdir -p "${SEED_DIR}"
  rm -f "${SEED_ISO}"
  cp "${FINAL_BOOTCFG}" "${SEED_DIR}/config.boot"

  (
    cd "${SEED_DIR}"
    genisoimage \
      -output "${SEED_ISO}" \
      -volid cidata \
      -joliet \
      -rock \
      config.boot >/dev/null 2>&1
  )

  echo "[INFO] Generated seed ISO at ${SEED_ISO}"
}

create_ssh_qemu_wrapper() {
  cat > /usr/local/bin/ssh_qemu <<EOF
#!/usr/bin/env bash
exec ssh -i "${SSH_PRIVATE_KEY}" -p "${SSH_FORWARD_PORT}" -o StrictHostKeyChecking=no -o ConnectTimeout=5 -o BatchMode=yes -o ServerAliveInterval=3 -o ServerAliveCountMax=3 "${USERNAME}@127.0.0.1" "\$@"
EOF

  chmod +x /usr/local/bin/ssh_qemu
  echo "[INFO] Created helper command: /usr/local/bin/ssh_qemu"
}

write_ssh_private_key
create_ssh_qemu_wrapper

QEMU_ARGS=(
  -enable-kvm
  -m "${RAM_MB}"
  -cpu host
  -drive "file=${IMAGE},if=virtio,format=qcow2"
  -nographic
  -serial mon:stdio
  -nic none
)

QEMU_ARGS+=(
  -netdev "user,id=mgmt0,net=${MGMT_NET},hostfwd=tcp:127.0.0.1:${SSH_FORWARD_PORT}-:22"
  -device "${NIC_MODEL},netdev=mgmt0,mac=52:54:00:12:34:56,bus=pci.0,addr=0x05"
)

# Determine the number of dataplane interfaces this pod is expected to have.
# kubendt writes per-pod expected counts into the kubendt-internal-iface-counts
# ConfigMap, mounted read-only at /etc/kubendt/iface-counts/. If the file for
# THIS pod is present we wait deterministically until that many data ifaces
# are visible; otherwise we fall back to the legacy fixed sleep so the image
# stays usable in environments that don't (yet) ship the ConfigMap.
#
# The kubelet projection of a ConfigMap volume is refreshed asynchronously,
# so for brand-new pods created during a scale-up the per-pod key may not
# materialize on disk the instant the entrypoint starts. We poll briefly
# for the file before deciding it's truly absent.
IFACE_COUNTS_FILE="/etc/kubendt/iface-counts/${POD_NAME}"
IFACE_FILE_WAIT_MAX="${IFACE_FILE_WAIT_MAX:-30}"
waited_file=0
while [[ ! -r "${IFACE_COUNTS_FILE}" ]] && (( waited_file < IFACE_FILE_WAIT_MAX )); do
  sleep 1
  waited_file=$((waited_file + 1))
done

if [[ -r "${IFACE_COUNTS_FILE}" ]]; then
  if (( waited_file > 0 )); then
    echo "[INFO] ${IFACE_COUNTS_FILE} appeared after ${waited_file}s (kubelet projection sync)"
  fi
  EXPECTED_IFACES="$(cat "${IFACE_COUNTS_FILE}" 2>/dev/null | tr -d '[:space:]')"
  if [[ "${EXPECTED_IFACES}" =~ ^[0-9]+$ ]]; then
    echo "[INFO] Expected dataplane interface count for ${POD_NAME}: ${EXPECTED_IFACES} (from ConfigMap)"
    IFACE_WAIT_MAX="${IFACE_WAIT_MAX:-60}"
    waited=0
    while (( waited < IFACE_WAIT_MAX )); do
      cur_count=$(get_data_ifaces | wc -l)
      if (( cur_count >= EXPECTED_IFACES )); then
        echo "[INFO] Dataplane interfaces ready: ${cur_count} present (>= ${EXPECTED_IFACES} expected) after ${waited}s"
        break
      fi
      sleep 1
      waited=$((waited + 1))
    done
    if (( waited >= IFACE_WAIT_MAX )); then
      cur_count=$(get_data_ifaces | wc -l)
      echo "[WARN] Timed out after ${IFACE_WAIT_MAX}s waiting for ${EXPECTED_IFACES} ifaces (have ${cur_count}). Continuing anyway."
    fi
  else
    echo "[WARN] ${IFACE_COUNTS_FILE} contents not a number (${EXPECTED_IFACES@Q}), falling back to fixed sleep"
    echo "[INFO] Waiting ${IFACE_SETTLE_SLEEP}s for dataplane interfaces to settle..."
    sleep "${IFACE_SETTLE_SLEEP}"
  fi
else
  echo "[WARN] ${IFACE_COUNTS_FILE} did not appear within ${IFACE_FILE_WAIT_MAX}s; falling back to fixed sleep"
  echo "[INFO] Waiting ${IFACE_SETTLE_SLEEP}s for dataplane interfaces to settle..."
  sleep "${IFACE_SETTLE_SLEEP}"
fi

DATA_IFACES=()
while IFS= read -r line; do
  [[ -n "${line}" ]] && DATA_IFACES+=("${line}")
done < <(get_data_ifaces)

echo "[INFO] Found dataplane interfaces: ${DATA_IFACES[*]:-<none>}"
echo "[INFO] MOVE_POD_IPS_TO_GUEST=${MOVE_POD_IPS_TO_GUEST}"
echo "[INFO] Management SSH will be forwarded on 127.0.0.1:${SSH_FORWARD_PORT}"
echo "[INFO] Management network: ${MGMT_NET}"
echo "[INFO] Management gateway: ${MGMT_GATEWAY}"
echo "[INFO] Management VM IP: ${MGMT_VM_IP}"
echo "[INFO] VyOS user: ${USERNAME}"
echo "[INFO] Nameservers: ${NAMESERVERS}"

mkdir -p "${WORKDIR}"
rm -rf "${SEED_DIR}"
mkdir -p "${SEED_DIR}"

INTERFACES_TMP="$(mktemp)"
NAMESERVERS_TMP="$(mktemp)"

ENCRYPTED_PASSWORD="$(mkpasswd -m sha-512 -R 656000 -S iQooHUXD1YCIzFZw "${PASSWORD}")"

generate_interfaces_block "${INTERFACES_TMP}"
generate_nameservers_block "${NAMESERVERS_TMP}"
generate_boot_config "${INTERFACES_TMP}" "${NAMESERVERS_TMP}"

rm -f "${INTERFACES_TMP}" "${NAMESERVERS_TMP}"

echo "[INFO] Generated interface bindings:"
grep -A10 -B0 '^    ethernet ' "${FINAL_BOOTCFG}" || true

generate_seed_iso

QEMU_ARGS+=(
  -drive "file=${SEED_ISO},format=raw,media=cdrom,readonly=on"
)

# MAC→ifname table consumed inside the VM by the kubendt-net-name udev helper
# (see 64-kubendt-net.rules). Delivered via SMBIOS Type 11 (OEM Strings) so
# it is readable from the guest at /sys/firmware/dmi/entries/11-0/raw before
# udev coldplug runs, the only way to get VyOS to honour non-sequential
# names (eth10, holes after a delete, etc.) at first boot. The seed CD-ROM
# is mounted too late.
#
# Why SMBIOS and not QEMU fw_cfg: VyOS' custom kernel builds with
# CONFIG_FW_CFG_SYSFS unset, so /sys/firmware/qemu_fw_cfg never appears.
# DMI/SMBIOS parsing is in the kernel core (CONFIG_DMI=y, ubiquitous) and
# requires no module.
echo "[INFO] Building MAC→ifname netmap (SMBIOS Type 11):"

tap_idx=0
REWIRE_PAIRS=""
for eth_if in "${DATA_IFACES[@]}"; do
  tap_idx=$((tap_idx + 1))
  # Tap name uses the same numeric suffix as the eth it bridges, so that
  # `tc filter show dev tap923` is self-explanatory and the eth↔tap mapping
  # is obvious in logs (eth2↔tap2, eth923↔tap923). tap_idx stays sequential
  # for the QEMU netdev id and PCI slot, which is what determines guest
  # enumeration order. Assumes ^eth\d+$, true for every QEMU driver today
  # (VyOS). If a future QEMU driver allows arbitrary names this needs a fallback.
  tap_if="tap${eth_if#eth}"
  guest_mac="$(get_iface_mac "${eth_if}")"
  pci_addr="$(qemu_pci_addr_for_iface "${tap_idx}")"

  echo "[INFO] Wiring ${eth_if} <-> ${tap_if}"
  echo "[INFO] ${eth_if} MAC: ${guest_mac}"
  echo "[INFO] ${eth_if} PCI addr: ${pci_addr}"

  wire_iface_to_tap "${eth_if}" "${tap_if}"

  echo "[INFO]     kubendt-netmap:${guest_mac}=${eth_if}"

  REWIRE_PAIRS="${REWIRE_PAIRS}${eth_if}:${tap_if} "

  QEMU_ARGS+=(
    -netdev "tap,id=p${tap_idx},ifname=${tap_if},script=no,downscript=no"
    -device "${NIC_MODEL},netdev=p${tap_idx},mac=${guest_mac},bus=pci.0,addr=${pci_addr}"
    -smbios "type=11,value=kubendt-netmap:${guest_mac}=${eth_if}"
  )
done

# Drop a tiny helper the backend can invoke after a peer restart to refresh
# our pod-side TC redirects. Idempotent: deletes any stale qdiscs/filters
# on each eth/tap and re-installs the L2-passthrough mirred pair against
# whatever the current ifindex is.
#
# Necessary because meshnet can occasionally recreate the peer end of a
# veth/vxlan device with a new ifindex (e.g. on a worker node change for
# the peer pod). When that happens, our pre-existing TC rules keep
# pointing at the dead ifindex and silently drop traffic. Re-running this
# script reattaches them against the live ifindex without restarting QEMU.
cat > /usr/local/bin/kubendt-rewire-tc <<EOF
#!/bin/sh
set -e
PAIRS="${REWIRE_PAIRS}"
for pair in \$PAIRS; do
    eth=\${pair%:*}
    tap=\${pair#*:}
    [ -d /sys/class/net/\$eth ] || continue
    [ -d /sys/class/net/\$tap ] || continue
    tc qdisc del dev \$eth ingress 2>/dev/null || true
    tc qdisc del dev \$tap ingress 2>/dev/null || true
    tc qdisc add dev \$eth ingress
    tc qdisc add dev \$tap ingress
    tc filter add dev \$eth parent ffff: protocol all u32 \\
        match u8 0 0 action mirred egress redirect dev \$tap
    tc filter add dev \$tap parent ffff: protocol all u32 \\
        match u8 0 0 action mirred egress redirect dev \$eth
    echo "kubendt-rewire-tc: \$eth <-> \$tap re-wired"
done
EOF
chmod +x /usr/local/bin/kubendt-rewire-tc
echo "[INFO] Generated /usr/local/bin/kubendt-rewire-tc with pairs: ${REWIRE_PAIRS}"

echo "[INFO] Seed ISO attached to QEMU as CD-ROM: ${SEED_ISO}"

if [[ -r "${FINAL_BOOTCFG}" ]]; then
  echo "[INFO] Generated config present at ${FINAL_BOOTCFG}"
else
  echo "[ERROR] Failed to generate ${FINAL_BOOTCFG}"
  exit 1
fi

echo "[INFO] Launching QEMU..."
exec "${QEMU_BIN}" "${QEMU_ARGS[@]}"