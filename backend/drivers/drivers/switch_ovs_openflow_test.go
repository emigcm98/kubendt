package drivers

import (
	"reflect"
	"testing"

	"kubendt/capabilities"
)

// ovsdb's Bridge and Controller tables of a switch pointed at a controller
// it cannot reach yet, as the inspection's script prints them.
const ovsOpenFlowBridgesSample = `{"data":[["br0",["uuid","2dcb9dc6-cd05-4efe-8e50-53313cdec937"],"secure","OpenFlow13","0000167ed5cc764f"]],"headings":["name","controller","fail_mode","protocols","datapath_id"]}
---
{"data":[[["uuid","2dcb9dc6-cd05-4efe-8e50-53313cdec937"],"tcp:10.0.100.1:6653",false]],"headings":["_uuid","target","is_connected"]}
`

func TestParseOVSOpenFlowBridges(t *testing.T) {
	rows, err := parseOVSOpenFlowBridges(ovsOpenFlowBridgesSample)
	if err != nil {
		t.Fatal(err)
	}
	want := capabilities.Rows{{"bridge": "br0", "controller": "tcp:10.0.100.1:6653", "connected": "no", "fail_mode": "secure", "protocols": "OpenFlow13", "datapath_id": "0000167ed5cc764f"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("got %v\nwant %v", rows, want)
	}
	plain := `{"data":[["br1",["set",[]],["set",[]],["set",[]],"00001234"]],"headings":["name","controller","fail_mode","protocols","datapath_id"]}` + "\n---\n" + `{"data":[],"headings":["_uuid","target","is_connected"]}` + "\n"
	rows, err = parseOVSOpenFlowBridges(plain)
	if err != nil {
		t.Fatal(err)
	}
	want = capabilities.Rows{{"bridge": "br1", "controller": "", "connected": "", "fail_mode": "standalone", "protocols": "", "datapath_id": "00001234"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("plain bridge: got %v\nwant %v", rows, want)
	}
}

// dump-flows lines behind their bridge name, header included: a drop flow
// by hand, the default NORMAL flow and one a learning controller installed.
const ovsFlowsSample = `br0 OFPST_FLOW reply (OF1.3) (xid=0x2):
br0 cookie=0x0, duration=12.345s, table=0, n_packets=3, n_bytes=294, priority=100,in_port=1 actions=drop
br0 cookie=0x0, duration=60.1s, table=0, n_packets=14, n_bytes=1200, priority=0 actions=NORMAL
br0 cookie=0x0, duration=3.2s, table=0, n_packets=2, n_bytes=196, priority=1,in_port=2,dl_src=aa:41:80:55:a1:83,dl_dst=4e:07:1f:09:0c:02 actions=output:1
`

func TestParseOVSFlows(t *testing.T) {
	rows := parseOVSFlows(ovsFlowsSample)
	want := capabilities.Rows{
		{"bridge": "br0", "table": "0", "priority": "100", "match": "in_port=1", "actions": "drop", "packets": "3"},
		{"bridge": "br0", "table": "0", "priority": "0", "match": "", "actions": "NORMAL", "packets": "14"},
		{"bridge": "br0", "table": "0", "priority": "1", "match": "in_port=2,dl_src=aa:41:80:55:a1:83,dl_dst=4e:07:1f:09:0c:02", "actions": "output:1", "packets": "2"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("got %v\nwant %v", rows, want)
	}
}
