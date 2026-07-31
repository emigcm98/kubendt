package drivers

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"kubendt/capabilities/capabilities"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/executor"
	"kubendt/types"
)

func NewVyOSRouterDriver() *VyOSRouterDriver {
	return &VyOSRouterDriver{
		Meta:         drivers_meta.NewMeta("VyOSRouterDriver", "router"),
		ExecutorMeta: executor.NewExecutorMeta(executor.VyOSSSHCLIExecutorName),
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

// ReadinessProbeCommands returns an SSH-based readiness probe: the pod is
// Ready once the QEMU guest answers over ssh_qemu. Probing starts at 30 s
// (boot optimisations pulled the envelope below the historical ~73 s) with
// the same total failure budget as before, 30 + 5*21 = 135 s.
func (VyOSRouterDriver) ReadinessProbeCommands() types.ReadinessProbeSpec {
	return types.ReadinessProbeSpec{
		Command:             []string{"sh", "-c", "ssh_qemu echo ok"},
		InitialDelaySeconds: 30,
		PeriodSeconds:       5,
		TimeoutSeconds:      8, // ssh_qemu ConnectTimeout=5, add margin
		FailureThreshold:    21,
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

// ResolveActionExecutionPlan routes configure-mode actions through
// vyos_api_apply (one atomic /configure request per batch). The default
// executor (vyos_ssh_cli) only covers op-mode.
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
		return executor.VyOSAPIApplyExecutorName, driver.LinkUp(g), true, nil
	case "link_down":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.LinkDown(g), true, nil

	// L3, interfaces
	case "set_ip":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.SetIP(g, action.CIDR), true, nil
	case "replace_ip":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.ReplaceIP(g, action.CIDR), true, nil
	case "remove_ip":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.RemoveIP(g, action.CIDR), true, nil

	// L3, rutas y DNS (no dependen de interfaz de datos)
	case "set_default_route":
		return executor.VyOSAPIApplyExecutorName, driver.SetDefaultRoute(action.Gateway), true, nil
	case "remove_default_route":
		return executor.VyOSAPIApplyExecutorName, driver.RemoveDefaultRoute(), true, nil
	case "add_static_route":
		return executor.VyOSAPIApplyExecutorName, driver.AddStaticRoute(action.DstCIDR, action.Gateway, action.Device), true, nil
	case "remove_static_route":
		return executor.VyOSAPIApplyExecutorName, driver.RemoveStaticRoute(action.DstCIDR, action.Gateway, action.Device), true, nil
	case "add_dns_nameserver":
		return executor.VyOSAPIApplyExecutorName, driver.AddDNSNameserver(action.DNSServer), true, nil
	case "remove_dns_nameserver":
		return executor.VyOSAPIApplyExecutorName, driver.RemoveDNSNameserver(action.DNSServer), true, nil
	case "add_dns_search":
		return executor.VyOSAPIApplyExecutorName, driver.AddDNSSearch(action.DNSDomain), true, nil
	case "remove_dns_search":
		return executor.VyOSAPIApplyExecutorName, driver.RemoveDNSSearch(action.DNSDomain), true, nil

	// NAT
	case "enable_snat":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.EnableSNAT(g), true, nil
	case "disable_snat":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.DisableSNAT(g), true, nil
	case "enable_dnat":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.EnableDNAT(g, action.ExternalPort, action.InternalIP, action.InternalPort, action.Protocol), true, nil
	case "disable_dnat":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.DisableDNAT(g, action.ExternalPort, action.InternalIP, action.InternalPort, action.Protocol), true, nil

	// OSPF
	case "ospf_add_network":
		return executor.VyOSAPIApplyExecutorName, driver.OSPFAddNetwork(action.CIDR, action.OSPFArea), true, nil
	case "ospf_remove_network":
		return executor.VyOSAPIApplyExecutorName, driver.OSPFRemoveNetwork(action.CIDR, action.OSPFArea), true, nil
	case "ospf_set_router_id":
		return executor.VyOSAPIApplyExecutorName, driver.OSPFSetRouterID(action.RouterID), true, nil
	case "ospf_remove_router_id":
		return executor.VyOSAPIApplyExecutorName, driver.OSPFRemoveRouterID(), true, nil
	case "ospf_passive_default":
		return executor.VyOSAPIApplyExecutorName, driver.OSPFPassiveDefault(), true, nil
	case "ospf_remove_passive_default":
		return executor.VyOSAPIApplyExecutorName, driver.OSPFRemovePassiveDefault(), true, nil
	case "ospf_no_passive":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.OSPFNoPassive(g), true, nil
	case "ospf_remove_no_passive":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.OSPFRemoveNoPassive(g), true, nil
	case "ospf_originate_default":
		return executor.VyOSAPIApplyExecutorName, driver.OSPFOriginateDefault(), true, nil
	case "ospf_remove_originate_default":
		return executor.VyOSAPIApplyExecutorName, driver.OSPFRemoveOriginateDefault(), true, nil
	case "ospf_mtu_ignore":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.OSPFMTUIgnore(g), true, nil
	case "ospf_remove_mtu_ignore":
		g, err := resolveIface(action.Iface)
		if err != nil {
			return "", nil, true, err
		}
		return executor.VyOSAPIApplyExecutorName, driver.OSPFRemoveMTUIgnore(g), true, nil

	default:
		return "", nil, false, nil
	}
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
	cur := interface{}(cfg)
	for _, key := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return "", false
		}
		cur, ok = m[key]
		if !ok {
			return "", false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, true
	case []interface{}:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s, true
			}
		}
	}
	return "", false
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
	if ip, ok := cfgLeaf(node, "address"); ok {
		entry["ipv4"] = ip
	}
	return entry
}

// GetEffectiveInterfaces implements types.EffectiveInterfaceInspector.
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

// GetEffectiveInterface implements types.EffectiveSingleInterfaceInspector.
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

// GetEffectiveInterfaceStates implements types.EffectiveInterfaceStateInspector.
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
