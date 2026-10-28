package capabilities

import (
	"fmt"
	"net"
	"strings"
)

// NAT64Base runs tayga, which the toolbox ships: a tun device the kernel
// routes the prefix and the pool into, and the daemon translating between
// them. Everything lives in the toolbox, so a recreated pod gets it back
// from the history like any other action.
type NAT64Base struct{}

// A toolbox older than tini has a sleep as PID 1 that reaps nothing, so a
// tayga killed by a disable stays as a zombie pgrep still lists; alive
// means not Z.
const taygaAlive = "for p in $(pgrep -x tayga); do grep -q '^State:.Z' /proc/$p/status || exit 0; done; exit 1"

const (
	taygaConf = "/etc/tayga.conf"
	taygaLog  = "/var/log/tayga.log"
	taygaDev  = "nat64"
)

// RFC 6052 forbids the well known prefix in front of a private IPv4
// address and tayga enforces it by default; a lab is all private
// addresses, so that check is off.
//
// The translator takes the pool's first address as its own IPv4 and, since
// tayga wants an IPv6 of its own next to the well known prefix, a ULA made
// from the pool so two routers never pick the same one.
func taygaAddresses(pool string) (v4, v6 string) {
	_, n, err := net.ParseCIDR(pool)
	if err != nil {
		return "", ""
	}
	first := make(net.IP, len(n.IP.To4()))
	copy(first, n.IP.To4())
	first[3]++
	v4 = first.String()
	v6 = fmt.Sprintf("fd64:%02x%02x:%02x%02x::1", n.IP.To4()[0], n.IP.To4()[1], n.IP.To4()[2], n.IP.To4()[3])
	return v4, v6
}

func (NAT64Base) NAT64Enable(prefix, pool string) [][]string {
	v4, v6 := taygaAddresses(pool)
	conf := strings.Join([]string{
		"tun-device " + taygaDev,
		"ipv4-addr " + v4,
		"ipv6-addr " + v6,
		"prefix " + prefix,
		"dynamic-pool " + pool,
		"wkpf-strict no",
	}, "\\n")
	script := strings.Join([]string{
		"pkill -9 -x tayga 2>/dev/null",
		"ip link del " + taygaDev + " 2>/dev/null",
		"printf '" + conf + "\\n' > " + taygaConf,
		"tayga --mktun -c " + taygaConf,
		"ip link set " + taygaDev + " up",
		"ip route replace " + pool + " dev " + taygaDev,
		"ip -6 route replace " + prefix + " dev " + taygaDev,
		"nohup tayga --nodetach --stdout -c " + taygaConf + " >" + taygaLog + " 2>&1 & sleep 1",
		taygaAlive,
	}, "; ")
	return [][]string{{"sh", "-c", script}}
}

// tayga catches SIGTERM but only acts on it with the next packet, so it is
// killed outright; it holds no state worth a clean exit.
func (NAT64Base) NAT64Disable() [][]string {
	return [][]string{{"sh", "-c",
		"pkill -9 -x tayga 2>/dev/null; tayga --rmtun -c " + taygaConf + " 2>/dev/null; ip link del " + taygaDev + " 2>/dev/null; rm -f " + taygaConf + "; true"}}
}
