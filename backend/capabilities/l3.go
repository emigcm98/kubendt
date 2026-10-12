package capabilities

import (
	"kubendt/actions"
	"kubendt/types"
)

type L3Capable interface {
	SetIP(iface, cidr string) [][]string
	ReplaceIP(iface, cidr string) [][]string
	RemoveIP(iface, cidr string) [][]string
	SetDefaultRoute(gateway string) [][]string
	RemoveDefaultRoute() [][]string
	AddStaticRoute(dstCIDR, via, dev string) [][]string
	RemoveStaticRoute(dstCIDR, via, dev string) [][]string
	AddDNSNameserver(server string) [][]string
	RemoveDNSNameserver(server string) [][]string
	AddDNSSearch(domain string) [][]string
	RemoveDNSSearch(domain string) [][]string
}

func init() {
	actions.Register[L3Capable](
		actions.Capability{ID: "L3Capable", Label: "Level 3: IP addressing", Description: "Management of addressing and routes."},
		actions.Action[L3Capable]{Type: "set_ip", Label: "Add IP address", Params: actions.P(actions.Iface, actions.CIDR),
			Run: func(c L3Capable, a types.ActionEntry) ([][]string, error) { return c.SetIP(a.Iface, a.CIDR), nil }},
		actions.Action[L3Capable]{Type: "replace_ip", Label: "Replace IP address", Params: actions.P(actions.Iface, actions.CIDR),
			Run: func(c L3Capable, a types.ActionEntry) ([][]string, error) { return c.ReplaceIP(a.Iface, a.CIDR), nil }},
		actions.Action[L3Capable]{Type: "remove_ip", Label: "Remove IP address", Params: actions.P(actions.Iface, actions.CIDR),
			Run: func(c L3Capable, a types.ActionEntry) ([][]string, error) { return c.RemoveIP(a.Iface, a.CIDR), nil }},
		actions.Action[L3Capable]{Type: "set_default_route", Label: "Set default route", Params: actions.P(actions.Gateway),
			Run: func(c L3Capable, a types.ActionEntry) ([][]string, error) { return c.SetDefaultRoute(a.Gateway), nil }},
		actions.Action[L3Capable]{Type: "remove_default_route", Label: "Remove default route",
			Run: func(c L3Capable, a types.ActionEntry) ([][]string, error) { return c.RemoveDefaultRoute(), nil }},
		actions.Action[L3Capable]{Type: "add_static_route", Label: "Add static route", Params: actions.P(actions.DstCIDR, actions.Gateway, actions.Device.Optional()),
			Run: func(c L3Capable, a types.ActionEntry) ([][]string, error) {
				return c.AddStaticRoute(a.DstCIDR, a.Gateway, a.Device), nil
			}},
		actions.Action[L3Capable]{Type: "remove_static_route", Label: "Remove static route", Params: actions.P(actions.DstCIDR, actions.Gateway, actions.Device.Optional()),
			Run: func(c L3Capable, a types.ActionEntry) ([][]string, error) {
				return c.RemoveStaticRoute(a.DstCIDR, a.Gateway, a.Device), nil
			}},
		actions.Action[L3Capable]{Type: "add_dns_nameserver", Label: "Add DNS nameserver", Params: actions.P(actions.DNSServer),
			Run: func(c L3Capable, a types.ActionEntry) ([][]string, error) {
				return c.AddDNSNameserver(a.DNSServer), nil
			}},
		actions.Action[L3Capable]{Type: "remove_dns_nameserver", Label: "Remove DNS nameserver", Params: actions.P(actions.DNSServer),
			Run: func(c L3Capable, a types.ActionEntry) ([][]string, error) {
				return c.RemoveDNSNameserver(a.DNSServer), nil
			}},
		actions.Action[L3Capable]{Type: "add_dns_search", Label: "Add DNS search domain", Params: actions.P(actions.DNSDomain),
			Run: func(c L3Capable, a types.ActionEntry) ([][]string, error) { return c.AddDNSSearch(a.DNSDomain), nil }},
		actions.Action[L3Capable]{Type: "remove_dns_search", Label: "Remove DNS search domain", Params: actions.P(actions.DNSDomain),
			Run: func(c L3Capable, a types.ActionEntry) ([][]string, error) { return c.RemoveDNSSearch(a.DNSDomain), nil }},
	)
}
