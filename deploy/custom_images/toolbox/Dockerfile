# The KubeNDT toolbox: the ephemeral container the backend injects into a pod
# for what its image lacks. Capture, traceroute and throughput tests always
# run here; ip, bridge, tc, iptables and tayga run here when the node's image
# does not ship them, so any image that stays running can be a node.
#
# Published as ghcr.io/emigcm98/kubendt-toolbox:<KubeNDT version> by
# release.yml, and as :latest on every change to this folder on main, which
# a development backend uses. KUBENDT_TOOLBOX_IMAGE points the backend
# elsewhere (nicolaka/netshoot works, it carries the same tools but tayga).
#
# The backend execs: tshark and editcap for capture, traceroute,
# tcptraceroute and mtr for the trace, iperf3 for throughput, ip and bridge
# from iproute2, tc, iptables and ip6tables, tayga for NAT64; sh keeps it
# alive with sleep.

# tayga, the NAT64 of the Linux routers, built from source: no Alpine release
# carries it. Static, so the binary does not care which libc the final
# stage has.
FROM alpine:3.20 AS tayga
ARG TAYGA_VERSION=0.9.6
ARG TAYGA_SHA256=64da04887e59207be0f62082e63dca98b1e770283fa37102ccb0a3e8d2acedc5
RUN apk add --no-cache build-base linux-headers curl \
 && curl -sSL "https://github.com/apalrd/tayga/archive/refs/tags/${TAYGA_VERSION}.tar.gz" -o tayga.tar.gz \
 && echo "${TAYGA_SHA256}  tayga.tar.gz" | sha256sum -c - \
 && tar xzf tayga.tar.gz \
 && make -s -C "tayga-${TAYGA_VERSION}" GIT=true TAYGA_VERSION="${TAYGA_VERSION}" LDFLAGS=-static \
 && install -D -m 755 "tayga-${TAYGA_VERSION}/tayga" /usr/local/sbin/tayga

FROM alpine:3.20

RUN apk add --no-cache tshark iproute2 traceroute tcptraceroute mtr iperf3 iptables
COPY --from=tayga /usr/local/sbin/tayga /usr/sbin/tayga
