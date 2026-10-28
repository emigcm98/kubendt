package capabilities

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"kubendt/executor"
)

type NATBase struct{}

// GetSNATInterface reads the masqueraded interface from iptables inside the
// pod (native Linux). VyOS reads its own config instead.
func (NATBase) GetSNATInterface(namespace, podName string) (string, error) {
	out, err := executor.ExecAndGetNetns(namespace, podName, []string{"iptables", "-t", "nat", "-S", "POSTROUTING"})
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "MASQUERADE") {
			if m := regexp.MustCompile(`-o\s+(\S+)`).FindStringSubmatch(line); len(m) > 1 {
				return m[1], nil
			}
		}
	}
	return "", nil
}

// The same rules through iptables or ip6tables; both come in the toolbox
// for images that ship neither.
func natTool(v6 bool) string {
	if v6 {
		return "ip6tables"
	}
	return "iptables"
}

// An IPv6 destination needs brackets before the port.
func natDestination(internalIP string, internalPort int, v6 bool) string {
	if v6 {
		return fmt.Sprintf("[%s]:%d", internalIP, internalPort)
	}
	return fmt.Sprintf("%s:%d", internalIP, internalPort)
}

// SNAT (MASQUERADE) idempotent
func (NATBase) EnableSNAT(iface string, v6 bool) [][]string {
	t := natTool(v6)
	cmd := fmt.Sprintf(
		"%s -t nat -C POSTROUTING -o %s -j MASQUERADE || %s -t nat -A POSTROUTING -o %s -j MASQUERADE",
		t, iface, t, iface,
	)
	return [][]string{{"sh", "-c", cmd}}
}

func (NATBase) DisableSNAT(iface string, v6 bool) [][]string {
	return [][]string{{
		natTool(v6), "-t", "nat", "-D", "POSTROUTING", "-o", iface, "-j", "MASQUERADE",
	}}
}

// DNAT (port-forward) idempotent
func (NATBase) EnableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string, v6 bool) [][]string {
	t := natTool(v6)
	to := natDestination(internalIP, internalPort, v6)
	cmd := fmt.Sprintf(
		"%s -t nat -C PREROUTING -i %s -p %s --dport %d -j DNAT --to-destination %s || "+
			"%s -t nat -A PREROUTING -i %s -p %s --dport %d -j DNAT --to-destination %s",
		t, iface, protocol, externalPort, to,
		t, iface, protocol, externalPort, to,
	)
	return [][]string{{"sh", "-c", cmd}}
}

func (NATBase) DisableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string, v6 bool) [][]string {
	return [][]string{{
		natTool(v6), "-t", "nat", "-D", "PREROUTING", "-i", iface, "-p", protocol,
		"--dport", strconv.Itoa(externalPort),
		"-j", "DNAT", "--to-destination", natDestination(internalIP, internalPort, v6),
	}}
}
