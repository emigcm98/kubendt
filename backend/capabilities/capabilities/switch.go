package capabilities

// Only bridge operations. No L2 here.
type SwitchCapable interface {
	SetupBridge(bridge string, ifaces []string) [][]string
	TeardownBridge(bridge string) [][]string
	AddInterfaceToBridge(iface, bridge string) [][]string
	RemoveInterfaceFromBridge(iface, bridge string) [][]string
}

var SwitchMethods = map[string]string{
	"setup_bridge":                 "SetupBridge",
	"teardown_bridge":              "TeardownBridge",
	"add_interface_to_bridge":      "AddInterfaceToBridge",
	"remove_interface_from_bridge": "RemoveInterfaceFromBridge",
}
