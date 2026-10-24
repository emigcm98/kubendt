# The KubeNDT toolbox: the ephemeral container the backend injects into a pod
# for what its image lacks. Capture, traceroute and throughput tests always
# run here; ip, bridge, tc and iptables run here when the node's image does
# not ship them, so any image that stays running can be a node.
#
# Published as ghcr.io/emigcm98/kubendt-toolbox:<KubeNDT version> by
# release.yml, and as :latest on every change to this folder on main, which
# a development backend uses. KUBENDT_TOOLBOX_IMAGE points the backend
# elsewhere (nicolaka/netshoot works, it carries the same tools).
#
# The backend execs: tshark and editcap for capture, traceroute,
# tcptraceroute and mtr for the trace, iperf3 for throughput, ip and bridge
# from iproute2, tc, iptables and ip6tables; sh keeps it alive with sleep.
FROM alpine:3.20

RUN apk add --no-cache tshark iproute2 traceroute tcptraceroute mtr iperf3 iptables
