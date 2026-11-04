# Lines every pod of this image runs once its API answers, with the values
# substituted by entrypoint.sh (__POD_NAME__). Applied live and never saved
# into the switch, a saved configuration costs every boot 150 s.
configure snmp sysName __POD_NAME__
