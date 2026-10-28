package capabilities

import (
	"fmt"

	"kubendt/actions"
	"kubendt/types"
)

// BGPCapable is one BGP process per router, its neighbours and what it
// announces. A neighbour or a prefix picks its address family by its
// address, redistribute takes `family` (4 by default). Only bgp_set_router
// carries the AS; the rest address the one process on the node.
type BGPCapable interface {
	BGPSetRouter(as int, routerID string) [][]string
	BGPRemoveRouter() [][]string
	BGPAddNeighbor(neighbor string, remoteAS int, updateSource string, multihop int, v6 bool) [][]string
	BGPRemoveNeighbor(neighbor string) [][]string
	BGPAddNetwork(network string, v6 bool) [][]string
	BGPRemoveNetwork(network string, v6 bool) [][]string
	BGPRedistribute(source string, v6 bool) [][]string
	BGPRemoveRedistribute(source string, v6 bool) [][]string
}

// BGPSources are the route sources a BGP process may redistribute; the
// driver names ospf and rip by family on the v6 side.
var BGPSources = []string{"connected", "static", "ospf", "rip", "isis", "kernel"}

const maxAS int64 = 1<<32 - 1

func bgpAS(a types.ActionEntry, field string, n int) error {
	if int64(n) < 1 || int64(n) > maxAS {
		return fmt.Errorf("%s: %s must be between 1 and %d, got %d", a.Type, field, maxAS, n)
	}
	return nil
}

func bgpSource(a types.ActionEntry) (string, error) {
	for _, s := range BGPSources {
		if a.Redistribute == s {
			return s, nil
		}
	}
	return "", fmt.Errorf("%s: redistribute must be one of %v, got %q", a.Type, BGPSources, a.Redistribute)
}

func init() {
	actions.Register[BGPCapable](
		actions.Capability{ID: "BGPCapable", Label: "BGP Routing", Description: "One BGP process per router: its AS, its neighbours and the prefixes it announces, IPv4 and IPv6 by the address."},
		actions.Action[BGPCapable]{Type: "bgp_set_router", Label: "Run BGP in an AS", Params: actions.P(actions.AS, actions.RouterID.Optional()), State: "bgp_router",
			Run: func(c BGPCapable, a types.ActionEntry) ([][]string, error) {
				if err := bgpAS(a, "as", a.AS); err != nil {
					return nil, err
				}
				return c.BGPSetRouter(a.AS, a.RouterID), nil
			}},
		actions.Action[BGPCapable]{Type: "bgp_remove_router", Label: "Stop BGP", State: "bgp_router", Clears: true, ClearsAlso: []string{"bgp_neighbor", "bgp_network", "bgp_redistribute"},
			Run: func(c BGPCapable, a types.ActionEntry) ([][]string, error) { return c.BGPRemoveRouter(), nil }},
		actions.Action[BGPCapable]{Type: "bgp_add_neighbor", Label: "Add neighbor", Params: actions.P(actions.Neighbor, actions.RemoteAS, actions.UpdateSource, actions.Multihop), State: "bgp_neighbor", Key: keyNeighbor,
			Run: func(c BGPCapable, a types.ActionEntry) ([][]string, error) {
				if err := bgpAS(a, "remote_as", a.RemoteAS); err != nil {
					return nil, err
				}
				if a.Multihop < 0 || a.Multihop > 255 {
					return nil, fmt.Errorf("%s: multihop must be between 1 and 255, got %d", a.Type, a.Multihop)
				}
				return c.BGPAddNeighbor(a.Neighbor, a.RemoteAS, a.UpdateSource, a.Multihop, IsIPv6(a.Neighbor)), nil
			}},
		actions.Action[BGPCapable]{Type: "bgp_remove_neighbor", Label: "Remove neighbor", Params: actions.P(actions.Neighbor), State: "bgp_neighbor", Key: keyNeighbor, Clears: true,
			Run: func(c BGPCapable, a types.ActionEntry) ([][]string, error) {
				return c.BGPRemoveNeighbor(a.Neighbor), nil
			}},
		actions.Action[BGPCapable]{Type: "bgp_add_network", Label: "Announce network", Params: actions.P(actions.CIDR), State: "bgp_network", Key: keyCIDR,
			Run: func(c BGPCapable, a types.ActionEntry) ([][]string, error) {
				return c.BGPAddNetwork(a.CIDR, IsIPv6(a.CIDR)), nil
			}},
		actions.Action[BGPCapable]{Type: "bgp_remove_network", Label: "Stop announcing network", Params: actions.P(actions.CIDR), State: "bgp_network", Key: keyCIDR, Clears: true,
			Run: func(c BGPCapable, a types.ActionEntry) ([][]string, error) {
				return c.BGPRemoveNetwork(a.CIDR, IsIPv6(a.CIDR)), nil
			}},
		actions.Action[BGPCapable]{Type: "bgp_redistribute", Label: "Redistribute routes", Params: actions.P(actions.Redistribute, actions.Family), State: "bgp_redistribute", Key: keyRedistributeFamily,
			Run: func(c BGPCapable, a types.ActionEntry) ([][]string, error) {
				s, err := bgpSource(a)
				if err != nil {
					return nil, err
				}
				return c.BGPRedistribute(s, a.Family == "6"), nil
			}},
		actions.Action[BGPCapable]{Type: "bgp_remove_redistribute", Label: "Stop redistributing routes", Params: actions.P(actions.Redistribute, actions.Family), State: "bgp_redistribute", Key: keyRedistributeFamily, Clears: true,
			Run: func(c BGPCapable, a types.ActionEntry) ([][]string, error) {
				s, err := bgpSource(a)
				if err != nil {
					return nil, err
				}
				return c.BGPRemoveRedistribute(s, a.Family == "6"), nil
			}},
	)
}
