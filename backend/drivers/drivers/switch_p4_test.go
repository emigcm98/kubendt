package drivers

import (
	"reflect"
	"strings"
	"testing"

	"kubendt/capabilities"
)

// What simple_switch_CLI printed on bmv2 1.15.8 for the tutorials' basic.p4.
const p4PortsSample = `Obtaining JSON from switch...
Done
Control utility for runtime P4 table manipulation
RuntimeCmd:   port #       iface name       status  extra info
==================================================
    1              eth1           UP    
    2              eth2           UP    
    3              eth3         DOWN    
RuntimeCmd: 
`

const p4TablesSample = `Obtaining JSON from switch...
Done
Control utility for runtime P4 table manipulation
RuntimeCmd: MyEgress.swtrace               [implementation=None, mk=]
MyIngress.ipv4_lpm             [implementation=None, mk=ipv4.dstAddr(lpm, 32)]
RuntimeCmd: 
`

const p4EntriesSample = `### MyIngress.ipv4_lpm
Obtaining JSON from switch...
Done
Control utility for runtime P4 table manipulation
RuntimeCmd: ==========
TABLE ENTRIES
**********
Dumping entry 0x0
Match key:
* ipv4.dstAddr        : LPM       0a000101/32
Action entry: MyIngress.ipv4_forward - 080000000111, 01
**********
Dumping entry 0x1f
Match key:
* ipv4.dstAddr        : LPM       0a000200/24
Action entry: MyIngress.ipv4_forward - 080000000222, 02
==========
Dumping default entry
Action entry: MyIngress.drop - 
==========
RuntimeCmd: 
### MyEgress.swtrace
RuntimeCmd: ==========
TABLE ENTRIES
==========
Dumping default entry
Action entry: NoAction - 
==========
RuntimeCmd: 
`

func TestParseP4Ports(t *testing.T) {
	want := capabilities.Rows{
		{"port": "1", "iface": "eth1", "status": "up"},
		{"port": "2", "iface": "eth2", "status": "up"},
		{"port": "3", "iface": "eth3", "status": "down"},
	}
	if got := parseP4Ports(p4PortsSample); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestParseP4Tables(t *testing.T) {
	want := capabilities.Rows{
		{"table": "MyEgress.swtrace", "match": ""},
		{"table": "MyIngress.ipv4_lpm", "match": "ipv4.dstAddr(lpm, 32)"},
	}
	if got := parseP4Tables(p4TablesSample); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestParseP4Entries(t *testing.T) {
	want := capabilities.Rows{
		{"table": "MyIngress.ipv4_lpm", "handle": "0", "match": "ipv4.dstAddr lpm 0a000101/32", "action": "MyIngress.ipv4_forward", "params": "080000000111, 01"},
		{"table": "MyIngress.ipv4_lpm", "handle": "31", "match": "ipv4.dstAddr lpm 0a000200/24", "action": "MyIngress.ipv4_forward", "params": "080000000222, 02"},
		{"table": "MyIngress.ipv4_lpm", "handle": "default", "match": "", "action": "MyIngress.drop", "params": ""},
		{"table": "MyEgress.swtrace", "handle": "default", "match": "", "action": "NoAction", "params": ""},
	}
	if got := parseP4Entries(p4EntriesSample); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestP4PortAdd(t *testing.T) {
	var d P4SwitchDriver
	cmds := d.AddInterfaceToBridge("eth3", "sw")
	if len(cmds) != 3 {
		t.Fatalf("want offloads, remove and add, got %d commands: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0][2], "ethtool -K eth3") || !strings.Contains(cmds[1][2], "port_remove 3") || !strings.Contains(cmds[2][2], "port_add eth3 3") {
		t.Errorf("unexpected commands: %v", cmds)
	}
	// The CLI exits 0 on its own errors, so the add checks the output itself.
	if !strings.Contains(cmds[2][2], "grep -q") {
		t.Errorf("port_add does not check the CLI's output: %v", cmds[2])
	}
	if bad := d.AddInterfaceToBridge("tap", "sw"); !strings.Contains(bad[0][2], "exit 1") {
		t.Errorf("an interface without a number must fail, got %v", bad)
	}
	if n := len(d.SetupBridge("sw", []string{"eth1", "eth2"})); n != 6 {
		t.Errorf("setup_bridge of two ports: want 6 commands, got %d", n)
	}
}

func TestP4EntryLine(t *testing.T) {
	cases := []struct {
		table, action, match, params string
		prio                         int
		want                         string
	}{
		{"MyIngress.ipv4_lpm", "MyIngress.ipv4_forward", "10.0.2.2/32", "08:00:00:00:02:22 2", -1, "table_add MyIngress.ipv4_lpm MyIngress.ipv4_forward 10.0.2.2/32 => 08:00:00:00:02:22 2"},
		{"MyIngress.acl", "MyIngress.drop", "10.0.1.1&&&255.255.255.255 0&&&0", "", 10, "table_add MyIngress.acl MyIngress.drop 10.0.1.1&&&255.255.255.255 0&&&0 => 10"},
		{"t", "a", "1", "", -1, "table_add t a 1 =>"},
	}
	for _, c := range cases {
		if got := p4EntryLine(c.table, c.action, c.match, c.params, c.prio); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
	var d P4SwitchDriver
	del := d.TableDelete("MyIngress.ipv4_lpm", "10.0.2.2/32", -1)[0][2]
	if !strings.Contains(del, "table_dump_entry_from_key MyIngress.ipv4_lpm 10.0.2.2/32") || !strings.Contains(del, "table_delete MyIngress.ipv4_lpm") {
		t.Errorf("table_delete must look the handle up by key: %s", del)
	}
	load := d.LoadProgram(`{"a":1}`)
	if !strings.Contains(load[0][2], p4ProgramPath) || !strings.Contains(load[1][2], "swap_configs") {
		t.Errorf("load_program must write the file and swap: %v", load)
	}
}
