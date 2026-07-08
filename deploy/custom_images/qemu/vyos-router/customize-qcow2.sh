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

# Patch grub.cfg to drop the 5s boot-menu timeout. The image has a single
# entry, the menu is non-interactive in our deployment, so any timeout is
# pure dead time on every pod start. We download the file, sed it, then
# re-upload alongside the rest of the uploads in the rw call below.
TMPDIR=$(mktemp -d)
trap 'rm -rf "${TMPDIR}"' EXIT
echo "  patching /boot/grub/grub.cfg: timeout → 0"
guestfish --ro -a "$QCOW2" -m /dev/sda3 download /boot/grub/grub.cfg "${TMPDIR}/grub.cfg"
sed -i 's/^set timeout=.*/set timeout=0/' "${TMPDIR}/grub.cfg"

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

upload ${TMPDIR}/grub.cfg /boot/grub/grub.cfg
EOF

echo "Done."
