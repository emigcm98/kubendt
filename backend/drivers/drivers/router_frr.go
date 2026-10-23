package drivers

import (
	"fmt"
	"strings"

	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/types"
)

type FRRRouterDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.L3Base
	capabilities.NATBase
}

func NewFRRRouterDriver() *FRRRouterDriver {
	return &FRRRouterDriver{
		Meta: drivers_meta.NewMeta("FRRRouterDriver", "router"),
	}
}

var _ capabilities.L2Capable = (*FRRRouterDriver)(nil)
var _ capabilities.L3Capable = (*FRRRouterDriver)(nil)
var _ capabilities.DefaultRoute6Remover = (*FRRRouterDriver)(nil)
var _ capabilities.NATCapable = (*FRRRouterDriver)(nil)
var _ capabilities.OSPFCapable = (*FRRRouterDriver)(nil)
var _ capabilities.RIPCapable = (*FRRRouterDriver)(nil)
var _ drivers_meta.ReadinessProbeProvider = (*FRRRouterDriver)(nil)
var _ drivers_meta.ShellProvider = (*FRRRouterDriver)(nil)
var _ drivers_meta.CapabilityPrecondition = (*FRRRouterDriver)(nil)

// ReadinessProbeCommands makes the pod Ready only when zebra, and every
// daemon enabled in /etc/frr/daemons, answer on their vty socket. The
// default probe (`command -v ip`) passes as soon as the image is up, while
// the daemons are still starting (or, in the shipped examples, while apk is
// still installing iptables), and an OSPF or NAT action landing in that gap
// fails. Daemons started by hand with ospfd=no in the file are not checked,
// so the old manual startup keeps working.
func (FRRRouterDriver) ReadinessProbeCommands() drivers_meta.ReadinessProbeSpec {
	return drivers_meta.ReadinessProbeSpec{
		Command: []string{"sh", "-c",
			`test -S /var/run/frr/zebra.vty && for d in $(sed -n 's/^\([a-z0-9]*d\)=yes$/\1/p' /etc/frr/daemons); do test -S /var/run/frr/$d.vty || exit 1; done`},
		InitialDelaySeconds: 0,
		PeriodSeconds:       2,
		TimeoutSeconds:      3,
		FailureThreshold:    30,
	}
}

// FRR starts a daemon only when its line in /etc/frr/daemons says yes, and
// never on the fly, so a capability that needs one is checked on the vty
// socket before its actions run, with a message that says what to enable.
func frrDaemonFor(capability string, a types.ActionEntry) string {
	switch capability {
	case "OSPFCapable":
		return "ospfd"
	case "RIPCapable":
		if a.Family == "6" || capabilities.IsIPv6(a.CIDR) {
			return "ripngd"
		}
		return "ripd"
	}
	return ""
}

func (FRRRouterDriver) CapabilityPrecondition(capability string, a types.ActionEntry) ([]string, string, bool) {
	daemon := frrDaemonFor(capability, a)
	if daemon == "" {
		return nil, "", false
	}
	return []string{"test", "-S", "/var/run/frr/" + daemon + ".vty"},
		daemon + " is not running: enable it in /etc/frr/daemons before FRR starts (" + daemon + "=yes), as the examples do from the node commands, and restart the node",
		true
}

// ─── L3Capable, routes ───────────────────────────────────────────────────────
// Routes go through staticd rather than `ip route`, so they sit in the
// running-config and `redistribute static` sees them. Addresses stay on `ip`,
// zebra takes them as connected. The pod's own default route through eth0
// (metric 0) would beat a staticd one (metric 20), so it is dropped when a
// default route is set or removed, as `ip route replace` did implicitly.

func frrRouteLine(prefix, via, dev string) string {
	words := "ip route"
	if capabilities.IsIPv6(prefix) {
		words = "ipv6 route"
	}
	line := words + " " + prefix + " " + via
	if dev != "" {
		line += " " + dev
	}
	return line
}

// Every static route of the prefix removed, whatever its next hop: `no ip
// route` needs the next hop, so it is read back from the running-config.
func frrClearPrefix(prefix string) string {
	words := "ip route"
	if capabilities.IsIPv6(prefix) {
		words = "ipv6 route"
	}
	pattern := "^" + strings.ReplaceAll(words+" "+prefix, ".", "\\.") + " "
	return "vtysh -c 'show running-config' | grep '" + pattern + "' | while read -r l; do vtysh -c 'configure terminal' -c \"no $l\"; done"
}

// The route set unless it is already there as is, replacing any other next
// hop for the prefix, so a replay does not blip and a new gateway wins.
func frrEnsureRoute(prefix, via, dev string) string {
	line := frrRouteLine(prefix, via, dev)
	return "{ vtysh -c 'show running-config' | grep -qxF '" + line + "' || { " + frrClearPrefix(prefix) + " && vtysh -c 'configure terminal' -c '" + line + "'; }; }"
}

func frrDropPodDefault(v6 bool) string {
	ip := "ip"
	if v6 {
		ip = "ip -6"
	}
	return "{ ! " + ip + " -o route show default dev eth0 | grep -q . || " + ip + " route del default dev eth0; }"
}

func (FRRRouterDriver) SetDefaultRoute(gateway string) [][]string {
	v6 := capabilities.IsIPv6(gateway)
	prefix := "0.0.0.0/0"
	if v6 {
		prefix = "::/0"
	}
	return [][]string{{"sh", "-c", frrDropPodDefault(v6) + " && " + frrEnsureRoute(prefix, gateway, "")}}
}

func (FRRRouterDriver) RemoveDefaultRoute() [][]string {
	return [][]string{{"sh", "-c", frrDropPodDefault(false) + " && " + frrClearPrefix("0.0.0.0/0")}}
}

func (FRRRouterDriver) RemoveDefaultRoute6() [][]string {
	return [][]string{{"sh", "-c", frrDropPodDefault(true) + " && " + frrClearPrefix("::/0")}}
}

func (FRRRouterDriver) AddStaticRoute(dstCIDR, via, dev string) [][]string {
	return [][]string{{"sh", "-c", frrEnsureRoute(dstCIDR, via, dev)}}
}

// Removing a route that is not there is a notice from staticd, not an error.
func (FRRRouterDriver) RemoveStaticRoute(dstCIDR, via, dev string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "no " + frrRouteLine(dstCIDR, via, dev)}}
}

// ─── OSPFCapable ─────────────────────────────────────────────────────────────
// Uses vtysh FRR syntax to apply configuration directly to the running process.

func (FRRRouterDriver) OSPFAddNetwork(network, area string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", fmt.Sprintf("network %s area %s", network, area)}}
}

func (FRRRouterDriver) OSPFRemoveNetwork(network, area string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", fmt.Sprintf("no network %s area %s", network, area)}}
}

func (FRRRouterDriver) OSPFSetRouterID(routerID string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", fmt.Sprintf("ospf router-id %s", routerID)}}
}

func (FRRRouterDriver) OSPFRemoveRouterID() [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "no ospf router-id"}}
}

func (FRRRouterDriver) OSPFPassiveDefault() [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "passive-interface default"}}
}

func (FRRRouterDriver) OSPFRemovePassiveDefault() [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "no passive-interface default"}}
}

func (FRRRouterDriver) OSPFNoPassive(iface string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", fmt.Sprintf("no passive-interface %s", iface)}}
}

func (FRRRouterDriver) OSPFRemoveNoPassive(iface string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", fmt.Sprintf("passive-interface %s", iface)}}
}

func (FRRRouterDriver) OSPFOriginateDefault() [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "default-information originate always"}}
}

func (FRRRouterDriver) OSPFRemoveOriginateDefault() [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "no default-information originate"}}
}

func (FRRRouterDriver) OSPFMTUIgnore(iface string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", fmt.Sprintf("interface %s", iface), "-c", "ip ospf mtu-ignore"}}
}

func (FRRRouterDriver) OSPFRemoveMTUIgnore(iface string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", fmt.Sprintf("interface %s", iface), "-c", "no ip ospf mtu-ignore"}}
}

func (FRRRouterDriver) OSPFRedistribute(source string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "redistribute " + source}}
}

func (FRRRouterDriver) OSPFRemoveRedistribute(source string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "no redistribute " + source}}
}

// ─── RIPCapable ──────────────────────────────────────────────────────────────
// The same statements under `router rip` or `router ripng`, by family.

func frrRIP(v6 bool, line string) [][]string {
	router := "router rip"
	if v6 {
		router = "router ripng"
	}
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", router, "-c", line}}
}

// FRR names the v6 OSPF ospf6 on the redistribute line.
func frrRIPSource(source string, v6 bool) string {
	if source == "ospf" && v6 {
		return "ospf6"
	}
	return source
}

func (FRRRouterDriver) RIPAddNetwork(network string, v6 bool) [][]string {
	return frrRIP(v6, "network "+network)
}

func (FRRRouterDriver) RIPRemoveNetwork(network string, v6 bool) [][]string {
	return frrRIP(v6, "no network "+network)
}

func (FRRRouterDriver) RIPPassiveInterface(iface string, v6 bool) [][]string {
	return frrRIP(v6, "passive-interface "+iface)
}

func (FRRRouterDriver) RIPRemovePassiveInterface(iface string, v6 bool) [][]string {
	return frrRIP(v6, "no passive-interface "+iface)
}

func (FRRRouterDriver) RIPRedistribute(source string, v6 bool) [][]string {
	return frrRIP(v6, "redistribute "+frrRIPSource(source, v6))
}

func (FRRRouterDriver) RIPRemoveRedistribute(source string, v6 bool) [][]string {
	return frrRIP(v6, "no redistribute "+frrRIPSource(source, v6))
}

func (FRRRouterDriver) RIPSetTimers(update, timeout, garbage int, v6 bool) [][]string {
	return frrRIP(v6, fmt.Sprintf("timers basic %d %d %d", update, timeout, garbage))
}

func (FRRRouterDriver) RIPRemoveTimers(v6 bool) [][]string {
	return frrRIP(v6, "no timers basic")
}

// Shells: vtysh, the FRR console.
func (FRRRouterDriver) Shells() []drivers_meta.ShellSpec {
	return []drivers_meta.ShellSpec{{Mode: "vtysh", Label: "vtysh console", Command: []string{"vtysh"}}}
}
