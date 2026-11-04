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

// VLANPorts reads tag, trunks and vlan_mode of every port but the bridges'
// own internal ones, from ovsdb's JSON, where a set of one value comes bare.
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
		if bridges[name] {
			continue
		}
		mode, tag, trunks := ovsScalar(r[1]), ovsScalar(r[2]), strings.Join(ovsSet(r[3]), ",")
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
