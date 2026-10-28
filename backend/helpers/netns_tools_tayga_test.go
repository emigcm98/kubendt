package helpers

import "testing"

// tayga is the rarest tool: an action that needs it runs whole in the
// toolbox, whatever else its rows call.
func TestToolClassTayga(t *testing.T) {
	enable := []string{"sh", "-c", "pkill -x tayga 2>/dev/null; ip link del nat64 2>/dev/null; printf 'x' > /etc/tayga.conf; tayga --mktun -c /etc/tayga.conf; ip link set nat64 up"}
	if got := toolClassOf(enable); got != toolTayga {
		t.Errorf("enable script: %q", got)
	}
	disable := []string{"sh", "-c", "pkill -x tayga 2>/dev/null; tayga --rmtun -c /etc/tayga.conf 2>/dev/null; ip link del nat64 2>/dev/null; true"}
	if got := toolClassOf(disable); got != toolTayga {
		t.Errorf("disable script: %q", got)
	}
	if got := toolClassOf([]string{"sh", "-c", "pkill -x tayga; ip link del nat64"}); got != toolNetns {
		t.Errorf("tayga as an argument is not the tool: %q", got)
	}
	if got := toolClassOfRows([][]string{{"ip", "link", "set", "eth1", "up"}, {"iptables", "-t", "nat", "-S"}, enable}); got != toolTayga {
		t.Errorf("rows with tayga: %q", got)
	}
	if got := toolClassOfRows([][]string{{"ip", "link", "set", "eth1", "up"}, {"iptables", "-t", "nat", "-S"}}); got != toolIptables {
		t.Errorf("rows with iptables: %q", got)
	}
	if toolProbe(toolTayga) != "tayga" {
		t.Errorf("probe: %q", toolProbe(toolTayga))
	}
}
