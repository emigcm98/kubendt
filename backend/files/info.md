# KubeNDT file storage

Files uploaded through the file manager (later mounted into pods) are stored here, organized per cluster and namespace:

    <cluster-id>/<namespace>/<files...>

`<cluster-id>` is the UID of the cluster's `kube-system` namespace, so the same namespace name in two different clusters does not collide. `clusters.json` maps each cluster-id to its last-seen context name and API server for readability.
