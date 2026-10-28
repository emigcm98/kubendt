package capabilities

import (
	"kubendt/actions"
	"kubendt/types"
)

// NATCapable builds NAT commands for both families: SNAT takes `family`
// (4 by default), a port forward follows its internal address. Reading the
// SNAT interface back is a drivers_meta.SNATInterfaceInspector, NATBase
// provides the Linux one.
type NATCapable interface {
	EnableSNAT(iface string, v6 bool) [][]string
	DisableSNAT(iface string, v6 bool) [][]string
	EnableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string, v6 bool) [][]string
	DisableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string, v6 bool) [][]string
}

// The SNAT toggles are the one thing allowed on eth0: masquerading the
// cluster interface is how a topology gets internet access.
func init() {
	snat := actions.P(actions.Iface, actions.Family)
	dnat := actions.P(actions.Iface, actions.ExternalPort, actions.InternalIP, actions.InternalPort, actions.Protocol)
	actions.Register[NATCapable](
		actions.Capability{ID: "NATCapable", Label: "Network Address Translation (NAT)", Description: "Masquerading and port forwards, IPv4 and IPv6 (NAT66) alike."},
		actions.Action[NATCapable]{Type: "enable_snat", Label: "Enable SNAT", Params: snat, AllowProtectedIface: true, State: "snat", Key: keyIfaceFamily,
			Run: func(c NATCapable, a types.ActionEntry) ([][]string, error) {
				return c.EnableSNAT(a.Iface, a.Family == "6"), nil
			}},
		actions.Action[NATCapable]{Type: "disable_snat", Label: "Disable SNAT", Params: snat, AllowProtectedIface: true, State: "snat", Key: keyIfaceFamily, Clears: true,
			Run: func(c NATCapable, a types.ActionEntry) ([][]string, error) {
				return c.DisableSNAT(a.Iface, a.Family == "6"), nil
			}},
		actions.Action[NATCapable]{Type: "enable_dnat", Label: "Add port forward", Params: dnat, State: "dnat", Key: keyPortForward,
			Run: func(c NATCapable, a types.ActionEntry) ([][]string, error) {
				return c.EnableDNAT(a.Iface, a.ExternalPort, a.InternalIP, a.InternalPort, a.Protocol, IsIPv6(a.InternalIP)), nil
			}},
		actions.Action[NATCapable]{Type: "disable_dnat", Label: "Remove port forward", Params: dnat, State: "dnat", Key: keyPortForward, Clears: true,
			Run: func(c NATCapable, a types.ActionEntry) ([][]string, error) {
				return c.DisableDNAT(a.Iface, a.ExternalPort, a.InternalIP, a.InternalPort, a.Protocol, IsIPv6(a.InternalIP)), nil
			}},
	)
}
