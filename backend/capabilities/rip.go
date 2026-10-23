package capabilities

import (
	"fmt"

	"kubendt/actions"
	"kubendt/types"
)

// RIPCapable covers RIPv2 and RIPng with one set of actions: a network's
// family comes from its prefix, the rest take `family` (4 by default), since
// both protocols are the same statements under another router block.
type RIPCapable interface {
	RIPAddNetwork(network string, v6 bool) [][]string
	RIPRemoveNetwork(network string, v6 bool) [][]string
	RIPPassiveInterface(iface string, v6 bool) [][]string
	RIPRemovePassiveInterface(iface string, v6 bool) [][]string
	RIPRedistribute(source string, v6 bool) [][]string
	RIPRemoveRedistribute(source string, v6 bool) [][]string
	RIPSetTimers(update, timeout, garbage int, v6 bool) [][]string
	RIPRemoveTimers(v6 bool) [][]string
}

// RIPSources are the route sources a RIP process may redistribute; the
// driver names ospf by family (ospf6 or ospfv3 on the RIPng side).
var RIPSources = []string{"connected", "static", "ospf", "bgp", "kernel"}

func ripSource(a types.ActionEntry) (string, error) {
	for _, s := range RIPSources {
		if a.Redistribute == s {
			return s, nil
		}
	}
	return "", fmt.Errorf("%s: redistribute must be one of %v, got %q", a.Type, RIPSources, a.Redistribute)
}

// Timers are bounded by the stricter of FRR (1 to 65535) and VyOS (5 up).
func ripTimers(a types.ActionEntry) (*types.RIPTimers, error) {
	t := a.Timers
	if t == nil {
		return nil, fmt.Errorf("%s: missing required field \"timers\"", a.Type)
	}
	for name, v := range map[string]int{"update": t.Update, "timeout": t.Timeout, "garbage": t.Garbage} {
		if v < 5 || v > 65535 {
			return nil, fmt.Errorf("%s: timers.%s must be between 5 and 65535 seconds, got %d", a.Type, name, v)
		}
	}
	return t, nil
}

func init() {
	actions.Register[RIPCapable](
		actions.Capability{ID: "RIPCapable", Label: "RIP Routing", Description: "RIPv2 and RIPng, the family picked by the prefix or the family field."},
		actions.Action[RIPCapable]{Type: "rip_add_network", Label: "Advertise network", Params: actions.P(actions.CIDR), State: "rip_network", Key: keyCIDR,
			Run: func(c RIPCapable, a types.ActionEntry) ([][]string, error) {
				return c.RIPAddNetwork(a.CIDR, IsIPv6(a.CIDR)), nil
			}},
		actions.Action[RIPCapable]{Type: "rip_remove_network", Label: "Stop advertising network", Params: actions.P(actions.CIDR), State: "rip_network", Key: keyCIDR, Clears: true,
			Run: func(c RIPCapable, a types.ActionEntry) ([][]string, error) {
				return c.RIPRemoveNetwork(a.CIDR, IsIPv6(a.CIDR)), nil
			}},
		actions.Action[RIPCapable]{Type: "rip_passive_interface", Label: "Make interface passive", Params: actions.P(actions.Iface, actions.Family), State: "rip_passive", Key: keyIfaceFamily,
			Run: func(c RIPCapable, a types.ActionEntry) ([][]string, error) {
				return c.RIPPassiveInterface(a.Iface, a.Family == "6"), nil
			}},
		actions.Action[RIPCapable]{Type: "rip_remove_passive_interface", Label: "Make interface active", Params: actions.P(actions.Iface, actions.Family), State: "rip_passive", Key: keyIfaceFamily, Clears: true,
			Run: func(c RIPCapable, a types.ActionEntry) ([][]string, error) {
				return c.RIPRemovePassiveInterface(a.Iface, a.Family == "6"), nil
			}},
		actions.Action[RIPCapable]{Type: "rip_redistribute", Label: "Redistribute routes", Params: actions.P(actions.Redistribute, actions.Family), State: "rip_redistribute", Key: keyRedistributeFamily,
			Run: func(c RIPCapable, a types.ActionEntry) ([][]string, error) {
				s, err := ripSource(a)
				if err != nil {
					return nil, err
				}
				return c.RIPRedistribute(s, a.Family == "6"), nil
			}},
		actions.Action[RIPCapable]{Type: "rip_remove_redistribute", Label: "Stop redistributing routes", Params: actions.P(actions.Redistribute, actions.Family), State: "rip_redistribute", Key: keyRedistributeFamily, Clears: true,
			Run: func(c RIPCapable, a types.ActionEntry) ([][]string, error) {
				s, err := ripSource(a)
				if err != nil {
					return nil, err
				}
				return c.RIPRemoveRedistribute(s, a.Family == "6"), nil
			}},
		actions.Action[RIPCapable]{Type: "rip_set_timers", Label: "Set timers", Params: actions.P(actions.Timers, actions.Family), State: "rip_timers", Key: keyFamily,
			Run: func(c RIPCapable, a types.ActionEntry) ([][]string, error) {
				t, err := ripTimers(a)
				if err != nil {
					return nil, err
				}
				return c.RIPSetTimers(t.Update, t.Timeout, t.Garbage, a.Family == "6"), nil
			}},
		actions.Action[RIPCapable]{Type: "rip_remove_timers", Label: "Default timers", Params: actions.P(actions.Family), State: "rip_timers", Key: keyFamily, Clears: true,
			Run: func(c RIPCapable, a types.ActionEntry) ([][]string, error) {
				return c.RIPRemoveTimers(a.Family == "6"), nil
			}},
	)
}
