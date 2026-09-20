package drivers

import (
	"kubendt/capabilities/capabilities"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/types"
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
var _ types.ReadinessProbeProvider = (*OpenVSwitchDriver)(nil)

// ReadinessProbeCommands makes the pod Ready only once ovs-vsctl can talk to
// ovsdb-server. The default probe (`command -v ip`) passes as soon as the
// image has iproute2, seconds before the OVS daemons listen, and a bridge
// setup or a replay landing in that window fails.
func (OpenVSwitchDriver) ReadinessProbeCommands() types.ReadinessProbeSpec {
	return types.ReadinessProbeSpec{
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
