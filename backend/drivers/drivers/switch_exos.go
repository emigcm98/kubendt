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

// ReadinessProbeCommands makes the pod Ready once the API answers and the
// switch reports itself operational, about ninety seconds after boot.
func (EXOSSwitchDriver) ReadinessProbeCommands() drivers_meta.ReadinessProbeSpec {
	return drivers_meta.ReadinessProbeSpec{
		Command:             []string{"sh", "-c", "exos_show 'show switch' | grep -q OPERATIONAL"},
		InitialDelaySeconds: 40,
		PeriodSeconds:       5,
		TimeoutSeconds:      8,
		FailureThreshold:    24,
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
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
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
		n, _ := o["port"].(float64)
		flags, _ := o["flags"].(string)
		link, _ := o["linkState"].(float64)
		ports = append(ports, exosPort{number: int(n), enabled: strings.HasPrefix(flags, "E"), link: link == 1})
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
		port, _ := o["port_list"].(float64)
		vlan, _ := o["vlan_id"].(float64)
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
