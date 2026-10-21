package capabilities

import (
	"net"
	"strings"
)

// IsIPv6 reports whether an address, with or without a prefix length, is
// IPv6, so a builder can pick the v6 form of a command.
func IsIPv6(addr string) bool {
	host, _, _ := strings.Cut(strings.TrimSpace(addr), "/")
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() == nil
}
