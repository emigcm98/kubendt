package capabilities

import (
	"reflect"
	"strings"
	"testing"
)

func TestNATBaseStructuredCommands(t *testing.T) {
	var nat NATBase

	if got, want := nat.DisableSNAT("eth0", false),
		[][]string{{"iptables", "-t", "nat", "-D", "POSTROUTING", "-o", "eth0", "-j", "MASQUERADE"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("DisableSNAT:\n got  %v\n want %v", got, want)
	}

	if got, want := nat.DisableDNAT("eth0", 8080, "10.0.0.5", 80, "tcp", false),
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
	snat := nat.EnableSNAT("eth0", false)
	if len(snat) != 1 || len(snat[0]) != 3 || snat[0][0] != "sh" || snat[0][1] != "-c" {
		t.Fatalf("EnableSNAT shape unexpected: %v", snat)
	}
	if !strings.Contains(snat[0][2], "MASQUERADE") ||
		!strings.Contains(snat[0][2], "-C POSTROUTING") ||
		!strings.Contains(snat[0][2], "-A POSTROUTING") {
		t.Errorf("EnableSNAT missing idempotent check/add: %q", snat[0][2])
	}

	dnat := nat.EnableDNAT("eth0", 8080, "10.0.0.5", 80, "tcp", false)
	if len(dnat) != 1 || dnat[0][0] != "sh" {
		t.Fatalf("EnableDNAT shape unexpected: %v", dnat)
	}
	if !strings.Contains(dnat[0][2], "--to-destination 10.0.0.5:80") ||
		!strings.Contains(dnat[0][2], "--dport 8080") {
		t.Errorf("EnableDNAT missing expected rule: %q", dnat[0][2])
	}
}

func TestNATBaseIPv6(t *testing.T) {
	var nat NATBase
	if got, want := nat.DisableSNAT("eth2", true),
		[][]string{{"ip6tables", "-t", "nat", "-D", "POSTROUTING", "-o", "eth2", "-j", "MASQUERADE"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("DisableSNAT v6:\n got  %v\n want %v", got, want)
	}
	if got, want := nat.DisableDNAT("eth2", 8080, "fd00:20::10", 80, "tcp", true),
		[][]string{{"ip6tables", "-t", "nat", "-D", "PREROUTING", "-i", "eth2", "-p", "tcp", "--dport", "8080", "-j", "DNAT", "--to-destination", "[fd00:20::10]:80"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("DisableDNAT v6:\n got  %v\n want %v", got, want)
	}
	snat := nat.EnableSNAT("eth2", true)[0][2]
	if !strings.HasPrefix(snat, "ip6tables -t nat -C POSTROUTING -o eth2") || !strings.Contains(snat, "|| ip6tables -t nat -A POSTROUTING") {
		t.Errorf("EnableSNAT v6: %q", snat)
	}
	dnat := nat.EnableDNAT("eth2", 8080, "fd00:20::10", 80, "tcp", true)[0][2]
	if !strings.HasPrefix(dnat, "ip6tables ") || !strings.Contains(dnat, "--to-destination [fd00:20::10]:80 ||") {
		t.Errorf("EnableDNAT v6: %q", dnat)
	}
}
