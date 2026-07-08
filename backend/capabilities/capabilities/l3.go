package capabilities

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

var L3Methods = map[string]string{
	"set_ip":                "SetIP",
	"replace_ip":            "ReplaceIP",
	"remove_ip":             "RemoveIP",
	"set_default_route":     "SetDefaultRoute",
	"remove_default_route":  "RemoveDefaultRoute",
	"add_static_route":      "AddStaticRoute",
	"remove_static_route":   "RemoveStaticRoute",
	"add_dns_nameserver":    "AddDNSNameserver",
	"remove_dns_nameserver": "RemoveDNSNameserver",
	"add_dns_search":        "AddDNSSearch",
	"remove_dns_search":     "RemoveDNSSearch",
}
