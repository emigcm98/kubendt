package drivers

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/executor"
)

// P4SwitchDriver is BMv2's simple_switch on the kubendt-p4 image. The image
// starts it with no program and no ports, and everything goes through its
// Thrift CLI: SwitchCapable is read as the ports the switch gets, numbered
// from the interface names, there is no bridge and its name is kept for the
// history only; P4Capable loads the program and fills its tables.
type P4SwitchDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
}

func NewP4SwitchDriver() *P4SwitchDriver {
	return &P4SwitchDriver{Meta: drivers_meta.NewMeta("P4SwitchDriver", "switch")}
}

var _ capabilities.L2Capable = (*P4SwitchDriver)(nil)
var _ capabilities.SwitchCapable = (*P4SwitchDriver)(nil)
var _ capabilities.P4Capable = (*P4SwitchDriver)(nil)
var _ capabilities.P4Inspector = (*P4SwitchDriver)(nil)
var _ drivers_meta.KnownImagesProvider = (*P4SwitchDriver)(nil)
var _ drivers_meta.ReadinessProbeProvider = (*P4SwitchDriver)(nil)

const (
	p4ThriftPort   = "9090"
	p4ProgramPath  = "/run/kubendt/program.json"
	p4CLI          = "simple_switch_CLI --thrift-port " + p4ThriftPort
	p4CLINoiseExpr = `Obtaining JSON\|^Done$\|Control utility`
	p4CLIErrorExpr = `Invalid \|Error\|Could not connect`
)

func (P4SwitchDriver) KnownImages() []string { return []string{"ghcr.io/emigcm98/kubendt-p4:1.15.8"} }

// ReadinessProbeCommands makes the pod Ready once the CLI port answers.
func (P4SwitchDriver) ReadinessProbeCommands() drivers_meta.ReadinessProbeSpec {
	return drivers_meta.ReadinessProbeSpec{
		Command:             []string{"python3", "-c", "import socket,sys; sys.exit(socket.socket().connect_ex(('127.0.0.1', " + p4ThriftPort + ")))"},
		InitialDelaySeconds: 2,
		PeriodSeconds:       3,
		TimeoutSeconds:      5,
		FailureThreshold:    20,
	}
}

// p4cli runs CLI lines and fails on the CLI's own error lines, since
// simple_switch_CLI exits 0 whatever happened to them.
func p4cli(lines ...string) []string {
	return []string{"sh", "-c", p4cliShell("printf '%s\\n' " + shellSingleQuote(strings.Join(lines, "\n")))}
}

// p4cliShell is the checked CLI run for a shell snippet that prints the
// lines, so a line can carry a shell variable.
func p4cliShell(lines string) string {
	return "out=$(" + lines + " | " + p4CLI + " 2>&1); " +
		"printf '%s\\n' \"$out\" | grep -v '" + p4CLINoiseExpr + "'; " +
		"! printf '%s\\n' \"$out\" | grep -q '" + p4CLIErrorExpr + "'"
}

// p4read runs CLI lines for their output, errors and all.
func p4read(lines ...string) []string {
	return []string{"sh", "-c", "printf '%s\\n' " + shellSingleQuote(strings.Join(lines, "\n")) + " | " + p4CLI + " 2>&1"}
}

// portAdd gives the switch an interface as the port its name numbers. The
// offloads go off first, since the switch reads raw frames and a GSO frame
// never crosses it, and a port already there is removed first, which is what
// a neighbour's restart needs: Meshnet recreates the interface and the port
// kept the dead one.
//
// A port on a VXLAN link reads through a veth pair of its own instead. The
// worker NIC's receive offload merges the TCP segments of a flow arriving
// through the tunnel into one frame of several segments, which the switch
// would read whole and could not send on; with TSO off on the veth, the
// kernel splits that frame again on its way in. Two tc redirects carry the
// frames between the link and the veth, as the QEMU images do with their
// taps. A veth link needs none, Meshnet's ends come with TSO off.
func portAdd(iface string) [][]string {
	n := portNumberFromName(iface)
	if n == "" {
		return [][]string{{"sh", "-c", "echo 'interface " + iface + " carries no port number in its name' >&2; exit 1"}}
	}
	tc, p4 := iface+"-tc", iface+"-p4"
	offloads := " tso off gso off gro off tx off rx off sg off >/dev/null 2>&1"
	wrap := "ethtool -K " + iface + offloads + "; " + portUnwrap(iface) + "; " +
		"ip -d link show " + iface + " | grep -qw vxlan || exit 0; set -e; " +
		"mtu=$(cat /sys/class/net/" + iface + "/mtu); " +
		"ip link add " + tc + " type veth peer name " + p4 + "; " +
		"for d in " + tc + " " + p4 + "; do ethtool -K $d" + offloads + " || true; ip link set $d mtu $mtu promisc on up; done; " +
		"ip link set " + iface + " promisc on; " +
		"tc qdisc add dev " + iface + " ingress; tc filter add dev " + iface + " ingress protocol all u32 match u8 0 0 action mirred egress redirect dev " + tc + "; " +
		"tc qdisc add dev " + tc + " ingress; tc filter add dev " + tc + " ingress protocol all u32 match u8 0 0 action mirred egress redirect dev " + iface
	return [][]string{
		{"sh", "-c", wrap},
		{"sh", "-c", "printf 'port_remove " + n + "\\n' | " + p4CLI + " >/dev/null 2>&1; true"},
		{"sh", "-c", "p=" + iface + "; [ -e /sys/class/net/" + p4 + " ] && p=" + p4 + "; " + p4cliShell("printf 'port_add %s "+n+"\\n' \"$p\"")},
	}
}

// portUnwrap takes the veth pair and the redirect of a port away, if it has
// them; the pair goes with its ingress qdisc.
func portUnwrap(iface string) string {
	return "tc qdisc del dev " + iface + " ingress 2>/dev/null; ip link del " + iface + "-tc 2>/dev/null; true"
}

func (P4SwitchDriver) SetupBridge(bridge string, ifaces []string) [][]string {
	if len(ifaces) == 0 {
		return [][]string{{"true"}}
	}
	var cmds [][]string
	for _, iface := range ifaces {
		cmds = append(cmds, portAdd(iface)...)
	}
	return cmds
}

// TeardownBridge removes every port the switch has, whatever added them, and
// the veth pairs of the ports on VXLAN links.
func (P4SwitchDriver) TeardownBridge(bridge string) [][]string {
	return [][]string{
		{"sh", "-c", "for p in $(printf 'show_ports\\n' | " + p4CLI + " 2>/dev/null | awk '/^ +[0-9]+ +/{print $1}'); do printf 'port_remove %s\\n' \"$p\"; done | " + p4CLI + " >/dev/null 2>&1; true"},
		{"sh", "-c", "for t in /sys/class/net/*-tc; do [ -e \"$t\" ] || continue; e=${t##*/}; e=${e%-tc}; tc qdisc del dev $e ingress 2>/dev/null; ip link del $e-tc 2>/dev/null; done; true"},
	}
}

func (P4SwitchDriver) AddInterfaceToBridge(iface, bridge string) [][]string { return portAdd(iface) }

func (P4SwitchDriver) RemoveInterfaceFromBridge(iface, bridge string) [][]string {
	n := portNumberFromName(iface)
	if n == "" {
		return [][]string{{"sh", "-c", "echo 'interface " + iface + " carries no port number in its name' >&2; exit 1"}}
	}
	return [][]string{p4cli("port_remove " + n), {"sh", "-c", portUnwrap(iface)}}
}

func (P4SwitchDriver) LoadProgram(content string) [][]string {
	return [][]string{
		{"sh", "-c", "mkdir -p /run/kubendt && printf '%s\\n' " + shellSingleQuote(content) + " > " + p4ProgramPath},
		p4cli("load_new_config_file "+p4ProgramPath, "swap_configs"),
	}
}

func p4EntryLine(table, action, match, params string, priority int) string {
	parts := []string{"table_add", table, action, match, "=>"}
	if params != "" {
		parts = append(parts, params)
	}
	if priority >= 0 {
		parts = append(parts, strconv.Itoa(priority))
	}
	return strings.Join(parts, " ")
}

func (P4SwitchDriver) TableAdd(table, action, match, params string, priority int) [][]string {
	return [][]string{p4cli(p4EntryLine(table, action, match, params, priority))}
}

// TableDelete looks the entry's handle up by its key first, since the CLI
// deletes by handle only.
func (P4SwitchDriver) TableDelete(table, match string, priority int) [][]string {
	key := table + " " + match
	if priority >= 0 {
		key += " " + strconv.Itoa(priority)
	}
	return [][]string{{"sh", "-c", "h=$(printf 'table_dump_entry_from_key " + key + "\\n' | " + p4CLI + " 2>&1 | sed -n 's/^.*Dumping entry 0x\\([0-9a-fA-F]*\\).*/\\1/p' | head -1); " +
		"[ -n \"$h\" ] || { echo 'no entry for " + match + " in " + table + "' >&2; exit 1; }; " +
		"out=$(printf 'table_delete " + table + " %s\\n' \"$((0x$h))\" | " + p4CLI + " 2>&1); printf '%s\\n' \"$out\" | grep -v '" + p4CLINoiseExpr + "'; " +
		"! printf '%s\\n' \"$out\" | grep -q '" + p4CLIErrorExpr + "'"}}
}

func (P4SwitchDriver) TableSetDefault(table, action, params string) [][]string {
	return [][]string{p4cli(strings.TrimRight("table_set_default "+table+" "+action+" "+params, " "))}
}

// mcNodesOf prints the node handles of a group out of mc_dump, for the
// shell: the group's line, then every node line under it.
func mcNodesOf(group int) string {
	return "printf 'mc_dump\\n' | " + p4CLI + " 2>/dev/null | sed 's/^RuntimeCmd: //' | awk -v g='mgrp(" + strconv.Itoa(group) + ")' '/^mgrp\\(/{on=($0==g)} on && /L1h=/{sub(/.*L1h=/, \"\"); sub(/,.*/, \"\"); print}'"
}

func mcDropNodes(group int) string {
	g := strconv.Itoa(group)
	return "for h in $(" + mcNodesOf(group) + "); do printf 'mc_node_dissociate " + g + " %s\\nmc_node_destroy %s\\n' \"$h\" \"$h\" | " + p4CLI + " >/dev/null 2>&1; done"
}

// AddMulticastGroup makes the group copy to exactly these ports: the group
// is created if new (the CLI says nothing for one that exists), its nodes
// are dropped, and one node with the ports is created and associated, its
// handle read from the CLI's answer. The node's replication id is the
// group's number, so the egress can tell a copy from a unicast packet.
func (P4SwitchDriver) AddMulticastGroup(group int, ports []int) [][]string {
	g := strconv.Itoa(group)
	ps := make([]string, len(ports))
	for i, p := range ports {
		ps[i] = strconv.Itoa(p)
	}
	return [][]string{{"sh", "-c",
		"printf 'mc_mgrp_create " + g + "\\n' | " + p4CLI + " >/dev/null 2>&1; " + mcDropNodes(group) + "; " +
			"out=$(printf 'mc_node_create " + g + " " + strings.Join(ps, " ") + "\\n' | " + p4CLI + " 2>&1); " +
			"h=$(printf '%s\\n' \"$out\" | sed -n 's/.*created with handle \\([0-9]*\\).*/\\1/p' | head -1); " +
			"[ -n \"$h\" ] || { printf '%s\\n' \"$out\" | grep -v '" + p4CLINoiseExpr + "' >&2; exit 1; }; " +
			p4cliShell("printf 'mc_node_associate "+g+" %s\\n' \"$h\"")}}
}

func (P4SwitchDriver) RemoveMulticastGroup(group int) [][]string {
	g := strconv.Itoa(group)
	return [][]string{{"sh", "-c",
		"printf 'mc_dump\\n' | " + p4CLI + " 2>/dev/null | sed 's/^RuntimeCmd: //' | grep -qx 'mgrp(" + g + ")' || { echo 'no multicast group " + g + " on the switch' >&2; exit 1; }; " +
			mcDropNodes(group) + "; " + p4cliShell("printf 'mc_mgrp_destroy "+g+"\\n'")}}
}

var (
	p4GroupRe = regexp.MustCompile(`^mgrp\((\d+)\)$`)
	p4NodeRe  = regexp.MustCompile(`L1h=(\d+),.*ports=\[([^\]]*)\]`)
	p4PortRe  = regexp.MustCompile(`^\s*(\d+)\s+(\S+)\s+(UP|DOWN)\b`)
	p4TableRe = regexp.MustCompile(`^(?:RuntimeCmd: )?([A-Za-z_][A-Za-z0-9_.]*)\s+\[(.*)\]\s*$`)
	p4EntryRe = regexp.MustCompile(`^(?:RuntimeCmd: )?Dumping entry 0x([0-9a-fA-F]+)`)
	p4FieldRe = regexp.MustCompile(`^\*\s+(\S+)\s*:\s*(\S+)\s+(\S+)`)
	p4ActRe   = regexp.MustCompile(`^Action entry:\s*(\S+)\s*-\s*(.*)$`)
)

func (P4SwitchDriver) P4Ports(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, p4read("show_ports"))
	if err != nil {
		return nil, err
	}
	return parseP4Ports(string(out)), nil
}

func parseP4Ports(text string) capabilities.Rows {
	rows := capabilities.Rows{}
	for _, line := range strings.Split(text, "\n") {
		if m := p4PortRe.FindStringSubmatch(line); m != nil {
			// A port on a VXLAN link reads its veth, named after the interface.
			rows = append(rows, map[string]string{"port": m[1], "iface": strings.TrimSuffix(m[2], "-p4"), "status": strings.ToLower(m[3])})
		}
	}
	return rows
}

func (P4SwitchDriver) P4MulticastGroups(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, p4read("mc_dump"))
	if err != nil {
		return nil, err
	}
	return parseP4MulticastGroups(string(out)), nil
}

// parseP4MulticastGroups reads mc_dump: a "mgrp(1)" line, then one line per
// node with its handle, replication id and ports.
func parseP4MulticastGroups(text string) capabilities.Rows {
	rows := capabilities.Rows{}
	group := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimPrefix(strings.TrimSpace(raw), "RuntimeCmd: ")
		if m := p4GroupRe.FindStringSubmatch(line); m != nil {
			group = m[1]
			continue
		}
		if m := p4NodeRe.FindStringSubmatch(line); m != nil && group != "" {
			rows = append(rows, map[string]string{"group": group, "ports": strings.TrimSpace(m[2]), "handle": m[1]})
		}
	}
	return rows
}

func (P4SwitchDriver) P4Tables(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, p4read("show_tables"))
	if err != nil {
		return nil, err
	}
	return parseP4Tables(string(out)), nil
}

// parseP4Tables reads "NAME [implementation=None, mk=ipv4.dstAddr(lpm, 32)]".
func parseP4Tables(text string) capabilities.Rows {
	rows := capabilities.Rows{}
	for _, line := range strings.Split(text, "\n") {
		m := p4TableRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		match := ""
		if i := strings.Index(m[2], "mk="); i >= 0 {
			match = m[2][i+3:]
		}
		rows = append(rows, map[string]string{"table": m[1], "match": match})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["table"] < rows[j]["table"] })
	return rows
}

// P4Entries dumps every table the program has, one CLI run per table behind
// a marker line with its name.
func (P4SwitchDriver) P4Entries(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, []string{"sh", "-c", "for t in $(printf 'show_tables\\n' | " + p4CLI + " 2>/dev/null | sed 's/^RuntimeCmd: //' | awk '/^[A-Za-z_][A-Za-z0-9_.]* +\\[/{print $1}'); do echo \"### $t\"; printf 'table_dump %s\\n' \"$t\" | " + p4CLI + " 2>&1; done"})
	if err != nil {
		return nil, err
	}
	return parseP4Entries(string(out)), nil
}

// parseP4Entries reads the table_dump blocks: a handle line, the match
// fields, the action with its parameters; the default entry has no handle.
func parseP4Entries(text string) capabilities.Rows {
	rows := capabilities.Rows{}
	table := ""
	var cur map[string]string
	var fields []string
	flush := func() {
		if cur != nil {
			cur["match"] = strings.Join(fields, ", ")
			rows = append(rows, cur)
		}
		cur, fields = nil, nil
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimPrefix(strings.TrimSpace(raw), "RuntimeCmd: ")
		switch {
		case strings.HasPrefix(line, "### "):
			flush()
			table = strings.TrimPrefix(line, "### ")
		case p4EntryRe.MatchString(line):
			flush()
			h, _ := strconv.ParseInt(p4EntryRe.FindStringSubmatch(line)[1], 16, 64)
			cur = map[string]string{"table": table, "handle": strconv.FormatInt(h, 10), "action": "", "params": ""}
		case strings.HasPrefix(line, "Dumping default entry"):
			flush()
			cur = map[string]string{"table": table, "handle": "default", "action": "", "params": ""}
		case cur != nil && p4FieldRe.MatchString(line):
			m := p4FieldRe.FindStringSubmatch(line)
			fields = append(fields, m[1]+" "+strings.ToLower(m[2])+" "+m[3])
		case cur != nil && p4ActRe.MatchString(line):
			m := p4ActRe.FindStringSubmatch(line)
			cur["action"], cur["params"] = m[1], strings.TrimSpace(m[2])
		}
	}
	flush()
	return rows
}
