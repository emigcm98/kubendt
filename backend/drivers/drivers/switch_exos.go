package drivers

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/executor"
	"kubendt/kubeclient"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// EXOSSwitchDriver runs Extreme's Switch Engine (EXOS) VM as a QEMU guest in
// the pod, the vendor switch next to VyOS as the vendor router: a simple
// lab switch, VLANs and spanning tree, no OpenFlow or LACP in the VM. The
// guest numbers its ports by NIC order and the pod's entrypoint writes
// which interface became which port; a VLAN is its bridging domain, so a
// bridge is a VLAN with untagged ports. Everything goes through the guest's
// JSON-RPC API over the exos_api wrapper, the serial console is the shell.
type EXOSSwitchDriver struct {
	drivers_meta.Meta
	executor.ExecutorMeta
}

func NewEXOSSwitchDriver() *EXOSSwitchDriver {
	return &EXOSSwitchDriver{
		Meta:         drivers_meta.NewMeta("EXOSSwitchDriver", "switch"),
		ExecutorMeta: executor.NewExecutorMeta(executor.EXOSAPIExecutorName),
	}
}

var _ capabilities.L2Capable = (*EXOSSwitchDriver)(nil)
var _ capabilities.SwitchCapable = (*EXOSSwitchDriver)(nil)
var _ capabilities.VLANCapable = (*EXOSSwitchDriver)(nil)
var _ capabilities.VLANInspector = (*EXOSSwitchDriver)(nil)
var _ capabilities.STPCapable = (*EXOSSwitchDriver)(nil)
var _ capabilities.STPInspector = (*EXOSSwitchDriver)(nil)
var _ drivers_meta.RuntimeProvider = (*EXOSSwitchDriver)(nil)
var _ drivers_meta.DeviceProvider = (*EXOSSwitchDriver)(nil)
var _ drivers_meta.ReadinessProbeProvider = (*EXOSSwitchDriver)(nil)
var _ drivers_meta.InterfaceNameConstrainer = (*EXOSSwitchDriver)(nil)
var _ drivers_meta.GuestInterfaceMapper = (*EXOSSwitchDriver)(nil)
var _ drivers_meta.GuestMTUSetter = (*EXOSSwitchDriver)(nil)
var _ drivers_meta.EffectiveInterfaceInspector = (*EXOSSwitchDriver)(nil)
var _ drivers_meta.EffectiveInterfaceStateInspector = (*EXOSSwitchDriver)(nil)
var _ drivers_meta.KnownImagesProvider = (*EXOSSwitchDriver)(nil)
var _ capabilities.FDBInspector = (*EXOSSwitchDriver)(nil)

func (EXOSSwitchDriver) Runtime() string { return drivers_meta.RuntimeQEMU }

func (EXOSSwitchDriver) Devices() []string { return []string{"/dev/kvm"} }

// The VM takes twelve ports; the names keep the platform's ethN form, the
// port each one gets comes from the pod's map.
var exosIfaceNameRe = regexp.MustCompile(`^eth([1-9]|1[0-2])$`)

func (EXOSSwitchDriver) InterfaceNameConstraints() drivers_meta.InterfaceNameConstraints {
	return drivers_meta.InterfaceNameConstraints{
		Pattern:      exosIfaceNameRe,
		PatternHuman: `^eth([1-9]|1[0-2])$ (eth1 to eth12, the VM has twelve ports)`,
		Reserved:     []string{"eth0"},
	}
}

// exosPortMap is what the entrypoint wrote at /run/kubendt/exos-ports, the
// pod interface each guest port came from, read once per pod incarnation.
type exosPortMap struct {
	uid     k8stypes.UID
	byIface map[string]string
	byPort  map[string]string
}

var exosPortMaps sync.Map // "namespace/pod" -> *exosPortMap

var exosIdentityPortMap = func() *exosPortMap {
	m := &exosPortMap{byIface: map[string]string{}, byPort: map[string]string{}}
	for i := 1; i <= 12; i++ {
		n := strconv.Itoa(i)
		m.byIface["eth"+n], m.byPort[n] = n, "eth"+n
	}
	return m
}()

const exosPortMapPath = "/run/kubendt/exos-ports"

func parseExosPortMap(text string) *exosPortMap {
	m := &exosPortMap{byIface: map[string]string{}, byPort: map[string]string{}}
	for _, line := range strings.Split(text, "\n") {
		iface, port, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || iface == "" || port == "" {
			continue
		}
		m.byIface[iface] = port
		m.byPort[port] = iface
	}
	return m
}

func exosPortMapFor(namespace, pod string) (*exosPortMap, error) {
	if kubeclient.Clientset == nil {
		// No cluster, as the golden plan tests run: ethN is port N.
		return exosIdentityPortMap, nil
	}
	p, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), pod, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	key := namespace + "/" + pod
	if v, ok := exosPortMaps.Load(key); ok && v.(*exosPortMap).uid == p.UID {
		return v.(*exosPortMap), nil
	}
	out, err := executor.ExecAndGet(namespace, pod, []string{"cat", exosPortMapPath})
	if err != nil {
		return nil, fmt.Errorf("the port map of %s is not readable yet: %w", pod, err)
	}
	m := parseExosPortMap(string(out))
	m.uid = p.UID
	exosPortMaps.Store(key, m)
	return m, nil
}

// GuestInterface is the guest port of a pod interface, from the pod's map.
func (EXOSSwitchDriver) GuestInterface(namespace, podName, podInterface string) (string, error) {
	if !exosIfaceNameRe.MatchString(podInterface) {
		return "", fmt.Errorf("interface %q is not a port of the switch (eth1 to eth12)", podInterface)
	}
	m, err := exosPortMapFor(namespace, podName)
	if err != nil {
		return "", err
	}
	port, ok := m.byIface[podInterface]
	if !ok {
		return "", fmt.Errorf("%s was not there when the switch booted, so the guest has no port for it: restart the node", podInterface)
	}
	return port, nil
}

// ReadinessProbeCommands makes the pod Ready once the entrypoint has sent
// its startup lines (the flag) and the API answers that the switch is
// operational, some ninety seconds after boot most times; one boot in four or
// five stalls 150 s and the entrypoint reboots the guest, so the budget is
// nine minutes, two of those in a row, and the deploy's wait follows it
// rather than rolling a slow switch back.
func (EXOSSwitchDriver) ReadinessProbeCommands() drivers_meta.ReadinessProbeSpec {
	return drivers_meta.ReadinessProbeSpec{
		Command:             []string{"sh", "-c", "test -f /run/exos/ready && exos_show 'show switch' | grep -q OPERATIONAL"},
		InitialDelaySeconds: 40,
		PeriodSeconds:       5,
		TimeoutSeconds:      8,
		FailureThreshold:    100,
	}
}

// GuestMTUCommands: the guest forwards 1500-byte frames as it is and the
// platform's MTU sits below that, so there is nothing to set inside.
func (EXOSSwitchDriver) GuestMTUCommands(iface string, mtu int) [][]string { return nil }

func (EXOSSwitchDriver) KnownImages() []string { return []string{"localhost/exos-switch:dev"} }

// --- L2 ---

func (EXOSSwitchDriver) LinkUp(port string) [][]string { return [][]string{{"enable ports " + port}} }
func (EXOSSwitchDriver) LinkDown(port string) [][]string {
	return [][]string{{"disable ports " + port}}
}

// --- switching: a VLAN as the bridge ---

// SetupBridge creates the VLAN and moves the ports into it untagged, which
// takes them out of the Default VLAN they start in.
func (EXOSSwitchDriver) SetupBridge(bridge string, ports []string) [][]string {
	cmds := [][]string{{"create vlan " + bridge}}
	if len(ports) > 0 {
		cmds = append(cmds, []string{"configure vlan " + bridge + " add ports " + strings.Join(ports, ",") + " untagged"})
	}
	return cmds
}

// TeardownBridge deletes the VLAN; its ports belong to none afterwards and
// forward nothing, as the ports of a deleted Linux bridge.
func (EXOSSwitchDriver) TeardownBridge(bridge string) [][]string {
	return [][]string{{"delete vlan " + bridge}}
}

func (EXOSSwitchDriver) AddInterfaceToBridge(port, bridge string) [][]string {
	return [][]string{{"configure vlan " + bridge + " add ports " + port + " untagged"}}
}

func (EXOSSwitchDriver) RemoveInterfaceFromBridge(port, bridge string) [][]string {
	return [][]string{{"configure vlan " + bridge + " delete ports " + port}}
}

// --- the ports as the UI sees them ---

// exosShow runs a show command through exos_show and returns the objects
// of the reply that carry the given key.
func exosShow(namespace, pod, command, key string) ([]map[string]any, error) {
	out, err := executor.ExecAndGet(namespace, pod, []string{"exos_show", command})
	if err != nil {
		return nil, err
	}
	return exosObjects(string(out), key)
}

func exosObjects(body, key string) ([]map[string]any, error) {
	var reply struct {
		Result []map[string]any `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&reply); err != nil {
		return nil, fmt.Errorf("exos api: %w", err)
	}
	if reply.Error != nil {
		return nil, fmt.Errorf("exos api: %s", reply.Error.Message)
	}
	var out []map[string]any
	for _, r := range reply.Result {
		if obj, ok := r[key].(map[string]any); ok {
			out = append(out, obj)
		}
	}
	return out, nil
}

// exosNum reads a number of a reply, decoded as json.Number; exosStr the
// text of any scalar, a bridge id among them, whose digits a float would
// have lost.
func exosNum(v any) float64 {
	switch x := v.(type) {
	case json.Number:
		f, _ := x.Float64()
		return f
	case float64:
		return x
	}
	return 0
}

func exosStr(v any) string {
	switch x := v.(type) {
	case json.Number:
		return x.String()
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

type exosPort struct {
	number  int
	enabled bool
	link    bool
}

// exosPorts reads `show ports information`: the flags open with E or D
// for the admin state, linkState is 1 when the link is active. Only ports
// with a NIC are listed, which is every pod interface.
func exosPorts(namespace, pod string) ([]exosPort, error) {
	objs, err := exosShow(namespace, pod, "show ports information", "show_ports_info")
	if err != nil {
		return nil, err
	}
	var ports []exosPort
	for _, o := range objs {
		flags, _ := o["flags"].(string)
		ports = append(ports, exosPort{number: int(exosNum(o["port"])), enabled: strings.HasPrefix(flags, "E"), link: exosNum(o["linkState"]) == 1})
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].number < ports[j].number })
	return ports, nil
}

func (EXOSSwitchDriver) GetEffectiveInterfaces(namespace, podName string) ([]map[string]string, error) {
	ports, err := exosPorts(namespace, podName)
	if err != nil {
		return nil, err
	}
	m, err := exosPortMapFor(namespace, podName)
	if err != nil {
		return nil, err
	}
	var result []map[string]string
	for _, p := range ports {
		iface, ok := m.byPort[strconv.Itoa(p.number)]
		if !ok {
			continue
		}
		state := "down"
		if p.enabled {
			state = "up"
		}
		result = append(result, map[string]string{"interface": iface, "guestInterface": strconv.Itoa(p.number), "state": state})
	}
	return result, nil
}

// GetEffectiveInterfaceStates: the admin state, what the link actions toggle.
func (EXOSSwitchDriver) GetEffectiveInterfaceStates(namespace, podName string) (map[string]bool, error) {
	ports, err := exosPorts(namespace, podName)
	if err != nil {
		return nil, err
	}
	m, err := exosPortMapFor(namespace, podName)
	if err != nil {
		return nil, err
	}
	states := make(map[string]bool, len(ports))
	for _, p := range ports {
		if iface, ok := m.byPort[strconv.Itoa(p.number)]; ok {
			states[iface] = p.enabled
		}
	}
	return states, nil
}

// BridgeFDB reads `show fdb`: one fdb_show object per learned address, the
// VLAN it was learned in as the bridge, the port it sits on, and the flags
// EXOS prints (d dynamic, s static, m MAC).
func (EXOSSwitchDriver) BridgeFDB(namespace, pod string) (capabilities.Rows, error) {
	objs, err := exosShow(namespace, pod, "show fdb", "fdb_show")
	if err != nil {
		return nil, err
	}
	m, err := exosPortMapFor(namespace, pod)
	if err != nil {
		return nil, err
	}
	return exosFDBRows(objs, func(port string) string {
		if iface, ok := m.byPort[port]; ok {
			return iface
		}
		return "port " + port
	}), nil
}

func exosFDBRows(objs []map[string]any, ifaceOf func(port string) string) capabilities.Rows {
	var rows capabilities.Rows
	for _, o := range objs {
		port := exosNum(o["port_list"])
		vlan := exosNum(o["vlan_id"])
		name, _ := o["vlan_name"].(string)
		mac, _ := o["mac_addr"].(string)
		flag, _ := o["flag"].(string)
		vid := strconv.Itoa(int(vlan))
		if vlan >= 4094 {
			vid = "" // an internal tag, the VLAN carries no 802.1Q tag
		}
		rows = append(rows, map[string]string{"bridge": name, "mac": mac, "interface": ifaceOf(strconv.Itoa(int(port))), "vlan": vid, "flags": strings.Join(strings.Fields(flag), " ")})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["interface"]+rows[i]["mac"] < rows[j]["interface"]+rows[j]["mac"] })
	return rows
}

// --- VLANs: a VLAN per tag, named v<tag>; a port's membership is rewritten
// whole through the wrapper's @clear-port, since EXOS has no command that
// takes a port out of every VLAN, and "-create" may find the VLAN there.

func exosVLAN(vlan int) string { return "v" + strconv.Itoa(vlan) }

func (EXOSSwitchDriver) VLANSetAccess(port string, vlan int) [][]string {
	v := exosVLAN(vlan)
	return [][]string{{"@clear-port " + port}, {"-create vlan " + v + " tag " + strconv.Itoa(vlan)}, {"configure vlan " + v + " add ports " + port + " untagged"}}
}

func (EXOSSwitchDriver) VLANSetTrunk(port string, vlans []int, native int) [][]string {
	cmds := [][]string{{"@clear-port " + port}}
	for _, vlan := range vlans {
		if vlan == native {
			continue
		}
		v := exosVLAN(vlan)
		cmds = append(cmds, []string{"-create vlan " + v + " tag " + strconv.Itoa(vlan)}, []string{"configure vlan " + v + " add ports " + port + " tagged"})
	}
	if native != 0 {
		v := exosVLAN(native)
		cmds = append(cmds, []string{"-create vlan " + v + " tag " + strconv.Itoa(native)}, []string{"configure vlan " + v + " add ports " + port + " untagged"})
	}
	return cmds
}

func (EXOSSwitchDriver) VLANClearPort(port string) [][]string {
	return [][]string{{"@clear-port " + port}, {"configure vlan Default add ports " + port + " untagged"}}
}

// VLANPorts reads `show ports vlan`: one object per port and VLAN with
// tagStatus 0 for untagged, 1 for tagged. A VLAN with an internal tag (a
// bridge made with setup_bridge, or Default's 4095 sibling) shows its name.
func (EXOSSwitchDriver) VLANPorts(namespace, pod string) (capabilities.Rows, error) {
	objs, err := exosShow(namespace, pod, "show ports vlan", "show_ports_info_detail_vlans")
	if err != nil {
		return nil, err
	}
	m, err := exosPortMapFor(namespace, pod)
	if err != nil {
		return nil, err
	}
	return exosVLANRows(objs, m), nil
}

func exosVLANRows(objs []map[string]any, m *exosPortMap) capabilities.Rows {
	type membership struct {
		tagged []string
		native string
	}
	ports := map[int]*membership{}
	var order []int
	for _, o := range objs {
		n := int(exosNum(o["port"]))
		if ports[n] == nil {
			ports[n] = &membership{}
			order = append(order, n)
		}
		id := int(exosNum(o["vlanId"]))
		name, _ := o["vlanName"].(string)
		label := strconv.Itoa(id)
		if id >= 4094 {
			label = name
		}
		if exosNum(o["tagStatus"]) == 1 {
			ports[n].tagged = append(ports[n].tagged, label)
		} else {
			ports[n].native = label
		}
	}
	sort.Ints(order)
	var rows capabilities.Rows
	for _, n := range order {
		iface, ok := m.byPort[strconv.Itoa(n)]
		if !ok {
			continue
		}
		p := ports[n]
		mode, vlans := "trunk", strings.Join(p.tagged, ",")
		switch {
		case len(p.tagged) == 0 && p.native == "":
			mode = "none"
		case len(p.tagged) == 0:
			mode, vlans = "access", p.native
		}
		rows = append(rows, map[string]string{"port": iface, "mode": mode, "vlans": vlans, "native": p.native})
	}
	return rows
}

// --- spanning tree: the one domain s0, which the bridge (a VLAN) joins;
// its mode is the domain's, so the last enable sets it for every VLAN in it.

func (EXOSSwitchDriver) STPModes() []string { return []string{"rstp", "stp"} }

// exosSTPVLAN is the VLAN a spanning tree action names: a bridge of
// setup_bridge by its name, a VLAN of the VLAN actions by its tag.
func exosSTPVLAN(bridge string) string {
	if n, err := strconv.Atoi(bridge); err == nil {
		return exosVLAN(n)
	}
	return bridge
}

func (EXOSSwitchDriver) STPEnable(bridge, mode string) [][]string {
	exosMode := "dot1w"
	if mode == "stp" {
		exosMode = "dot1d"
	}
	return [][]string{{"-disable stpd s0"}, {"configure stpd s0 mode " + exosMode}, {"enable stpd s0 auto-bind vlan " + exosSTPVLAN(bridge)}, {"enable stpd s0"}}
}

func (EXOSSwitchDriver) STPDisable(bridge string) [][]string {
	v := exosSTPVLAN(bridge)
	return [][]string{{"-disable stpd s0 auto-bind vlan " + v}, {"-configure stpd s0 delete vlan " + v + " ports all"}}
}

func (EXOSSwitchDriver) STPSetBridgePriority(bridge string, priority int) [][]string {
	return [][]string{{"configure stpd s0 priority " + strconv.Itoa(priority)}}
}

func (EXOSSwitchDriver) STPSetPort(port string, cost, priority int) [][]string {
	var cmds [][]string
	if cost > 0 {
		cmds = append(cmds, []string{"configure stpd s0 ports cost " + strconv.Itoa(cost) + " " + port})
	}
	if priority >= 0 {
		cmds = append(cmds, []string{"configure stpd s0 ports port-priority " + strconv.Itoa(priority) + " " + port})
	}
	return cmds
}

var exosSTPModes = map[string]string{"1": "stp", "2": "rstp", "3": "mstp"}

// exosBridgeID formats the id EXOS prints as digits, priority then MAC,
// into priority.mac.
func exosBridgeID(v any) string {
	s := exosStr(v)
	if len(s) != 16 {
		return s
	}
	mac := s[4:]
	var pairs []string
	for i := 0; i+2 <= len(mac); i += 2 {
		pairs = append(pairs, mac[i:i+2])
	}
	return s[:4] + "." + strings.Join(pairs, ":")
}

func (d EXOSSwitchDriver) STPBridges(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, []string{"exos_show", "show stpd s0"})
	if err != nil {
		return nil, err
	}
	ports, err := d.STPPorts(namespace, pod)
	if err != nil {
		return nil, err
	}
	return exosSTPBridgeRows(string(out), ports)
}

func exosSTPBridgeRows(body string, ports capabilities.Rows) (capabilities.Rows, error) {
	row := map[string]string{"bridge": "s0", "mode": "off", "priority": "", "bridge_id": "", "root_id": "", "root": "no", "root_port": ""}
	if objs, err := exosObjects(body, "stp_domain_enable"); err != nil {
		return nil, err
	} else if len(objs) > 0 && exosNum(objs[0]["stpd_enabled"]) == 1 {
		row["mode"] = exosSTPModes[exosStr(objs[0]["protocol_mode"])]
	}
	if objs, _ := exosObjects(body, "stp_domain"); len(objs) > 0 {
		row["priority"] = exosStr(objs[0]["priority"])
	}
	if objs, _ := exosObjects(body, "stp_domain_stats"); len(objs) > 0 {
		row["bridge_id"] = exosBridgeID(objs[0]["bridge_id"])
		row["root_id"] = exosBridgeID(objs[0]["designated_root"])
		if row["bridge_id"] == row["root_id"] {
			row["root"] = "yes"
		}
	}
	for _, p := range ports {
		if p["role"] == "root" {
			row["root_port"] = p["port"]
		}
	}
	return capabilities.Rows{row}, nil
}

// STPPorts parses the table of `show stpd s0 ports`: port, mode, state,
// cost, flags (the role letter second), priority, port id, bridge. The
// JSON next to it has the same facts as enum numbers nobody documents.
func (EXOSSwitchDriver) STPPorts(namespace, pod string) (capabilities.Rows, error) {
	out, err := executor.ExecAndGet(namespace, pod, []string{"exos_show", "show stpd s0 ports"})
	if err != nil {
		return nil, err
	}
	m, err := exosPortMapFor(namespace, pod)
	if err != nil {
		return nil, err
	}
	text, err := exosCLIText(string(out))
	if err != nil {
		return nil, err
	}
	return exosSTPPortRows(text, m), nil
}

// exosCLIText joins the CLIoutput pieces of a reply.
func exosCLIText(body string) (string, error) {
	var reply struct {
		Result []map[string]any `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		return "", fmt.Errorf("exos api: %w", err)
	}
	var b strings.Builder
	for _, r := range reply.Result {
		if s, ok := r["CLIoutput"].(string); ok {
			b.WriteString(s)
		}
	}
	return b.String(), nil
}

var exosSTPRoles = map[byte]string{'R': "root", 'D': "designated", 'A': "alternate", 'B': "backup", 'M': "master"}

func exosSTPPortRows(text string, m *exosPortMap) capabilities.Rows {
	var rows capabilities.Rows
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || !strings.HasPrefix(f[1], "802.") {
			continue
		}
		iface, ok := m.byPort[f[0]]
		if !ok {
			continue
		}
		role := ""
		if len(f[4]) > 1 {
			role = exosSTPRoles[f[4][1]]
		}
		rows = append(rows, map[string]string{"bridge": "s0", "port": iface, "role": role, "state": strings.ToLower(f[2]), "cost": f[3], "priority": f[5]})
	}
	return rows
}
