# KubeNDT in the cluster

> Last reviewed: 26 Nov 2026

`kubectl apply -k deploy/k8s` installs KubeNDT inside the cluster it manages: the `kubendt-system` namespace, the ServiceAccount with the ClusterRole the backend needs (`rbac.yaml`, verb by verb), the backend and the UI. The admin password goes in a Secret named `kubendt-backend-env` made beforehand, `optional/ingress.yaml` is an Ingress to adapt, and `kustomization.yaml` pins the image tag of a release. The backend reaches the cluster with its ServiceAccount and serves that cluster only. To run it outside the cluster on a kubeconfig, use the compose. The steps and how the two installs differ are in [doc/DEPLOYMENT.md](../../doc/DEPLOYMENT.md).
