package drivers

import (
	"reflect"
	"testing"

	"kubendt/capabilities"
)

// A `show ports information` reply of a two-port switch, port 1 disabled,
// with the CLI text and the trailing status objects the API adds.
const exosPortsSample = `{"id":1,"jsonrpc":"2.0","result":[{"CLIoutput":"Port Summary ...\n"},{"show_ports_info":{"diagTestStatus":1,"flags":"Dm------e--fMB-----","jumboSize":9216,"linkState":0,"port":1,"portList":1},"status":"MORE"},{"show_ports_info":{"diagTestStatus":1,"flags":"Em------e--fMB-----","jumboSize":9216,"linkState":1,"port":2,"portList":2},"status":"SUCCESS"},{"status":"SUCCESS","stp_port_info":{"port_count":2}}]}`

func TestEXOSObjects(t *testing.T) {
	objs, err := exosObjects(exosPortsSample, "show_ports_info")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 || exosNum(objs[0]["port"]) != 1 || objs[1]["flags"] != "Em------e--fMB-----" {
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

func TestEXOSVLANRows(t *testing.T) {
	// `show ports vlan` of a switch with port 1 access v10, port 3 a trunk of
	// v10 and v20 with v30 untagged, port 4 in Default, port 5 in the bridge
	// VLAN "lab" of setup_bridge (an internal tag).
	objs := []map[string]any{
		{"port": 1.0, "vlanId": 10.0, "vlanName": "v10", "tagStatus": 0.0},
		{"port": 3.0, "vlanId": 10.0, "vlanName": "v10", "tagStatus": 1.0},
		{"port": 3.0, "vlanId": 20.0, "vlanName": "v20", "tagStatus": 1.0},
		{"port": 3.0, "vlanId": 30.0, "vlanName": "v30", "tagStatus": 0.0},
		{"port": 4.0, "vlanId": 1.0, "vlanName": "Default", "tagStatus": 0.0},
		{"port": 5.0, "vlanId": 4094.0, "vlanName": "lab", "tagStatus": 0.0},
		{"port": 9.0, "vlanId": 1.0, "vlanName": "Default", "tagStatus": 0.0},
	}
	m := parseExosPortMap("eth1=1\neth3=3\neth4=4\neth5=5\n")
	rows := exosVLANRows(objs, m)
	want := capabilities.Rows{
		{"port": "eth1", "mode": "access", "vlans": "10", "native": "10"},
		{"port": "eth3", "mode": "trunk", "vlans": "10,20", "native": "30"},
		{"port": "eth4", "mode": "access", "vlans": "1", "native": "1"},
		{"port": "eth5", "mode": "access", "vlans": "lab", "native": "lab"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("got %v\nwant %v", rows, want)
	}
}

const exosSTPPortsSample = `Port     Mode   State      Cost  Flags     Priority Port ID Designated Bridge
1      802.1D FORWARDING 200000 eDae-w---- 128      8001    10:00:52:54:00:12:34:56
3      802.1W FORWARDING 50     eRapaw---- 32       2003    10:00:02:ab:cd:ef:01:02
4      802.1W BLOCKING   200000 eAappw---- 128      8004    10:00:02:ab:cd:ef:01:02

Total Ports: 3

 ------------------------- Flags: ----------------------------
1:                e=Enable, d=Disable
`

func TestEXOSSTPPortRows(t *testing.T) {
	m := parseExosPortMap("eth1=1\neth3=3\neth4=4\n")
	rows := exosSTPPortRows(exosSTPPortsSample, m)
	want := capabilities.Rows{
		{"bridge": "s0", "port": "eth1", "role": "designated", "state": "forwarding", "cost": "200000", "priority": "128"},
		{"bridge": "s0", "port": "eth3", "role": "root", "state": "forwarding", "cost": "50", "priority": "32"},
		{"bridge": "s0", "port": "eth4", "role": "alternate", "state": "blocking", "cost": "200000", "priority": "128"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("got %v\nwant %v", rows, want)
	}
}

func TestEXOSSTPBridgeRows(t *testing.T) {
	body := `{"id":1,"jsonrpc":"2.0","result":[{"CLIoutput":"Stpd: s0\n"},{"status":"MORE","stp_domain":{"priority":4096,"protocol_mode":2,"stpd_name":"s0"}},{"status":"MORE","stp_domain_enable":{"protocol_mode":2,"stpd_enabled":1,"stpd_name":"s0"}},{"status":"SUCCESS","stp_domain_stats":{"bridge_id":1000525400123456,"designated_root":1000525400123456,"root_path_cost":0}}]}`
	ports := capabilities.Rows{{"port": "eth3", "role": "designated"}}
	rows, err := exosSTPBridgeRows(body, ports)
	if err != nil {
		t.Fatal(err)
	}
	want := capabilities.Rows{{"bridge": "s0", "mode": "rstp", "priority": "4096", "bridge_id": "1000.52:54:00:12:34:56", "root_id": "1000.52:54:00:12:34:56", "root": "yes", "root_port": ""}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("got %v\nwant %v", rows, want)
	}
	if got := exosBridgeID("80000ab1cd2ef305"); got != "8000.0a:b1:cd:2e:f3:05" {
		t.Errorf("bridge id with letters: %q", got)
	}
}
