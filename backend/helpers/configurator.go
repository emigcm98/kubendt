package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"kubendt/capabilities/capabilities"
	drivers_registry "kubendt/drivers/registry"
	"kubendt/executor"
	"kubendt/kubeclient"
	"kubendt/types"
	"log"
	"slices"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NormalizeCustomCommand converts the raw JSON command field into a []string
// ready for kubectl exec. A JSON string is wrapped in ["sh", "-c", <cmd>] so
// that pipes, redirects and shell builtins work. A JSON array is used as-is.
func NormalizeCustomCommand(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("custom action requires a non-empty 'command' field")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("custom action 'command' string must not be empty")
		}
		return []string{"sh", "-c", s}, nil
	}
	var args []string
	if err := json.Unmarshal(raw, &args); err == nil {
		if len(args) == 0 {
			return nil, fmt.Errorf("custom action 'command' array must not be empty")
		}
		return args, nil
	}
	return nil, fmt.Errorf("custom action 'command' must be a string or an array of strings")
}

// ResolveActionFlags returns ActionFlags for one action.
// Defaults are persist=true and get=false, and per-action options override them.
func ResolveActionFlags(action types.ActionEntry) types.ActionFlags {
	flags := types.DefaultActionFlags()
	if action.Options == nil {
		return flags
	}
	if action.Options.PersistHistory != nil {
		flags.Persist = *action.Options.PersistHistory
	}
	if action.Options.CaptureOutput != nil {
		flags.Get = *action.Options.CaptureOutput
	}
	return flags
}

func GetDriverForPod(namespace, podName string) (interface{}, error) {
	pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return GetDriverForListedPod(pod)
}

func GetDriverForListedPod(pod *v1.Pod) (interface{}, error) {
	if pod == nil {
		return nil, fmt.Errorf("pod is nil")
	}

	drvName := pod.Labels["kubendt/driver"]
	if drvName == "" {
		return nil, fmt.Errorf("pod %s/%s has no label kubendt/driver", pod.Namespace, pod.Name)
	}

	drv, err := drivers_registry.NewByName(drvName)
	if err != nil {
		return nil, fmt.Errorf("driver %q not registered for pod %s/%s: %w", drvName, pod.Namespace, pod.Name, err)
	}

	return drv, nil
}

func ResolveDriverCommands(driver interface{}, action types.ActionEntry) [][]string {
	commands, err := ResolveDriverCommandsForPod("", "", driver, action)
	if err != nil {
		log.Printf("❌ Could not resolve action '%s': %v", action.Type, err)
		return nil
	}
	return commands
}

// checkProtectedInterface rejects actions targeting eth0 (the pod's cluster
// interface) except the SNAT toggles, the supported way to give a topology
// internet access. Runs before the driver planners so VyOS/XRd are covered too.
func checkProtectedInterface(action types.ActionEntry) error {
	const protectedInterface = "eth0"
	protected := action.Iface == protectedInterface || action.Device == protectedInterface || slices.Contains(action.Ifaces, protectedInterface)
	if !protected {
		return nil
	}

	allowedOnEth0 := map[string]bool{
		"enable_snat":  true,
		"disable_snat": true,
	}
	if allowedOnEth0[action.Type] {
		return nil
	}

	return fmt.Errorf("action '%s' is not allowed on protected interface eth0 (only enable_snat/disable_snat)", action.Type)
}

// ResolveDriverExecutionPlanForPod resolves commands and the executor name to
// use for one action on one pod. Drivers may override this via
// EffectiveActionExecutionPlanResolver.
func ResolveDriverExecutionPlanForPod(namespace, podName string, driver interface{}, action types.ActionEntry) (string, [][]string, error) {
	if err := checkProtectedInterface(action); err != nil {
		log.Printf("❌ %v", err)
		return "", nil, err
	}

	// Traffic shaping is universal: tc runs in the pod netns, so it resolves the
	// same for every driver and executes via the tc_universal executor (the
	// pod's own tc, or an ephemeral toolbox for images that ship none).
	if action.Type == "add_qdisc" || action.Type == "del_qdisc" {
		commands, err := ResolveDriverCommandsForPod(namespace, podName, driver, action)
		if err != nil {
			return "", nil, err
		}
		return executor.TCUniversalExecutorName, commands, nil
	}

	if planner, ok := driver.(types.EffectiveActionExecutionPlanResolver); ok {
		executorName, commands, handled, err := planner.ResolveActionExecutionPlan(namespace, podName, action)
		if err != nil {
			return "", nil, err
		}
		if handled {
			if strings.TrimSpace(executorName) == "" {
				executorName = executor.DefaultExecutorName
			}
			return executorName, commands, nil
		}
	}

	commands, err := ResolveDriverCommandsForPod(namespace, podName, driver, action)
	if err != nil {
		return "", nil, err
	}

	_, executorName, err := executor.ResolveForDriver(driver)
	if err != nil {
		return "", nil, err
	}

	return executorName, commands, nil
}

func ResolveDriverCommandsForPod(namespace, podName string, driver interface{}, action types.ActionEntry) ([][]string, error) {

	// custom bypasses the driver/capability system entirely.
	if action.Type == "custom" {
		args, err := NormalizeCustomCommand(action.Command)
		if err != nil {
			return nil, err
		}
		return [][]string{args}, nil
	}

	if err := checkProtectedInterface(action); err != nil {
		return nil, err
	}

	// L2 actions (switch, hosts, routers)
	if l2, ok := driver.(capabilities.L2Capable); ok {
		switch action.Type {
		case "link_up":
			return l2.LinkUp(action.Iface), nil
		case "link_down":
			return l2.LinkDown(action.Iface), nil
		}
	}

	// L3 actions (hosts, routers)
	if l3, ok := driver.(capabilities.L3Capable); ok {
		switch action.Type {
		case "set_ip":
			return l3.SetIP(action.Iface, action.CIDR), nil
		case "replace_ip":
			return l3.ReplaceIP(action.Iface, action.CIDR), nil
		case "remove_ip":
			return l3.RemoveIP(action.Iface, action.CIDR), nil
		case "set_default_route":
			return l3.SetDefaultRoute(action.Gateway), nil
		case "remove_default_route":
			return l3.RemoveDefaultRoute(), nil
		case "add_static_route":
			return l3.AddStaticRoute(action.DstCIDR, action.Gateway, action.Device), nil
		case "remove_static_route":
			return l3.RemoveStaticRoute(action.DstCIDR, action.Gateway, action.Device), nil
		case "add_dns_nameserver":
			return l3.AddDNSNameserver(action.DNSServer), nil
		case "remove_dns_nameserver":
			return l3.RemoveDNSNameserver(action.DNSServer), nil
		case "add_dns_search":
			return l3.AddDNSSearch(action.DNSDomain), nil
		case "remove_dns_search":
			return l3.RemoveDNSSearch(action.DNSDomain), nil
		}
	}

	// NAT actions (NAT, only routers)
	if nat, ok := driver.(capabilities.NATCapable); ok {
		switch action.Type {
		case "enable_snat":
			return nat.EnableSNAT(action.Iface), nil
		case "disable_snat":
			return nat.DisableSNAT(action.Iface), nil
		case "enable_dnat":
			return nat.EnableDNAT(action.Iface, action.ExternalPort, action.InternalIP, action.InternalPort, action.Protocol), nil
		case "disable_dnat":
			return nat.DisableDNAT(action.Iface, action.ExternalPort, action.InternalIP, action.InternalPort, action.Protocol), nil
		}
	}

	// TC actions (traffic shaping) are universal: tc runs in the pod netns, so
	// any pod can be shaped without a driver capability. The command builder
	// stays shared; execution is routed to the tc_universal executor by
	// ResolveDriverExecutionPlanForPod.
	switch action.Type {
	case "add_qdisc":
		if action.TCParams == nil {
			log.Println("❌ TCParams not defined in add_qdisc action")
			return nil, nil
		} else if action.TCParams.Qdisc == "" {
			log.Println("❌ Qdisc not specified")
			return nil, nil
		}
		validQdiscs := map[string]bool{"netem": true, "tbf": true}
		if !validQdiscs[action.TCParams.Qdisc] {
			log.Printf("❌ Qdisc '%s' not allowed. Supported: [netem, tbf]", action.TCParams.Qdisc)
			return nil, nil
		}
		params := buildTCParamsFromStruct(action.TCParams)
		return buildAddQdiscCommand(action.Iface, action.TCParams.Qdisc, params), nil
	case "del_qdisc":
		return buildDelQdiscCommand(action.Iface), nil
	}

	// Switch actions (linux switch + ovswitch)
	if sw, ok := driver.(capabilities.SwitchCapable); ok {

		switch action.Type {
		case "setup_bridge":
			return sw.SetupBridge(action.Bridge, action.Ifaces), nil
		case "teardown_bridge":
			return sw.TeardownBridge(action.Bridge), nil
		case "add_interface_to_bridge":
			return sw.AddInterfaceToBridge(action.Iface, action.Bridge), nil
		case "remove_interface_from_bridge":
			return sw.RemoveInterfaceFromBridge(action.Iface, action.Bridge), nil
		}
	}

	// OSPF actions (FRR and other OSPF-capable routers)
	if ospf, ok := driver.(capabilities.OSPFCapable); ok {
		switch action.Type {
		case "ospf_add_network":
			return ospf.OSPFAddNetwork(action.CIDR, action.OSPFArea), nil
		case "ospf_remove_network":
			return ospf.OSPFRemoveNetwork(action.CIDR, action.OSPFArea), nil
		case "ospf_set_router_id":
			return ospf.OSPFSetRouterID(action.RouterID), nil
		case "ospf_remove_router_id":
			return ospf.OSPFRemoveRouterID(), nil
		case "ospf_passive_default":
			return ospf.OSPFPassiveDefault(), nil
		case "ospf_remove_passive_default":
			return ospf.OSPFRemovePassiveDefault(), nil
		case "ospf_no_passive":
			return ospf.OSPFNoPassive(action.Iface), nil
		case "ospf_remove_no_passive":
			return ospf.OSPFRemoveNoPassive(action.Iface), nil
		case "ospf_originate_default":
			return ospf.OSPFOriginateDefault(), nil
		case "ospf_remove_originate_default":
			return ospf.OSPFRemoveOriginateDefault(), nil
		case "ospf_mtu_ignore":
			return ospf.OSPFMTUIgnore(action.Iface), nil
		case "ospf_remove_mtu_ignore":
			return ospf.OSPFRemoveMTUIgnore(action.Iface), nil
		}
	}

	// No implemented capability produced commands. Distinguish a genuinely
	// unknown action (typo) from a known action whose capability this driver /
	// node type does not support (e.g. an L3 op like set_ip on a switch), so
	// the caller can surface a precise message instead of a generic one.
	if capName, known := capabilities.ActionCapability(action.Type); known {
		name, typ := driverNameType(driver)
		return nil, fmt.Errorf("node '%s' (type %s, driver %s) does not support %s operations, so action %q cannot be applied", podName, typ, name, capName, action.Type)
	}

	return nil, nil
}

// driverNameType best-effort extracts a driver's registered name and logical
// type for error messages.
func driverNameType(driver interface{}) (name, typ string) {
	if d, ok := driver.(interface {
		Name() string
		Type() string
	}); ok {
		return d.Name(), d.Type()
	}
	return "unknown", "unknown"
}

func buildTCParamsFromStruct(p *types.TCParamEntry) []string {
	args := []string{}

	add := func(k, v string) {
		if v != "" {
			args = append(args, k, v)
		}
	}

	addNumeric := func(k string, val *int) {
		if val != nil && *val > 0 {
			args = append(args, k, fmt.Sprintf("%d", *val))
		}
	}

	// Validations
	if p.Qdisc == "" {
		log.Println("⚠️ Empty qdisc, defaulting to 'netem'")
		p.Qdisc = "netem"
	}

	// --- NETEM ---
	if p.Qdisc == "netem" {

		// Jitter without delay → invalid
		if p.Jitter != "" && p.Delay == "" {
			log.Printf("⚠️ Ignoring jitter='%s' because there is no delay defined", p.Jitter)
			p.Jitter = ""
		}

		// Validate numeric values (milliseconds or %)
		parseInt := func(s string) int {
			val, _ := strconv.Atoi(strings.TrimSuffix(s, "ms"))
			return val
		}
		parseFloat := func(s string) float64 {
			f, _ := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
			return f
		}

		// Delay: 0–60000 ms
		if p.Delay != "" {
			delay := parseInt(p.Delay)
			if delay < 0 || delay > 60000 {
				log.Printf("⚠️ Ignoring invalid delay: %s (out of range 0–60000ms)", p.Delay)
				p.Delay = ""
			}
		}

		// Jitter: 0–10000 ms
		if p.Jitter != "" {
			jitter := parseInt(p.Jitter)
			if jitter < 0 || jitter > 10000 {
				log.Printf("⚠️ Ignoring invalid jitter: %s (out of range 0–10000ms)", p.Jitter)
				p.Jitter = ""
			}
		}

		// Loss: 0–100 %
		if p.Loss != "" {
			loss := parseFloat(p.Loss)
			if loss < 0 || loss > 100 {
				log.Printf("⚠️ Ignoring invalid loss: %s (out of range 0–100%%)", p.Loss)
				p.Loss = ""
			}
		}

		// Duplicate: 0–100 %
		if p.Duplicate != "" {
			dup := parseFloat(p.Duplicate)
			if dup < 0 || dup > 100 {
				log.Printf("⚠️ Ignoring invalid duplicate: %s (out of range 0–100%%)", p.Duplicate)
				p.Duplicate = ""
			}
		}

		// Corrupt: 0–100 %
		if p.Corrupt != "" {
			cor := parseFloat(p.Corrupt)
			if cor < 0 || cor > 100 {
				log.Printf("⚠️ Ignoring invalid corrupt: %s (out of range 0–100%%)", p.Corrupt)
				p.Corrupt = ""
			}
		}

		// Limit: 100–10000
		if p.Limit != nil {
			if *p.Limit < 100 || *p.Limit > 10000 {
				log.Printf("⚠️ Ignoring invalid limit: %d (out of range 100–10000)", *p.Limit)
				p.Limit = nil
			}
		}

		// --- netem construction ---
		if p.Delay != "" {
			args = append(args, "delay", p.Delay)
			if p.Jitter != "" {
				args = append(args, p.Jitter)
			}
		}
		add("loss", p.Loss)
		add("duplicate", p.Duplicate)
		add("corrupt", p.Corrupt)
		addNumeric("limit", p.Limit)
	}

	// --- TBF ---
	if p.Qdisc == "tbf" {

		// Basic validations (rate required)
		if p.Rate == "" {
			log.Println("⚠️ Qdisc tbf requires 'rate'")
			return args
		}

		// Burst: no negativo, valor razonable (1–100000kbit)
		if p.Burst != "" {
			val := strings.TrimSuffix(p.Burst, "Kb")
			n, _ := strconv.Atoi(val)
			if n < 1 || n > 100000 {
				log.Printf("⚠️ Ignoring invalid burst: %s (out of range 1–100000Kb)", p.Burst)
				p.Burst = ""
			}
		}

		// Latency: 1–5000ms
		if p.Latency != "" {
			val, _ := strconv.Atoi(strings.TrimSuffix(p.Latency, "ms"))
			if val < 1 || val > 5000 {
				log.Printf("⚠️ Ignoring invalid latency: %s (out of range 1–5000ms)", p.Latency)
				p.Latency = ""
			}
		}

		// --- tbf construction ---
		add("rate", p.Rate)
		add("burst", p.Burst)
		add("latency", p.Latency)
	}

	return args
}
