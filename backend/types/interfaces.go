package types

import "strings"

// pseudoInterfaceBases lists kernel-internal virtual interfaces that are never
// user-manageable network endpoints (PIM multicast register and the default
// tunnel devices created in every network namespace). They are filtered from
// interface listings regardless of scope.
var pseudoInterfaceBases = map[string]bool{
	"pimreg":   true,
	"pim6reg":  true,
	"ip6tnl0":  true,
	"tunl0":    true,
	"sit0":     true,
	"gre0":     true,
	"gretap0":  true,
	"erspan0":  true,
	"ip_vti0":  true,
	"ip6_vti0": true,
	"ip6gre0":  true,
}

// IsPseudoInterface reports whether name is a kernel-internal pseudo-interface
// that should never be shown as a manageable network interface. It tolerates
// the "name@parent" form (e.g. "pim6reg@NONE").
func IsPseudoInterface(name string) bool {
	name = strings.TrimSpace(name)
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name = name[:i]
	}
	return pseudoInterfaceBases[name]
}
