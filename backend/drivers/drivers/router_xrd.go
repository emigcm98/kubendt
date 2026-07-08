package drivers

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"

	"kubendt/capabilities/capabilities"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/executor"
	"kubendt/types"
)

type XRDRouterDriver struct {
	drivers_meta.Meta
	executor.ExecutorMeta
	capabilities.L2Base
}

func NewXRDRouterDriver() *XRDRouterDriver {
	return &XRDRouterDriver{
		Meta:         drivers_meta.NewMeta("XRDRouterDriver", "router"),
		ExecutorMeta: executor.NewExecutorMeta(executor.XRCLIExecutorName),
	}
}

var _ capabilities.L2Capable = (*XRDRouterDriver)(nil)
var _ capabilities.L3Capable = (*XRDRouterDriver)(nil)
var _ types.EffectiveInterfaceInspector = (*XRDRouterDriver)(nil)
var _ types.EffectiveSingleInterfaceInspector = (*XRDRouterDriver)(nil)
var _ types.EffectiveInterfaceStateInspector = (*XRDRouterDriver)(nil)
var _ types.EffectiveActionExecutionPlanResolver = (*XRDRouterDriver)(nil)
var _ types.ReadinessProbeProvider = (*XRDRouterDriver)(nil)

// ReadinessProbeCommands gates pod readiness on Loopback0 being up, that
// only happens once XR has applied the generated startup-config.
//
// Timings: tight Period=5s catches the ready flip quickly; Timeout=15s
// because xr_cli can take >8s while startup-config is processing (8s was
// counting busy-but-alive XR as failed, ~18s wasted per false negative);
// envelope ≈ 170s, matching the VyOS probe.
func (XRDRouterDriver) ReadinessProbeCommands() types.ReadinessProbeSpec {
	return types.ReadinessProbeSpec{
		Command: []string{"sh", "-c",
			`/pkg/bin/xr_cli "show interfaces Loopback0" 2>&1 | grep -q "Loopback0 is up, line protocol is up"`,
		},
		InitialDelaySeconds: 20,
		PeriodSeconds:       5,
		TimeoutSeconds:      15,
		FailureThreshold:    30,
	}
}

// ─── L2 ──────────────────────────────────────────────────────────────────────

// LinkUp/LinkDown define the native IOS-XR payloads for interface state changes.
func (XRDRouterDriver) LinkUp(iface string) [][]string {
	return [][]string{{"interface " + iface, "no shutdown"}}
}

func (XRDRouterDriver) LinkDown(iface string) [][]string {
	return [][]string{{"interface " + iface, "shutdown"}}
}

// ─── L3 ──────────────────────────────────────────────────────────────────────
//
// IOS-XR does not accept CIDR in "ipv4 address", it requires IP + dotted-decimal mask.
// L3Capable methods delegate to internal helpers after the conversion.

func (d XRDRouterDriver) SetIP(iface, cidr string) [][]string {
	ip, mask, err := xrCIDRToIPMask(cidr)
	if err != nil {
		return nil
	}
	return [][]string{{"interface " + iface, " ipv4 address " + ip + " " + mask}}
}

func (d XRDRouterDriver) ReplaceIP(iface, cidr string) [][]string {
	// IOS-XR replaces the address when "ipv4 address" is applied again.
	return d.SetIP(iface, cidr)
}

func (d XRDRouterDriver) RemoveIP(iface, cidr string) [][]string {
	return [][]string{{"interface " + iface, " no ipv4 address"}}
}

func (XRDRouterDriver) SetDefaultRoute(gateway string) [][]string {
	return [][]string{{"router static", "address-family ipv4 unicast", " 0.0.0.0/0 " + gateway, "!"}}
}

func (XRDRouterDriver) RemoveDefaultRoute() [][]string {
	return [][]string{{"router static", "address-family ipv4 unicast", " no 0.0.0.0/0", "!"}}
}

func (XRDRouterDriver) AddStaticRoute(dstCIDR, via, dev string) [][]string {
	return [][]string{{"router static", "address-family ipv4 unicast", " " + dstCIDR + " " + via, "!"}}
}

func (XRDRouterDriver) RemoveStaticRoute(dstCIDR, via, dev string) [][]string {
	return [][]string{{"router static", "address-family ipv4 unicast", " no " + dstCIDR, "!"}}
}

func (XRDRouterDriver) AddDNSNameserver(server string) [][]string {
	return [][]string{{"domain name-server " + server}}
}

func (XRDRouterDriver) RemoveDNSNameserver(server string) [][]string {
	return [][]string{{"no domain name-server " + server}}
}

func (XRDRouterDriver) AddDNSSearch(domain string) [][]string {
	return [][]string{{"domain list " + domain}}
}

func (XRDRouterDriver) RemoveDNSSearch(domain string) [][]string {
	return [][]string{{"no domain list " + domain}}
}

// ─── ResolveActionExecutionPlan ───────────────────────────────────────────────

// ResolveActionExecutionPlan routes L2 and L3 actions through xr_apply,
// which wraps configure blocks in an xrapply_string transaction.
// xr_cli only supports op-mode; configure-mode requires xr_apply.
func (d *XRDRouterDriver) ResolveActionExecutionPlan(namespace, podName string, action types.ActionEntry) (string, [][]string, bool, error) {
	// resolveIface loads the iface map only when the action needs it.
	resolveIface := func(podIntf string) (string, error) {
		ifaceMap, err := d.readIfaceMap(namespace, podName)
		if err != nil {
			return "", err
		}
		g, ok := ifaceMap[podIntf]
		if !ok {
			return "", fmt.Errorf("interface %q not found in iface-map for pod %s/%s", podIntf, namespace, podName)
		}
		return g, nil
	}

	driver := XRDRouterDriver{}

	switch action.Type {
	// L2
	case "link_up":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.XRApplyExecutorName, driver.LinkUp(g), true, nil
	case "link_down":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.XRApplyExecutorName, driver.LinkDown(g), true, nil

	// L3, interfaces (require name resolution + CIDR→mask conversion)
	case "set_ip":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		ip, mask, err := xrCIDRToIPMask(action.CIDR)
		if err != nil {
			return "", nil, true, err
		}
		return executor.XRApplyExecutorName,
			[][]string{{"interface " + g, " ipv4 address " + ip + " " + mask}},
			true, nil
	case "replace_ip":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		ip, mask, err := xrCIDRToIPMask(action.CIDR)
		if err != nil {
			return "", nil, true, err
		}
		return executor.XRApplyExecutorName,
			[][]string{{"interface " + g, " ipv4 address " + ip + " " + mask}},
			true, nil
	case "remove_ip":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.XRApplyExecutorName, driver.RemoveIP(g, ""), true, nil

	// L3, rutas y DNS (no dependen de interfaz de datos)
	case "set_default_route":
		return executor.XRApplyExecutorName, driver.SetDefaultRoute(action.Gateway), true, nil
	case "remove_default_route":
		return executor.XRApplyExecutorName, driver.RemoveDefaultRoute(), true, nil
	case "add_static_route":
		return executor.XRApplyExecutorName, driver.AddStaticRoute(action.DstCIDR, action.Gateway, action.Device), true, nil
	case "remove_static_route":
		return executor.XRApplyExecutorName, driver.RemoveStaticRoute(action.DstCIDR, action.Gateway, action.Device), true, nil
	case "add_dns_nameserver":
		return executor.XRApplyExecutorName, driver.AddDNSNameserver(action.DNSServer), true, nil
	case "remove_dns_nameserver":
		return executor.XRApplyExecutorName, driver.RemoveDNSNameserver(action.DNSServer), true, nil
	case "add_dns_search":
		return executor.XRApplyExecutorName, driver.AddDNSSearch(action.DNSDomain), true, nil
	case "remove_dns_search":
		return executor.XRApplyExecutorName, driver.RemoveDNSSearch(action.DNSDomain), true, nil

	default:
		return "", nil, false, nil
	}
}

// xrCIDRToIPMask convierte "10.0.0.1/24" a ("10.0.0.1", "255.255.255.0", nil).
// IOS-XR does not accept CIDR notation in interface commands.
func xrCIDRToIPMask(cidr string) (ip, mask string, err error) {
	parts := strings.SplitN(cidr, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid CIDR %q: expected IP/prefix", cidr)
	}
	ip = parts[0]
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", "", fmt.Errorf("parsing CIDR %q: %w", cidr, err)
	}
	m := ipNet.Mask
	mask = fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3])
	return
}

// xrdIfaceMapPath is the file written by the entrypoint at boot,
// mapping pod interface names to XR guest interface names.
// when XRd init starts and manages its namespaces.
const xrdIfaceMapPath = "/var/lib/xrd/iface-map.json"

// GetEffectiveInterfaces implements types.EffectiveInterfaceInspector.
// It reads the interface map written by the XRD entrypoint at boot,
// then queries XR once and parses all GigabitEthernet interfaces in bulk.
// This method is read-only and never persists anything.
func (d *XRDRouterDriver) GetEffectiveInterfaces(namespace, podName string) ([]map[string]string, error) {
	ifaceMap, err := d.readIfaceMap(namespace, podName)
	if err != nil {
		return nil, err
	}

	xrExec, err := executor.Get(executor.XRCLIExecutorName)
	if err != nil {
		return nil, fmt.Errorf("xr_cli executor not found: %w", err)
	}

	bulkOut, bulkErr := xrExec.ExecCommandAndGet(podName, namespace,
		executor.NewLineCommand("show interfaces GigabitEthernet*"))
	bulkByGuest := map[string]map[string]string{}
	if bulkErr == nil {
		bulkByGuest = parseXRBulkGigabitInterfaces(bulkOut)
	}

	var result []map[string]string
	for podIntf, guestIntf := range ifaceMap {
		entry := map[string]string{
			"interface":      podIntf,
			"guestInterface": guestIntf,
		}

		if details, ok := bulkByGuest[guestIntf]; ok {
			if state := details["state"]; state != "" {
				entry["state"] = state
			}
			if mac := details["mac"]; mac != "" {
				entry["mac"] = mac
			}
			if ip := details["ipv4"]; ip != "" {
				entry["ipv4"] = ip
			}
		} else {
			// Fallback for parser misses or wildcard command behavior changes.
			fallbackEntry, _ := d.inspectGuestInterface(namespace, podName, podIntf, guestIntf)
			if fallbackEntry != nil {
				if mac := fallbackEntry["mac"]; mac != "" {
					entry["mac"] = mac
				}
				if ip := fallbackEntry["ipv4"]; ip != "" {
					entry["ipv4"] = ip
				}
			}
		}

		result = append(result, entry)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i]["interface"] < result[j]["interface"]
	})

	return result, nil
}

// GetEffectiveInterfaceStates returns pod-interface keyed up/down states based
// on guest XR interface states.
func (d *XRDRouterDriver) GetEffectiveInterfaceStates(namespace, podName string) (map[string]bool, error) {
	ifaceMap, err := d.readIfaceMap(namespace, podName)
	if err != nil {
		return nil, err
	}

	xrExec, err := executor.Get(executor.XRCLIExecutorName)
	if err != nil {
		return nil, fmt.Errorf("xr_cli executor not found: %w", err)
	}

	bulkOut, err := xrExec.ExecCommandAndGet(podName, namespace,
		executor.NewLineCommand("show interfaces GigabitEthernet*"))
	if err != nil {
		return nil, err
	}

	guestStates := parseXRBulkGigabitStates(bulkOut)
	podStates := make(map[string]bool, len(ifaceMap))
	for podIntf, guestIntf := range ifaceMap {
		if up, ok := guestStates[guestIntf]; ok {
			podStates[podIntf] = up
		}
	}

	return podStates, nil
}

// GetEffectiveInterface resolves only one pod interface and is intended for
// low-overhead, tooltip-style lookups.
func (d *XRDRouterDriver) GetEffectiveInterface(namespace, podName, podInterface string) (map[string]string, error) {
	ifaceMap, err := d.readIfaceMap(namespace, podName)
	if err != nil {
		return nil, err
	}

	guestIntf, ok := ifaceMap[podInterface]
	if !ok {
		return nil, fmt.Errorf("interface %q not found in iface-map for pod %s/%s", podInterface, namespace, podName)
	}

	entry, err := d.inspectGuestInterface(namespace, podName, podInterface, guestIntf)
	if err != nil {
		return nil, err
	}
	return entry, nil
}

func (d *XRDRouterDriver) readIfaceMap(namespace, podName string) (map[string]string, error) {
	// Read iface-map.json from the pod container filesystem via kubectl exec (not xr_cli).
	kubectlExec, err := executor.Get(executor.DefaultExecutorName)
	if err != nil {
		return nil, fmt.Errorf("kubectl executor not found: %w", err)
	}

	ifaceMapRaw, err := kubectlExec.ExecCommandAndGet(podName, namespace,
		executor.NewArgsCommand([]string{"cat", xrdIfaceMapPath}))
	if err != nil {
		return nil, fmt.Errorf("iface-map.json not yet available in pod %s/%s: %w", namespace, podName, err)
	}

	// podIntf -> guestIntf, e.g. "eth1" -> "GigabitEthernet0/0/0/0"
	var ifaceMap map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(ifaceMapRaw)), &ifaceMap); err != nil {
		return nil, fmt.Errorf("parsing iface-map.json: %w", err)
	}

	return ifaceMap, nil
}

func (d *XRDRouterDriver) inspectGuestInterface(namespace, podName, podIntf, guestIntf string) (map[string]string, error) {
	entry := map[string]string{
		"interface":      podIntf,
		"guestInterface": guestIntf,
	}

	xrExec, err := executor.Get(executor.XRCLIExecutorName)
	if err != nil {
		return nil, fmt.Errorf("xr_cli executor not found: %w", err)
	}

	// Query XR CLI for IP and MAC on this guest interface (read-only).
	xrOut, err := xrExec.ExecCommandAndGet(podName, namespace,
		executor.NewLineCommand(fmt.Sprintf("show interfaces %s", guestIntf)))
	if err != nil {
		return nil, err
	}

	if mac := parseXRMac(xrOut); mac != "" {
		entry["mac"] = mac
	}
	if ip := parseXRIPv4(xrOut); ip != "" {
		entry["ipv4"] = ip
	}

	return entry, nil
}

var xrIntfLineRe = regexp.MustCompile(`^(GigabitEthernet[^ ]+) is `)
var xrIntfStateLineRe = regexp.MustCompile(`^(GigabitEthernet[^ ]+) is ([^,]+), line protocol is ([^ ]+)`)

// parseXRBulkGigabitInterfaces parses output from "show interfaces GigabitEthernet*"
// and returns guest interface details keyed by guest interface name.
func parseXRBulkGigabitInterfaces(output string) map[string]map[string]string {
	byGuest := map[string]map[string]string{}
	currentIntf := ""

	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		if m := xrIntfLineRe.FindStringSubmatch(line); len(m) > 1 {
			currentIntf = m[1]
			if _, ok := byGuest[currentIntf]; !ok {
				byGuest[currentIntf] = map[string]string{}
			}
			if stateMatch := xrIntfStateLineRe.FindStringSubmatch(line); len(stateMatch) > 3 {
				adminUp := strings.EqualFold(stateMatch[2], "up")
				lineUp := strings.EqualFold(stateMatch[3], "up")
				if adminUp && lineUp {
					byGuest[currentIntf]["state"] = "up"
				} else {
					byGuest[currentIntf]["state"] = "down"
				}
			}
			continue
		}

		if currentIntf == "" {
			continue
		}

		if mac := parseXRMac(line); mac != "" {
			byGuest[currentIntf]["mac"] = mac
		}
		if ip := parseXRIPv4(line); ip != "" {
			byGuest[currentIntf]["ipv4"] = ip
		}
	}

	return byGuest
}

func parseXRBulkGigabitStates(output string) map[string]bool {
	states := map[string]bool{}
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		m := xrIntfStateLineRe.FindStringSubmatch(line)
		if len(m) < 4 {
			continue
		}
		adminUp := strings.EqualFold(m[2], "up")
		lineUp := strings.EqualFold(m[3], "up")
		states[m[1]] = adminUp && lineUp
	}
	return states
}

// xrMacRe matches the hardware MAC address in XR "show interfaces" output.
// XR format: "address is 0252.8a52.64f3 (bia ...)"
var xrMacRe = regexp.MustCompile(`address is ([0-9a-fA-F]{4}\.[0-9a-fA-F]{4}\.[0-9a-fA-F]{4})`)

// parseXRMac extracts and converts the XR MAC format "0252.8a52.64f3" to "02:52:8a:52:64:f3".
func parseXRMac(output string) string {
	m := xrMacRe.FindStringSubmatch(output)
	if len(m) < 2 {
		return ""
	}
	raw := strings.ReplaceAll(m[1], ".", "")
	if len(raw) != 12 {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		raw[0:2], raw[2:4], raw[4:6], raw[6:8], raw[8:10], raw[10:12])
}

// xrIPv4Re matches the IPv4 address in XR "show interfaces" output.
// XR format: "Internet address is 10.0.0.1/24"
var xrIPv4Re = regexp.MustCompile(`Internet address is (\d+\.\d+\.\d+\.\d+/\d+)`)

// parseXRIPv4 extracts the IPv4 address with prefix from XR "show interfaces" output.
func parseXRIPv4(output string) string {
	m := xrIPv4Re.FindStringSubmatch(output)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
