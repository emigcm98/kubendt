package drivers

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"kubendt/capabilities/capabilities"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/executor"
	"kubendt/types"
)

func NewVyOSRouterDriver() *VyOSRouterDriver {
	return &VyOSRouterDriver{
		Meta:         drivers_meta.NewMeta("VyOSRouterDriver", "router"),
		ExecutorMeta: executor.NewExecutorMeta(executor.VyOSCLIExecutorName),
	}
}

var _ capabilities.L2Capable = (*VyOSRouterDriver)(nil)
var _ capabilities.L3Capable = (*VyOSRouterDriver)(nil)
var _ capabilities.OSPFCapable = (*VyOSRouterDriver)(nil)
var _ types.EffectiveActionExecutionPlanResolver = (*VyOSRouterDriver)(nil)
var _ types.EffectiveInterfaceInspector = (*VyOSRouterDriver)(nil)
var _ types.EffectiveSingleInterfaceInspector = (*VyOSRouterDriver)(nil)
var _ types.EffectiveInterfaceStateInspector = (*VyOSRouterDriver)(nil)
var _ types.ReadinessProbeProvider = (*VyOSRouterDriver)(nil)
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

// InterfaceNameConstraints reflects what VyOS' configd accepts for
// `interfaces ethernet <name>`. eth0 is reserved for the management NIC.
func (VyOSRouterDriver) InterfaceNameConstraints() drivers_meta.InterfaceNameConstraints {
	return drivers_meta.InterfaceNameConstraints{
		Pattern:      vyosIfaceNameRe,
		PatternHuman: `^eth\d+$ (e.g. eth1, eth10, eth42)`,
		Reserved:     []string{"eth0"},
	}
}

// ReadinessProbeCommands returns an SSH-based readiness probe so the pod is
// only declared Ready once the QEMU guest is reachable via ssh_qemu.
//
// Timings are tuned to VyOS' real boot envelope (~73 s from QEMU launch to
// SSH up, plus a few seconds in the entrypoint waiting for veths to settle).
// InitialDelaySeconds skips the period when probing is pointless; once we
// cross it, a tight PeriodSeconds catches the ready flip fast without
// burning many failed probes.
//
// Envelope: 60 + 5*15 = 135 s before kubelet declares the pod unready,
// comfortably above the observed boot time but tight enough to surface real
// failures quickly.
func (VyOSRouterDriver) ReadinessProbeCommands() types.ReadinessProbeSpec {
	return types.ReadinessProbeSpec{
		Command:             []string{"sh", "-c", "ssh_qemu echo ok"},
		InitialDelaySeconds: 60,
		PeriodSeconds:       5,
		TimeoutSeconds:      8, // ssh_qemu ConnectTimeout=5, add margin
		FailureThreshold:    15,
	}
}

type VyOSRouterDriver struct {
	drivers_meta.Meta
	executor.ExecutorMeta
	capabilities.L2Base
	capabilities.NATBase
}

var _ capabilities.NATCapable = (*VyOSRouterDriver)(nil)

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

// GetSNATInterface consulta `show nat source rules` en el guest VyOS y devuelve
// la interfaz con MASQUERADE activo, o "" si no hay NAT configurado.
func (d *VyOSRouterDriver) GetSNATInterface(namespace, podName string) (string, error) {
	vyosExec, err := executor.Get(executor.VyOSCLIExecutorName)
	if err != nil {
		return "", err
	}
	out, err := vyosExec.ExecCommandAndGet(podName, namespace,
		executor.NewArgsCommand([]string{"show", "nat", "source", "rules"}))
	if err != nil {
		// VyOS returns a non-zero exit when no NAT is configured.
		if strings.Contains(err.Error(), "NAT is not configured") ||
			strings.Contains(out, "NAT is not configured") {
			return "", nil
		}
		return "", err
	}
	return parseVyOSNATSourceRules(out), nil
}

// parseVyOSNATSourceRules extracts the first interface with masquerade translation.
// Formato esperado de `show nat source rules`:
//
//	Rule    Source     Destination    Proto    Out-Int    Translation
//	------  ---------  -------------  -------  ---------  -------------
//	100     0.0.0.0/0  0.0.0.0/0      any      eth4       masquerade
func parseVyOSNATSourceRules(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if strings.EqualFold(f, "masquerade") && i > 0 {
				return fields[i-1]
			}
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
	return [][]string{{"set protocols static route 0.0.0.0/0 next-hop " + gateway}}
}

func (VyOSRouterDriver) RemoveDefaultRoute() [][]string {
	return [][]string{{"delete protocols static route 0.0.0.0/0"}}
}

func (VyOSRouterDriver) AddStaticRoute(dstCIDR, via, dev string) [][]string {
	return [][]string{{"set protocols static route " + dstCIDR + " next-hop " + via}}
}

func (VyOSRouterDriver) RemoveStaticRoute(dstCIDR, via, dev string) [][]string {
	return [][]string{{"delete protocols static route " + dstCIDR + " next-hop " + via}}
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

// ─── ResolveActionExecutionPlan ───────────────────────────────────────────────

// ResolveActionExecutionPlan routes L2 and L3 actions through vyos_apply,
// which wraps configure-mode commands in a vbash transaction.
// L2/L3 actions require configure-mode; the default executor (vyos_cli)
// only supports op-mode.
func (d *VyOSRouterDriver) ResolveActionExecutionPlan(namespace, podName string, action types.ActionEntry) (string, [][]string, bool, error) {
	// VyOS always uses the same interface names as the pod (identity mapping),
	// so no name translation is needed.
	resolveIface := func(podIntf string) (string, error) { return podIntf, nil }

	driver := VyOSRouterDriver{}

	switch action.Type {
	// L2
	case "link_up":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.LinkUp(g), true, nil
	case "link_down":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.LinkDown(g), true, nil

	// L3, interfaces
	case "set_ip":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.SetIP(g, action.CIDR), true, nil
	case "replace_ip":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.ReplaceIP(g, action.CIDR), true, nil
	case "remove_ip":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.RemoveIP(g, action.CIDR), true, nil

	// L3, rutas y DNS (no dependen de interfaz de datos)
	case "set_default_route":
		return executor.VyOSApplyExecutorName, driver.SetDefaultRoute(action.Gateway), true, nil
	case "remove_default_route":
		return executor.VyOSApplyExecutorName, driver.RemoveDefaultRoute(), true, nil
	case "add_static_route":
		return executor.VyOSApplyExecutorName, driver.AddStaticRoute(action.DstCIDR, action.Gateway, action.Device), true, nil
	case "remove_static_route":
		return executor.VyOSApplyExecutorName, driver.RemoveStaticRoute(action.DstCIDR, action.Gateway, action.Device), true, nil
	case "add_dns_nameserver":
		return executor.VyOSApplyExecutorName, driver.AddDNSNameserver(action.DNSServer), true, nil
	case "remove_dns_nameserver":
		return executor.VyOSApplyExecutorName, driver.RemoveDNSNameserver(action.DNSServer), true, nil
	case "add_dns_search":
		return executor.VyOSApplyExecutorName, driver.AddDNSSearch(action.DNSDomain), true, nil
	case "remove_dns_search":
		return executor.VyOSApplyExecutorName, driver.RemoveDNSSearch(action.DNSDomain), true, nil

	// NAT
	case "enable_snat":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.EnableSNAT(g), true, nil
	case "disable_snat":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.DisableSNAT(g), true, nil
	case "enable_dnat":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.EnableDNAT(g, action.ExternalPort, action.InternalIP, action.InternalPort, action.Protocol), true, nil
	case "disable_dnat":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.DisableDNAT(g, action.ExternalPort, action.InternalIP, action.InternalPort, action.Protocol), true, nil

	// OSPF
	case "ospf_add_network":
		return executor.VyOSApplyExecutorName, driver.OSPFAddNetwork(action.CIDR, action.OSPFArea), true, nil
	case "ospf_remove_network":
		return executor.VyOSApplyExecutorName, driver.OSPFRemoveNetwork(action.CIDR, action.OSPFArea), true, nil
	case "ospf_set_router_id":
		return executor.VyOSApplyExecutorName, driver.OSPFSetRouterID(action.RouterID), true, nil
	case "ospf_remove_router_id":
		return executor.VyOSApplyExecutorName, driver.OSPFRemoveRouterID(), true, nil
	case "ospf_passive_default":
		return executor.VyOSApplyExecutorName, driver.OSPFPassiveDefault(), true, nil
	case "ospf_remove_passive_default":
		return executor.VyOSApplyExecutorName, driver.OSPFRemovePassiveDefault(), true, nil
	case "ospf_no_passive":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.OSPFNoPassive(g), true, nil
	case "ospf_remove_no_passive":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.OSPFRemoveNoPassive(g), true, nil
	case "ospf_originate_default":
		return executor.VyOSApplyExecutorName, driver.OSPFOriginateDefault(), true, nil
	case "ospf_remove_originate_default":
		return executor.VyOSApplyExecutorName, driver.OSPFRemoveOriginateDefault(), true, nil
	case "ospf_mtu_ignore":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.OSPFMTUIgnore(g), true, nil
	case "ospf_remove_mtu_ignore":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSApplyExecutorName, driver.OSPFRemoveMTUIgnore(g), true, nil

	default:
		return "", nil, false, nil
	}
}

// ─── EffectiveInterfaceInspector ─────────────────────────────────────────────

// GetEffectiveInterfaces implements types.EffectiveInterfaceInspector.
// Returns IP, MAC and state for all data interfaces by parsing a single
// "show interfaces" call. VyOS always uses the same interface names as the
// pod, so the interface name doubles as the guest interface name.
func (d *VyOSRouterDriver) GetEffectiveInterfaces(namespace, podName string) ([]map[string]string, error) {
	vyosExec, err := executor.Get(executor.VyOSCLIExecutorName)
	if err != nil {
		return nil, fmt.Errorf("vyos_cli executor not found: %w", err)
	}

	out, err := vyosExec.ExecCommandAndGet(podName, namespace,
		executor.NewArgsCommand([]string{"show", "interfaces"}))
	if err != nil {
		return nil, err
	}

	byGuest := parseVyOSBriefTable(out)

	var result []map[string]string
	for iface, details := range byGuest {
		if iface == "lo" || types.IsPseudoInterface(iface) {
			continue
		}
		entry := map[string]string{
			"interface":      iface,
			"guestInterface": iface,
		}
		if v := details["state"]; v != "" {
			entry["state"] = v
		}
		if v := details["mac"]; v != "" {
			entry["mac"] = v
		}
		if v := details["ipv4"]; v != "" {
			entry["ipv4"] = v
		}
		result = append(result, entry)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i]["interface"] < result[j]["interface"]
	})
	return result, nil
}

// GetEffectiveInterface implements types.EffectiveSingleInterfaceInspector.
// Returns the state of a single interface, used for the hover tooltip.
// VyOS uses the same interface name as the pod, so no name translation is needed.
func (d *VyOSRouterDriver) GetEffectiveInterface(namespace, podName, podInterface string) (map[string]string, error) {
	vyosExec, err := executor.Get(executor.VyOSCLIExecutorName)
	if err != nil {
		return nil, fmt.Errorf("vyos_cli executor not found: %w", err)
	}

	out, err := vyosExec.ExecCommandAndGet(podName, namespace,
		executor.NewArgsCommand([]string{"show", "interfaces"}))
	if err != nil {
		return nil, err
	}

	byGuest := parseVyOSBriefTable(out)
	entry := map[string]string{
		"interface":      podInterface,
		"guestInterface": podInterface,
	}
	if details, ok := byGuest[podInterface]; ok {
		if v := details["state"]; v != "" {
			entry["state"] = v
		}
		if v := details["mac"]; v != "" {
			entry["mac"] = v
		}
		if v := details["ipv4"]; v != "" {
			entry["ipv4"] = v
		}
	}
	return entry, nil
}

// GetEffectiveInterfaceStates implements types.EffectiveInterfaceStateInspector.
// Returns up/down state for all data interfaces via a single "show interfaces"
// call. VyOS interface names are always identical to pod interface names.
func (d *VyOSRouterDriver) GetEffectiveInterfaceStates(namespace, podName string) (map[string]bool, error) {
	vyosExec, err := executor.Get(executor.VyOSCLIExecutorName)
	if err != nil {
		return nil, fmt.Errorf("vyos_cli executor not found: %w", err)
	}

	out, err := vyosExec.ExecCommandAndGet(podName, namespace,
		executor.NewArgsCommand([]string{"show", "interfaces"}))
	if err != nil {
		return nil, err
	}

	byGuest := parseVyOSBriefTable(out)
	states := make(map[string]bool, len(byGuest))
	for iface, details := range byGuest {
		if iface == "eth0" {
			continue
		}
		states[iface] = details["state"] == "up"
	}
	return states, nil
}

// ─── parsers de salida VyOS CLI ───────────────────────────────────────────────

// parseVyOSBriefTable parses "show interfaces" by fixed column position:
// Interface(0) IP(1) MAC(2) VRF(3) MTU(4) S/L(5). S/L is "<admin>/<link>"
// where u=up, D=down, A=admin-down, up only when both are 'u'.
//
//	Interface    IP Address         MAC                VRF        MTU  S/L    Description
//	eth1         10.0.10.1/30       26:7e:60:c9:07:69  default   1500  u/u    KubeNDT eth1
//	eth2         10.0.0.1/24        82:59:33:ea:8f:fa  default   1500  A/D    KubeNDT eth2
var vyosBriefTableRe = regexp.MustCompile(
	`^(eth\S+)\s+(\S+)\s+([0-9a-fA-F]{2}(?::[0-9a-fA-F]{2}){5})\s+\S+\s+\S+\s+([uDA])/([uDA])`,
)

func parseVyOSBriefTable(output string) map[string]map[string]string {
	result := map[string]map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		m := vyosBriefTableRe.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) < 6 {
			continue
		}
		iface := m[1]
		ip := m[2]
		mac := m[3]
		adminUp := m[4] == "u"
		linkUp := m[5] == "u"

		state := "down"
		if adminUp && linkUp {
			state = "up"
		}

		entry := map[string]string{"state": state, "mac": mac}
		if ip != "-" {
			entry["ipv4"] = ip
		}
		result[iface] = entry
	}
	return result
}
