#!/usr/bin/env bash
# EXOS switch pod: wires the pod's data interfaces to taps that QEMU hands
# the guest as its ports, in the order of their names (eth1 is port 1), and
# exposes the guest's HTTP API on loopback for the backend. The same tap and
# tc layout as the VyOS entrypoint, without the guest configuration: the
# image boots ready and the driver configures the switch over the API.
set -euo pipefail

IMAGE="${IMAGE:-/exos.qcow2}"
OVERLAY="${OVERLAY:-/run/exos/overlay.qcow2}"
RAM_MB="${RAM_MB:-1024}"
CPU_CORES="${CPU_CORES:-1}"
QEMU_BIN="${QEMU_BIN:-qemu-system-x86_64}"
NIC_MODEL="${NIC_MODEL:-virtio-net-pci}"

# The guest's HTTP API (JSON-RPC on port 80) over a loopback hostfwd, where
# the exos_api wrapper the backend execs talks to it.
API_FORWARD_PORT="${API_FORWARD_PORT:-8080}"
API_USER="${API_USER:-admin}"
API_PASSWORD="${API_PASSWORD:-}"
STARTUP_TEMPLATE="${STARTUP_TEMPLATE:-/startup.xsf.tpl}"

# What the driver reads to translate the pod's interfaces into the guest's
# port numbers: the guest numbers its ports by NIC order, so eth3 is port 2
# when eth2 does not exist, and only this file knows.
PORT_MAP="${PORT_MAP:-/run/kubendt/exos-ports}"

# The platform's link MTU, handed over by the backend: set on the pod's
# data interfaces and their taps. The guest forwards 1500-byte frames as it
# is, below that nothing in it needs setting.
LINK_MTU="${KUBENDT_LINK_MTU:-}"

POD_NAME="$(hostname)"
MGMT_NIC_MAC="52:54:00:12:34:56"

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
  # Everything but lo, eth0 (the cluster, which stays with the pod), the
  # taps and the usual system interfaces, in name order: eth1, eth2, eth10.
  ip -o link show \
    | awk -F': ' '{print $2}' \
    | sed 's/@.*//' \
    | grep -Ev '^(lo$|eth0$|tap[0-9]|tun[0-9]|veth|docker|cni|flannel|dummy)' \
    | sort -V
}

get_iface_mac() {
  cat "/sys/class/net/$1/address"
}

# A host uplink is a macvlan on the worker's NIC: the platform leaves its MTU
# to the host network, so the link MTU is only written on Meshnet's links.
is_uplink_iface() {
  ip -d link show "$1" 2>/dev/null | grep -q " macvlan "
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
  printf '0x%x' "$(($1 + 5))"
}

create_api_wrappers() {
  # exos_api <cli line>...: one JSON-RPC call per line, each body printed as
  # received, stopping at the first line the switch rejects, which it marks
  # with a status of ERROR. exos_show is the same call for reads, where a
  # show command may carry an ERROR of some module that is not running and
  # the backend parses the JSON itself.
  cat > /usr/local/bin/exos_api <<EOF
#!/usr/bin/env bash
strict="\${EXOS_API_STRICT:-1}"
for line in "\$@"; do
  payload=\$(jq -cn --arg c "\$line" '{jsonrpc:"2.0",method:"cli",params:[\$c],id:1}')
  if ! body=\$(curl -sS --fail-with-body --connect-timeout 5 --max-time 25 \\
      -u "${API_USER}:${API_PASSWORD}" -H 'Content-Type: application/json' \\
      -d "\$payload" "http://127.0.0.1:${API_FORWARD_PORT}/jsonrpc/"); then
    echo "\$body"
    exit 1
  fi
  echo "\$body"
  if [ "\$strict" = 1 ]; then
    case "\$body" in *'"status":"ERROR"'*|*'"status": "ERROR"'*) exit 1 ;; esac
  fi
done
EOF
  cat > /usr/local/bin/exos_show <<'EOF'
#!/usr/bin/env bash
EXOS_API_STRICT=0 exec /usr/local/bin/exos_api "$@"
EOF
  chmod +x /usr/local/bin/exos_api /usr/local/bin/exos_show
  echo "[INFO] Created helper commands: /usr/local/bin/exos_api, /usr/local/bin/exos_show"
}

create_api_wrappers

# Boot from an overlay of the image: the pod's writes stay in a small
# file instead of copying the whole qcow2 up into the container layer, and
# a restart starts clean again. cache=unsafe since it is all ephemeral.
mkdir -p "$(dirname "${OVERLAY}")"
rm -f "${OVERLAY}"
qemu-img create -q -f qcow2 -F qcow2 -b "${IMAGE}" "${OVERLAY}"

QEMU_ARGS=(
  -enable-kvm
  -m "${RAM_MB}"
  -smp "${CPU_CORES}"
  -cpu host
  -drive "file=${OVERLAY},if=ide,format=qcow2,cache=unsafe"
  -nographic
  -serial mon:stdio
  -nic none
  # The guest's 5.10 kernel takes minutes to seed its random pool on its own
  # and EXOS then waits 150 s for "all applications" before loading its
  # configuration, at random between boots; a virtio rng ends that.
  -device virtio-rng-pci
)

# How many data interfaces this pod is expected to have: the backend writes
# per-pod counts into the kubendt-internal-iface-counts ConfigMap, mounted
# at /etc/kubendt/iface-counts/. Wait for that many before starting QEMU,
# since a NIC cannot be added to the guest afterwards; the kubelet projects
# the ConfigMap asynchronously, so the file itself is waited for briefly.
IFACE_COUNTS_FILE="/etc/kubendt/iface-counts/${POD_NAME}"
IFACE_FILE_WAIT_MAX="${IFACE_FILE_WAIT_MAX:-30}"
waited_file=0
while [[ ! -r "${IFACE_COUNTS_FILE}" ]] && (( waited_file < IFACE_FILE_WAIT_MAX )); do
  sleep 1
  waited_file=$((waited_file + 1))
done

if [[ -r "${IFACE_COUNTS_FILE}" ]]; then
  EXPECTED_IFACES="$(tr -d '[:space:]' < "${IFACE_COUNTS_FILE}" 2>/dev/null)"
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
      echo "[WARN] Timed out after ${IFACE_WAIT_MAX}s waiting for ${EXPECTED_IFACES} ifaces (have $(get_data_ifaces | wc -l)). Continuing anyway."
    fi
  else
    echo "[WARN] ${IFACE_COUNTS_FILE} is not a number, falling back to a 5s settle sleep"
    sleep 5
  fi
else
  echo "[WARN] ${IFACE_COUNTS_FILE} did not appear within ${IFACE_FILE_WAIT_MAX}s, falling back to a 5s settle sleep"
  sleep 5
fi

DATA_IFACES=()
while IFS= read -r line; do
  [[ -n "${line}" ]] && DATA_IFACES+=("${line}")
done < <(get_data_ifaces)

echo "[INFO] Found dataplane interfaces: ${DATA_IFACES[*]:-<none>}"
echo "[INFO] Guest HTTP API will be forwarded on 127.0.0.1:${API_FORWARD_PORT}"

# The guest numbers its ports by NIC order after the management one, so
# the first data NIC is port 1 whatever its name; the port map tells the
# driver which is which.
mkdir -p "$(dirname "${PORT_MAP}")"
: > "${PORT_MAP}"
QEMU_ARGS+=(
  -netdev "user,id=mgmt0,hostfwd=tcp:127.0.0.1:${API_FORWARD_PORT}-:80"
  -device "${NIC_MODEL},netdev=mgmt0,mac=${MGMT_NIC_MAC},bus=pci.0,addr=0x5"
)

REWIRE_PAIRS=""
tap_idx=0
for eth_if in "${DATA_IFACES[@]}"; do
  tap_idx=$((tap_idx + 1))
  tap_if="tap${eth_if#eth}"
  guest_mac="$(get_iface_mac "${eth_if}")"
  pci_addr="$(qemu_pci_addr_for_iface "${tap_idx}")"

  echo "${eth_if}=${tap_idx}" >> "${PORT_MAP}"
  echo "[INFO] Wiring ${eth_if} <-> ${tap_if} (guest port ${tap_idx}, MAC ${guest_mac}, PCI ${pci_addr})"

  wire_iface_to_tap "${eth_if}" "${tap_if}"

  if [[ -n "${LINK_MTU}" ]] && ! is_uplink_iface "${eth_if}"; then
    ip link set dev "${eth_if}" mtu "${LINK_MTU}" 2>/dev/null \
      || echo "[WARN] ${eth_if} refused mtu ${LINK_MTU}, the backend's MTU pass will settle it"
    ip link set dev "${tap_if}" mtu "${LINK_MTU}" 2>/dev/null || true
  fi

  REWIRE_PAIRS="${REWIRE_PAIRS}${eth_if}:${tap_if} "

  QEMU_ARGS+=(
    -netdev "tap,id=p${tap_idx},ifname=${tap_if},script=no,downscript=no"
    -device "${NIC_MODEL},netdev=p${tap_idx},mac=${guest_mac},bus=pci.0,addr=${pci_addr}"
  )
done

# The backend runs this after a peer restart, when Meshnet may have given
# the peer end a new ifindex: the tc redirects are rebuilt against the live
# devices without restarting QEMU. Same helper as the VyOS pod's.
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

echo "[INFO] Port map at ${PORT_MAP}: $(tr '\n' ' ' < "${PORT_MAP}")"

# The startup lines, rendered from the template and sent once the API
# answers: a background job, QEMU owns the foreground and the console.
if [[ -r "${STARTUP_TEMPLATE}" ]]; then
  mkdir -p /run/exos
  sed -e "s|__POD_NAME__|${POD_NAME//|/\\|}|g" "${STARTUP_TEMPLATE}" | grep -v '^[[:space:]]*#' | grep -v '^[[:space:]]*$' > /run/exos/startup.xsf
  (
    for i in $(seq 1 90); do
      if exos_show 'show switch' 2>/dev/null | grep -q OPERATIONAL; then
        while IFS= read -r line; do
          if exos_api "${line}" >/dev/null 2>&1; then
            echo "[INFO] startup: ${line}"
          else
            echo "[WARN] startup line rejected: ${line}"
          fi
        done < /run/exos/startup.xsf
        exit 0
      fi
      sleep 5
    done
    echo "[WARN] startup lines not applied, the API never answered"
  ) &
fi

echo "[INFO] Launching QEMU (${RAM_MB} MB, ${CPU_CORES} vCPU, ${#DATA_IFACES[@]} port(s))..."
exec "${QEMU_BIN}" "${QEMU_ARGS[@]}"
