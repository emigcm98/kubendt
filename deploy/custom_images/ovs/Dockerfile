# The KubeNDT Open vSwitch node: Alpine's openvswitch package, ovsdb-server
# and ovs-vswitchd started by the entrypoint under tini, iproute2 for the
# shell. Packaging only, no patches: what upstream lacks is a maintained
# image, not features (openvswitch/ovs and globocom/openvswitch both stopped
# in 2018).
#
# Published as ghcr.io/emigcm98/kubendt-ovs:<OVS version> and :latest by
# ovs-image.yml on every change to this folder on main. The tag is the Open
# vSwitch version, not KubeNDT's: the backend only talks ovs-vsctl to it, so
# nothing ties the two.
FROM alpine:3.24

# The version in the tag. apk's ~ takes any Alpine revision of it.
ARG OVS_VERSION=3.7.1
RUN apk add --no-cache "openvswitch~${OVS_VERSION}" tini iproute2 \
 && mkdir -p /var/run/openvswitch /etc/openvswitch \
 && ovsdb-tool create /etc/openvswitch/conf.db /usr/share/openvswitch/vswitch.ovsschema

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/entrypoint.sh"]
