#!/bin/sh
# Inject the kubendt-specific assets into a VyOS qcow2 produced by Phase 1 of
# README-vyos.md (i.e. straight out of `install image`, no manual edits).
#
# This replaces the prior virt-customize-based approach because libguestfs'
# inspect-os does not recognise VyOS' SquashFS+overlayfs layout (the rootfs
# lives inside /boot/<version>/<version>.squashfs and there's no /etc/os-release
# at the top of any partition). virt-customize aborts with
# "no operating systems were found in the guest image".
#
# guestfish does not run inspect-os, it just mounts whatever we tell it.
#
# What we inject and where (paths as the guest will see them at runtime):
#   - /opt/vyatta/etc/config/scripts/vyos-preconfig-bootup.script  (VyOS hook
#       that copies the per-pod config.boot from the seed CD-ROM at boot)
#   - /etc/udev/rules.d/64-kubendt-net.rules  (interface-naming fix)
#   - /usr/local/bin/kubendt-net-name         (helper called by the rule)
#
# All three live on /dev/sda3 inside /boot/<version>/rw/, which is the upper
# layer of the overlayfs that VyOS mounts as / at runtime. <version> is
# detected dynamically so this script keeps working across VyOS releases.
#
# Asset files (the three named above) are read from the current working
# directory.

set -eu

QCOW2="${1:?Usage: $0 <qcow2-path>}"

if [ ! -f "$QCOW2" ]; then
    echo "ERROR: qcow2 not found: $QCOW2" >&2
    exit 1
fi

for f in vyos-preconfig-bootup.script 64-kubendt-net.rules kubendt-net-name; do
    if [ ! -f "$f" ]; then
        echo "ERROR: asset not found in CWD: $f" >&2
        exit 1
    fi
done

# Detect the version dir under /boot. A fresh `install image` creates exactly
# one such directory; the other entries (efi, grub, lost+found) are static.
VERSION_DIR=$(guestfish --ro -a "$QCOW2" -m /dev/sda3 ls /boot \
    | grep -Ev '^(efi|grub|lost\+found)$' \
    | head -n1)

if [ -z "$VERSION_DIR" ]; then
    echo "ERROR: could not detect VyOS version dir under /boot in $QCOW2" >&2
    echo "       /boot contents:" >&2
    guestfish --ro -a "$QCOW2" -m /dev/sda3 ls /boot >&2 || true
    exit 1
fi

RW="/boot/${VERSION_DIR}/rw"
echo "Injecting kubendt assets into ${QCOW2}"
echo "  detected VyOS version dir: ${VERSION_DIR}"
echo "  writing under:              ${RW}"

# Drop the 5s GRUB boot-menu timeout. The image has a single entry and the
# menu is non-interactive in our deployment, so any timeout is pure dead
# time on every pod start. Where the timeout lives depends on the release
# (older ones keep it in grub.cfg, recent rolling moved it to
# grub.cfg.d/20-vyos-defaults-autoload.cfg), so patch whichever files exist
# and actually contain a timeout line.
TMPDIR=$(mktemp -d)
trap 'rm -rf "${TMPDIR}"' EXIT

GRUB_UPLOADS=""
n=0
for cfg in /boot/grub/grub.cfg /boot/grub/grub.cfg.d/20-vyos-defaults-autoload.cfg; do
    [ "$(guestfish --ro -a "$QCOW2" -m /dev/sda3 is-file "$cfg")" = "true" ] || continue
    n=$((n + 1))
    copy="${TMPDIR}/grub-${n}.cfg"
    guestfish --ro -a "$QCOW2" -m /dev/sda3 download "$cfg" "$copy"
    grep -q '^set timeout=' "$copy" || continue
    echo "  patching ${cfg}: timeout → 0"
    sed -i 's/^set timeout=.*/set timeout="0"/' "$copy"
    GRUB_UPLOADS="${GRUB_UPLOADS}upload ${copy} ${cfg}
"
done

# Strip hw-id lines from the stock config.boot. At udev coldplug the per-pod
# seed hasn't been applied yet, and vyos_net_name gives config hw-id mappings
# absolute priority over the predefined (netmap) name AND treats their names
# as taken. The stock install-time entry (eth0 ↔ install NIC MAC) would
# therefore steal "eth0" from the cluster-passthrough NIC and shift every
# rename. Without hw-id lines the netmap predef always wins. The seed
# config.boot replaces this file on every boot anyway.
STOCKCFG="${RW}/opt/vyatta/etc/config/config.boot"
echo "  stripping hw-id lines from stock config.boot"
guestfish --ro -a "$QCOW2" -m /dev/sda3 download "${STOCKCFG}" "${TMPDIR}/config.boot"
sed -i '/^[[:space:]]*hw-id[[:space:]]/d' "${TMPDIR}/config.boot"

# Note: chmod is set on each upload. We don't chown explicitly, the parent
# /opt/vyatta/etc/config/ already has setgid + group=vyattacfg, so files
# created under it inherit the correct group.
guestfish --rw -a "$QCOW2" -m /dev/sda3 <<EOF
mkdir-p ${RW}/opt/vyatta/etc/config/scripts
upload vyos-preconfig-bootup.script ${RW}/opt/vyatta/etc/config/scripts/vyos-preconfig-bootup.script
chmod 0755 ${RW}/opt/vyatta/etc/config/scripts/vyos-preconfig-bootup.script

mkdir-p ${RW}/etc/udev/rules.d
upload 64-kubendt-net.rules ${RW}/etc/udev/rules.d/64-kubendt-net.rules
chmod 0644 ${RW}/etc/udev/rules.d/64-kubendt-net.rules

mkdir-p ${RW}/usr/local/bin
upload kubendt-net-name ${RW}/usr/local/bin/kubendt-net-name
chmod 0755 ${RW}/usr/local/bin/kubendt-net-name

${GRUB_UPLOADS}
upload ${TMPDIR}/config.boot ${STOCKCFG}
chmod 0660 ${STOCKCFG}
EOF

echo "Done."
