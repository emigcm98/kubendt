package drivers

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/executor"
)

type OpenVSwitchDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.SwitchBase // embedded but methods are overridden below
}

func NewOpenVSwitchDriver() *OpenVSwitchDriver {
	return &OpenVSwitchDriver{
		Meta: drivers_meta.NewMeta("OpenVSwitchDriver", "switch"),
	}
}

var _ capabilities.L2Capable = (*OpenVSwitchDriver)(nil)
var _ capabilities.SwitchCapable = (*OpenVSwitchDriver)(nil)
var _ drivers_meta.ReadinessProbeProvider = (*OpenVSwitchDriver)(nil)

// ReadinessProbeCommands makes the pod Ready only once ovs-vsctl can talk to
// ovsdb-server: the image's entrypoint starts the daemons seconds after the
// container runs, and a bridge setup or a replay landing in that window fails.
func (OpenVSwitchDriver) ReadinessProbeCommands() drivers_meta.ReadinessProbeSpec {
	return drivers_meta.ReadinessProbeSpec{
		Command:             []string{"sh", "-c", "ovs-vsctl --timeout=2 show >/dev/null"},
		InitialDelaySeconds: 0,
		PeriodSeconds:       2,
		TimeoutSeconds:      3,
		FailureThreshold:    30,
	}
}

// --- OVS-specific overrides ---

func (OpenVSwitchDriver) SetupBridge(bridge string, ifaces []string) [][]string {
	cmds := [][]string{
		{"sh", "-c", "ovs-vsctl br-exists " + bridge + " || ovs-vsctl add-br " + bridge},
		{"ip", "link", "set", bridge, "up"},
	}
	for _, iface := range ifaces {
		cmds = append(cmds, ovsAddPort(bridge, iface))
	}
	return cmds
}

// ovsAddPort attaches the interface with a linux-noop QoS, which tells OVS
// to leave its qdisc alone: since 3.x a port with no QoS has its qdisc
// replaced on attach, which wiped the netem of the link attributes.
func ovsAddPort(bridge, iface string) []string {
	return []string{"ovs-vsctl", "--may-exist", "add-port", bridge, iface, "--", "--id=@q", "create", "qos", "type=linux-noop", "--", "set", "port", iface, "qos=@q"}
}

func (OpenVSwitchDriver) TeardownBridge(bridge string) [][]string {
	return [][]string{
		{"ovs-vsctl", "del-br", bridge},
	}
}

func (OpenVSwitchDriver) AddInterfaceToBridge(iface, bridge string) [][]string {
	return [][]string{ovsAddPort(bridge, iface)}
}

func (OpenVSwitchDriver) RemoveInterfaceFromBridge(iface, bridge string) [][]string {
	return [][]string{
		{"ovs-vsctl", "del-port", bridge, iface},
	}
}

var _ capabilities.VLANCapable = (*OpenVSwitchDriver)(nil)

func (OpenVSwitchDriver) VLANSetAccess(port string, vlan int) [][]string {
	return [][]string{{"ovs-vsctl", "set", "port", port, "vlan_mode=access", "tag=" + strconv.Itoa(vlan), "--", "clear", "port", port, "trunks"}}
}

// VLANSetTrunk with a native VLAN is OVS's native-untagged mode, the native
// VLAN in tag and admitted on the trunk whether or not trunks lists it.
func (OpenVSwitchDriver) VLANSetTrunk(port string, vlans []int, native int) [][]string {
	ids := make([]string, 0, len(vlans))
	for _, v := range vlans {
		ids = append(ids, strconv.Itoa(v))
	}
	trunks := "trunks=" + strings.Join(ids, ",")
	if native != 0 {
		return [][]string{{"ovs-vsctl", "set", "port", port, "vlan_mode=native-untagged", "tag=" + strconv.Itoa(native), trunks}}
	}
	return [][]string{{"ovs-vsctl", "set", "port", port, "vlan_mode=trunk", trunks, "--", "clear", "port", port, "tag"}}
}

func (OpenVSwitchDriver) VLANClearPort(port string) [][]string {
	return [][]string{{"ovs-vsctl", "clear", "port", port, "tag", "trunks", "vlan_mode"}}
}

var _ capabilities.VLANInspector = (*OpenVSwitchDriver)(nil)

// VLANPorts reads tag, trunks and vlan_mode of every port from ovsdb's JSON,
// where a set of one value comes bare; a bridge's own internal port is
// listed only when it has VLAN settings, as the SVI's uplink.
func (OpenVSwitchDriver) VLANPorts(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, []string{"sh", "-c", "ovs-vsctl list-br; echo ---; ovs-vsctl --format=json --columns=name,vlan_mode,tag,trunks list Port"})
	if err != nil {
		return nil, err
	}
	return parseOVSVLANPorts(string(out))
}

func parseOVSVLANPorts(out string) (capabilities.Rows, error) {
	brText, jsonText, _ := strings.Cut(out, "---\n")
	bridges := map[string]bool{}
	for _, b := range strings.Fields(brText) {
		bridges[b] = true
	}
	var table struct {
		Data [][]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(jsonText), &table); err != nil {
		return nil, fmt.Errorf("ovs-vsctl list Port: %w", err)
	}
	var rows capabilities.Rows
	for _, r := range table.Data {
		if len(r) < 4 {
			continue
		}
		name := ovsScalar(r[0])
		mode, tag, trunks := ovsScalar(r[1]), ovsScalar(r[2]), strings.Join(ovsSet(r[3]), ",")
		if bridges[name] && mode == "" && tag == "" && trunks == "" {
			continue
		}
		vlans, native := trunks, ""
		switch {
		case mode == "access" || (mode == "" && tag != ""):
			mode, vlans = "access", tag
		case strings.HasPrefix(mode, "native"):
			native = tag
		case mode == "" && trunks != "":
			mode = "trunk"
		case mode == "":
			mode = "none"
		}
		rows = append(rows, map[string]string{"port": name, "mode": mode, "vlans": vlans, "native": native})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["port"] < rows[j]["port"] })
	return rows, nil
}

// ovsSet lists the values of an ovsdb column: a bare value, or ["set", [...]].
func ovsSet(v any) []string {
	if arr, ok := v.([]any); ok && len(arr) == 2 && arr[0] == "set" {
		inner, _ := arr[1].([]any)
		out := make([]string, 0, len(inner))
		for _, x := range inner {
			out = append(out, ovsScalar(x))
		}
		return out
	}
	if s := ovsScalar(v); s != "" {
		return []string{s}
	}
	return nil
}

func ovsScalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.Itoa(int(x))
	}
	return ""
}

var _ capabilities.STPCapable = (*OpenVSwitchDriver)(nil)

// RSTP first, the default: it converges in seconds, which a lab notices.
func (OpenVSwitchDriver) STPModes() []string { return []string{"rstp", "stp"} }

func (OpenVSwitchDriver) STPEnable(bridge, mode string) [][]string {
	if mode == "stp" {
		return [][]string{{"ovs-vsctl", "set", "bridge", bridge, "stp_enable=true", "rstp_enable=false"}}
	}
	return [][]string{{"ovs-vsctl", "set", "bridge", bridge, "rstp_enable=true", "stp_enable=false"}}
}

func (OpenVSwitchDriver) STPDisable(bridge string) [][]string {
	return [][]string{{"ovs-vsctl", "set", "bridge", bridge, "stp_enable=false", "rstp_enable=false"}}
}

// Both flavours take the same numbers, set for whichever runs.
func (OpenVSwitchDriver) STPSetBridgePriority(bridge string, priority int) [][]string {
	p := strconv.Itoa(priority)
	return [][]string{{"ovs-vsctl", "set", "bridge", bridge, "other_config:stp-priority=" + p, "other_config:rstp-priority=" + p}}
}

func (OpenVSwitchDriver) STPSetPort(port string, cost, priority int) [][]string {
	args := []string{"ovs-vsctl", "set", "port", port}
	if cost > 0 {
		c := strconv.Itoa(cost)
		args = append(args, "other_config:stp-path-cost="+c, "other_config:rstp-path-cost="+c)
	}
	if priority >= 0 {
		p := strconv.Itoa(priority)
		args = append(args, "other_config:stp-port-priority="+p, "other_config:rstp-port-priority="+p)
	}
	return [][]string{args}
}

var _ capabilities.STPInspector = (*OpenVSwitchDriver)(nil)

// ovsSTPScript prints each bridge with its mode and, when one runs, the
// text of ovs-appctl stp/show or rstp/show, which share one layout.
const ovsSTPScript = `for b in $(ovs-vsctl list-br); do m=off; [ "$(ovs-vsctl get bridge $b rstp_enable)" = true ] && m=rstp; [ "$(ovs-vsctl get bridge $b stp_enable)" = true ] && m=stp; echo "=== $b $m"; [ $m = off ] || ovs-appctl $m/show $b; done`

func (OpenVSwitchDriver) STPBridges(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, []string{"sh", "-c", ovsSTPScript})
	if err != nil {
		return nil, err
	}
	bridges, _ := parseOVSSTP(string(out))
	return bridges, nil
}

func (OpenVSwitchDriver) STPPorts(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, []string{"sh", "-c", ovsSTPScript})
	if err != nil {
		return nil, err
	}
	_, ports := parseOVSSTP(string(out))
	return ports, nil
}

// parseOVSSTP reads the blocks of ovsSTPScript: a Root ID and a Bridge ID
// block with priority and system id, "This bridge is the root" when it is,
// and a port table of name, role, state, cost and priority.number.
func parseOVSSTP(text string) (bridges, ports capabilities.Rows) {
	var cur map[string]string
	var ids []string
	var prio string
	inTable := false
	flush := func() {
		if cur == nil {
			return
		}
		switch len(ids) {
		case 1:
			cur["bridge_id"] = ids[0]
		default:
			if len(ids) >= 2 {
				cur["root_id"], cur["bridge_id"] = ids[0], ids[1]
			}
		}
		cur["priority"] = strings.SplitN(cur["bridge_id"], ".", 2)[0]
		if cur["root"] == "yes" {
			cur["root_id"] = cur["bridge_id"]
		}
		bridges = append(bridges, cur)
	}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch {
		case f[0] == "===" && len(f) == 3:
			flush()
			cur = map[string]string{"bridge": f[1], "mode": f[2], "priority": "", "bridge_id": "", "root_id": "", "root": "no", "root_port": ""}
			ids, prio, inTable = nil, "", false
		case cur == nil:
		case f[0] == "stp-priority" && len(f) == 2:
			prio = f[1]
		case f[0] == "stp-system-id" && len(f) == 2:
			ids = append(ids, prio+"."+f[1])
		case strings.Contains(line, "This bridge is the root"):
			cur["root"] = "yes"
		case f[0] == "Interface":
			inTable = true
		case inTable && len(f) >= 5 && !strings.HasPrefix(f[0], "-"):
			role, state := strings.ToLower(f[1]), strings.ToLower(f[2])
			if role == "root" {
				cur["root_port"] = f[0]
			}
			ports = append(ports, map[string]string{"bridge": cur["bridge"], "port": f[0], "role": role, "state": state, "cost": f[3], "priority": strings.SplitN(f[4], ".", 2)[0]})
		}
	}
	flush()
	return bridges, ports
}

var _ capabilities.OpenFlowCapable = (*OpenVSwitchDriver)(nil)

// ofVersions lets ovs-ofctl speak whatever the bridge offers, since its own
// default is OpenFlow 1.0 alone.
const ofVersions = "OpenFlow10,OpenFlow11,OpenFlow12,OpenFlow13,OpenFlow14,OpenFlow15"

func ofctl(args ...string) []string { return append([]string{"ovs-ofctl", "-O", ofVersions}, args...) }

func (OpenVSwitchDriver) SetController(bridge, target, protocols string) [][]string {
	cmd := []string{"ovs-vsctl", "set-controller", bridge, target}
	if protocols != "" {
		cmd = append(cmd, "--", "set", "bridge", bridge, "protocols="+protocols)
	}
	return [][]string{cmd}
}

func (OpenVSwitchDriver) RemoveController(bridge string) [][]string {
	return [][]string{{"ovs-vsctl", "del-controller", bridge}}
}

func (OpenVSwitchDriver) SetFailMode(bridge, mode string) [][]string {
	return [][]string{{"ovs-vsctl", "set-fail-mode", bridge, mode}}
}

func (OpenVSwitchDriver) SetDatapathID(bridge, id string) [][]string {
	return [][]string{{"ovs-vsctl", "set", "bridge", bridge, "other_config:datapath-id=" + id}}
}

func (OpenVSwitchDriver) AddFlow(bridge, flow string) [][]string {
	return [][]string{ofctl("add-flow", bridge, flow)}
}

// DeleteFlow is strict: the match and priority as given, not every flow the
// match covers.
func (OpenVSwitchDriver) DeleteFlow(bridge, match string) [][]string {
	return [][]string{ofctl("--strict", "del-flows", bridge, match)}
}

var _ capabilities.OpenFlowInspector = (*OpenVSwitchDriver)(nil)

// OpenFlowBridges joins the Bridge and Controller tables of ovsdb.
func (OpenVSwitchDriver) OpenFlowBridges(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, []string{"sh", "-c", "ovs-vsctl --format=json --columns=name,controller,fail_mode,protocols,datapath_id list Bridge; echo ---; ovs-vsctl --format=json --columns=_uuid,target,is_connected list Controller"})
	if err != nil {
		return nil, err
	}
	return parseOVSOpenFlowBridges(string(out))
}

func parseOVSOpenFlowBridges(out string) (capabilities.Rows, error) {
	bridgesText, controllersText, _ := strings.Cut(out, "---\n")
	var bridges, controllers struct {
		Data [][]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(bridgesText), &bridges); err != nil {
		return nil, fmt.Errorf("ovs-vsctl list Bridge: %w", err)
	}
	if err := json.Unmarshal([]byte(controllersText), &controllers); err != nil {
		return nil, fmt.Errorf("ovs-vsctl list Controller: %w", err)
	}
	type ctl struct {
		target    string
		connected bool
	}
	byUUID := map[string]ctl{}
	for _, r := range controllers.Data {
		if len(r) < 3 {
			continue
		}
		connected, _ := r[2].(bool)
		byUUID[ovsUUID(r[0])] = ctl{target: ovsScalar(r[1]), connected: connected}
	}
	var rows capabilities.Rows
	for _, r := range bridges.Data {
		if len(r) < 5 {
			continue
		}
		var targets []string
		connected := ""
		for _, u := range ovsUUIDs(r[1]) {
			c := byUUID[u]
			targets = append(targets, c.target)
			if connected == "" {
				connected = "no"
			}
			if c.connected {
				connected = "yes"
			}
		}
		failMode := ovsScalar(r[2])
		if failMode == "" {
			failMode = "standalone"
		}
		rows = append(rows, map[string]string{"bridge": ovsScalar(r[0]), "controller": strings.Join(targets, ","), "connected": connected, "fail_mode": failMode, "protocols": strings.Join(ovsSet(r[3]), ","), "datapath_id": ovsScalar(r[4])})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["bridge"] < rows[j]["bridge"] })
	return rows, nil
}

// ovsUUID reads ["uuid", "<id>"]; ovsUUIDs a bare one or a set of them.
func ovsUUID(v any) string {
	if arr, ok := v.([]any); ok && len(arr) == 2 && arr[0] == "uuid" {
		s, _ := arr[1].(string)
		return s
	}
	return ""
}

func ovsUUIDs(v any) []string {
	if u := ovsUUID(v); u != "" {
		return []string{u}
	}
	if arr, ok := v.([]any); ok && len(arr) == 2 && arr[0] == "set" {
		inner, _ := arr[1].([]any)
		out := make([]string, 0, len(inner))
		for _, x := range inner {
			if u := ovsUUID(x); u != "" {
				out = append(out, u)
			}
		}
		return out
	}
	return nil
}

func (OpenVSwitchDriver) OpenFlowFlows(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, []string{"sh", "-c", "for b in $(ovs-vsctl list-br); do ovs-ofctl -O " + ofVersions + " dump-flows $b | sed \"s/^ */$b /\"; done"})
	if err != nil {
		return nil, err
	}
	return parseOVSFlows(string(out)), nil
}

var flowStatKeys = map[string]bool{"cookie": true, "duration": true, "table": true, "n_packets": true, "n_bytes": true, "idle_age": true, "hard_age": true, "idle_timeout": true, "hard_timeout": true, "importance": true, "send_flow_rem": true, "reset_counts": true}

// parseOVSFlows reads dump-flows lines prefixed with their bridge: the
// statistics come first as `key=value, ` pairs, the match last, then
// ` actions=`. The priority is lifted out of the match into its column.
func parseOVSFlows(text string) capabilities.Rows {
	var rows capabilities.Rows
	for _, line := range strings.Split(text, "\n") {
		bridge, rest, ok := strings.Cut(line, " ")
		if !ok || !strings.Contains(rest, "actions=") {
			continue
		}
		left, actions, _ := strings.Cut(rest, " actions=")
		stats := map[string]string{}
		var match []string
		for _, tok := range strings.Split(left, ", ") {
			k, v, _ := strings.Cut(tok, "=")
			if flowStatKeys[k] && !strings.Contains(tok, ",") {
				stats[k] = v
			} else if tok != "" {
				match = append(match, tok)
			}
		}
		priority := "32768"
		var keep []string
		for _, item := range strings.Split(strings.Join(match, ","), ",") {
			if v, ok := strings.CutPrefix(item, "priority="); ok {
				priority = v
			} else if item != "" {
				keep = append(keep, item)
			}
		}
		rows = append(rows, map[string]string{"bridge": bridge, "table": stats["table"], "priority": priority, "match": strings.Join(keep, ","), "actions": actions, "packets": stats["n_packets"]})
	}
	return rows
}

var _ capabilities.FDBInspector = (*OpenVSwitchDriver)(nil)

// BridgeFDB reads every bridge's learned table from ovs-appctl, whose text
// is `port VLAN MAC Age` per row.
func (OpenVSwitchDriver) BridgeFDB(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, []string{"sh", "-c", "for b in $(ovs-vsctl list-br); do ovs-appctl fdb/show $b | awk -v b=$b 'NR>1 {print b, $1, $2, $3, $4}'; done"})
	if err != nil {
		return nil, err
	}
	var rows capabilities.Rows
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		rows = append(rows, map[string]string{"bridge": f[0], "mac": f[3], "interface": "port " + f[1], "vlan": f[2], "flags": "learned"})
	}
	return rows, nil
}

var _ drivers_meta.KnownImagesProvider = (*OpenVSwitchDriver)(nil)

// KnownImages: our own image, tagged by the Open vSwitch version, and the
// third-party one the examples ran on until 1.8.0, which still works.
func (OpenVSwitchDriver) KnownImages() []string {
	return []string{"ghcr.io/emigcm98/kubendt-ovs:3.7.1", "globocom/openvswitch:latest"}
}
