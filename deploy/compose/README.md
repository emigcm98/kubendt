# KubeNDT outside the cluster

> Last reviewed: 26 Nov 2026

`compose.yaml` runs KubeNDT next to a cluster rather than in it: the published backend and UI images, the backend on the kubeconfig at `~/.kube/config` (mounted read-only and copied at every start) and the UI on port 80. From this directory, `docker compose up -d`, then http://localhost; `KUBENDT_TAG` pins a release. The backend's container has to reach the API server at the address the kubeconfig names, which the cluster guide sets up for kind, minikube and k3s. The install inside the cluster is `deploy/k8s`. How the two differ, the variables and moving from the old `docker-compose.prod.yml` are in [doc/DEPLOYMENT.md](../../doc/DEPLOYMENT.md).
