package drivers

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/executor"
	"kubendt/types"
)

func NewVyOSRouterDriver() *VyOSRouterDriver {
	return &VyOSRouterDriver{
		Meta: drivers_meta.NewMeta("VyOSRouterDriver", "router"),
		// Every action is a configure-mode batch through the guest's HTTP API,
		// one atomic commit each. Reads go through vyos_api on their own.
		ExecutorMeta: executor.NewExecutorMeta(executor.VyOSAPIApplyExecutorName),
	}
}

var _ capabilities.L2Capable = (*VyOSRouterDriver)(nil)
var _ capabilities.L3Capable = (*VyOSRouterDriver)(nil)
var _ capabilities.OSPFCapable = (*VyOSRouterDriver)(nil)
var _ capabilities.RIPCapable = (*VyOSRouterDriver)(nil)
var _ capabilities.OSPF6Capable = (*VyOSRouterDriver)(nil)
var _ capabilities.BGPCapable = (*VyOSRouterDriver)(nil)
var _ drivers_meta.EffectiveInterfaceInspector = (*VyOSRouterDriver)(nil)
var _ drivers_meta.EffectiveSingleInterfaceInspector = (*VyOSRouterDriver)(nil)
var _ drivers_meta.EffectiveInterfaceStateInspector = (*VyOSRouterDriver)(nil)
var _ drivers_meta.ReadinessProbeProvider = (*VyOSRouterDriver)(nil)
var _ drivers_meta.GuestToolProvider = (*VyOSRouterDriver)(nil)
var _ drivers_meta.GuestMTUSetter = (*VyOSRouterDriver)(nil)
var _ drivers_meta.DeviceProvider = (*VyOSRouterDriver)(nil)
var _ drivers_meta.ShellProvider = (*VyOSRouterDriver)(nil)
var _ drivers_meta.InterfaceNameConstrainer = (*VyOSRouterDriver)(nil)
var _ drivers_meta.RuntimeProvider = (*VyOSRouterDriver)(nil)

// Runtime tells the rest of the backend that VyOS pods run a QEMU guest VM,
// which drives pod-spec choices like KVM device requests, privileged mode,
// the iface-counts ConfigMap mount and the serial-shell behaviour. The flag
// is derived here rather than asked from the user.
func (VyOSRouterDriver) Runtime() string {
	return drivers_meta.RuntimeQEMU
}

// vyosIfaceNameRe enforces VyOS' ethernet config schema: only ethN names.
var vyosIfaceNameRe = regexp.MustCompile(`^eth\d+$`)

// vyosInternalMgmtIface is the guest name of the slirp NIC carrying the
// backend's ssh_qemu/vyos_api channel. Renamed away from eth0 so the cluster
// passthrough can take that name; must never surface as a data interface.
const vyosInternalMgmtIface = "eth999"

// InterfaceNameConstraints reflects what VyOS' configd accepts for
// `interfaces ethernet <name>`. eth0 is reserved for the cluster (primary CNI)
// passthrough NIC, eth999 for the internal management NIC.
func (VyOSRouterDriver) InterfaceNameConstraints() drivers_meta.InterfaceNameConstraints {
	return drivers_meta.InterfaceNameConstraints{
		Pattern:      vyosIfaceNameRe,
		PatternHuman: `^eth\d+$ (e.g. eth1, eth10, eth42)`,
		Reserved:     []string{"eth0", vyosInternalMgmtIface},
	}
}

// ReadinessProbeCommands makes the pod Ready only when both control channels
// into the guest answer: ssh_qemu (rescue path) and the HTTP API (hot path,
// what configure and replay use). SSH comes up 7-10 s before the API and
// nginx answers 502 in between, so probing SSH alone made Ready arrive while
// the router could not yet be configured. Probing starts at 30 s (boot
// optimisations pulled the envelope below the historical ~73 s) with the
// same total failure budget as before, 30 + 5*21 = 135 s.
func (VyOSRouterDriver) ReadinessProbeCommands() drivers_meta.ReadinessProbeSpec {
	return drivers_meta.ReadinessProbeSpec{
		Command: []string{"sh", "-c",
			`ssh_qemu echo ok >/dev/null && vyos_api retrieve '{"op":"showConfig","path":["system","host-name"]}' >/dev/null`},
		InitialDelaySeconds: 30,
		PeriodSeconds:       5,
		TimeoutSeconds:      12, // ssh ConnectTimeout=5 plus curl --connect-timeout 5, with margin
		FailureThreshold:    21,
	}
}

// GuestWrapper runs a Linux argv inside the guest over ssh_qemu, with sudo
// for raw sockets. The pod netns has no connectivity (the cluster IP lives
// in the guest) and a tool must use the guest routing table anyway.
func (VyOSRouterDriver) GuestWrapper() []string {
	return []string{"ssh_qemu", "sudo"}
}

// GuestTools lists what the guest image ships: traceroute, tcptraceroute and
// mtr from VyOS itself, and the static iperf3 that Dockerfile.kubendt puts
// in the overlay, since VyOS's own 3.12 has no --json-stream.
func (VyOSRouterDriver) GuestTools() []string {
	return []string{drivers_meta.ToolTrace, drivers_meta.ToolIperf}
}

// Devices: the VM needs KVM.
func (VyOSRouterDriver) Devices() []string { return []string{"/dev/kvm"} }

// GuestMTUCommands sets the MTU of a guest interface through the API; the
// pod's ethN and tapN are set by the platform itself.
func (VyOSRouterDriver) GuestMTUCommands(iface string, mtu int) [][]string {
	return [][]string{{"set interfaces ethernet " + iface + " mtu " + strconv.Itoa(mtu)}}
}

// Shells: the VyOS CLI, an ssh session into the guest over the same channel
// the executors use, which lands in the operational shell.
func (VyOSRouterDriver) Shells() []drivers_meta.ShellSpec {
	return []drivers_meta.ShellSpec{{Mode: "vyos", Label: "VyOS CLI (ssh)", Command: []string{"ssh_qemu"}}}
}

type VyOSRouterDriver struct {
	drivers_meta.Meta
	executor.ExecutorMeta
	capabilities.L2Base
	capabilities.NATBase
}

var _ capabilities.NATCapable = (*VyOSRouterDriver)(nil)
var _ drivers_meta.SNATInterfaceInspector = (*VyOSRouterDriver)(nil)

// ─── NAT ─────────────────────────────────────────────────────────────────────

// vyosNATRuleNum returns a deterministic NAT rule number derived from the
// key string and the base offset (100 for SNAT, 200 for DNAT).
func vyosNATRuleNum(key string, base int) string {
	sum := 0
	for _, c := range key {
		sum += int(c)
	}
	return strconv.Itoa(base + (sum%90)*10)
}

// EnableSNAT configures MASQUERADE on VyOS for the given interface.
func (VyOSRouterDriver) EnableSNAT(iface string) [][]string {
	rule := vyosNATRuleNum(iface, 100)
	return [][]string{
		{"set nat source rule " + rule + " outbound-interface name " + iface},
		{"set nat source rule " + rule + " source address 0.0.0.0/0"},
		{"set nat source rule " + rule + " translation address masquerade"},
	}
}

// DisableSNAT removes the MASQUERADE rule on VyOS for the given interface.
func (VyOSRouterDriver) DisableSNAT(iface string) [][]string {
	return [][]string{
		{"delete nat source rule " + vyosNATRuleNum(iface, 100)},
	}
}

// EnableDNAT configures DNAT port-forwarding on VyOS.
func (VyOSRouterDriver) EnableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string) [][]string {
	rule := vyosNATRuleNum(fmt.Sprintf("%s%d%s", iface, externalPort, protocol), 200)
	return [][]string{
		{"set nat destination rule " + rule + " inbound-interface name " + iface},
		{"set nat destination rule " + rule + " protocol " + protocol},
		{"set nat destination rule " + rule + " destination port " + strconv.Itoa(externalPort)},
		{"set nat destination rule " + rule + " translation address " + internalIP},
		{"set nat destination rule " + rule + " translation port " + strconv.Itoa(internalPort)},
	}
}

// DisableDNAT removes a DNAT rule on VyOS.
func (VyOSRouterDriver) DisableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string) [][]string {
	return [][]string{
		{"delete nat destination rule " + vyosNATRuleNum(fmt.Sprintf("%s%d%s", iface, externalPort, protocol), 200)},
	}
}

// GetSNATInterface returns the interface with an active MASQUERADE rule, or
// "" when no NAT is configured. Read from the cached /retrieve config JSON,
// which replaced the ~1.8s `show nat source rules` op-mode call.
func (d *VyOSRouterDriver) GetSNATInterface(namespace, podName string) (string, error) {
	cfg, err := vyosRetrieveConfig(namespace, podName)
	if err != nil {
		return "", err
	}
	return vyosSNATInterfaceFromConfig(cfg), nil
}

// vyosSNATInterfaceFromConfig walks nat.source.rule.* looking for the first
// rule whose translation address is "masquerade" and returns its outbound
// interface. Handles both the current schema (outbound-interface { name X })
// and the pre-1.4 flat form (outbound-interface X).
func vyosSNATInterfaceFromConfig(cfg map[string]interface{}) string {
	rules, ok := cfgChild(cfg, "nat", "source", "rule")
	if !ok {
		return ""
	}
	// Iterate rule numbers in sorted order so the result is deterministic
	// when several masquerade rules exist.
	nums := make([]string, 0, len(rules))
	for n := range rules {
		nums = append(nums, n)
	}
	sort.Strings(nums)
	for _, n := range nums {
		rule, ok := rules[n].(map[string]interface{})
		if !ok {
			continue
		}
		if addr, _ := cfgLeaf(rule, "translation", "address"); addr != "masquerade" {
			continue
		}
		if name, ok := cfgLeaf(rule, "outbound-interface", "name"); ok {
			return name
		}
		if name, ok := cfgLeaf(rule, "outbound-interface"); ok {
			return name
		}
	}
	return ""
}

// ─── L2 ──────────────────────────────────────────────────────────────────────

// LinkDown returns the VyOS configure-mode commands to disable an interface.
func (VyOSRouterDriver) LinkDown(iface string) [][]string {
	return [][]string{{"set interfaces ethernet " + iface + " disable"}}
}

// LinkUp returns the VyOS configure-mode commands to enable an interface.
func (VyOSRouterDriver) LinkUp(iface string) [][]string {
	return [][]string{{"delete interfaces ethernet " + iface + " disable"}}
}

// ─── L3 ──────────────────────────────────────────────────────────────────────

func (VyOSRouterDriver) SetIP(iface, cidr string) [][]string {
	return [][]string{{"set interfaces ethernet " + iface + " address " + cidr}}
}

func (VyOSRouterDriver) ReplaceIP(iface, cidr string) [][]string {
	return [][]string{
		{"delete interfaces ethernet " + iface + " address"},
		{"set interfaces ethernet " + iface + " address " + cidr},
	}
}

func (VyOSRouterDriver) RemoveIP(iface, cidr string) [][]string {
	return [][]string{{"delete interfaces ethernet " + iface + " address " + cidr}}
}

func (VyOSRouterDriver) SetDefaultRoute(gateway string) [][]string {
	if capabilities.IsIPv6(gateway) {
		return [][]string{{"set protocols static route6 ::/0 next-hop " + gateway}}
	}
	return [][]string{{"set protocols static route 0.0.0.0/0 next-hop " + gateway}}
}

func (VyOSRouterDriver) RemoveDefaultRoute() [][]string {
	return [][]string{{"delete protocols static route 0.0.0.0/0"}}
}

func (VyOSRouterDriver) RemoveDefaultRoute6() [][]string {
	return [][]string{{"delete protocols static route6 ::/0"}}
}

// vyosStaticRoute is the static route node of a prefix's family.
func vyosStaticRoute(dstCIDR string) string {
	if capabilities.IsIPv6(dstCIDR) {
		return "protocols static route6 "
	}
	return "protocols static route "
}

func (VyOSRouterDriver) AddStaticRoute(dstCIDR, via, dev string) [][]string {
	return [][]string{{"set " + vyosStaticRoute(dstCIDR) + dstCIDR + " next-hop " + via}}
}

func (VyOSRouterDriver) RemoveStaticRoute(dstCIDR, via, dev string) [][]string {
	return [][]string{{"delete " + vyosStaticRoute(dstCIDR) + dstCIDR + " next-hop " + via}}
}

func (VyOSRouterDriver) AddDNSNameserver(server string) [][]string {
	return [][]string{{"set system name-server " + server}}
}

func (VyOSRouterDriver) RemoveDNSNameserver(server string) [][]string {
	return [][]string{{"delete system name-server " + server}}
}

func (VyOSRouterDriver) AddDNSSearch(domain string) [][]string {
	return [][]string{{"set system domain-search " + domain}}
}

func (VyOSRouterDriver) RemoveDNSSearch(domain string) [][]string {
	return [][]string{{"delete system domain-search " + domain}}
}

// ─── OSPFv3 ──────────────────────────────────────────────────────────────────

func (VyOSRouterDriver) OSPF6SetRouterID(routerID string) [][]string {
	return [][]string{{"set protocols ospfv3 parameters router-id " + routerID}}
}

func (VyOSRouterDriver) OSPF6RemoveRouterID() [][]string {
	return [][]string{{"delete protocols ospfv3 parameters router-id"}}
}

func (VyOSRouterDriver) OSPF6AddInterface(iface, area string) [][]string {
	return [][]string{{"set protocols ospfv3 interface " + iface + " area " + area}}
}

func (VyOSRouterDriver) OSPF6RemoveInterface(iface, area string) [][]string {
	return [][]string{{"delete protocols ospfv3 interface " + iface + " area"}}
}

func (VyOSRouterDriver) OSPF6PassiveInterface(iface string) [][]string {
	return [][]string{{"set protocols ospfv3 interface " + iface + " passive"}}
}

func (VyOSRouterDriver) OSPF6RemovePassiveInterface(iface string) [][]string {
	return [][]string{{"delete protocols ospfv3 interface " + iface + " passive"}}
}

func (VyOSRouterDriver) OSPF6Redistribute(source string) [][]string {
	return [][]string{{"set protocols ospfv3 redistribute " + source}}
}

func (VyOSRouterDriver) OSPF6RemoveRedistribute(source string) [][]string {
	return [][]string{{"delete protocols ospfv3 redistribute " + source}}
}

func (VyOSRouterDriver) OSPF6OriginateDefault() [][]string {
	return [][]string{{"set protocols ospfv3 default-information originate always"}}
}

func (VyOSRouterDriver) OSPF6RemoveOriginateDefault() [][]string {
	return [][]string{{"delete protocols ospfv3 default-information originate"}}
}

// ─── RIP ─────────────────────────────────────────────────────────────────────
// `protocols rip` and `protocols ripng` carry the same nodes.

func vyosRIP(v6 bool) string {
	if v6 {
		return "protocols ripng "
	}
	return "protocols rip "
}

// VyOS names the v6 OSPF ospfv3 on the redistribute node.
func vyosRIPSource(source string, v6 bool) string {
	if source == "ospf" && v6 {
		return "ospfv3"
	}
	return source
}

func (VyOSRouterDriver) RIPAddNetwork(network string, v6 bool) [][]string {
	return [][]string{{"set " + vyosRIP(v6) + "network " + network}}
}

func (VyOSRouterDriver) RIPRemoveNetwork(network string, v6 bool) [][]string {
	return [][]string{{"delete " + vyosRIP(v6) + "network " + network}}
}

func (VyOSRouterDriver) RIPPassiveInterface(iface string, v6 bool) [][]string {
	return [][]string{{"set " + vyosRIP(v6) + "passive-interface " + iface}}
}

func (VyOSRouterDriver) RIPRemovePassiveInterface(iface string, v6 bool) [][]string {
	return [][]string{{"delete " + vyosRIP(v6) + "passive-interface " + iface}}
}

func (VyOSRouterDriver) RIPRedistribute(source string, v6 bool) [][]string {
	return [][]string{{"set " + vyosRIP(v6) + "redistribute " + vyosRIPSource(source, v6)}}
}

func (VyOSRouterDriver) RIPRemoveRedistribute(source string, v6 bool) [][]string {
	return [][]string{{"delete " + vyosRIP(v6) + "redistribute " + vyosRIPSource(source, v6)}}
}

func (VyOSRouterDriver) RIPSetTimers(update, timeout, garbage int, v6 bool) [][]string {
	p := vyosRIP(v6)
	return [][]string{
		{fmt.Sprintf("set %stimers update %d", p, update)},
		{fmt.Sprintf("set %stimers timeout %d", p, timeout)},
		{fmt.Sprintf("set %stimers garbage-collection %d", p, garbage)},
	}
}

func (VyOSRouterDriver) RIPRemoveTimers(v6 bool) [][]string {
	return [][]string{{"delete " + vyosRIP(v6) + "timers"}}
}

// ─── BGP ─────────────────────────────────────────────────────────────────────
// One process under `protocols bgp`; VyOS leaves ebgp-requires-policy off
// unless asked, so nothing to clear there.

func vyosBGPFamily(v6 bool) string {
	if v6 {
		return "protocols bgp address-family ipv6-unicast "
	}
	return "protocols bgp address-family ipv4-unicast "
}

// VyOS names the v6 OSPF and RIP ospfv3 and ripng on the redistribute node.
func vyosBGPSource(source string, v6 bool) string {
	if v6 {
		switch source {
		case "ospf":
			return "ospfv3"
		case "rip":
			return "ripng"
		}
	}
	return source
}

func (VyOSRouterDriver) BGPSetRouter(as int, routerID string) [][]string {
	cmds := [][]string{{fmt.Sprintf("set protocols bgp system-as %d", as)}}
	if routerID != "" {
		cmds = append(cmds, []string{"set protocols bgp parameters router-id " + routerID})
	}
	return cmds
}

func (VyOSRouterDriver) BGPRemoveRouter() [][]string {
	return [][]string{{"delete protocols bgp"}}
}

// Only set lines: a delete of an absent node fails the commit, so an option
// dropped from a neighbour means removing and adding it again.
func (VyOSRouterDriver) BGPAddNeighbor(neighbor string, remoteAS int, updateSource string, multihop int, v6 bool) [][]string {
	n := "set protocols bgp neighbor " + neighbor + " "
	cmds := [][]string{{fmt.Sprintf("%sremote-as %d", n, remoteAS)}}
	if updateSource != "" {
		cmds = append(cmds, []string{n + "update-source " + updateSource})
	}
	if multihop > 0 {
		cmds = append(cmds, []string{fmt.Sprintf("%sebgp-multihop %d", n, multihop)})
	}
	af := "address-family ipv4-unicast"
	if v6 {
		af = "address-family ipv6-unicast"
	}
	return append(cmds, []string{n + af})
}

func (VyOSRouterDriver) BGPRemoveNeighbor(neighbor string) [][]string {
	return [][]string{{"delete protocols bgp neighbor " + neighbor}}
}

func (VyOSRouterDriver) BGPAddNetwork(network string, v6 bool) [][]string {
	return [][]string{{"set " + vyosBGPFamily(v6) + "network " + network}}
}

func (VyOSRouterDriver) BGPRemoveNetwork(network string, v6 bool) [][]string {
	return [][]string{{"delete " + vyosBGPFamily(v6) + "network " + network}}
}

func (VyOSRouterDriver) BGPRedistribute(source string, v6 bool) [][]string {
	return [][]string{{"set " + vyosBGPFamily(v6) + "redistribute " + vyosBGPSource(source, v6)}}
}

func (VyOSRouterDriver) BGPRemoveRedistribute(source string, v6 bool) [][]string {
	return [][]string{{"delete " + vyosBGPFamily(v6) + "redistribute " + vyosBGPSource(source, v6)}}
}

// ─── OSPF ────────────────────────────────────────────────────────────────────

func (VyOSRouterDriver) OSPFAddNetwork(network, area string) [][]string {
	return [][]string{{"set protocols ospf area " + area + " network " + network}}
}

func (VyOSRouterDriver) OSPFRemoveNetwork(network, area string) [][]string {
	return [][]string{{"delete protocols ospf area " + area + " network " + network}}
}

func (VyOSRouterDriver) OSPFSetRouterID(routerID string) [][]string {
	return [][]string{{"set protocols ospf parameters router-id " + routerID}}
}

func (VyOSRouterDriver) OSPFRemoveRouterID() [][]string {
	return [][]string{{"delete protocols ospf parameters router-id"}}
}

func (VyOSRouterDriver) OSPFPassiveDefault() [][]string {
	return [][]string{{"set protocols ospf passive-interface default"}}
}

func (VyOSRouterDriver) OSPFRemovePassiveDefault() [][]string {
	return [][]string{{"delete protocols ospf passive-interface default"}}
}

// OSPFNoPassive enables OSPF on a specific interface when passive-interface
// default is active. Uses "passive disable" per interface to override
// the global default without affecting other interfaces.
func (VyOSRouterDriver) OSPFNoPassive(iface string) [][]string {
	return [][]string{{"set protocols ospf interface " + iface + " passive disable"}}
}

// OSPFRemoveNoPassive lets the global passive default control the interface again.
func (VyOSRouterDriver) OSPFRemoveNoPassive(iface string) [][]string {
	return [][]string{{"delete protocols ospf interface " + iface + " passive disable"}}
}

func (VyOSRouterDriver) OSPFOriginateDefault() [][]string {
	return [][]string{{"set protocols ospf default-information originate always"}}
}

func (VyOSRouterDriver) OSPFRemoveOriginateDefault() [][]string {
	return [][]string{{"delete protocols ospf default-information originate"}}
}

func (VyOSRouterDriver) OSPFMTUIgnore(iface string) [][]string {
	return [][]string{{"set protocols ospf interface " + iface + " mtu-ignore"}}
}

func (VyOSRouterDriver) OSPFRemoveMTUIgnore(iface string) [][]string {
	return [][]string{{"delete protocols ospf interface " + iface + " mtu-ignore"}}
}

func (VyOSRouterDriver) OSPFRedistribute(source string) [][]string {
	return [][]string{{"set protocols ospf redistribute " + source}}
}

func (VyOSRouterDriver) OSPFRemoveRedistribute(source string) [][]string {
	return [][]string{{"delete protocols ospf redistribute " + source}}
}

func (VyOSRouterDriver) OSPFSetCost(iface string, cost int) [][]string {
	return [][]string{{fmt.Sprintf("set protocols ospf interface %s cost %d", iface, cost)}}
}

func (VyOSRouterDriver) OSPFRemoveCost(iface string) [][]string {
	return [][]string{{"delete protocols ospf interface " + iface + " cost"}}
}

func (VyOSRouterDriver) OSPFSetPriority(iface string, priority int) [][]string {
	return [][]string{{fmt.Sprintf("set protocols ospf interface %s priority %d", iface, priority)}}
}

func (VyOSRouterDriver) OSPFRemovePriority(iface string) [][]string {
	return [][]string{{"delete protocols ospf interface " + iface + " priority"}}
}

func (VyOSRouterDriver) OSPFSetTimers(iface string, hello, dead int) [][]string {
	return [][]string{
		{fmt.Sprintf("set protocols ospf interface %s hello-interval %d", iface, hello)},
		{fmt.Sprintf("set protocols ospf interface %s dead-interval %d", iface, dead)},
	}
}

func (VyOSRouterDriver) OSPFRemoveTimers(iface string) [][]string {
	return [][]string{
		{"delete protocols ospf interface " + iface + " hello-interval"},
		{"delete protocols ospf interface " + iface + " dead-interval"},
	}
}

func (VyOSRouterDriver) OSPFSetNetworkType(iface, networkType string) [][]string {
	return [][]string{{"set protocols ospf interface " + iface + " network " + networkType}}
}

func (VyOSRouterDriver) OSPFRemoveNetworkType(iface string) [][]string {
	return [][]string{{"delete protocols ospf interface " + iface + " network"}}
}

// vyosAreaType maps the Cisco names to VyOS's: totally means no-summary.
func vyosAreaType(areaType string) string {
	switch areaType {
	case "totally-stub":
		return "stub no-summary"
	case "totally-nssa":
		return "nssa no-summary"
	default:
		return areaType
	}
}

func (VyOSRouterDriver) OSPFSetAreaType(area, areaType string) [][]string {
	return [][]string{{"set protocols ospf area " + area + " area-type " + vyosAreaType(areaType)}}
}

func (VyOSRouterDriver) OSPFRemoveAreaType(area, areaType string) [][]string {
	return [][]string{{"delete protocols ospf area " + area + " area-type"}}
}

func (VyOSRouterDriver) OSPFAddAreaRange(area, cidr string) [][]string {
	return [][]string{{"set protocols ospf area " + area + " range " + cidr}}
}

func (VyOSRouterDriver) OSPFRemoveAreaRange(area, cidr string) [][]string {
	return [][]string{{"delete protocols ospf area " + area + " range " + cidr}}
}

func (VyOSRouterDriver) OSPFSetAuthentication(iface, authType, key string, keyID int) [][]string {
	if authType == "md5" {
		return [][]string{{fmt.Sprintf("set protocols ospf interface %s authentication md5 key-id %d md5-key %s", iface, keyID, key)}}
	}
	return [][]string{{"set protocols ospf interface " + iface + " authentication plaintext-password " + key}}
}

func (VyOSRouterDriver) OSPFRemoveAuthentication(iface, authType string, keyID int) [][]string {
	return [][]string{{"delete protocols ospf interface " + iface + " authentication"}}
}

// ─── EffectiveInterfaceInspector ─────────────────────────────────────────────

// vyosConfigCache collapses the bursts of /retrieve calls the poller and the
// UI issue for the same pod (interfaces, states and NAT all read the same
// config snapshot). TTL is far below the poller period, so post-commit
// staleness is invisible in practice.
const vyosConfigTTL = 2 * time.Second

type vyosConfigEntry struct {
	cfg map[string]interface{}
	at  time.Time
}

var (
	vyosConfigMu       sync.Mutex
	vyosConfigCache    = map[string]vyosConfigEntry{}
	vyosConfigInflight = map[string]chan struct{}{}
)

// vyosRetrieveConfig fetches the guest's full config as JSON (POST /retrieve
// via the vyos_api wrapper), cached briefly per pod. Errors are never cached.
// Concurrent misses are deduplicated via the inflight gate: the poller fires
// the states and NAT reads in parallel, which would otherwise cost two
// identical kubectl execs per poll.
func vyosRetrieveConfig(namespace, podName string) (map[string]interface{}, error) {
	key := namespace + "/" + podName

	for {
		vyosConfigMu.Lock()
		if e, ok := vyosConfigCache[key]; ok && time.Since(e.at) < vyosConfigTTL {
			vyosConfigMu.Unlock()
			return e.cfg, nil
		}
		wait, inflight := vyosConfigInflight[key]
		if !inflight {
			ch := make(chan struct{})
			vyosConfigInflight[key] = ch
			vyosConfigMu.Unlock()

			cfg, err := vyosFetchConfig(namespace, podName)

			vyosConfigMu.Lock()
			delete(vyosConfigInflight, key)
			close(ch)
			if err == nil {
				// Prune expired entries (pods that no longer exist).
				for k, e := range vyosConfigCache {
					if time.Since(e.at) >= vyosConfigTTL {
						delete(vyosConfigCache, k)
					}
				}
				vyosConfigCache[key] = vyosConfigEntry{cfg: cfg, at: time.Now()}
			}
			vyosConfigMu.Unlock()
			return cfg, err
		}
		vyosConfigMu.Unlock()

		// Someone else is fetching; wait and re-check. If they failed, the
		// loop makes us the next fetcher instead of inheriting their error.
		<-wait
	}
}

// vyosFetchConfig is the uncached POST /retrieve round trip.
func vyosFetchConfig(namespace, podName string) (map[string]interface{}, error) {
	apiExec, err := executor.Get(executor.VyOSAPIExecutorName)
	if err != nil {
		return nil, fmt.Errorf("vyos_api executor not found: %w", err)
	}

	out, err := apiExec.ExecCommandAndGet(podName, namespace,
		executor.NewArgsCommand([]string{"retrieve", `{"op": "showConfig", "path": []}`}))
	if err != nil {
		return nil, err
	}

	return parseVyOSAPIEnvelope(out)
}

// parseVyOSAPIEnvelope unwraps the {"success": ..., "data": ..., "error": ...}
// envelope every VyOS HTTP API response uses and returns data as a map.
func parseVyOSAPIEnvelope(raw string) (map[string]interface{}, error) {
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, fmt.Errorf("unparseable VyOS API response: %w (body: %.200s)", err, raw)
	}
	if !envelope.Success {
		return nil, fmt.Errorf("VyOS API error: %s", string(envelope.Error))
	}
	cfg := map[string]interface{}{}
	if err := json.Unmarshal(envelope.Data, &cfg); err != nil {
		return nil, fmt.Errorf("VyOS API data is not an object: %w", err)
	}
	return cfg, nil
}

// cfgChild walks nested config maps and returns the map at the given path.
func cfgChild(cfg map[string]interface{}, path ...string) (map[string]interface{}, bool) {
	cur := cfg
	for _, key := range path {
		next, ok := cur[key].(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// cfgLeaf returns the string value at the given path. Multi-valued leaves
// (JSON arrays) yield their first element, matching the single-value
// semantics of the old text parsers.
func cfgLeaf(cfg map[string]interface{}, path ...string) (string, bool) {
	if values := cfgLeaves(cfg, path...); len(values) > 0 {
		return values[0], true
	}
	return "", false
}

// cfgLeaves returns every value of a leaf: VyOS gives a string for one value
// and a list for several (an interface with two addresses).
func cfgLeaves(cfg map[string]interface{}, path ...string) []string {
	cur := interface{}(cfg)
	for _, key := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		if cur, ok = m[key]; !ok {
			return nil
		}
	}
	switch v := cur.(type) {
	case string:
		return []string{v}
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// vyosInterfaceEntry builds the UI interface map from one ethernet config
// node. State is the admin state (`disable` present), authoritative here:
// carrier on virtio↔tap is always up and link actions toggle exactly this flag.
func vyosInterfaceEntry(iface string, node map[string]interface{}) map[string]string {
	entry := map[string]string{
		"interface":      iface,
		"guestInterface": iface,
	}
	if _, disabled := node["disable"]; disabled {
		entry["state"] = "down"
	} else {
		entry["state"] = "up"
	}
	if mac, ok := cfgLeaf(node, "hw-id"); ok {
		entry["mac"] = mac
	}
	addrs := cfgLeaves(node, "address")
	for _, addr := range addrs {
		key := "ipv4"
		if capabilities.IsIPv6(addr) {
			key = "ipv6"
		}
		if _, seen := entry[key]; !seen {
			entry[key] = addr
		}
	}
	if len(addrs) > 0 {
		entry["addresses"] = strings.Join(addrs, ",")
	}
	if mtu, ok := cfgLeaf(node, "mtu"); ok {
		entry["mtu"] = mtu
	}
	return entry
}

// GetEffectiveInterfaces implements drivers_meta.EffectiveInterfaceInspector.
// Returns IP, MAC and state for all data interfaces from one cached
// /retrieve call. VyOS always uses the same interface names as the pod, so
// the interface name doubles as the guest interface name.
func (d *VyOSRouterDriver) GetEffectiveInterfaces(namespace, podName string) ([]map[string]string, error) {
	cfg, err := vyosRetrieveConfig(namespace, podName)
	if err != nil {
		return nil, err
	}

	ethernets, _ := cfgChild(cfg, "interfaces", "ethernet")

	var result []map[string]string
	for iface, raw := range ethernets {
		node, ok := raw.(map[string]interface{})
		if !ok || iface == vyosInternalMgmtIface || types.IsPseudoInterface(iface) {
			continue
		}
		result = append(result, vyosInterfaceEntry(iface, node))
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i]["interface"] < result[j]["interface"]
	})
	return result, nil
}

// GetEffectiveInterface implements drivers_meta.EffectiveSingleInterfaceInspector.
// Returns the state of a single interface, used for the hover tooltip.
// VyOS uses the same interface name as the pod, so no name translation is needed.
func (d *VyOSRouterDriver) GetEffectiveInterface(namespace, podName, podInterface string) (map[string]string, error) {
	cfg, err := vyosRetrieveConfig(namespace, podName)
	if err != nil {
		return nil, err
	}

	entry := map[string]string{
		"interface":      podInterface,
		"guestInterface": podInterface,
	}
	if ethernets, ok := cfgChild(cfg, "interfaces", "ethernet"); ok {
		if node, ok := ethernets[podInterface].(map[string]interface{}); ok {
			entry = vyosInterfaceEntry(podInterface, node)
		}
	}
	return entry, nil
}

// GetEffectiveInterfaceStates implements drivers_meta.EffectiveInterfaceStateInspector.
// Returns up/down state for all data interfaces from the cached /retrieve call.
func (d *VyOSRouterDriver) GetEffectiveInterfaceStates(namespace, podName string) (map[string]bool, error) {
	cfg, err := vyosRetrieveConfig(namespace, podName)
	if err != nil {
		return nil, err
	}

	ethernets, _ := cfgChild(cfg, "interfaces", "ethernet")
	states := make(map[string]bool, len(ethernets))
	for iface, raw := range ethernets {
		// eth0 (cluster passthrough) has no topology link to colour, and the
		// internal management NIC is not a data interface at all.
		if iface == "eth0" || iface == vyosInternalMgmtIface {
			continue
		}
		node, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		_, disabled := node["disable"]
		states[iface] = !disabled
	}
	return states, nil
}
