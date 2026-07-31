#!/usr/bin/env bash
# Unattended replacement for Phase 1 of README-vyos.md.
#
# VyOS only publishes rolling releases as ISO (no qcow2 asset), so someone
# has to sit through `install image` once. This script is that someone: it
# downloads the ISO and answers the installer prompts over the serial console
# with expect, inside a throwaway container (podman or docker, whichever can
# reach /dev/kvm) so the host needs nothing else. With KVM it takes ~5
# minutes; without, TCG works but go grab a coffee.
#
# The resulting vyos.qcow2 is the "virgin" image the Dockerfile build context
# expects, no manual edits, customize-qcow2.sh does the rest at build time.

set -euo pipefail

REPO="vyos/vyos-nightly-build"
API="https://api.github.com/repos/${REPO}"

VERSION=""
OUTPUT="./vyos.qcow2"
DISK_GB="10"
CACHE_DIR="./iso-cache"
BUILDER_IMAGE="debian:bookworm-slim"
LIST_COUNT="15"
ENGINE=""

usage() {
  cat <<EOF
Usage: $(basename "$0") [OPTIONS] [VERSION]

Builds a virgin VyOS qcow2 (the input for Dockerfile.kubendt) by running
the VyOS installer unattended inside a container (podman or docker).

Arguments:
  VERSION              Rolling release tag, e.g. 2026.07.29-0032-rolling.
                       Omit it to build the latest release.

Options:
  -l, --list           List the most recent rolling releases and exit.
  -o, --output PATH    Where to write the qcow2 (default: ${OUTPUT}).
  -s, --disk-size GB   Disk size in GB (default: ${DISK_GB}).
  -c, --cache-dir DIR  ISO download cache (default: ${CACHE_DIR}).
  -e, --engine NAME    Container engine, podman or docker. By default both
                       are probed and the first one that can use /dev/kvm
                       wins (no KVM anywhere → TCG, much slower).
  -h, --help           This text.

Examples:
  $(basename "$0")                          # latest rolling → ./vyos.qcow2
  $(basename "$0") --list                   # see what's available
  $(basename "$0") 2026.07.29-0032-rolling  # pin a specific release
  $(basename "$0") -o /tmp/vyos.qcow2 -s 20
EOF
}

die() { echo "ERROR: $*" >&2; exit 1; }
info() { echo "[INFO] $*"; }

# GitHub API responses are JSON; parse with python3 when we have it and fall
# back to grep otherwise. Both paths return one item per line.
api_get() {
  curl -fsSL --retry 3 --retry-delay 2 "$1" \
    || die "GitHub API request failed: $1 (rate limit? network?)"
}

latest_version() {
  api_get "${API}/releases/latest" | {
    if command -v python3 >/dev/null; then
      python3 -c 'import json,sys; print(json.load(sys.stdin)["tag_name"])'
    else
      grep -m1 '"tag_name"' | cut -d'"' -f4
    fi
  }
}

list_releases() {
  info "Last ${LIST_COUNT} rolling releases of ${REPO} (newest first):"
  api_get "${API}/releases?per_page=${LIST_COUNT}" | {
    if command -v python3 >/dev/null; then
      python3 -c '
import json, sys
for r in json.load(sys.stdin):
    print("  {:34} ({})".format(r["tag_name"], r["published_at"][:10]))'
    else
      grep '"tag_name"' | cut -d'"' -f4 | sed 's/^/  /'
    fi
  }
}

# ── argument parsing ─────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)      usage; exit 0 ;;
    -l|--list)      list_releases; exit 0 ;;
    -o|--output)    [[ $# -ge 2 ]] || die "$1 needs a value"; OUTPUT="$2"; shift 2 ;;
    -s|--disk-size) [[ $# -ge 2 ]] || die "$1 needs a value"; DISK_GB="$2"; shift 2 ;;
    -c|--cache-dir) [[ $# -ge 2 ]] || die "$1 needs a value"; CACHE_DIR="$2"; shift 2 ;;
    -e|--engine)    [[ $# -ge 2 ]] || die "$1 needs a value"; ENGINE="$2"; shift 2 ;;
    -*)             die "unknown option: $1 (see --help)" ;;
    *)              [[ -z "${VERSION}" ]] || die "only one VERSION argument allowed"; VERSION="$1"; shift ;;
  esac
done

[[ "${DISK_GB}" =~ ^[0-9]+$ ]] || die "--disk-size must be a number, got '${DISK_GB}'"

# ── preflight ────────────────────────────────────────────────────────────────
command -v curl >/dev/null || die "curl not found"

# The installer runs inside a container; podman and docker both work. What
# varies is /dev/kvm access across the container boundary: docker's rootful
# daemon reaches the device as plain root, while rootless podman drops the
# user's supplementary groups (kvm among them) unless the OCI runtime honours
# keep-groups. crun does, runc does not. Rather than guessing runtimes, just
# probe each candidate with `test -w /dev/kvm` and take the first that can
# accelerate. If none can, TCG still works, only much slower.
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
  (( ${#candidates[@]} > 0 )) || die "neither podman nor docker found (the installer runs containerized)"
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
  echo "[WARN] no engine can reach /dev/kvm, using ${ENGINE_BIN} with TCG, expect a much slower install (30+ min)"
  echo "[WARN] hints: is $USER in the kvm group? rootless podman also needs the crun runtime for keep-groups"
fi

if [[ -e "${OUTPUT}" ]]; then
  die "refusing to overwrite existing ${OUTPUT}, move it away or pick another --output"
fi

if [[ -z "${VERSION}" ]]; then
  info "No version given, resolving latest rolling release..."
  VERSION="$(latest_version)"
  [[ -n "${VERSION}" ]] || die "could not resolve the latest release tag"
fi
info "Building VyOS ${VERSION}"

ISO="vyos-${VERSION}-generic-amd64.iso"
ISO_URL="https://github.com/${REPO}/releases/download/${VERSION}/${ISO}"

# ── locate or download the ISO ───────────────────────────────────────────────
# Reuse an already-downloaded ISO if it's sitting in the cache, the current
# directory, or next to this script, no point in pulling 600 MB twice.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ISO_DIR=""
for d in "${CACHE_DIR}" "." "${SCRIPT_DIR}"; do
  if [[ -f "${d}/${ISO}" ]]; then
    ISO_DIR="$(realpath "${d}")"
    info "Using existing ${ISO_DIR}/${ISO}"
    break
  fi
done

if [[ -z "${ISO_DIR}" ]]; then
  # Fail fast on a typo'd version instead of a mid-download 404.
  curl -fsIL -o /dev/null "${ISO_URL}" \
    || die "release asset not found: ${ISO_URL} (check the tag with --list)"
  mkdir -p "${CACHE_DIR}"
  info "Downloading ${ISO_URL}"
  curl -fL --retry 3 -o "${CACHE_DIR}/${ISO}.part" "${ISO_URL}"
  mv "${CACHE_DIR}/${ISO}.part" "${CACHE_DIR}/${ISO}"
  ISO_DIR="$(realpath "${CACHE_DIR}")"
fi

# A VyOS ISO is several hundred MB; anything tiny is an error page or a
# truncated download that would fail cryptically inside QEMU.
iso_size=$(stat -c%s "${ISO_DIR}/${ISO}")
if (( iso_size < 300*1024*1024 )); then
  die "${ISO_DIR}/${ISO} is only $((iso_size/1024/1024)) MB, corrupt download? Delete it and retry."
fi

WORKDIR="$(mktemp -d)"
trap 'rm -rf "${WORKDIR}"' EXIT

# ── the expect dialog ────────────────────────────────────────────────────────
# Mirrors the interactive sequence in README-vyos.md Phase 1: defaults for
# everything, vyos/vyos credentials. If a VyOS release ever rewords a prompt,
# this is the place to fix, run the manual install once and compare.
cat > "${WORKDIR}/install.expect" <<'EXPECT'
set timeout 900
log_user 1

set iso   $env(ISO)
set disk  $env(DISK)
set accel $env(ACCEL)

spawn sh -c "qemu-system-x86_64 $accel -m 2048 -smp 2 \
  -drive file=$disk,if=virtio,format=qcow2 \
  -drive file=$iso,media=cdrom \
  -nographic -serial mon:stdio"

# If qemu dies (or a prompt never shows up) fail loudly instead of the
# cryptic "spawn id not open" cascade. Must be armed AFTER spawn: without a
# spawn id, expect_before binds to expect's own stdin, which is closed in a
# non-interactive container and fires eof instantly.
expect_before {
    eof     { puts "\nFATAL: qemu exited before the installer finished"; exit 1 }
    timeout { puts "\nFATAL: timed out waiting for an installer prompt (did VyOS reword it?)"; exit 1 }
}

# Live ISO boot can be slow, especially under TCG.
expect -timeout 1800 "vyos login:"   { send "vyos\r" }
expect "Password:"                   { send "vyos\r" }
expect "$ "                          { send "install image\r" }
expect "continue?*\[y/N\]"           { send "y\r" }
expect "name this image?*:"          { send "\r" }
expect "password for the*user*:"     { send "vyos\r" }
expect "confirm password*:"          { send "vyos\r" }
expect "console should be used*:"    { send "\r" }
expect "used for installation?*:"    { send "\r" }
expect "delete all data*\[y/N\]"     { send "y\r" }
expect "free space on the drive?*\[Y/n\]" { send "y\r" }
expect "as boot config?*:"           { send "\r" }
# Partitioning + squashfs copy + grub run here; then back to the prompt.
expect -timeout 1800 "$ "            { send "poweroff now\r" }

# From here on eof is the expected outcome, not a failure.
expect_before
expect -timeout 300 eof
EXPECT

# ── run the installer ────────────────────────────────────────────────────────
info "Running unattended install (${DISK_GB}G disk)..."
"${ENGINE_BIN}" run --rm \
  "${KVM_ARGS[@]}" \
  -v "${ISO_DIR}":/iso:ro \
  -v "${WORKDIR}":/work \
  -w /work \
  -e ISO="/iso/${ISO}" \
  -e DISK="/work/vyos.qcow2" \
  -e OUT_UID="$(id -u)" \
  -e OUT_GID="$(id -g)" \
  "${BUILDER_IMAGE}" \
  bash -c "apt-get update -qq >/dev/null \
    && apt-get install -yqq --no-install-recommends qemu-system-x86 qemu-utils expect >/dev/null \
    && qemu-img create -f qcow2 /work/vyos.qcow2 ${DISK_GB}G >/dev/null \
    && if [ -w /dev/kvm ]; then export ACCEL='-enable-kvm -cpu host'; \
       else echo '[WARN] no usable /dev/kvm inside the container, using TCG'; export ACCEL='-accel tcg'; fi \
    && expect /work/install.expect \
    && { chown \"\${OUT_UID}:\${OUT_GID}\" /work/vyos.qcow2 2>/dev/null || true; }" \
  || die "unattended install failed, scroll up for the last installer prompt expect saw"

# If expect lost sync with the dialog the qcow2 may exist but be empty.
[[ -s "${WORKDIR}/vyos.qcow2" ]] || die "installer produced no qcow2"
out_size=$(du -m "${WORKDIR}/vyos.qcow2" | cut -f1)
(( out_size > 300 )) || die "qcow2 is only ${out_size} MB, install likely did not complete"

mv "${WORKDIR}/vyos.qcow2" "${OUTPUT}"
info "Done: ${OUTPUT} (${out_size} MB), drop it into the Dockerfile build context."
