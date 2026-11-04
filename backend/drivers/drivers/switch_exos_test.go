package drivers

import (
	"testing"
)

// A `show ports information` reply of a two-port switch, port 1 disabled,
// with the CLI text and the trailing status objects the API adds.
const exosPortsSample = `{"id":1,"jsonrpc":"2.0","result":[{"CLIoutput":"Port Summary ...\n"},{"show_ports_info":{"diagTestStatus":1,"flags":"Dm------e--fMB-----","jumboSize":9216,"linkState":0,"port":1,"portList":1},"status":"MORE"},{"show_ports_info":{"diagTestStatus":1,"flags":"Em------e--fMB-----","jumboSize":9216,"linkState":1,"port":2,"portList":2},"status":"SUCCESS"},{"status":"SUCCESS","stp_port_info":{"port_count":2}}]}`

func TestEXOSObjects(t *testing.T) {
	objs, err := exosObjects(exosPortsSample, "show_ports_info")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 || objs[0]["port"] != float64(1) || objs[1]["flags"] != "Em------e--fMB-----" {
		t.Errorf("objects: %v", objs)
	}
	if _, err := exosObjects(`{"error":{"code":500,"message":"OtherError: boom"},"id":1}`, "x"); err == nil {
		t.Error("an API error must surface")
	}
	if _, err := exosObjects("not json", "x"); err == nil {
		t.Error("garbage must surface")
	}
}

func TestEXOSCommands(t *testing.T) {
	d := NewEXOSSwitchDriver()
	if got := d.SetupBridge("lab", []string{"1", "2", "3"}); len(got) != 2 || got[1][0] != "configure vlan lab add ports 1,2,3 untagged" {
		t.Errorf("setup_bridge: %v", got)
	}
	if got := d.SetupBridge("lab", nil); len(got) != 1 {
		t.Errorf("setup_bridge without ports: %v", got)
	}
	if _, err := d.GuestInterface("ns", "pod", "eth13"); err == nil {
		t.Error("eth13 is past the twelve ports")
	}
	if _, err := d.GuestInterface("ns", "pod", "eth0"); err == nil {
		t.Error("eth0 is not a port")
	}
}

func TestEXOSFDBRows(t *testing.T) {
	objs := []map[string]any{
		{"age": 48.0, "flag": " d m           ", "mac_addr": "fe:3e:2f:e1:9b:05", "port_list": 2.0, "vlan_id": 4094.0, "vlan_name": "lab"},
		{"age": 3.0, "flag": " d m           ", "mac_addr": "a2:b3:ae:cb:13:5b", "port_list": 1.0, "vlan_id": 10.0, "vlan_name": "v10"},
	}
	m := parseExosPortMap("eth1=1\neth3=2\n")
	rows := exosFDBRows(objs, func(port string) string { return m.byPort[port] })
	if len(rows) != 2 || rows[0]["interface"] != "eth1" || rows[0]["vlan"] != "10" || rows[0]["bridge"] != "v10" || rows[0]["flags"] != "d m" {
		t.Errorf("first row: %v", rows[0])
	}
	if rows[1]["interface"] != "eth3" || rows[1]["vlan"] != "" || rows[1]["bridge"] != "lab" {
		t.Errorf("second row: %v", rows[1])
	}
}

func TestExosPortMap(t *testing.T) {
	m := parseExosPortMap("eth1=1\neth3=2\n\nbroken\n")
	if m.byIface["eth3"] != "2" || m.byPort["2"] != "eth3" || len(m.byIface) != 2 {
		t.Errorf("map: %v %v", m.byIface, m.byPort)
	}
}
