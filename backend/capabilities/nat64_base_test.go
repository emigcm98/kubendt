package capabilities

import (
	"strings"
	"testing"
)

func TestTaygaAddresses(t *testing.T) {
	v4, v6 := taygaAddresses("172.22.19.0/24")
	if v4 != "172.22.19.1" || v6 != "fd64:ac16:1300::1" {
		t.Errorf("addresses for the pool: %s %s", v4, v6)
	}
}

func TestNAT64BaseScript(t *testing.T) {
	var b NAT64Base
	rows := b.NAT64Enable("64:ff9b::/96", "172.22.19.0/24")
	if len(rows) != 1 || rows[0][0] != "sh" {
		t.Fatalf("enable shape: %v", rows)
	}
	script := rows[0][2]
	for _, want := range []string{
		"printf 'tun-device nat64\\nipv4-addr 172.22.19.1\\nipv6-addr fd64:ac16:1300::1\\nprefix 64:ff9b::/96\\ndynamic-pool 172.22.19.0/24\\nwkpf-strict no\\n' > /etc/tayga.conf",
		"tayga --mktun -c /etc/tayga.conf",
		"ip route replace 172.22.19.0/24 dev nat64",
		"ip -6 route replace 64:ff9b::/96 dev nat64",
		"nohup tayga --nodetach --stdout -c /etc/tayga.conf",
		"grep -q '^State:.Z' /proc/$p/status || exit 0",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("enable script lacks %q:\n%s", want, script)
		}
	}
	off := b.NAT64Disable()[0][2]
	if !strings.HasPrefix(off, "pkill -9 -x tayga") || !strings.Contains(off, "tayga --rmtun") || !strings.Contains(off, "ip link del nat64") || !strings.HasSuffix(off, "; true") {
		t.Errorf("disable script: %s", off)
	}
}
