package capabilities

import (
	"fmt"
	"regexp"

	"kubendt/actions"
	"kubendt/types"
)

// ISISCapable is one IS-IS process per router, the interfaces that join it
// and what it redistributes. IS-IS carries IPv4 and IPv6 in one protocol,
// so an interface joins for both families unless `family` says one, and
// redistribute takes `family` (4 by default) and the level routes go into.
type ISISCapable interface {
	ISISSetRouter(net, level string) [][]string
	ISISRemoveRouter() [][]string
	ISISAddInterface(iface, level string, v4, v6 bool) [][]string
	ISISRemoveInterface(iface string) [][]string
	ISISPassiveInterface(iface string) [][]string
	ISISRemovePassiveInterface(iface string) [][]string
	ISISSetMetric(iface string, metric int) [][]string
	ISISRemoveMetric(iface string) [][]string
	ISISSetNetworkType(iface, networkType string) [][]string
	ISISRemoveNetworkType(iface string) [][]string
	ISISRedistribute(source, level string, v6 bool) [][]string
	ISISRemoveRedistribute(source, level string, v6 bool) [][]string
}

// ISISSources are the route sources an IS-IS process may redistribute; the
// driver names ospf and rip by family on the v6 side.
var ISISSources = []string{"connected", "static", "ospf", "rip", "bgp", "kernel"}

// ISISLevels are the process and circuit types; a redistribution goes into
// one level only.
var ISISLevels = []string{"level-1", "level-2", "level-1-2"}

var ISISNetworkTypes = []string{"broadcast", "point-to-point"}

// ISISLSPMTU is the LSP size every KubeNDT router uses, 3 bytes under the
// 1280 of the smallest link the platform allows, so LSPs cross any link
// and routers of different drivers agree on it.
const ISISLSPMTU = 1277

// A NET is an AFI, an area of whole 16-bit groups, a six-byte system id and
// the 00 selector, 8 to 20 bytes in all.
var isisNETRe = regexp.MustCompile(`^[0-9a-fA-F]{2}(\.[0-9a-fA-F]{4}){3,9}\.00$`)

func isisNET(a types.ActionEntry) (string, error) {
	if !isisNETRe.MatchString(a.NET) {
		return "", fmt.Errorf("%s: net must be an IS-IS NET such as 49.0001.0000.0000.0001.00, got %q", a.Type, a.NET)
	}
	return a.NET, nil
}

func isisLevel(a types.ActionEntry, allowed []string) (string, error) {
	if a.Level == "" {
		return "", nil
	}
	return isisOneOf(a, "level", a.Level, allowed)
}

// isisRedistributeLevel is the level a redistribution goes into, level-2
// unless the action says level-1; the key of the state uses it too.
func isisRedistributeLevel(a types.ActionEntry) string {
	if a.Level == "level-1" {
		return "level-1"
	}
	return "level-2"
}

func isisSource(a types.ActionEntry) (string, error) {
	return isisOneOf(a, "redistribute", a.Redistribute, ISISSources)
}

func isisOneOf(a types.ActionEntry, field, value string, allowed []string) (string, error) {
	for _, s := range allowed {
		if value == s {
			return s, nil
		}
	}
	return "", fmt.Errorf("%s: %s must be one of %v, got %q", a.Type, field, allowed, value)
}

func init() {
	actions.Register[ISISCapable](
		actions.Capability{ID: "ISISCapable", Label: "IS-IS Routing", Description: "One IS-IS process per router: its NET and level, the interfaces that join it for IPv4, IPv6 or both, and what it redistributes."},
		actions.Action[ISISCapable]{Type: "isis_set_router", Label: "Run IS-IS", Params: actions.P(actions.NET, actions.Level), State: "isis_router",
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) {
				net, err := isisNET(a)
				if err != nil {
					return nil, err
				}
				level, err := isisLevel(a, ISISLevels)
				if err != nil {
					return nil, err
				}
				return c.ISISSetRouter(net, level), nil
			}},
		actions.Action[ISISCapable]{Type: "isis_remove_router", Label: "Stop IS-IS", State: "isis_router", Clears: true, ClearsAlso: []string{"isis_interface", "isis_passive", "isis_metric", "isis_network_type", "isis_redistribute"},
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) { return c.ISISRemoveRouter(), nil }},
		actions.Action[ISISCapable]{Type: "isis_add_interface", Label: "Add interface", Params: actions.P(actions.Iface, actions.Level, actions.Family), State: "isis_interface", Key: keyIface,
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) {
				level, err := isisLevel(a, ISISLevels)
				if err != nil {
					return nil, err
				}
				return c.ISISAddInterface(a.Iface, level, a.Family != "6", a.Family != "4"), nil
			}},
		actions.Action[ISISCapable]{Type: "isis_remove_interface", Label: "Remove interface", Params: actions.P(actions.Iface), State: "isis_interface", Key: keyIface, Clears: true, ClearsAlso: []string{"isis_passive", "isis_metric", "isis_network_type"},
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) {
				return c.ISISRemoveInterface(a.Iface), nil
			}},
		actions.Action[ISISCapable]{Type: "isis_passive_interface", Label: "Make interface passive", Params: actions.P(actions.Iface), State: "isis_passive", Key: keyIface,
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) {
				return c.ISISPassiveInterface(a.Iface), nil
			}},
		actions.Action[ISISCapable]{Type: "isis_remove_passive_interface", Label: "Make interface active", Params: actions.P(actions.Iface), State: "isis_passive", Key: keyIface, Clears: true,
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) {
				return c.ISISRemovePassiveInterface(a.Iface), nil
			}},
		actions.Action[ISISCapable]{Type: "isis_set_metric", Label: "Set interface metric", Params: actions.P(actions.Iface, actions.Cost), State: "isis_metric", Key: keyIface,
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) {
				if a.Cost < 1 || a.Cost > 16777215 {
					return nil, fmt.Errorf("%s: cost must be between 1 and 16777215, got %d", a.Type, a.Cost)
				}
				return c.ISISSetMetric(a.Iface, a.Cost), nil
			}},
		actions.Action[ISISCapable]{Type: "isis_remove_metric", Label: "Default interface metric", Params: actions.P(actions.Iface), State: "isis_metric", Key: keyIface, Clears: true,
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) {
				return c.ISISRemoveMetric(a.Iface), nil
			}},
		actions.Action[ISISCapable]{Type: "isis_set_network_type", Label: "Set interface network type", Params: actions.P(actions.Iface, actions.NetworkType), State: "isis_network_type", Key: keyIface,
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) {
				t, err := isisOneOf(a, "network_type", a.NetworkType, ISISNetworkTypes)
				if err != nil {
					return nil, err
				}
				return c.ISISSetNetworkType(a.Iface, t), nil
			}},
		actions.Action[ISISCapable]{Type: "isis_remove_network_type", Label: "Default interface network type", Params: actions.P(actions.Iface), State: "isis_network_type", Key: keyIface, Clears: true,
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) {
				return c.ISISRemoveNetworkType(a.Iface), nil
			}},
		actions.Action[ISISCapable]{Type: "isis_redistribute", Label: "Redistribute routes", Params: actions.P(actions.Redistribute, actions.Family, actions.Level), State: "isis_redistribute", Key: keyRedistributeFamilyLevel,
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) {
				s, err := isisSource(a)
				if err != nil {
					return nil, err
				}
				if _, err := isisLevel(a, []string{"level-1", "level-2"}); err != nil {
					return nil, err
				}
				return c.ISISRedistribute(s, isisRedistributeLevel(a), a.Family == "6"), nil
			}},
		actions.Action[ISISCapable]{Type: "isis_remove_redistribute", Label: "Stop redistributing routes", Params: actions.P(actions.Redistribute, actions.Family, actions.Level), State: "isis_redistribute", Key: keyRedistributeFamilyLevel, Clears: true,
			Run: func(c ISISCapable, a types.ActionEntry) ([][]string, error) {
				s, err := isisSource(a)
				if err != nil {
					return nil, err
				}
				if _, err := isisLevel(a, []string{"level-1", "level-2"}); err != nil {
					return nil, err
				}
				return c.ISISRemoveRedistribute(s, isisRedistributeLevel(a), a.Family == "6"), nil
			}},
	)
}
