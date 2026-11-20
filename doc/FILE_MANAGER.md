# Namespace File Manager

The file manager lets you upload, edit and mount text files into pods deployed by KubeNDT. This document covers where the files live, what limits apply, and how the `sensitive` flag makes a file a Secret instead of a ConfigMap.

## Overview

A namespace's files are objects of the namespace itself: one `ConfigMap` per file, or one `Secret` when the file is flagged sensitive, named after the path (`kubendt-file-<path>` and `kubendt-secret-file-<path>`, slashes and dots turned into dashes), labelled `kubendt/kind=file`, with the original path in the `kubendt/mount-file-path` annotation and the content under one key. The object is created the moment the file is uploaded and it is the same object a pod mounts, so the file manager and the mounts never disagree, any backend sees the same files, and the files die with the namespace.

An empty folder is a marker `ConfigMap` (`kubendt-folder-<path>`, `kubendt/kind=folder`) until a file lands in it. A folder with files in it needs no marker.

Pods reference the object through a `SubPath` `VolumeMount` so the file lands at exactly the path declared in `mount.mountTo`.

## Mount model

Mounts are always **read-only**. Writes from inside the container don't persist, and `SubPath` is a static bind mount: an edit to the file does not reach running pods, they need a restart to see it. Upload and edit responses say how many pods mount the file so the UI can prompt for it.

## Source path preservation

Object names and data keys cannot contain `/`, so a file in a subfolder is stored under a sanitized key where separators become `_` (`web-server/index.html` is stored as `web-server_index.html`). The original path is the `kubendt/mount-file-path` annotation, which `get-network` reads to show the real path in the pod panel.

## Deleted and renamed files

A file nobody mounts is deleted outright. A file some StatefulSet mounts is kept for those pods: the object loses its `kubendt/kind=file` label and stays as a mount, the file manager stops listing it, the pod panel flags the mount as missing its source, and the garbage collection removes the object once no StatefulSet references it (a node deletion, a clear, a redeploy). A rename is a new object with the same content, the old one treated as a deleted file. Uploading a file at the path of such a mount takes the mount over: the pods get the new content on their next restart, as they always did after an upload.

## Migration from an older backend

Backends before this one kept the files on disk under `FILES_BASE_PATH` and made the objects only when a deploy mounted them. The first time a namespace is touched, its files on disk become objects (the `sensitive` flag taken from the old `namespace_file_meta` rows, the mounts an older deploy already made taken over as the files they were copies of), a `.kubendt-migrated` marker is written in the folder and the disk is never read again for that namespace. The folder stays for the operator; the variable and its volume are only needed for that move.

## Limits

- **Size**: 1 MiB max, what a ConfigMap or a Secret holds in etcd. Larger uploads are rejected with `400`.
- **Encoding**: UTF-8 text only. Binary uploads are rejected with `400`, since `ConfigMap.Data` is a string field and casting bytes corrupts content silently. Use a PVC or an init container to ship binaries into a pod.
- **Names**: each part of a path may hold letters, digits, `-`, `_` and `.`, since the path is also a ConfigMap data key.

## ConfigMap vs Secret: the `sensitive` flag

Each file has a `sensitive` flag (default `false`). The flag is the kind of the file's object:

| `sensitive` | Object | Visible to `kubectl describe`? | Encrypted at rest? |
| --- | --- | --- | --- |
| `false` (default) | `ConfigMap` | yes (data in plain text) | no by default |
| `true` | `Secret` (`Opaque`) | yes (base64) | yes if etcd encryption is enabled |

Setting the flag:

- Via the file manager UI (lock icon) or `PUT /file-meta/:namespace/*filename`. Accepts true or false.
- Via the topology JSON on `deploy-network` or `modify-network`: any mount with `"sensitive": true` makes the file a Secret before the pods are created. Cannot unmark from JSON (`omitempty` makes absent and false indistinguishable).

Flipping the flag moves the content to an object of the other kind at once. Pods that mount the file keep the object they mounted until they are redeployed, the old one staying for them as a mount; the response says so with `redeploy_required: true` and the count of mounting pods.

## API surface

```
POST   /files/:namespace/                    upload a new file (multipart, "path" field for nested folders)
GET    /files/:namespace/*filename           read file content
PUT    /files/:namespace/*filename           overwrite file content (JSON {"content": "..."})
PUT    /file-meta/:namespace/*filename       toggle metadata (JSON {"sensitive": true|false})
DELETE /files/:namespace/*filename           delete file or folder
DELETE /files/:namespace                     delete every file and folder
GET    /files/:namespace                     hierarchical file tree (entries include "sensitive": true on leaves)
POST   /file-ops/:namespace/folder           create an empty folder
POST   /file-ops/:namespace/rename           rename a file or folder
POST   /file-ops/:namespace/import           import a .zip, .tar.gz or .tgz
GET    /file-ops/:namespace/export           download the files as a .zip or a .tar.gz
```

Responses from upload and update carry hints when pods mount the file:

- `resource_synced` (bool) and `resource_kind` ("ConfigMap" or "Secret"): the object pods mount is the one just written.
- `pods_mounting` (int): how many pods mount the file. They need a restart to see the new content.
- `redeploy_required` (bool, only on a flag flip): the file is the other kind now and the mounting pods still reference the previous one.

An import answers `imported` with the count of files written and, when an archive entry could not become a file (too big, binary, a name that cannot be a mount key), `skipped` with the reasons; the rest is imported.

## Garbage collection

When a node is deleted via `modify-network`, its env ConfigMap (`<node>-env`) goes unconditionally, and every object it mounted is checked: a file of the manager stays whatever mounts it, and the mount of a deleted or renamed file goes once no other live StatefulSet references it. `clear-topology` deletes the mounts of deleted files along with the StatefulSets and leaves the files alone unless asked to delete them. `kubendt-internal-iface-counts` is never collected, it lives on its own lifecycle.

## Things to know

- **Do not store production credentials in mounted files.** Kubernetes Secrets are not a vault: they need etcd encryption at rest and proper namespace RBAC to actually protect data. For real secrets use Vault, sealed-secrets, or similar.
- **Pods must be restarted to see changes** (SubPath mount limitation). The file manager surfaces an "N pods need restart" hint after each save.
- **The `sensitive` field in a mount declaration can mark a file as sensitive on import**, it cannot unmark it: omitting the field is indistinguishable from `false`, so use the file manager toggle to clear the flag. `get-network` emits the field per mount when the file is sensitive, so export → import roundtrips correctly.
- **Deleted sources are flagged, not hidden.** If a mounted file is removed from the file manager, the pod's Mounted Files panel shows it with a warning and disables the link. The pod keeps serving the old content until it is redeployed.
- **Path sanitization can collide.** Files whose paths sanitize to the same name share one object: `web-server/index.html` and a root file literally named `web-server_index.html` both map to `kubendt-file-web-server-index-html`. The second upload replaces the first. Avoid file names that mimic a folder separator with `_`.
- **Internal state is not a file.** `kubendt-internal-iface-counts`, `kubendt-internal-layout` and the rest of the platform's own objects carry the `kubendt-internal-` prefix and their own `kubendt/kind`, so they never appear in the file manager nor in a pod's Mounted Files list.
