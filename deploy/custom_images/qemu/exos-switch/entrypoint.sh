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
# Two vCPUs: the guest answers an API call in half a second with them and
# in three to five with one, since every call spawns its CLI.
CPU_CORES="${CPU_CORES:-2}"
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

# The guest takes the management NIC's MAC as its system MAC, the one in
# its bridge id, so it must differ per pod: the pod's own eth0 MAC with
# the locally administered bit, which nothing else on the lab carries.
mgmt_nic_mac() {
  local base="52:54:00:12:34:56"
  if [[ -r /sys/class/net/eth0/address ]]; then
    base="$(cat /sys/class/net/eth0/address)"
  fi
  printf '02:%s' "${base#*:}"
}
MGMT_NIC_MAC="$(mgmt_nic_mac)"

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

# Idempotent, so a container restarted over a netns that still has the
# taps (a kill that skipped the cleanup) wires them again instead of dying.
wire_iface_to_tap() {
  local eth_if="$1"
  local tap_if="$2"

  ip link show "$tap_if" >/dev/null 2>&1 || ip tuntap add dev "$tap_if" mode tap
  ip link set "$tap_if" up
  ip link set "$eth_if" up || true

  ip link set "$eth_if" promisc on || true
  ip link set "$tap_if" promisc on || true

  tc qdisc del dev "$eth_if" ingress 2>/dev/null || true
  tc qdisc add dev "$eth_if" ingress
  tc filter add dev "$eth_if" parent ffff: protocol all u32 \
    match u8 0 0 action mirred egress redirect dev "$tap_if"

  tc qdisc del dev "$tap_if" ingress 2>/dev/null || true
  tc qdisc add dev "$tap_if" ingress
  tc filter add dev "$tap_if" parent ffff: protocol all u32 \
    match u8 0 0 action mirred egress redirect dev "$eth_if"
}

qemu_pci_addr_for_iface() {
  printf '0x%x' "$(($1 + 5))"
}

create_api_wrappers() {
  # exos_api <cli line>...: one JSON-RPC call per line, each body printed as
  # received, stopping at the first line the switch rejects, which it marks
  # with a status of ERROR. A line starting with "-" may fail (a VLAN that
  # exists already), and "@clear-port N" takes port N out of every VLAN it
  # is in, which the CLI has no single command for. exos_show is the same
  # call for reads, where a show command may carry an ERROR of some module
  # that is not running and the backend parses the JSON itself.
  cat > /usr/local/bin/exos_api <<EOF
#!/usr/bin/env bash
strict="\${EXOS_API_STRICT:-1}"
call() {
  payload=\$(jq -cn --arg c "\$1" '{jsonrpc:"2.0",method:"cli",params:[\$c],id:1}')
  body=\$(curl -sS --fail-with-body --connect-timeout 5 --max-time 25 \\
      -u "${API_USER}:${API_PASSWORD}" -H 'Content-Type: application/json' \\
      -d "\$payload" "http://127.0.0.1:${API_FORWARD_PORT}/jsonrpc/") || { echo "\$body"; return 1; }
  echo "\$body"
  case "\$body" in *'"status":"ERROR"'*|*'"status": "ERROR"'*) return 1 ;; esac
  return 0
}
for line in "\$@"; do
  case "\$line" in
    @clear-port\ *)
      port="\${line#@clear-port }"
      for vlan in \$(EXOS_API_STRICT=0 /usr/local/bin/exos_api "show ports \$port vlan" | jq -r '.result[]? | .show_ports_info_detail_vlans? | select(. != null) | .vlanName' | sort -u); do
        call "configure vlan \$vlan delete ports \$port" >/dev/null || true
      done
      echo '{"clear-port":"'"\$port"'"}'
      ;;
    -*)
      call "\${line#-}" || true
      ;;
    *)
      call "\$line" || if [ "\$strict" = 1 ]; then exit 1; fi
      ;;
  esac
done
exit 0
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
  # The guest's 5.10 kernel takes minutes to seed its random pool on its
  # own; a virtio rng makes the 150 s boot stall below rarer, not gone.
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

# The backend runs this after a peer restart, when Meshnet recreates the
# pod's end of the link: the new device gets the same wiring as at boot,
# without restarting QEMU. The same helper as the VyOS pod's.
cat > /usr/local/bin/kubendt-rewire-tc <<EOF
#!/bin/sh
set -e
PAIRS="${REWIRE_PAIRS}"
for pair in \$PAIRS; do
    eth=\${pair%:*}
    tap=\${pair#*:}
    [ -d /sys/class/net/\$eth ] || continue
    [ -d /sys/class/net/\$tap ] || continue
    ip link set \$eth up || true
    ip link set \$eth promisc on || true
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

# The startup lines, rendered from the template and sent once the switch
# says OPERATIONAL.
mkdir -p /run/exos
STARTUP_LINES=/run/exos/startup.xsf
: > "${STARTUP_LINES}"
if [[ -r "${STARTUP_TEMPLATE}" ]]; then
  sed -e "s|__POD_NAME__|${POD_NAME//|/\\|}|g" "${STARTUP_TEMPLATE}" | grep -v '^[[:space:]]*#' | grep -v '^[[:space:]]*$' > "${STARTUP_LINES}" || true
fi

# About one boot in five the guest gives up loading its configuration 150 s
# in ("waiting for all applications") and sits at the login prompt with its
# API dead for good. A reboot cures it, so the console is watched for that
# line and the API for OPERATIONAL, and the guest is booted again on the
# same taps, a few times at most.
BOOT_TIMEOUT="${BOOT_TIMEOUT:-300}"
MAX_BOOTS="${MAX_BOOTS:-3}"
CONSOLE_LOG=/run/exos/console.log
QEMU_PIDFILE=/run/exos/qemu.pid
REBOOT_FLAG=/run/exos/reboot
STOPPING=0

request_reboot() {
  echo "[WARN] ${1}, rebooting the guest"
  touch "${REBOOT_FLAG}"
  kill -TERM "$(cat "${QEMU_PIDFILE}" 2>/dev/null)" 2>/dev/null || true
}

watch_console() {
  while sleep 2; do
    if grep -q 'Failed to load configuration' "${CONSOLE_LOG}" 2>/dev/null; then
      request_reboot "the guest gave up loading its configuration"
      return 0
    fi
  done
}

# The readiness probe wants this file too, so the pod is Ready only once
# the startup lines are in and the API answers again after them.
READY_FLAG=/run/exos/ready

watch_api() {
  local started=${SECONDS} applied=0
  while (( SECONDS - started < BOOT_TIMEOUT )); do
    if exos_show 'show switch' 2>/dev/null | grep -q OPERATIONAL; then
      if (( applied == 0 )); then
        echo "[INFO] The switch is OPERATIONAL after $((SECONDS - started))s"
        while IFS= read -r line; do
          if exos_api "${line}" >/dev/null 2>&1; then
            echo "[INFO] startup: ${line}"
          else
            echo "[WARN] startup line rejected: ${line}"
          fi
        done < "${STARTUP_LINES}"
        applied=1
        continue
      fi
      touch "${READY_FLAG}"
      echo "[INFO] Startup lines applied and the API answers after $((SECONDS - started))s"
      return 0
    fi
    sleep 5
  done
  request_reboot "the switch was not OPERATIONAL after ${BOOT_TIMEOUT}s"
}

boot_guest() {
  local status=0 console_pid api_pid
  rm -f "${OVERLAY}" "${REBOOT_FLAG}" "${READY_FLAG}"
  : > "${CONSOLE_LOG}"
  qemu-img create -q -f qcow2 -F qcow2 -b "${IMAGE}" "${OVERLAY}"
  # The console still goes to the pod's stdio, where the log and the UI's
  # serial console find it, with a copy in a file for the watcher.
  "${QEMU_BIN}" "${QEMU_ARGS[@]}" <&0 > >(tee -a "${CONSOLE_LOG}") 2>&1 &
  echo "$!" > "${QEMU_PIDFILE}"
  watch_console &
  console_pid=$!
  watch_api &
  api_pid=$!
  wait "$(cat "${QEMU_PIDFILE}")" || status=$?
  kill "${console_pid}" "${api_pid}" 2>/dev/null || true
  return "${status}"
}

# The pod's stop lands on this shell now, not on QEMU.
on_stop() {
  STOPPING=1
  kill -TERM "$(cat "${QEMU_PIDFILE}" 2>/dev/null)" 2>/dev/null || true
}
trap on_stop TERM INT

boots=0
while :; do
  boots=$((boots + 1))
  echo "[INFO] Launching QEMU (${RAM_MB} MB, ${CPU_CORES} vCPU, ${#DATA_IFACES[@]} port(s)), boot ${boots}..."
  status=0
  boot_guest || status=$?
  if [[ -f "${REBOOT_FLAG}" && ${STOPPING} -eq 0 && ${boots} -lt ${MAX_BOOTS} ]]; then
    continue
  fi
  exit "${status}"
done
