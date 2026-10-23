package capabilities

import (
	"fmt"

	"kubendt/actions"
	"kubendt/types"
)

// OSPF6Capable is OSPFv3, its own capability because the protocol is
// configured per interface (an interface joins an area) rather than with
// network statements, and FRR runs it in another daemon.
type OSPF6Capable interface {
	OSPF6SetRouterID(routerID string) [][]string
	OSPF6RemoveRouterID() [][]string
	OSPF6AddInterface(iface, area string) [][]string
	OSPF6RemoveInterface(iface, area string) [][]string
	OSPF6PassiveInterface(iface string) [][]string
	OSPF6RemovePassiveInterface(iface string) [][]string
	OSPF6Redistribute(source string) [][]string
	OSPF6RemoveRedistribute(source string) [][]string
	OSPF6OriginateDefault() [][]string
	OSPF6RemoveOriginateDefault() [][]string
}

// OSPF6Sources are the route sources an OSPFv3 process may redistribute.
var OSPF6Sources = []string{"connected", "static", "ripng", "bgp", "kernel"}

func ospf6Source(a types.ActionEntry) (string, error) {
	for _, s := range OSPF6Sources {
		if a.Redistribute == s {
			return s, nil
		}
	}
	return "", fmt.Errorf("%s: redistribute must be one of %v, got %q", a.Type, OSPF6Sources, a.Redistribute)
}

func init() {
	actions.Register[OSPF6Capable](
		actions.Capability{ID: "OSPF6Capable", Label: "OSPFv3 Routing", Description: "OSPF for IPv6, configured per interface."},
		actions.Action[OSPF6Capable]{Type: "ospf6_set_router_id", Label: "Set router ID", Params: actions.P(actions.RouterID), State: "ospf6_router_id",
			Run: func(c OSPF6Capable, a types.ActionEntry) ([][]string, error) {
				return c.OSPF6SetRouterID(a.RouterID), nil
			}},
		actions.Action[OSPF6Capable]{Type: "ospf6_remove_router_id", Label: "Remove router ID", State: "ospf6_router_id", Clears: true,
			Run: func(c OSPF6Capable, a types.ActionEntry) ([][]string, error) { return c.OSPF6RemoveRouterID(), nil }},
		actions.Action[OSPF6Capable]{Type: "ospf6_add_interface", Label: "Run OSPFv3 on interface", Params: actions.P(actions.Iface, actions.OSPFArea), State: "ospf6_interface", Key: keyIface,
			Run: func(c OSPF6Capable, a types.ActionEntry) ([][]string, error) {
				return c.OSPF6AddInterface(a.Iface, a.OSPFArea), nil
			}},
		actions.Action[OSPF6Capable]{Type: "ospf6_remove_interface", Label: "Stop OSPFv3 on interface", Params: actions.P(actions.Iface, actions.OSPFArea), State: "ospf6_interface", Key: keyIface, Clears: true,
			Run: func(c OSPF6Capable, a types.ActionEntry) ([][]string, error) {
				return c.OSPF6RemoveInterface(a.Iface, a.OSPFArea), nil
			}},
		actions.Action[OSPF6Capable]{Type: "ospf6_passive_interface", Label: "Make interface passive", Params: actions.P(actions.Iface), State: "ospf6_passive", Key: keyIface,
			Run: func(c OSPF6Capable, a types.ActionEntry) ([][]string, error) {
				return c.OSPF6PassiveInterface(a.Iface), nil
			}},
		actions.Action[OSPF6Capable]{Type: "ospf6_remove_passive_interface", Label: "Make interface active", Params: actions.P(actions.Iface), State: "ospf6_passive", Key: keyIface, Clears: true,
			Run: func(c OSPF6Capable, a types.ActionEntry) ([][]string, error) {
				return c.OSPF6RemovePassiveInterface(a.Iface), nil
			}},
		actions.Action[OSPF6Capable]{Type: "ospf6_redistribute", Label: "Redistribute routes", Params: actions.P(actions.Redistribute), State: "ospf6_redistribute", Key: keyRedistribute,
			Run: func(c OSPF6Capable, a types.ActionEntry) ([][]string, error) {
				s, err := ospf6Source(a)
				if err != nil {
					return nil, err
				}
				return c.OSPF6Redistribute(s), nil
			}},
		actions.Action[OSPF6Capable]{Type: "ospf6_remove_redistribute", Label: "Stop redistributing routes", Params: actions.P(actions.Redistribute), State: "ospf6_redistribute", Key: keyRedistribute, Clears: true,
			Run: func(c OSPF6Capable, a types.ActionEntry) ([][]string, error) {
				s, err := ospf6Source(a)
				if err != nil {
					return nil, err
				}
				return c.OSPF6RemoveRedistribute(s), nil
			}},
		actions.Action[OSPF6Capable]{Type: "ospf6_originate_default", Label: "Originate default route", State: "ospf6_originate_default",
			Run: func(c OSPF6Capable, a types.ActionEntry) ([][]string, error) { return c.OSPF6OriginateDefault(), nil }},
		actions.Action[OSPF6Capable]{Type: "ospf6_remove_originate_default", Label: "Stop originating default route", State: "ospf6_originate_default", Clears: true,
			Run: func(c OSPF6Capable, a types.ActionEntry) ([][]string, error) {
				return c.OSPF6RemoveOriginateDefault(), nil
			}},
	)
}
