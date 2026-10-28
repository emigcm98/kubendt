package capabilities

import (
	"fmt"
	"net"

	"kubendt/actions"
	"kubendt/types"
)

// NAT64Capable is one translator per router: IPv6 hosts reach IPv4 hosts
// through a prefix that embeds the IPv4 address, and the router maps them
// onto an IPv4 pool it owns, which the IPv4 side routes back to it.
type NAT64Capable interface {
	NAT64Enable(prefix, pool string) [][]string
	NAT64Disable() [][]string
}

// NAT64WellKnownPrefix is RFC 6052's, the one every NAT64 defaults to.
const NAT64WellKnownPrefix = "64:ff9b::/96"

// nat64Prefix is the translation prefix, a /96 so the IPv4 address fills the
// low 32 bits.
func nat64Prefix(a types.ActionEntry) (string, error) {
	if a.Prefix == "" {
		return NAT64WellKnownPrefix, nil
	}
	ip, n, err := net.ParseCIDR(a.Prefix)
	if err != nil || ip.To4() != nil {
		return "", fmt.Errorf("%s: prefix must be an IPv6 prefix, got %q", a.Type, a.Prefix)
	}
	if ones, _ := n.Mask.Size(); ones != 96 {
		return "", fmt.Errorf("%s: prefix must be a /96, got %q", a.Type, a.Prefix)
	}
	return n.String(), nil
}

// nat64Pool is the IPv4 pool, a /30 at least so the translator keeps one
// address for itself and maps at least one host.
func nat64Pool(a types.ActionEntry) (string, error) {
	ip, n, err := net.ParseCIDR(a.Pool)
	if err != nil || ip.To4() == nil {
		return "", fmt.Errorf("%s: pool must be an IPv4 prefix, got %q", a.Type, a.Pool)
	}
	if ones, _ := n.Mask.Size(); ones > 30 {
		return "", fmt.Errorf("%s: pool must be a /30 or larger, got %q", a.Type, a.Pool)
	}
	return n.String(), nil
}

func init() {
	actions.Register[NAT64Capable](
		actions.Capability{ID: "NAT64Capable", Label: "NAT64", Description: "IPv6-only hosts reaching IPv4 through a translation prefix and an IPv4 pool of the router."},
		actions.Action[NAT64Capable]{Type: "nat64_enable", Label: "Enable NAT64", Params: actions.P(actions.Pool, actions.Prefix), State: "nat64",
			Run: func(c NAT64Capable, a types.ActionEntry) ([][]string, error) {
				prefix, err := nat64Prefix(a)
				if err != nil {
					return nil, err
				}
				pool, err := nat64Pool(a)
				if err != nil {
					return nil, err
				}
				return c.NAT64Enable(prefix, pool), nil
			}},
		actions.Action[NAT64Capable]{Type: "nat64_disable", Label: "Disable NAT64", State: "nat64", Clears: true,
			Run: func(c NAT64Capable, a types.ActionEntry) ([][]string, error) { return c.NAT64Disable(), nil }},
	)
}
