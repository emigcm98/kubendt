package capabilities

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"kubendt/executor"
)

type NATBase struct{}

// GetSNATInterface base implementation: uses iptables inside the pod (native Linux).
// VyOS and XRD override this with their own commands.
func (NATBase) GetSNATInterface(namespace, podName string) (string, error) {
	out, err := executor.ExecAndGet(namespace, podName, []string{"iptables", "-t", "nat", "-S", "POSTROUTING"})
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

// SNAT (MASQUERADE) idempotent
func (NATBase) EnableSNAT(iface string) [][]string {
	cmd := fmt.Sprintf(
		"iptables -t nat -C POSTROUTING -o %s -j MASQUERADE || iptables -t nat -A POSTROUTING -o %s -j MASQUERADE",
		iface, iface,
	)
	return [][]string{{"sh", "-c", cmd}}
}

func (NATBase) DisableSNAT(iface string) [][]string {
	return [][]string{{
		"iptables", "-t", "nat", "-D", "POSTROUTING", "-o", iface, "-j", "MASQUERADE",
	}}
}

// DNAT (port-forward) idempotent
func (NATBase) EnableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string) [][]string {
	cmd := fmt.Sprintf(
		"iptables -t nat -C PREROUTING -i %s -p %s --dport %d -j DNAT --to-destination %s:%d || "+
			"iptables -t nat -A PREROUTING -i %s -p %s --dport %d -j DNAT --to-destination %s:%d",
		iface, protocol, externalPort, internalIP, internalPort,
		iface, protocol, externalPort, internalIP, internalPort,
	)
	return [][]string{{"sh", "-c", cmd}}
}

func (NATBase) DisableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string) [][]string {
	return [][]string{{
		"iptables", "-t", "nat", "-D", "PREROUTING", "-i", iface, "-p", protocol,
		"--dport", strconv.Itoa(externalPort),
		"-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%d", internalIP, internalPort),
	}}
}
