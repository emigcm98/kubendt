package capabilities

import (
	"kubendt/actions"
	"kubendt/types"
)

type NATCapable interface {
	// Non-persistent query to get the SNAT interface for a given pod
	GetSNATInterface(namespace, podName string) (string, error)
	EnableSNAT(iface string) [][]string
	DisableSNAT(iface string) [][]string
	EnableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string) [][]string
	DisableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string) [][]string
}

// The SNAT toggles are the one thing allowed on eth0: masquerading the
// cluster interface is how a topology gets internet access.
func init() {
	dnat := actions.P(actions.Iface, actions.ExternalPort, actions.InternalIP, actions.InternalPort, actions.Protocol)
	actions.Register[NATCapable](
		actions.Capability{ID: "NATCapable", Label: "Network Address Translation (NAT)", Description: "Management of NAT rules."},
		actions.Action[NATCapable]{Type: "enable_snat", Label: "Enable SNAT", Params: actions.P(actions.Iface), AllowProtectedIface: true,
			Run: func(c NATCapable, a types.ActionEntry) ([][]string, error) { return c.EnableSNAT(a.Iface), nil }},
		actions.Action[NATCapable]{Type: "disable_snat", Label: "Disable SNAT", Params: actions.P(actions.Iface), AllowProtectedIface: true,
			Run: func(c NATCapable, a types.ActionEntry) ([][]string, error) { return c.DisableSNAT(a.Iface), nil }},
		actions.Action[NATCapable]{Type: "enable_dnat", Label: "Add port forward", Params: dnat,
			Run: func(c NATCapable, a types.ActionEntry) ([][]string, error) {
				return c.EnableDNAT(a.Iface, a.ExternalPort, a.InternalIP, a.InternalPort, a.Protocol), nil
			}},
		actions.Action[NATCapable]{Type: "disable_dnat", Label: "Remove port forward", Params: dnat,
			Run: func(c NATCapable, a types.ActionEntry) ([][]string, error) {
				return c.DisableDNAT(a.Iface, a.ExternalPort, a.InternalIP, a.InternalPort, a.Protocol), nil
			}},
	)
}
