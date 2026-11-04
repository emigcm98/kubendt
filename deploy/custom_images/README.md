# Custom images

The images this repository builds. The rule for a node image: KubeNDT publishes one only when upstream offers no maintained, versioned image that runs as a single container, and the image is packaging alone, the distribution's package with no patches. Anything with a living upstream image stays on it: FRR from `quay.io/frrouting/frr`, Faucet from `faucet/faucet`, plain `alpine` and `ubuntu`, the Open5GS images.

| Folder | Image | Role | Tag | Built |
|---|---|---|---|---|
| `toolbox/` | `ghcr.io/emigcm98/kubendt-toolbox` | The container the backend injects into a pod for capture, trace, throughput tests and the tools an image lacks | KubeNDT's version, since the backend pulls its own, and `latest` | On every change to the folder (`latest`) and on every release |
| `ovs/` | `ghcr.io/emigcm98/kubendt-ovs` | The Open vSwitch node: the only upstream images stopped in 2018 | Open vSwitch's version and `latest` | On every change to the folder |
| `qemu/vyos-router/` | `localhost/vyos-router:dev` | VyOS as a QEMU guest in a pod | Built by hand from a VyOS rolling ISO and imported on the workers, see [README-vyos.md](qemu/vyos-router/README-vyos.md) | Not published, it weighs over a gigabyte |

> Last reviewed: 4 Nov 2026
