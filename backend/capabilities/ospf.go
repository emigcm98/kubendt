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
	OSPFSetCost(iface string, cost int) [][]string
	OSPFRemoveCost(iface string) [][]string
	OSPFSetPriority(iface string, priority int) [][]string
	OSPFRemovePriority(iface string) [][]string
	OSPFSetTimers(iface string, hello, dead int) [][]string
	OSPFRemoveTimers(iface string) [][]string
	OSPFSetNetworkType(iface, networkType string) [][]string
	OSPFRemoveNetworkType(iface string) [][]string
	OSPFSetAreaType(area, areaType string) [][]string
	OSPFRemoveAreaType(area, areaType string) [][]string
	OSPFAddAreaRange(area, cidr string) [][]string
	OSPFRemoveAreaRange(area, cidr string) [][]string
	OSPFSetAuthentication(iface, authType, key string, keyID int) [][]string
	OSPFRemoveAuthentication(iface, authType string, keyID int) [][]string
}

// OSPFAreaTypes are the area types an ABR and its area's routers may set,
// by their Cisco names: totally-stub is stub with no-summary on the ABR.
var OSPFAreaTypes = []string{"stub", "totally-stub", "nssa", "totally-nssa"}

// OSPFAuthTypes are the interface authentication modes: a plain key of up
// to 8 characters, or MD5 with a key id and a key of up to 16.
var OSPFAuthTypes = []string{"plain", "md5"}

func ospfOneOf(a types.ActionEntry, field, value string, allowed []string) error {
	for _, s := range allowed {
		if value == s {
			return nil
		}
	}
	return fmt.Errorf("%s: %s must be one of %v, got %q", a.Type, field, allowed, value)
}

// ospfAuth checks the authentication fields and fills the key id default.
func ospfAuth(a types.ActionEntry, withKey bool) (keyID int, err error) {
	if err := ospfOneOf(a, "auth_type", a.AuthType, OSPFAuthTypes); err != nil {
		return 0, err
	}
	keyID = a.AuthKeyID
	if keyID == 0 {
		keyID = 1
	}
	if err := ospfRange(a.Type, "auth_key_id", keyID, 1, 255); err != nil {
		return 0, err
	}
	if !withKey {
		return keyID, nil
	}
	limit := 8
	if a.AuthType == "md5" {
		limit = 16
	}
	if len(a.AuthKey) > limit {
		return 0, fmt.Errorf("%s: auth_key must be at most %d characters for %s, got %d", a.Type, limit, a.AuthType, len(a.AuthKey))
	}
	return keyID, nil
}

// OSPFNetworkTypes are the interface network types OSPF accepts; point-to-point
// on a /30 skips the DR election, as a serial line would.
var OSPFNetworkTypes = []string{"broadcast", "non-broadcast", "point-to-multipoint", "point-to-point"}

func ospfRange(action, field string, n, lo, hi int) error {
	if n < lo || n > hi {
		return fmt.Errorf("%s: %s must be between %d and %d, got %d", action, field, lo, hi, n)
	}
	return nil
}

func ospfNetworkType(a types.ActionEntry) (string, error) {
	for _, t := range OSPFNetworkTypes {
		if a.NetworkType == t {
			return t, nil
		}
	}
	return "", fmt.Errorf("%s: network_type must be one of %v, got %q", a.Type, OSPFNetworkTypes, a.NetworkType)
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
		actions.Action[OSPFCapable]{Type: "ospf_set_cost", Label: "Set interface cost", Params: actions.P(actions.Iface, actions.Cost), State: "ospf_cost", Key: keyIface,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				if err := ospfRange(a.Type, "cost", a.Cost, 1, 65535); err != nil {
					return nil, err
				}
				return c.OSPFSetCost(a.Iface, a.Cost), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_remove_cost", Label: "Reset interface cost", Params: actions.P(actions.Iface), State: "ospf_cost", Key: keyIface, Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) { return c.OSPFRemoveCost(a.Iface), nil }},
		actions.Action[OSPFCapable]{Type: "ospf_set_priority", Label: "Set interface priority", Params: actions.P(actions.Iface, actions.Priority), State: "ospf_priority", Key: keyIface,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				if err := ospfRange(a.Type, "priority", *a.Priority, 0, 255); err != nil {
					return nil, err
				}
				return c.OSPFSetPriority(a.Iface, *a.Priority), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_remove_priority", Label: "Reset interface priority", Params: actions.P(actions.Iface), State: "ospf_priority", Key: keyIface, Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				return c.OSPFRemovePriority(a.Iface), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_set_timers", Label: "Set hello and dead intervals", Params: actions.P(actions.Iface, actions.HelloInterval, actions.DeadInterval), State: "ospf_timers", Key: keyIface,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				if err := ospfRange(a.Type, "hello_interval", a.HelloInterval, 1, 65535); err != nil {
					return nil, err
				}
				if err := ospfRange(a.Type, "dead_interval", a.DeadInterval, 1, 65535); err != nil {
					return nil, err
				}
				if a.DeadInterval <= a.HelloInterval {
					return nil, fmt.Errorf("%s: dead_interval must be longer than hello_interval, got %d and %d", a.Type, a.DeadInterval, a.HelloInterval)
				}
				return c.OSPFSetTimers(a.Iface, a.HelloInterval, a.DeadInterval), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_remove_timers", Label: "Reset hello and dead intervals", Params: actions.P(actions.Iface), State: "ospf_timers", Key: keyIface, Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) { return c.OSPFRemoveTimers(a.Iface), nil }},
		actions.Action[OSPFCapable]{Type: "ospf_set_network_type", Label: "Set interface network type", Params: actions.P(actions.Iface, actions.NetworkType), State: "ospf_network_type", Key: keyIface,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				t, err := ospfNetworkType(a)
				if err != nil {
					return nil, err
				}
				return c.OSPFSetNetworkType(a.Iface, t), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_remove_network_type", Label: "Reset interface network type", Params: actions.P(actions.Iface), State: "ospf_network_type", Key: keyIface, Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				return c.OSPFRemoveNetworkType(a.Iface), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_set_area_type", Label: "Set area type", Params: actions.P(actions.OSPFArea, actions.AreaType), State: "ospf_area_type", Key: keyArea,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				if err := ospfOneOf(a, "area_type", a.AreaType, OSPFAreaTypes); err != nil {
					return nil, err
				}
				return c.OSPFSetAreaType(a.OSPFArea, a.AreaType), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_remove_area_type", Label: "Make area normal again", Params: actions.P(actions.OSPFArea, actions.AreaType), State: "ospf_area_type", Key: keyArea, Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				if err := ospfOneOf(a, "area_type", a.AreaType, OSPFAreaTypes); err != nil {
					return nil, err
				}
				return c.OSPFRemoveAreaType(a.OSPFArea, a.AreaType), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_add_area_range", Label: "Summarize area", Params: actions.P(actions.OSPFArea, actions.CIDR), State: "ospf_area_range", Key: keyNetworkArea,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				return c.OSPFAddAreaRange(a.OSPFArea, a.CIDR), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_remove_area_range", Label: "Stop summarizing area", Params: actions.P(actions.OSPFArea, actions.CIDR), State: "ospf_area_range", Key: keyNetworkArea, Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				return c.OSPFRemoveAreaRange(a.OSPFArea, a.CIDR), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_set_authentication", Label: "Authenticate interface", Params: actions.P(actions.Iface, actions.AuthType, actions.AuthKey, actions.AuthKeyID), State: "ospf_auth", Key: keyIface,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				keyID, err := ospfAuth(a, true)
				if err != nil {
					return nil, err
				}
				return c.OSPFSetAuthentication(a.Iface, a.AuthType, a.AuthKey, keyID), nil
			}},
		actions.Action[OSPFCapable]{Type: "ospf_remove_authentication", Label: "Stop authenticating interface", Params: actions.P(actions.Iface, actions.AuthType, actions.AuthKeyID), State: "ospf_auth", Key: keyIface, Clears: true,
			Run: func(c OSPFCapable, a types.ActionEntry) ([][]string, error) {
				keyID, err := ospfAuth(a, false)
				if err != nil {
					return nil, err
				}
				return c.OSPFRemoveAuthentication(a.Iface, a.AuthType, keyID), nil
			}},
	)
}
