package drivers

import (
	"fmt"
	"net"
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
var _ capabilities.OSPF6Capable = (*FRRRouterDriver)(nil)
var _ capabilities.BGPCapable = (*FRRRouterDriver)(nil)
var _ capabilities.ISISCapable = (*FRRRouterDriver)(nil)
var _ drivers_meta.ReadinessProbeProvider = (*FRRRouterDriver)(nil)
var _ drivers_meta.ShellProvider = (*FRRRouterDriver)(nil)
var _ drivers_meta.CapabilityPrecondition = (*FRRRouterDriver)(nil)
var _ drivers_meta.StartupCommandsProvider = (*FRRRouterDriver)(nil)

// ReadinessProbeCommands makes the pod Ready only when zebra, and every
// daemon enabled in /etc/frr/daemons, answer on their vty socket; the
// container runs seconds before they do, and an OSPF action landing in that
// gap fails. Daemons started by hand with ospfd=no in the file are not
// checked, so a manual startup keeps working.
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

// frrDaemons are the daemons behind the driver's capabilities, enabled by the
// default startup command; an FRR image enables none out of the box.
var frrDaemons = []string{"ospfd", "ospf6d", "ripd", "ripngd", "bgpd", "isisd"}

// StartupCommands prepares a stock FRR image: the daemons the capabilities
// need, the two config files the image ships without, then the image's own
// start. iptables is not installed here, NAT says so when it is missing.
func (FRRRouterDriver) StartupCommands() []string {
	var sed []string
	for _, d := range frrDaemons {
		sed = append(sed, "s/^"+d+"=no/"+d+"=yes/")
	}
	return []string{"sh", "-c",
		"sed -i '" + strings.Join(sed, "; ") + "' /etc/frr/daemons && " +
			"touch /etc/frr/frr.conf /etc/frr/vtysh.conf && chown frr:frr /etc/frr/frr.conf /etc/frr/vtysh.conf && " +
			"exec /sbin/tini -- /usr/lib/frr/docker-start"}
}

// FRR starts a daemon only when its line in /etc/frr/daemons says yes, and
// never on the fly, so a capability that needs one is checked on the vty
// socket before its actions run, with a message that says what to enable.
func frrDaemonFor(capability string, a types.ActionEntry) string {
	switch capability {
	case "OSPFCapable":
		return "ospfd"
	case "OSPF6Capable":
		return "ospf6d"
	case "BGPCapable":
		return "bgpd"
	case "ISISCapable":
		return "isisd"
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

// frrOSPFIface applies interface-level OSPF lines (`ip ospf ...`).
func frrOSPFIface(iface string, lines ...string) [][]string {
	cmd := []string{"vtysh", "-c", "configure terminal", "-c", "interface " + iface}
	for _, l := range lines {
		cmd = append(cmd, "-c", l)
	}
	return [][]string{cmd}
}

func (FRRRouterDriver) OSPFSetCost(iface string, cost int) [][]string {
	return frrOSPFIface(iface, fmt.Sprintf("ip ospf cost %d", cost))
}

func (FRRRouterDriver) OSPFRemoveCost(iface string) [][]string {
	return frrOSPFIface(iface, "no ip ospf cost")
}

func (FRRRouterDriver) OSPFSetPriority(iface string, priority int) [][]string {
	return frrOSPFIface(iface, fmt.Sprintf("ip ospf priority %d", priority))
}

func (FRRRouterDriver) OSPFRemovePriority(iface string) [][]string {
	return frrOSPFIface(iface, "no ip ospf priority")
}

func (FRRRouterDriver) OSPFSetTimers(iface string, hello, dead int) [][]string {
	return frrOSPFIface(iface, fmt.Sprintf("ip ospf hello-interval %d", hello), fmt.Sprintf("ip ospf dead-interval %d", dead))
}

func (FRRRouterDriver) OSPFRemoveTimers(iface string) [][]string {
	return frrOSPFIface(iface, "no ip ospf hello-interval", "no ip ospf dead-interval")
}

func (FRRRouterDriver) OSPFSetNetworkType(iface, networkType string) [][]string {
	return frrOSPFIface(iface, "ip ospf network "+networkType)
}

func (FRRRouterDriver) OSPFRemoveNetworkType(iface string) [][]string {
	return frrOSPFIface(iface, "no ip ospf network")
}

func frrOSPF(line string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", line}}
}

// frrAreaType maps the Cisco names to FRR's: totally means no-summary.
func frrAreaType(areaType string) string {
	switch areaType {
	case "totally-stub":
		return "stub no-summary"
	case "totally-nssa":
		return "nssa no-summary"
	default:
		return areaType
	}
}

// A no-summary set on an area that is already stub or nssa only flips a
// flag in FRR, nothing withdraws the summaries until the next ABR run, so
// the area type is dropped and set again, which does schedule one.
func (FRRRouterDriver) OSPFSetAreaType(area, areaType string) [][]string {
	if !strings.HasPrefix(areaType, "totally-") {
		return frrOSPF("area " + area + " " + frrAreaType(areaType))
	}
	kind := strings.TrimPrefix(areaType, "totally-")
	return [][]string{{"sh", "-c",
		"vtysh -c 'configure terminal' -c 'router ospf' -c 'no area " + area + " " + kind + "' >/dev/null 2>&1; " +
			"vtysh -c 'configure terminal' -c 'router ospf' -c 'area " + area + " " + frrAreaType(areaType) + "'"}}
}

// Dropping the stub or nssa flag drops its no-summary with it.
func (FRRRouterDriver) OSPFRemoveAreaType(area, areaType string) [][]string {
	kind := "stub"
	if strings.HasSuffix(areaType, "nssa") {
		kind = "nssa"
	}
	return frrOSPF("no area " + area + " " + kind)
}

// frrRangePrefix writes the range with its host bits set. ospfd dies when a
// range summary collides in Link State ID with a more specific summary this
// router already originated (RFC 2328 E.3 in ospf_handle_summarylsa_lsId_chg,
// FRR 9.1 to 10.7.1); an ID with the host bits set is what appendix E gives
// a colliding summary anyway, receivers mask it, and FRR shows the range
// normalized in its running-config.
func frrRangePrefix(cidr string) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil || ipnet.IP.To4() == nil {
		return cidr
	}
	ip := ipnet.IP.To4()
	for i := range ip {
		ip[i] |= ^ipnet.Mask[i]
	}
	ones, _ := ipnet.Mask.Size()
	return fmt.Sprintf("%s/%d", ip, ones)
}

func (FRRRouterDriver) OSPFAddAreaRange(area, cidr string) [][]string {
	return frrOSPF("area " + area + " range " + frrRangePrefix(cidr))
}

func (FRRRouterDriver) OSPFRemoveAreaRange(area, cidr string) [][]string {
	return frrOSPF("no area " + area + " range " + frrRangePrefix(cidr))
}

func (FRRRouterDriver) OSPFSetAuthentication(iface, authType, key string, keyID int) [][]string {
	if authType == "md5" {
		return frrOSPFIface(iface, "ip ospf authentication message-digest", fmt.Sprintf("ip ospf message-digest-key %d md5 %s", keyID, key))
	}
	return frrOSPFIface(iface, "ip ospf authentication", "ip ospf authentication-key "+key)
}

func (FRRRouterDriver) OSPFRemoveAuthentication(iface, authType string, keyID int) [][]string {
	if authType == "md5" {
		return frrOSPFIface(iface, "no ip ospf authentication", fmt.Sprintf("no ip ospf message-digest-key %d", keyID))
	}
	return frrOSPFIface(iface, "no ip ospf authentication", "no ip ospf authentication-key")
}

// ─── OSPF6Capable ────────────────────────────────────────────────────────────
// The process lives under `router ospf6`, the membership under the interface.

func frrOSPF6(line string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf6", "-c", line}}
}

func frrOSPF6Iface(iface, line string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "interface " + iface, "-c", line}}
}

func (FRRRouterDriver) OSPF6SetRouterID(routerID string) [][]string {
	return frrOSPF6("ospf6 router-id " + routerID)
}

func (FRRRouterDriver) OSPF6RemoveRouterID() [][]string { return frrOSPF6("no ospf6 router-id") }

func (FRRRouterDriver) OSPF6AddInterface(iface, area string) [][]string {
	return frrOSPF6Iface(iface, "ipv6 ospf6 area "+area)
}

func (FRRRouterDriver) OSPF6RemoveInterface(iface, area string) [][]string {
	return frrOSPF6Iface(iface, "no ipv6 ospf6 area "+area)
}

func (FRRRouterDriver) OSPF6PassiveInterface(iface string) [][]string {
	return frrOSPF6Iface(iface, "ipv6 ospf6 passive")
}

func (FRRRouterDriver) OSPF6RemovePassiveInterface(iface string) [][]string {
	return frrOSPF6Iface(iface, "no ipv6 ospf6 passive")
}

func (FRRRouterDriver) OSPF6Redistribute(source string) [][]string {
	return frrOSPF6("redistribute " + source)
}

func (FRRRouterDriver) OSPF6RemoveRedistribute(source string) [][]string {
	return frrOSPF6("no redistribute " + source)
}

func (FRRRouterDriver) OSPF6OriginateDefault() [][]string {
	return frrOSPF6("default-information originate always")
}

func (FRRRouterDriver) OSPF6RemoveOriginateDefault() [][]string {
	return frrOSPF6("no default-information originate")
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

// ─── BGPCapable ──────────────────────────────────────────────────────────────
// One process per router. `router bgp` with no AS enters the one configured
// and fails with "No BGP process is configured" otherwise, so only
// bgp_set_router carries the number.

func frrBGP(lines ...string) [][]string {
	argv := []string{"vtysh", "-c", "configure terminal", "-c", "router bgp"}
	for _, l := range lines {
		argv = append(argv, "-c", l)
	}
	return [][]string{argv}
}

func frrBGPFamily(v6 bool, lines ...string) [][]string {
	af := "address-family ipv4 unicast"
	if v6 {
		af = "address-family ipv6 unicast"
	}
	return frrBGP(append([]string{af}, lines...)...)
}

// FRR names the v6 OSPF and RIP ospf6 and ripng on a redistribute line.
func frrV6Source(source string, v6 bool) string {
	if v6 {
		switch source {
		case "ospf":
			return "ospf6"
		case "rip":
			return "ripng"
		}
	}
	return source
}

// FRR's RFC 8212 default drops every eBGP route that no policy allows,
// which a lab never wants, so the process starts with it off. Re-setting
// with another AS fails in FRR ("already running"); stop BGP first.
func (FRRRouterDriver) BGPSetRouter(as int, routerID string) [][]string {
	argv := []string{"vtysh", "-c", "configure terminal", "-c", fmt.Sprintf("router bgp %d", as), "-c", "no bgp ebgp-requires-policy"}
	if routerID != "" {
		argv = append(argv, "-c", "bgp router-id "+routerID)
	} else {
		argv = append(argv, "-c", "no bgp router-id")
	}
	return [][]string{argv}
}

func (FRRRouterDriver) BGPRemoveRouter() [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "no router bgp"}}
}

// The neighbour is activated in the family of its address; the options not
// given are cleared, so a second add replaces the first.
func (FRRRouterDriver) BGPAddNeighbor(neighbor string, remoteAS int, updateSource string, multihop int, v6 bool) [][]string {
	lines := []string{fmt.Sprintf("neighbor %s remote-as %d", neighbor, remoteAS)}
	if updateSource != "" {
		lines = append(lines, fmt.Sprintf("neighbor %s update-source %s", neighbor, updateSource))
	} else {
		lines = append(lines, "no neighbor "+neighbor+" update-source")
	}
	if multihop > 0 {
		lines = append(lines, fmt.Sprintf("neighbor %s ebgp-multihop %d", neighbor, multihop))
	} else {
		lines = append(lines, "no neighbor "+neighbor+" ebgp-multihop")
	}
	af := "address-family ipv4 unicast"
	if v6 {
		af = "address-family ipv6 unicast"
	}
	lines = append(lines, af, "neighbor "+neighbor+" activate")
	return frrBGP(lines...)
}

func (FRRRouterDriver) BGPRemoveNeighbor(neighbor string) [][]string {
	return frrBGP("no neighbor " + neighbor)
}

func (FRRRouterDriver) BGPAddNetwork(network string, v6 bool) [][]string {
	return frrBGPFamily(v6, "network "+network)
}

func (FRRRouterDriver) BGPRemoveNetwork(network string, v6 bool) [][]string {
	return frrBGPFamily(v6, "no network "+network)
}

func (FRRRouterDriver) BGPRedistribute(source string, v6 bool) [][]string {
	return frrBGPFamily(v6, "redistribute "+frrV6Source(source, v6))
}

func (FRRRouterDriver) BGPRemoveRedistribute(source string, v6 bool) [][]string {
	return frrBGPFamily(v6, "no redistribute "+frrV6Source(source, v6))
}

// ─── ISISCapable ─────────────────────────────────────────────────────────────
// One process, `router isis kubendt`, since FRR wants a tag and VyOS has
// none. Interface parameters need the interface in IS-IS first (the YANG
// model refuses them otherwise), and stopping the process leaves every
// interface line in place, so the stop sweeps them, parameters first.

const frrISISTag = "kubendt"

func frrISIS(lines ...string) [][]string {
	argv := []string{"vtysh", "-c", "configure terminal", "-c", "router isis " + frrISISTag}
	for _, l := range lines {
		argv = append(argv, "-c", l)
	}
	return [][]string{argv}
}

func frrISISIface(iface string, lines ...string) [][]string {
	argv := []string{"vtysh", "-c", "configure terminal", "-c", "interface " + iface}
	for _, l := range lines {
		argv = append(argv, "-c", l)
	}
	return [][]string{argv}
}

// FRR says level-2-only where the others say level-2.
func frrISISLevel(level string) string {
	if level == "level-2" {
		return "level-2-only"
	}
	return level
}

func (FRRRouterDriver) ISISSetRouter(net, level string) [][]string {
	mtu := fmt.Sprintf("lsp-mtu %d", capabilities.ISISLSPMTU)
	if level == "" {
		return frrISIS("net "+net, mtu, "no is-type")
	}
	return frrISIS("net "+net, mtu, "is-type "+frrISISLevel(level))
}

func (FRRRouterDriver) ISISRemoveRouter() [][]string {
	return [][]string{{"sh", "-c",
		"vtysh -c 'configure terminal' -c 'no router isis " + frrISISTag + "'; " +
			"for p in '^ isis ' '^ (ip|ipv6) router isis '; do " +
			"vtysh -c 'show running-config' | awk -v p=\"$p\" '/^interface /{i=$2} $0 ~ p {print i\" \"$0}' | " +
			"while read -r i l; do vtysh -c 'configure terminal' -c \"interface $i\" -c \"no $l\"; done; done"}}
}

// The wanted family joins first so the interface holds the area tag before
// the other family is cleared.
func (FRRRouterDriver) ISISAddInterface(iface, level string, v4, v6 bool) [][]string {
	var lines []string
	if v4 {
		lines = append(lines, "ip router isis "+frrISISTag)
	}
	if v6 {
		lines = append(lines, "ipv6 router isis "+frrISISTag)
	}
	if !v4 {
		lines = append(lines, "no ip router isis "+frrISISTag)
	}
	if !v6 {
		lines = append(lines, "no ipv6 router isis "+frrISISTag)
	}
	if level == "" {
		lines = append(lines, "no isis circuit-type")
	} else {
		lines = append(lines, "isis circuit-type "+frrISISLevel(level))
	}
	return frrISISIface(iface, lines...)
}

// Only when the interface is in IS-IS, since the parameter lines fail on
// one that is not; nothing to do then.
func (FRRRouterDriver) ISISRemoveInterface(iface string) [][]string {
	return [][]string{{"sh", "-c",
		"if vtysh -c 'show running-config' | awk -v i=" + iface + " '$0==\"interface \"i{f=1;next} /^exit/{f=0} f && / router isis /{m=1} END{exit !m}'; then " +
			"vtysh -c 'configure terminal' -c 'interface " + iface + "' -c 'no isis passive' -c 'no isis metric' -c 'no isis circuit-type' -c 'no isis network point-to-point' " +
			"-c 'no ip router isis " + frrISISTag + "' -c 'no ipv6 router isis " + frrISISTag + "'; fi"}}
}

func (FRRRouterDriver) ISISPassiveInterface(iface string) [][]string {
	return frrISISIface(iface, "isis passive")
}

func (FRRRouterDriver) ISISRemovePassiveInterface(iface string) [][]string {
	return frrISISIface(iface, "no isis passive")
}

func (FRRRouterDriver) ISISSetMetric(iface string, metric int) [][]string {
	return frrISISIface(iface, fmt.Sprintf("isis metric %d", metric))
}

func (FRRRouterDriver) ISISRemoveMetric(iface string) [][]string {
	return frrISISIface(iface, "no isis metric")
}

// Broadcast is FRR's default and has no keyword of its own.
func (FRRRouterDriver) ISISSetNetworkType(iface, networkType string) [][]string {
	if networkType == "point-to-point" {
		return frrISISIface(iface, "isis network point-to-point")
	}
	return frrISISIface(iface, "no isis network point-to-point")
}

func (FRRRouterDriver) ISISRemoveNetworkType(iface string) [][]string {
	return frrISISIface(iface, "no isis network point-to-point")
}

func frrISISFamily(v6 bool) string {
	if v6 {
		return "ipv6"
	}
	return "ipv4"
}

func (FRRRouterDriver) ISISRedistribute(source, level string, v6 bool) [][]string {
	return frrISIS("redistribute " + frrISISFamily(v6) + " " + frrV6Source(source, v6) + " " + level)
}

func (FRRRouterDriver) ISISRemoveRedistribute(source, level string, v6 bool) [][]string {
	return frrISIS("no redistribute " + frrISISFamily(v6) + " " + frrV6Source(source, v6) + " " + level)
}

// Shells: vtysh, the FRR console.
func (FRRRouterDriver) Shells() []drivers_meta.ShellSpec {
	return []drivers_meta.ShellSpec{{Mode: "vtysh", Label: "vtysh console", Command: []string{"vtysh"}}}
}
