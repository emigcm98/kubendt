package capabilities

import (
	"kubendt/actions"
	"kubendt/types"
)

// Only bridge operations. No L2 here.
type SwitchCapable interface {
	SetupBridge(bridge string, ifaces []string) [][]string
	TeardownBridge(bridge string) [][]string
	AddInterfaceToBridge(iface, bridge string) [][]string
	RemoveInterfaceFromBridge(iface, bridge string) [][]string
}

func init() {
	actions.Register[SwitchCapable](
		actions.Capability{ID: "SwitchCapable", Label: "Switching", Description: "Management of switching functions."},
		actions.Action[SwitchCapable]{Type: "setup_bridge", Label: "Create bridge", Params: actions.P(actions.Bridge, actions.Ifaces.Optional()),
			Run: func(c SwitchCapable, a types.ActionEntry) ([][]string, error) {
				return c.SetupBridge(a.Bridge, a.Ifaces), nil
			}},
		actions.Action[SwitchCapable]{Type: "teardown_bridge", Label: "Delete bridge", Params: actions.P(actions.Bridge),
			Run: func(c SwitchCapable, a types.ActionEntry) ([][]string, error) { return c.TeardownBridge(a.Bridge), nil }},
		actions.Action[SwitchCapable]{Type: "add_interface_to_bridge", Label: "Add interface to bridge", Params: actions.P(actions.Iface, actions.Bridge),
			Run: func(c SwitchCapable, a types.ActionEntry) ([][]string, error) {
				return c.AddInterfaceToBridge(a.Iface, a.Bridge), nil
			}},
		actions.Action[SwitchCapable]{Type: "remove_interface_from_bridge", Label: "Remove interface from bridge", Params: actions.P(actions.Iface, actions.Bridge),
			Run: func(c SwitchCapable, a types.ActionEntry) ([][]string, error) {
				return c.RemoveInterfaceFromBridge(a.Iface, a.Bridge), nil
			}},
	)
}
