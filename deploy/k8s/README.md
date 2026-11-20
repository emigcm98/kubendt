# KubeNDT in the cluster

> Last reviewed: 20 Nov 2026

`kubectl apply -k deploy/k8s` installs KubeNDT inside the cluster it manages: the `kubendt-system` namespace, the ServiceAccount with the ClusterRole the backend needs (`rbac.yaml`, verb by verb), the backend and the UI. The admin password goes in a Secret named `kubendt-backend-env` made beforehand, `optional/ingress.yaml` is an Ingress to adapt, and `kustomization.yaml` pins the image tag of a release. The steps and the rest are in [doc/DEPLOYMENT.md](../../doc/DEPLOYMENT.md).
