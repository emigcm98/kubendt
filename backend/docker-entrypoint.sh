#!/bin/sh
set -e

# This script starts as root so it can prepare root-owned mounts, then drops to
# the unprivileged "kubendt" user to run the actual server.

# Where the server reads the kubeconfig. Mirrors the app: KUBECONFIG is the
# source of truth (falls back to the default when unset).
KUBECONFIG_PATH="${KUBECONFIG:-/data/.kube/config}"
KUBE_DIR="$(dirname "$KUBECONFIG_PATH")"

# Named volumes mount as root-owned; create and make them writable by the user.
mkdir -p /data /app/files "$KUBE_DIR"

# Copy the mounted kubeconfig at every start, so a context added to it on the
# host shows after a restart. The mounted file is often 0600 and owned by
# someone else, which only root (this entrypoint) can read.
if [ -f /kube/config ]; then
    cp /kube/config "$KUBECONFIG_PATH"
    echo "entrypoint: kubeconfig copied from /kube/config -> $KUBECONFIG_PATH"
fi

chown -R kubendt:kubendt /data /app/files "$KUBE_DIR"
[ -f "$KUBECONFIG_PATH" ] && chmod 600 "$KUBECONFIG_PATH"

# Drop root and exec the server as the non-root user (env is preserved).
exec su-exec kubendt:kubendt "$@"
