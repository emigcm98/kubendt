# Files of a backend older than 1.12

Since 1.12 the files of a namespace live in the cluster, as ConfigMaps and Secrets in the namespace. A backend older than that kept them here, under `<cluster-id>/<namespace>/`, and a newer one reads this folder once per namespace to move them, leaving a `.kubendt-migrated` marker. While a database from such a backend is open, `clusters.json` maps each cluster ID to its last context and API server.

Once everything is moved, this folder and the old `kubendt.db` can go.
