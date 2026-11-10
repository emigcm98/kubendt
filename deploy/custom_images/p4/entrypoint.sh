#!/bin/sh
# simple_switch starts with no program and no ports; the driver adds the pod's
# interfaces and loads the program through the CLI. Swapping must be enabled
# at start, and the option belongs to the target, hence the separator.
exec simple_switch --no-p4 --thrift-port 9090 -- --enable-swap
