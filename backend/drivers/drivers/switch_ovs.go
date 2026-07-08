package drivers

import (
	"kubendt/capabilities/capabilities"
	drivers_meta "kubendt/drivers/meta"
)

type OpenVSwitchDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.SwitchBase // embedded but methods are overridden below
	capabilities.TCBase
}

func NewOpenVSwitchDriver() *OpenVSwitchDriver {
	return &OpenVSwitchDriver{
		Meta: drivers_meta.NewMeta("OpenVSwitchDriver", "switch"),
	}
}

var _ capabilities.L2Capable = (*OpenVSwitchDriver)(nil)
var _ capabilities.SwitchCapable = (*OpenVSwitchDriver)(nil)
var _ capabilities.TCCapable = (*OpenVSwitchDriver)(nil)

// --- OVS-specific overrides ---

func (OpenVSwitchDriver) SetupBridge(bridge string, ifaces []string) [][]string {
	cmds := [][]string{
		{"sh", "-c", "ovs-vsctl br-exists " + bridge + " || ovs-vsctl add-br " + bridge},
		{"ip", "link", "set", bridge, "up"},
	}
	for _, iface := range ifaces {
		cmds = append(cmds, []string{"ovs-vsctl", "add-port", bridge, iface})
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
		{"ovs-vsctl", "add-port", bridge, iface},
	}
}

func (OpenVSwitchDriver) RemoveInterfaceFromBridge(iface, bridge string) [][]string {
	return [][]string{
		{"ovs-vsctl", "del-port", bridge, iface},
	}
}
