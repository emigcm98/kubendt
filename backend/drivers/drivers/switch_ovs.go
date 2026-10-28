package drivers

import (
	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/executor"
	"strings"
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
		cmds = append(cmds, []string{"ovs-vsctl", "--may-exist", "add-port", bridge, iface})
	}
	return cmds
}

func (OpenVSwitchDriver) TeardownBridge(bridge string) [][]string {
	return [][]string{
		{"ovs-vsctl", "del-br", bridge},
	}
}

func (OpenVSwitchDriver) AddInterfaceToBridge(iface, bridge string) [][]string {
	return [][]string{
		{"ovs-vsctl", "--may-exist", "add-port", bridge, iface},
	}
}

func (OpenVSwitchDriver) RemoveInterfaceFromBridge(iface, bridge string) [][]string {
	return [][]string{
		{"ovs-vsctl", "del-port", bridge, iface},
	}
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
