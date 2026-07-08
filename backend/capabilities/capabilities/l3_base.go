package capabilities

import "fmt"

type L3Base struct{}

func (L3Base) SetIP(iface, cidr string) [][]string {
	return [][]string{{"ip", "addr", "add", cidr, "dev", iface}}
}

func (L3Base) ReplaceIP(iface, cidr string) [][]string {
	return [][]string{
		{"ip", "addr", "flush", "dev", iface},
		{"ip", "addr", "add", cidr, "dev", iface},
	}
}

func (L3Base) RemoveIP(iface, cidr string) [][]string {
	return [][]string{{"ip", "addr", "del", cidr, "dev", iface}}
}

func (L3Base) SetDefaultRoute(gateway string) [][]string {
	return [][]string{{"ip", "route", "replace", "default", "via", gateway}}
}

func (L3Base) RemoveDefaultRoute() [][]string {
	return [][]string{{"ip", "route", "del", "default"}}
}

func (L3Base) AddStaticRoute(dstCIDR, via, dev string) [][]string {
	cmd := []string{"ip", "route", "add", dstCIDR, "via", via}
	if dev != "" {
		cmd = append(cmd, "dev", dev)
	}
	return [][]string{cmd}
}

func (L3Base) RemoveStaticRoute(dstCIDR, via, dev string) [][]string {
	cmd := []string{"ip", "route", "del", dstCIDR, "via", via}
	if dev != "" {
		cmd = append(cmd, "dev", dev)
	}
	return [][]string{cmd}
}

func (L3Base) AddDNSNameserver(server string) [][]string {
	// Idempotent: only adds if missing (uses tee instead of sed -i)
	cmd := fmt.Sprintf(
		"grep -q '^nameserver %s$' /etc/resolv.conf || echo 'nameserver %s' | tee -a /etc/resolv.conf > /dev/null",
		server, server,
	)
	return [][]string{{"sh", "-c", cmd}}
}

func (L3Base) RemoveDNSNameserver(server string) [][]string {
	// tee for idempotency: only removes if exists, keeping other nameservers intact
	cmd := fmt.Sprintf(
		"cat /etc/resolv.conf | grep -v '^nameserver %s$' | tee /etc/resolv.conf > /dev/null",
		server,
	)
	return [][]string{{"sh", "-c", cmd}}
}

func (L3Base) AddDNSSearch(domain string) [][]string {
	// If search line does not exist, creates it; if it exists, adds domain if missing
	cmd := fmt.Sprintf(
		"if grep -q '^search ' /etc/resolv.conf; then "+
			"grep -q '^search .* %s' /etc/resolv.conf || "+
			"(cat /etc/resolv.conf | sed 's/^search .*/& %s/' | tee /etc/resolv.conf > /dev/null); "+
			"else echo 'search %s' | tee -a /etc/resolv.conf > /dev/null; fi",
		domain, domain, domain,
	)
	return [][]string{{"sh", "-c", cmd}}
}

func (L3Base) RemoveDNSSearch(domain string) [][]string {
	// Removes domain from search line (keeping other domains)
	cmd := fmt.Sprintf(
		"cat /etc/resolv.conf | sed 's/^search \\(.*\\) %s /search \\1 /' | sed 's/^search \\(.*\\) %s$/search \\1/' | sed 's/ *$//' | tee /etc/resolv.conf > /dev/null",
		domain, domain,
	)
	return [][]string{{"sh", "-c", cmd}}
}
