# Slim toolbox image for KubeNDT.
#
# The toolbox is the ephemeral container the backend injects into a pod for
# packet capture, traceroute, traffic control on images without tc, and
# throughput tests. By default it is nicolaka/netshoot, pulled from Docker Hub
# with no build step. Build this one for a smaller image or an offline
# registry, then point the backend at it:
#
#   docker build -t kubendt/toolbox:latest deploy/custom_images/toolbox
#   export KUBENDT_TOOLBOX_IMAGE=kubendt/toolbox:latest
#
# It needs a /bin/sh and the programs the backend execs: tshark and editcap
# for capture, traceroute, tcptraceroute and mtr for the trace, tc for
# shaping and iperf3 for throughput. The backend keeps it alive with sleep.
FROM alpine:3.20

RUN apk add --no-cache tshark iproute2 traceroute tcptraceroute mtr iperf3
