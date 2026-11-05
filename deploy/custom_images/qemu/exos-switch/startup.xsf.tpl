# Lines every pod of this image runs once its API answers, with the values
# substituted by entrypoint.sh (__POD_NAME__). Applied live and never saved
# into the switch, a saved configuration costs every boot 150 s.
configure snmp sysName __POD_NAME__
# A factory-default switch runs auto-provision (ZTP), which probes DHCP on
# every VLAN tag out of every port once a minute, and only on a Default VLAN
# without an address. A link-local one stops it at once; "disable
# auto-provision" does too but leaves the API dead for two minutes.
configure vlan Default ipaddress 169.254.0.2/16
