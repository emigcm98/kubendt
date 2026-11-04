#!/bin/sh
# Starts Open vSwitch without ovs-ctl's init-system baggage: ovsdb-server
# detached, then ovs-vswitchd in the foreground, so tini supervises it and
# `kubectl logs` shows what it says.
set -e
mkdir -p /var/run/openvswitch /etc/openvswitch
[ -f /etc/openvswitch/conf.db ] || ovsdb-tool create /etc/openvswitch/conf.db /usr/share/openvswitch/vswitch.ovsschema
ovsdb-server /etc/openvswitch/conf.db \
  --remote=punix:/var/run/openvswitch/db.sock \
  --remote=db:Open_vSwitch,Open_vSwitch,manager_options \
  --pidfile --detach -vconsole:warn
ovs-vsctl --no-wait init
exec ovs-vswitchd --pidfile -vconsole:info
