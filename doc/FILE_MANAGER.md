# Namespace File Manager

The file manager lets you upload, edit and mount text files into pods deployed by KubeNDT. This document covers how the storage works, what limits apply, and how the "sensitive" flag promotes a file from a ConfigMap-backed mount to a Secret-backed one.

## Overview

A file lives in two places once it is referenced by a deploy or modify operation:

1. **On disk** in the backend (`files/<namespace>/<path>`). This is the source of truth that the file manager UI shows and edits.
2. **In Kubernetes** as a `ConfigMap` (or `Secret`, for sensitive files). One resource per file per namespace, shared across every pod that mounts it.

Pods reference the resource via a `SubPath` `VolumeMount` so the file lands at exactly the path declared in `mount.mountTo`.

## Mount model

Mounts are always **read-only**. Writes from inside the container don't persist:
- The ConfigMap/Secret is rebuilt from disk on every sync, so container-side writes are lost on the next kubelet reconcile.
- `SubPath` is a static bind mount: updates to the ConfigMap/Secret do NOT propagate to running pods.

## Persistence

Per-file metadata (the `sensitive` flag) lives in the backend's SQLite DB:

```sql
CREATE TABLE namespace_file_meta (
    namespace TEXT NOT NULL,
    path TEXT NOT NULL,
    sensitive INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (namespace, path)
);
```

The metadata is namespace-scoped and follows the file through renames and folder operations:
- File deleted → metadata row deleted.
- File renamed → metadata row updated to the new path.
- Folder deleted → every metadata row under that prefix is deleted.
- All files cleared → all metadata for the namespace is deleted.

## Limits

- **Size**: 1 MiB max (ConfigMaps and Secrets cap at this in etcd). Uploads larger than 1 MiB are rejected at the API boundary with `400` and never touch disk.
- **Encoding**: UTF-8 text only. Binary uploads are rejected with `400` because the K8s `ConfigMap.Data` field is a Go string and casting non-UTF-8 bytes corrupts content silently. Use a PVC or init container if you need to ship binaries into a pod.

## ConfigMap vs Secret: the `sensitive` flag

Each file has a `sensitive` flag (default `false`). The flag is a property of the **file**, stored in `namespace_file_meta`. It controls which Kubernetes resource backs the mount when it is materialised:

| `sensitive` | Backing resource | Visible to `kubectl describe`? | Encrypted at rest? |
|---|---|---|---|
| `false` (default) | `ConfigMap` | yes (data in plain text) | no by default |
| `true` | `Secret` (`Opaque`) | yes (base64) | yes if etcd encryption is enabled |

Setting the flag:
- Via the file manager UI (lock icon) or `PUT /file-meta/:namespace/*filename`. Accepts true or false.
- Via the topology JSON on `deploy-network` or `modify-network`: any mount with `"sensitive": true` upgrades the file's flag before the resource is materialised. Cannot unmark from JSON (`omitempty` makes absent and false indistinguishable).

Switching the flag:
- **Setting sensitive=true on a file currently mounted as ConfigMap**: the file's K8s resource becomes a Secret on the next deploy/modify that materialises this mount. Existing pods keep using the old ConfigMap until they are redeployed. The API response surfaces `redeploy_required: true` and the count of mounting pods so the UI can prompt the user.
- **Setting sensitive=false on a file currently mounted as Secret**: same story in reverse. Next deploy creates a ConfigMap, existing pods keep the Secret until redeploy.

The `CreateMountResourceForFile` helper cleans up the "other kind" of resource at create-time (e.g. removes the stale ConfigMap when creating the new Secret for the same file), so the namespace never carries both at once for the same file once a deploy/modify has run after the flag flip.

## API surface

```
POST   /files/:namespace/                    upload a new file (multipart, "path" field for nested folders)
GET    /files/:namespace/*filename           read file content
PUT    /files/:namespace/*filename           overwrite file content (JSON {"content": "..."})
PUT    /file-meta/:namespace/*filename       toggle metadata (JSON {"sensitive": true|false})
DELETE /files/:namespace/*filename           delete file or folder
GET    /files/:namespace                     hierarchical file tree (entries include "sensitive": true on leaves)
POST   /file-ops/:namespace/rename           rename a file or folder
```

Responses from upload and update include extra hints when KubeNDT detects pods are already mounting the file:
- `resource_synced` (bool): true if the underlying K8s resource (ConfigMap or Secret, depending on the file's sensitive flag) was updated by this operation. Absent if no pod currently mounts the file (nothing to sync).
- `resource_kind` (string): "ConfigMap" or "Secret", set together with `resource_synced=true` so the UI can show a precise message.
- `pods_mounting` (int): number of pods that currently mount the file. They need a restart to see the new content.
- `redeploy_required` (bool, only on flag flip via `PUT /file-meta/...`): set when the mount type changes (ConfigMap ↔ Secret) and existing pods still reference the previous type.
- `sync_warning` (string): present if the underlying K8s resource exists but could not be updated (validation failure, transient API error). The file content on disk is still saved.

## Garbage collection

When a node is deleted via `modify-network`:
- The node's env ConfigMap (`<node>-env`) is deleted unconditionally. It is per-node, never shared.
- Every mount resource (ConfigMap or Secret) the node referenced is checked: if no other live StatefulSet in the namespace still references it, the resource is deleted. Otherwise it is kept (some other pod still mounts the file).

`kubendt-internal-iface-counts` is explicitly skipped from GC because it lives on a different lifecycle (rebuilt on every topology mutation).

When `clear-topology` runs, every mount ConfigMap and Secret declared by every StatefulSet is deleted regardless of reference count, since the entire topology is going away.

## Things to know

- **Do not store production credentials in mounted files.** K8s Secrets are not a vault: they need etcd encryption-at-rest configured and proper namespace RBAC to actually protect data. For real secrets use Vault, sealed-secrets, or similar.
- **Pods must be restarted to see changes** (SubPath mount limitation). The file manager surfaces an "N pods need restart" hint after each save.
- **The `sensitive` field in a mount declaration can mark a file as sensitive on import** (any mount with `sensitive: true` upgrades the file's metadata before the resource is materialised). It cannot **unmark**: omitting the field is indistinguishable from `false`, so use the file manager toggle to clear the flag. `get-network` emits the field per mount when the file is sensitive, so export → import roundtrips correctly.
