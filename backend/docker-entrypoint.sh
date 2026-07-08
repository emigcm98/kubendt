#!/bin/sh
set -e

# This script starts as root so it can prepare root-owned mounts, then drops to
# the unprivileged "kubendt" user to run the actual server.

# Where the kubeconfig is read from and written to. Mirrors the app: KUBECONFIG
# is the source of truth (falls back to the default when unset).
KUBECONFIG_PATH="${KUBECONFIG:-/data/.kube/config}"
KUBE_DIR="$(dirname "$KUBECONFIG_PATH")"

# Named volumes mount as root-owned; create and make them writable by the user.
mkdir -p /data /app/files "$KUBE_DIR"

# Seed the persistent kubeconfig from a mounted one on first run only, so that
# kubeconfigs uploaded later through the API survive restarts. The mounted file
# is often 0600 and root-owned, which only root (this entrypoint) can read.
if [ ! -f "$KUBECONFIG_PATH" ] && [ -f /kube/config ]; then
    cp /kube/config "$KUBECONFIG_PATH"
    echo "entrypoint: seeded kubeconfig from /kube/config -> $KUBECONFIG_PATH"
fi

chown -R kubendt:kubendt /data /app/files "$KUBE_DIR"
[ -f "$KUBECONFIG_PATH" ] && chmod 600 "$KUBECONFIG_PATH"

# Drop root and exec the server as the non-root user (env is preserved).
exec su-exec kubendt:kubendt "$@"
