#!/usr/bin/env bash
# Fetches the EXOS qcow2 (the input for Dockerfile.kubendt): Extreme's own
# Switch Engine VM image, verified against the md5 its repository gives,
# then booted once in a throwaway container to check that it comes up and
# answers its HTTP API on this host, which is what the pod will need. The
# image is used as published: a configuration saved into it makes every
# boot wait 150 s for "all applications" before loading it, and the API
# needs nothing saved, it is on by default; the first-login questions
# only concern a human on the serial console. Like build-vyos-qcow2.sh,
# the boot runs in a container (podman or docker, whichever reaches
# /dev/kvm).
#
# The image is Extreme's: github.com/extremenetworks/Virtual_EXOS publishes
# it for lab use with a BSD-style notice. It is not redistributed from here,
# each site fetches its own, as with VyOS.

set -euo pipefail

BASE_URL="https://akamai-ep.extremenetworks.com/Extreme_P/github-en/Virtual_EXOS"

# The releases Virtual_EXOS lists, newest first, with the md5 its README
# gives. The oldest has a "v" in its file name. Add a line when Extreme
# publishes one; there is no API to ask.
RELEASES=(
  "33.6.1.14  EXOS-VM_33.6.1.14.qcow2   01ea4bea3f321f91910565bb223a2540"
  "33.1.1.31  EXOS-VM_33.1.1.31.qcow2   75a5422e040af92cefd238d3613a66bc"
  "32.7.2.19  EXOS-VM_32.7.2.19.qcow2   eba580a2e18d2a9cc972c9ece8917ea8"
  "32.6.3.126 EXOS-VM_v32.6.3.126.qcow2 5856b6c427bd605fe1c7adb6ee6b2659"
)

# The release the driver was written and tested against.
PINNED_VERSION="33.6.1.14"
VERSION=""
OUTPUT="./exos.qcow2"
CACHE_DIR="./qcow2-cache"
BUILDER_IMAGE="debian:bookworm-slim"
ENGINE=""
BOOT_CHECK=1

usage() {
  cat <<EOF
Usage: $(basename "$0") [OPTIONS] [VERSION]

Downloads Extreme's EXOS VM qcow2, verifies it, boots it once to check it
comes up with its HTTP API on this host, and writes the qcow2
Dockerfile.kubendt expects.

Arguments:
  VERSION              A release from --list, e.g. 33.6.1.14, or "latest".
                       Omit it for the pinned release the driver was tested
                       with (${PINNED_VERSION}).

Options:
  -l, --list           List the known releases and exit.
  -o, --output PATH    Where to write the qcow2 (default: ${OUTPUT}).
  -c, --cache-dir DIR  Download cache (default: ${CACHE_DIR}).
  -e, --engine NAME    Container engine, podman or docker. By default both
                       are probed and the first one that can use /dev/kvm
                       wins (no KVM anywhere means TCG, much slower).
  -n, --no-boot        Skip the boot check, just download and verify.
  -h, --help           This text.
EOF
}

die() { echo "ERROR: $*" >&2; exit 1; }
info() { echo "[INFO] $*"; }

list_releases() {
  info "Known EXOS VM releases (newest first):"
  for r in "${RELEASES[@]}"; do
    # shellcheck disable=SC2086 # the table rows are ours and word-split on purpose
    set -- $r
    printf '  %-12s %s\n' "$1" "$2"
  done
}

# ── argument parsing ─────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)      usage; exit 0 ;;
    -l|--list)      list_releases; exit 0 ;;
    -o|--output)    [[ $# -ge 2 ]] || die "$1 needs a value"; OUTPUT="$2"; shift 2 ;;
    -c|--cache-dir) [[ $# -ge 2 ]] || die "$1 needs a value"; CACHE_DIR="$2"; shift 2 ;;
    -e|--engine)    [[ $# -ge 2 ]] || die "$1 needs a value"; ENGINE="$2"; shift 2 ;;
    -n|--no-boot)   BOOT_CHECK=0; shift ;;
    -*)             die "unknown option: $1 (see --help)" ;;
    *)              [[ -z "${VERSION}" ]] || die "only one VERSION argument allowed"; VERSION="$1"; shift ;;
  esac
done

command -v curl >/dev/null || die "curl not found"
command -v md5sum >/dev/null || die "md5sum not found"

# ── the container engine, as build-vyos-qcow2.sh picks it ────────────────────
kvm_args_for() {
  case "$1" in
    podman) echo "--device /dev/kvm --group-add keep-groups" ;;
    docker) echo "--device /dev/kvm" ;;
  esac
}

probe_kvm() {
  [[ -e /dev/kvm ]] || return 1
  # shellcheck disable=SC2046 # kvm_args_for output is ours and word-splits safely
  "$1" run --rm $(kvm_args_for "$1") "${BUILDER_IMAGE}" test -w /dev/kvm >/dev/null 2>&1
}

candidates=()
if [[ -n "${ENGINE}" ]]; then
  [[ "${ENGINE}" == "podman" || "${ENGINE}" == "docker" ]] || die "--engine must be podman or docker"
  command -v "${ENGINE}" >/dev/null || die "${ENGINE} not found on this host"
  candidates=("${ENGINE}")
else
  for e in podman docker; do
    command -v "$e" >/dev/null && candidates+=("$e")
  done
  (( ${#candidates[@]} > 0 )) || die "neither podman nor docker found (the boot runs containerized)"
fi

ENGINE_BIN=""
KVM_ARGS=()
for e in "${candidates[@]}"; do
  if probe_kvm "$e"; then
    ENGINE_BIN="$e"
    read -ra KVM_ARGS <<< "$(kvm_args_for "$e")"
    info "Container engine: ${e} (KVM acceleration available)"
    break
  fi
done
if [[ -z "${ENGINE_BIN}" ]]; then
  ENGINE_BIN="${candidates[0]}"
  echo "[WARN] no engine can reach /dev/kvm, using ${ENGINE_BIN} with TCG: EXOS needs a real CPU model, expect trouble"
fi

[[ -e "${OUTPUT}" ]] && die "refusing to overwrite existing ${OUTPUT}, move it away or pick another --output"

# ── resolve the release ──────────────────────────────────────────────────────
if [[ -z "${VERSION}" ]]; then
  VERSION="${PINNED_VERSION}"
  info "No version given, building the pinned release ${VERSION} (pass 'latest' or one from --list for another)"
elif [[ "${VERSION}" == "latest" ]]; then
  # shellcheck disable=SC2086
  set -- ${RELEASES[0]}
  VERSION="$1"
  info "latest is ${VERSION}"
fi

QCOW2=""
MD5=""
for r in "${RELEASES[@]}"; do
  # shellcheck disable=SC2086
  set -- $r
  if [[ "$1" == "${VERSION}" ]]; then
    QCOW2="$2"
    MD5="$3"
    break
  fi
done
[[ -n "${QCOW2}" ]] || die "unknown release ${VERSION}, see --list (add it to RELEASES if Extreme published it)"
info "Building EXOS VM ${VERSION}"

# ── locate or download the qcow2 ─────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC=""
for d in "${CACHE_DIR}" "." "${SCRIPT_DIR}"; do
  if [[ -f "${d}/${QCOW2}" ]]; then
    SRC="$(realpath "${d}/${QCOW2}")"
    info "Using existing ${SRC}"
    break
  fi
done

if [[ -z "${SRC}" ]]; then
  mkdir -p "${CACHE_DIR}"
  info "Downloading ${BASE_URL}/${QCOW2}"
  curl -fL --retry 3 -o "${CACHE_DIR}/${QCOW2}.part" "${BASE_URL}/${QCOW2}"
  mv "${CACHE_DIR}/${QCOW2}.part" "${CACHE_DIR}/${QCOW2}"
  SRC="$(realpath "${CACHE_DIR}/${QCOW2}")"
fi

got="$(md5sum "${SRC}" | cut -d' ' -f1)"
[[ "${got}" == "${MD5}" ]] || die "md5 of ${SRC} is ${got}, expected ${MD5}: corrupt download, or Extreme republished the file (then update RELEASES)"
info "md5 verified"

if (( BOOT_CHECK == 0 )); then
  cp "${SRC}" "${OUTPUT}"
  info "Done: ${OUTPUT} (boot check skipped), drop it into the Dockerfile build context."
  exit 0
fi

WORKDIR="$(mktemp -d)"
trap 'rm -rf "${WORKDIR}"' EXIT
# ── the boot check ───────────────────────────────────────────────────────────
# An overlay of the image boots with the management NIC alone and the API
# forwarded, as the pod does; the check is the pod's readiness probe,
# `show switch` reporting the switch operational. About two minutes with
# KVM. The image itself is never written.
cp "${SRC}" "${WORKDIR}/exos.qcow2"
cat > "${WORKDIR}/boot-check.sh" <<'CHECK'
#!/bin/bash
set -e
qemu-img create -q -f qcow2 -F qcow2 -b /work/exos.qcow2 /work/overlay.qcow2
qemu-system-x86_64 $ACCEL -m 1024 -smp 1 \
  -drive file=/work/overlay.qcow2,if=ide,format=qcow2,cache=unsafe \
  -nographic -serial file:/work/console.log -monitor none -nic none \
  -netdev user,id=mgmt0,hostfwd=tcp:127.0.0.1:8080-:80 \
  -device virtio-net-pci,netdev=mgmt0 &
qemu=$!
payload='{"jsonrpc":"2.0","method":"cli","params":["show switch"],"id":1}'
for i in $(seq 1 60); do
  if curl -s -m 4 -u admin: -H 'Content-Type: application/json' -d "$payload" http://127.0.0.1:8080/jsonrpc/ 2>/dev/null | grep -q OPERATIONAL; then
    echo "[INFO] the switch is operational and the API answers after about $((i*5)) s"
    kill $qemu; wait $qemu 2>/dev/null || true
    exit 0
  fi
  kill -0 $qemu 2>/dev/null || { echo "FATAL: qemu exited"; tail -20 /work/console.log; exit 1; }
  sleep 5
done
echo "FATAL: the API never answered; the console said:"; tail -20 /work/console.log
kill $qemu; exit 1
CHECK

info "Booting once to check the image comes up with its API on this host..."
"${ENGINE_BIN}" run --rm \
  "${KVM_ARGS[@]}" \
  -v "${WORKDIR}":/work \
  -w /work \
  "${BUILDER_IMAGE}" \
  bash -c "apt-get update -qq >/dev/null \
    && apt-get install -yqq --no-install-recommends qemu-system-x86 qemu-utils curl >/dev/null \
    && if [ -w /dev/kvm ]; then export ACCEL='-enable-kvm -cpu host'; \
       else echo '[WARN] no usable /dev/kvm inside the container, using TCG'; export ACCEL='-accel tcg -cpu max'; fi \
    && bash /work/boot-check.sh" \
  || die "the boot check failed, scroll up for the console"

mv "${WORKDIR}/exos.qcow2" "${OUTPUT}"
info "Done: ${OUTPUT}, drop it into the Dockerfile build context."
