package capabilities

import (
	"fmt"

	"kubendt/actions"
	"kubendt/types"
)

type OSPFCapable interface {
	OSPFAddNetwork(network, area string) [][]string
	OSPFRemoveNetwork(network, area string) [][]string
	OSPFSetRouterID(routerID string) [][]string
	OSPFRemoveRouterID() [][]string
	OSPFPassiveDefault() [][]string
	OSPFRemovePassiveDefault() [][]string
	OSPFNoPassive(iface string) [][]string
	OSPFRemoveNoPassive(iface string) [][]string
	OSPFOriginateDefault() [][]string
	OSPFRemoveOriginateDefault() [][]string
	OSPFMTUIgnore(iface string) [][]string
	OSPFRemoveMTUIgnore(iface string) [][]string
	OSPFRedistribute(source string) [][]string
	OSPFRemoveRedistribute(source string) [][]string
}

// OSPFSources are the route sources an OSPF process may redistribute.
var OSPFSources = []string{"connected", "static", "rip", "bgp", "kernel"}

func ospfSource(a types.ActionEntry) (string, error) {
	for _, s := range OSPFSources {
		if a.Redistribute == s {
			return s, nil
		}
	}
	return "", fmt.Errorf("%s: redistribute must be one of %v, got %q", a.Type, OSPFSources, a.Redistribute)
}

func init() {
	actions.Register[OSPFCapable](
		actions.Capability{ID: "OSPFCapable", Label: "OSPF Routing", Description: "Management of OSPF routing protocol configuration."},
		actions.Action[OSPFCapable]{Type: "ospf_add_network", Label: "Advertise network", Params: actions.P(actions.CIDR, actions.OSPFArea), State: "ospf_network", Key: keyNetworkArea,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				return c.OSPFAddNetwork(a.CIDR, a.OSPFArea), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_remove_network", Label: "Stop advertising network", Params: actions.P(actions.CIDR, actions.OSPFArea), State: "ospf_network", Key: keyNetworkArea, Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				return c.OSPFRemoveNetwork(a.CIDR, a.OSPFArea), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_set_router_id", Label: "Set router ID", Params: actions.P(actions.RouterID), State: "ospf_router_id",
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				return c.OSPFSetRouterID(a.RouterID), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_remove_router_id", Label: "Remove router ID", State: "ospf_router_id", Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) { return c.OSPFRemoveRouterID(), nil }},
		actions.Action[OSPFCapable]{Type: "ospf_passive_default", Label: "Passive interfaces by default", State: "ospf_passive_default",
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) { return c.OSPFPassiveDefault(), nil }},
		actions.Action[OSPFCapable]{Type: "ospf_remove_passive_default", Label: "Active interfaces by default", State: "ospf_passive_default", Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) { return c.OSPFRemovePassiveDefault(), nil }},
		actions.Action[OSPFCapable]{Type: "ospf_no_passive", Label: "Make interface active", Params: actions.P(actions.Iface), State: "ospf_no_passive", Key: keyIface,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) { return c.OSPFNoPassive(a.Iface), nil }},
		actions.Action[OSPFCapable]{Type: "ospf_remove_no_passive", Label: "Make interface passive", Params: actions.P(actions.Iface), State: "ospf_no_passive", Key: keyIface, Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				return c.OSPFRemoveNoPassive(a.Iface), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_originate_default", Label: "Originate default route", State: "ospf_originate_default",
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) { return c.OSPFOriginateDefault(), nil }},
		actions.Action[OSPFCapable]{Type: "ospf_remove_originate_default", Label: "Stop originating default route", State: "ospf_originate_default", Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				return c.OSPFRemoveOriginateDefault(), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_mtu_ignore", Label: "Ignore MTU mismatch", Params: actions.P(actions.Iface), State: "ospf_mtu_ignore", Key: keyIface,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) { return c.OSPFMTUIgnore(a.Iface), nil }},
		actions.Action[OSPFCapable]{Type: "ospf_remove_mtu_ignore", Label: "Check MTU again", Params: actions.P(actions.Iface), State: "ospf_mtu_ignore", Key: keyIface, Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				return c.OSPFRemoveMTUIgnore(a.Iface), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_redistribute", Label: "Redistribute routes", Params: actions.P(actions.Redistribute), State: "ospf_redistribute", Key: keyRedistribute,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				s, err := ospfSource(a)
				if err != nil {
					return nil, err
				}
				return c.OSPFRedistribute(s), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_remove_redistribute", Label: "Stop redistributing routes", Params: actions.P(actions.Redistribute), State: "ospf_redistribute", Key: keyRedistribute, Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				s, err := ospfSource(a)
				if err != nil {
					return nil, err
				}
				return c.OSPFRemoveRedistribute(s), nil
			}},
	)
}
