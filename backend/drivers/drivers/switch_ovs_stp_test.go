package drivers

import (
	"reflect"
	"testing"

	"kubendt/capabilities"
)

// An OVS switch of the ring running classic STP, not the root, as
// ovs-appctl stp/show prints it behind the script's header line.
const ovsSTPSample = `=== br0 stp
---- br0 ----
Root ID:
  stp-priority  32768
  stp-system-id   0a:0d:94:c4:e4:6d
  stp-hello-time  2s
  stp-max-age     20s
  stp-fwd-delay   15s
  root-port       eth3
  root-path-cost  2

Bridge ID:
  stp-priority  32768
  stp-system-id   1a:48:77:cc:79:40
  stp-hello-time  2s
  stp-max-age     20s
  stp-fwd-delay   15s

  Interface  Role       State      Cost  Pri.Nbr 
  ---------- ---------- ---------- ----- -------
  eth3       root       forwarding 2     128.1
  eth2       designated forwarding 2     128.2

`

func TestParseOVSSTP(t *testing.T) {
	bridges, ports := parseOVSSTP(ovsSTPSample)
	if len(bridges) != 1 {
		t.Fatalf("bridges: got %v", bridges)
	}
	b := bridges[0]
	if b["bridge"] != "br0" || b["mode"] != "stp" || b["priority"] != "32768" || b["root"] != "no" || b["root_port"] != "eth3" || b["bridge_id"] == b["root_id"] || b["root_id"] == "" {
		t.Errorf("bridge row: %v", b)
	}
	wantPorts := capabilities.Rows{
		{"bridge": "br0", "port": "eth3", "role": "root", "state": "forwarding", "cost": "2", "priority": "128"},
		{"bridge": "br0", "port": "eth2", "role": "designated", "state": "forwarding", "cost": "2", "priority": "128"},
	}
	if !reflect.DeepEqual(ports, wantPorts) {
		t.Errorf("ports: got %v\nwant %v", ports, wantPorts)
	}
	off, offPorts := parseOVSSTP("=== br1 off\n")
	if len(off) != 1 || off[0]["mode"] != "off" || len(offPorts) != 0 {
		t.Errorf("bridge without stp: %v %v", off, offPorts)
	}
}
