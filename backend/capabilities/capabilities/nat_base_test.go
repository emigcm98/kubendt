package capabilities

import (
	"reflect"
	"strings"
	"testing"
)

func TestNATBaseStructuredCommands(t *testing.T) {
	var nat NATBase

	if got, want := nat.DisableSNAT("eth0"),
		[][]string{{"iptables", "-t", "nat", "-D", "POSTROUTING", "-o", "eth0", "-j", "MASQUERADE"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("DisableSNAT:\n got  %v\n want %v", got, want)
	}

	if got, want := nat.DisableDNAT("eth0", 8080, "10.0.0.5", 80, "tcp"),
		[][]string{{
			"iptables", "-t", "nat", "-D", "PREROUTING", "-i", "eth0", "-p", "tcp",
			"--dport", "8080", "-j", "DNAT", "--to-destination", "10.0.0.5:80",
		}}; !reflect.DeepEqual(got, want) {
		t.Errorf("DisableDNAT:\n got  %v\n want %v", got, want)
	}
}

func TestNATBaseIdempotentCommands(t *testing.T) {
	var nat NATBase

	// The enable variants wrap an idempotent shell one-liner ("check || add").
	snat := nat.EnableSNAT("eth0")
	if len(snat) != 1 || len(snat[0]) != 3 || snat[0][0] != "sh" || snat[0][1] != "-c" {
		t.Fatalf("EnableSNAT shape unexpected: %v", snat)
	}
	if !strings.Contains(snat[0][2], "MASQUERADE") ||
		!strings.Contains(snat[0][2], "-C POSTROUTING") ||
		!strings.Contains(snat[0][2], "-A POSTROUTING") {
		t.Errorf("EnableSNAT missing idempotent check/add: %q", snat[0][2])
	}

	dnat := nat.EnableDNAT("eth0", 8080, "10.0.0.5", 80, "tcp")
	if len(dnat) != 1 || dnat[0][0] != "sh" {
		t.Fatalf("EnableDNAT shape unexpected: %v", dnat)
	}
	if !strings.Contains(dnat[0][2], "--to-destination 10.0.0.5:80") ||
		!strings.Contains(dnat[0][2], "--dport 8080") {
		t.Errorf("EnableDNAT missing expected rule: %q", dnat[0][2])
	}
}
