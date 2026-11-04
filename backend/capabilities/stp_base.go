package capabilities

import (
	"sort"
	"strconv"
	"strings"
)

// STPBase is the Linux bridge implementation: classic STP, what the kernel
// runs, set through iproute2 (busybox's ip has no bridge options and the
// container's sysfs is read-only) and read from sysfs, which any image can.
type STPBase struct{}

func (STPBase) STPModes() []string { return []string{"stp"} }

func (STPBase) STPEnable(bridge, mode string) [][]string {
	return [][]string{{"ip", "link", "set", bridge, "type", "bridge", "stp_state", "1"}}
}

func (STPBase) STPDisable(bridge string) [][]string {
	return [][]string{{"ip", "link", "set", bridge, "type", "bridge", "stp_state", "0"}}
}

func (STPBase) STPSetBridgePriority(bridge string, priority int) [][]string {
	return [][]string{{"ip", "link", "set", bridge, "type", "bridge", "priority", strconv.Itoa(priority)}}
}

func (STPBase) STPSetPort(port string, cost, priority int) [][]string {
	args := []string{"bridge", "link", "set", "dev", port}
	if cost > 0 {
		args = append(args, "cost", strconv.Itoa(cost))
	}
	if priority >= 0 {
		args = append(args, "priority", strconv.Itoa(priority))
	}
	return [][]string{args}
}

// stpSysfsScript prints one line per bridge (B) and per port (P) from sysfs.
const stpSysfsScript = `for b in /sys/class/net/*/bridge; do [ -d "$b" ] || continue; d=${b%/bridge}; n=${d##*/}; echo "B $n $(cat $b/stp_state) $(cat $b/priority) $(cat $b/bridge_id) $(cat $b/root_id) $(cat $b/root_port) $(cat $b/root_path_cost)"; done; for p in /sys/class/net/*/brport; do [ -d "$p" ] || continue; d=${p%/brport}; n=${d##*/}; br=$(readlink $p/bridge); echo "P $n ${br##*/} $(cat $p/state) $(cat $p/path_cost) $(cat $p/priority) $(cat $p/port_no)"; done`

func (STPBase) STPBridges(namespace, pod string) (Rows, error) {
	out, err := netnsReader(namespace, pod)([]string{"sh", "-c", stpSysfsScript})
	if err != nil {
		return nil, err
	}
	bridges, _ := parseSTPSysfs(string(out))
	return bridges, nil
}

func (STPBase) STPPorts(namespace, pod string) (Rows, error) {
	out, err := netnsReader(namespace, pod)([]string{"sh", "-c", stpSysfsScript})
	if err != nil {
		return nil, err
	}
	_, ports := parseSTPSysfs(string(out))
	return ports, nil
}

var linuxSTPStates = map[string]string{"0": "disabled", "1": "listening", "2": "learning", "3": "forwarding", "4": "blocking"}

// parseSTPSysfs reads the script's lines. The kernel keeps no port role, so
// it is told from the bridge's root port and the port's state: the root
// port, a blocking port is an alternate, the rest designated.
func parseSTPSysfs(text string) (bridges, ports Rows) {
	type bridgeInfo struct {
		stp      bool
		rootPort int
	}
	type portLine struct {
		name, bridge, state, cost, priority string
		number                              int
	}
	info := map[string]*bridgeInfo{}
	portNames := map[string]map[int]string{}
	var portLines []portLine
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 8 && f[0] == "B":
			rootPort, _ := strconv.Atoi(f[6])
			info[f[1]] = &bridgeInfo{stp: f[2] == "1", rootPort: rootPort}
			mode, root := "off", "no"
			if f[2] == "1" {
				mode = "stp"
			}
			if f[4] == f[5] {
				root = "yes"
			}
			bridges = append(bridges, map[string]string{"bridge": f[1], "mode": mode, "priority": f[3], "bridge_id": f[4], "root_id": f[5], "root": root, "root_port": ""})
		case len(f) == 7 && f[0] == "P":
			number, _ := strconv.ParseInt(strings.TrimPrefix(f[6], "0x"), 16, 32)
			if portNames[f[2]] == nil {
				portNames[f[2]] = map[int]string{}
			}
			portNames[f[2]][int(number)] = f[1]
			portLines = append(portLines, portLine{name: f[1], bridge: f[2], state: linuxSTPStates[f[3]], cost: f[4], priority: f[5], number: int(number)})
		}
	}
	for _, p := range portLines {
		role := ""
		if b := info[p.bridge]; b != nil && b.stp {
			switch {
			case p.number == b.rootPort:
				role = "root"
			case p.state == "blocking":
				role = "alternate"
			default:
				role = "designated"
			}
		}
		ports = append(ports, map[string]string{"bridge": p.bridge, "port": p.name, "role": role, "state": p.state, "cost": p.cost, "priority": p.priority})
	}
	for _, b := range bridges {
		if i := info[b["bridge"]]; i != nil && i.rootPort != 0 {
			b["root_port"] = portNames[b["bridge"]][i.rootPort]
		}
	}
	sort.Slice(ports, func(i, j int) bool {
		return ports[i]["bridge"]+" "+ports[i]["port"] < ports[j]["bridge"]+" "+ports[j]["port"]
	})
	return bridges, ports
}
