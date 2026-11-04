package capabilities

import (
	"strconv"
	"strings"
)

// VLANBase is the Linux bridge implementation: VLAN filtering goes on with
// the first rule, on the bridge the port belongs to, and a port's VLANs are
// rewritten whole so a set replaces what was there. A cleared port goes back
// to VLAN 1 untagged, where every port starts. The bridge device itself
// takes the same rules, with `self`: a trunk on it is what a `br0.10`
// subinterface, the SVI, hangs off.
type VLANBase struct{}

// vlanPrelude finds where the device's VLANs live, its bridge for a port and
// itself for a bridge (busybox and iproute2 print `master <bridge>` alike),
// turns filtering on there and sets $s to `self` for a bridge device.
func vlanPrelude(dev string) string {
	return "if [ -d /sys/class/net/" + dev + "/bridge ]; then b=" + dev + "; s=self; else b=$(ip link show " + dev + " | sed -n 's/.* master \\([^ ]*\\).*/\\1/p' | head -1); s=; fi; [ -n \"$b\" ] && ip link set \"$b\" type bridge vlan_filtering 1"
}

func vlanFlush(dev string) string {
	return "bridge vlan del dev " + dev + " vid 1-4094 $s"
}

func vlanAdd(dev string, vlan int, flags string) string {
	return "bridge vlan add dev " + dev + " vid " + strconv.Itoa(vlan) + flags + " $s"
}

func (VLANBase) VLANSetAccess(port string, vlan int) [][]string {
	cmd := vlanPrelude(port) + "; " + vlanFlush(port) + "; " + vlanAdd(port, vlan, " pvid untagged")
	return [][]string{{"sh", "-c", cmd}}
}

func (VLANBase) VLANSetTrunk(port string, vlans []int, native int) [][]string {
	parts := []string{vlanPrelude(port), vlanFlush(port)}
	for _, v := range vlans {
		if v == native {
			continue
		}
		parts = append(parts, vlanAdd(port, v, ""))
	}
	if native != 0 {
		parts = append(parts, vlanAdd(port, native, " pvid untagged"))
	}
	return [][]string{{"sh", "-c", strings.Join(parts, "; ")}}
}

func (VLANBase) VLANClearPort(port string) [][]string {
	cmd := vlanPrelude(port) + "; " + vlanFlush(port) + "; " + vlanAdd(port, 1, " pvid untagged")
	return [][]string{{"sh", "-c", cmd}}
}

func (VLANBase) VLANPorts(namespace, pod string) (Rows, error) {
	out, err := netnsReader(namespace, pod)([]string{"bridge", "vlan", "show"})
	if err != nil {
		return nil, err
	}
	return parseBridgeVLAN(string(out)), nil
}

// parseBridgeVLAN reads `bridge vlan show`: a name at the start of a line
// opens a port, each line under it is one VLAN with its flags, and the
// PVID that egresses untagged is the native one.
func parseBridgeVLAN(text string) Rows {
	type portVLANs struct {
		name   string
		tagged []string
		native string
	}
	var ports []*portVLANs
	var cur *portVLANs
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "port") {
			continue
		}
		tokens := strings.Fields(line)
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			cur = &portVLANs{name: tokens[0]}
			ports = append(ports, cur)
			tokens = tokens[1:]
		}
		if cur == nil || len(tokens) == 0 {
			continue
		}
		flags := strings.Join(tokens[1:], " ")
		if strings.Contains(flags, "PVID") && strings.Contains(flags, "Untagged") {
			cur.native = tokens[0]
		} else {
			cur.tagged = append(cur.tagged, tokens[0])
		}
	}
	var rows Rows
	for _, p := range ports {
		mode, vlans := "trunk", strings.Join(p.tagged, ",")
		switch {
		case len(p.tagged) == 0 && p.native == "":
			mode = "none"
		case len(p.tagged) == 0:
			mode, vlans = "access", p.native
		}
		rows = append(rows, map[string]string{"port": p.name, "mode": mode, "vlans": vlans, "native": p.native})
	}
	return rows
}
