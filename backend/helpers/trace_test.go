package helpers

import (
	"net"
	"testing"
)

func TestParseTraceHop(t *testing.T) {
	cases := []struct {
		line    string
		wantTTL int
		wantIP  string
		wantRTT float64
		wantTO  bool
		wantOK  bool
	}{
		{" 1  10.5.0.1  0.123 ms", 1, "10.5.0.1", 0.123, false, true},
		{" 3  10.5.254.2  1.234 ms", 3, "10.5.254.2", 1.234, false, true},
		{" 3  8.8.8.8 [open]  2.005 ms", 3, "8.8.8.8", 2.005, false, true}, // tcptraceroute
		{" 2  fd00:12::2  0.544 ms", 2, "fd00:12::2", 0.544, false, true},
		{" 1  ::1  0.021 ms", 1, "::1", 0.021, false, true},
		{" 5  * * *", 5, "", 0, true, true},
		{" 7  *", 7, "", 0, true, true},
		{"traceroute to 8.8.8.8 (8.8.8.8), 30 hops max, 60 byte packets", 0, "", 0, false, false},
		{"", 0, "", 0, false, false},
	}
	for _, c := range cases {
		ttl, ip, rtt, to, ok := ParseTraceHop(c.line)
		if ok != c.wantOK || ttl != c.wantTTL || ip != c.wantIP || to != c.wantTO || rtt != c.wantRTT {
			t.Errorf("ParseTraceHop(%q) = (%d,%q,%v,%v,%v), want (%d,%q,%v,%v,%v)",
				c.line, ttl, ip, rtt, to, ok, c.wantTTL, c.wantIP, c.wantRTT, c.wantTO, c.wantOK)
		}
	}
}

func TestValidTraceDest(t *testing.T) {
	good := []string{"8.8.8.8", "google.com", "10.5.0.1", "web-server", "a.b.c.d.example.com", "fd00:12::2", "2001:db8::1", "::1"}
	bad := []string{"", "8.8.8.8; rm -rf /", "$(whoami)", "a b", "-flag", "he re.com", "fd00::1; id", "fd00:::1"}
	for _, d := range good {
		if !ValidTraceDest(d) {
			t.Errorf("ValidTraceDest(%q) = false, want true", d)
		}
	}
	for _, d := range bad {
		if ValidTraceDest(d) {
			t.Errorf("ValidTraceDest(%q) = true, want false", d)
		}
	}
}

func TestBFSPath(t *testing.T) {
	// host-0 - switch-0 - router-0 - router-1
	adj := map[string][]string{
		"host-0":   {"switch-0"},
		"switch-0": {"host-0", "router-0"},
		"router-0": {"switch-0", "router-1"},
		"router-1": {"router-0"},
	}

	got := BFSPath(adj, "host-0", "router-0")
	want := []string{"host-0", "switch-0", "router-0"}
	if !equalSlice(got, want) {
		t.Errorf("BFSPath host-0→router-0 = %v, want %v", got, want)
	}

	if p := BFSPath(adj, "router-0", "router-0"); !equalSlice(p, []string{"router-0"}) {
		t.Errorf("BFSPath self = %v, want [router-0]", p)
	}

	// Disconnected (tunnel case): no path.
	adj["ue-0"] = []string{}
	if p := BFSPath(adj, "ue-0", "router-1"); p != nil {
		t.Errorf("BFSPath disconnected = %v, want nil", p)
	}
}

func TestBuildTraceCommand(t *testing.T) {
	icmp := BuildTraceCommand("8.8.8.8", "icmp")
	if !contains(icmp, "-I") || icmp[len(icmp)-1] != "8.8.8.8" {
		t.Errorf("icmp command = %v", icmp)
	}
	if contains(BuildTraceCommand("x", "udp"), "-I") {
		t.Errorf("udp command should not contain -I")
	}
	tcp := BuildTraceCommand("x", "tcp")
	if tcp[0] != "tcptraceroute" {
		t.Errorf("tcp command should use tcptraceroute, got %v", tcp)
	}
	v6 := BuildTraceCommand("fd00:12::2", "icmp")
	if v6[0] != "traceroute" || v6[1] != "-6" || contains(v6, "-I") || v6[len(v6)-1] != "fd00:12::2" {
		t.Errorf("v6 icmp command should send UDP probes with -6: %v", v6)
	}
	if contains(BuildTraceCommand("10.0.0.1", "udp"), "-6") {
		t.Errorf("a v4 trace should carry no -6")
	}
	if TraceMethodError("fd00:12::2", "tcp", false) == nil {
		t.Errorf("a TCP trace toward v6 has no tool")
	}
	for _, ok := range []struct {
		dest, method string
		metrics      bool
	}{{"fd00:12::2", "icmp", false}, {"fd00:12::2", "tcp", true}, {"10.0.0.1", "tcp", false}} {
		if err := TraceMethodError(ok.dest, ok.method, ok.metrics); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
}

func TestParsePodAddresses(t *testing.T) {
	out := `1: lo    inet 127.0.0.1/8 scope host lo\       valid_lft forever preferred_lft forever
1: lo    inet6 ::1/128 scope host proto kernel_lo \       valid_lft forever preferred_lft forever
2: eth0    inet 10.244.1.201/24 brd 10.244.1.255 scope global eth0\       valid_lft forever preferred_lft forever
2: eth0    inet6 fe80::80f9:14ff:fe55:e9b6/64 scope link proto kernel_ll \       valid_lft forever preferred_lft forever
17: eth1@if18    inet 10.0.12.1/30 scope global eth1\       valid_lft forever preferred_lft forever
17: eth1@if18    inet6 fd00:12::1/64 scope global \       valid_lft forever preferred_lft forever
19: uesimtun0    inet 10.45.0.2/32 scope global uesimtun0\       valid_lft forever preferred_lft forever
`
	got := parsePodAddresses(out)
	want := map[string]string{"10.244.1.201": "eth0", "10.0.12.1": "eth1", "fd00:12::1": "eth1", "10.45.0.2": "uesimtun0"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for ip, iface := range want {
		if got[ip] != iface {
			t.Errorf("%s: got %q, want %q", ip, got[ip], iface)
		}
	}
}

func TestParseTraceUnreachable(t *testing.T) {
	cases := map[string]string{
		" 3  10.5.0.1  0.5 ms !N":  "!N",
		" 2  10.5.1.1  1.0 ms !H":  "!H",
		" 4  10.0.0.1  0.4 ms !X":  "!X",
		" 6  10.0.0.1  0.4 ms !13": "!13",
		" 1  10.5.0.1  0.3 ms":     "",
		" 5  * * *":                "",
	}
	for line, want := range cases {
		if got := ParseTraceUnreachable(line); got != want {
			t.Errorf("ParseTraceUnreachable(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestParseTraceTarget(t *testing.T) {
	cases := map[string]string{
		"traceroute to 8.8.8.8 (8.8.8.8), 30 hops max, 60 byte packets":       "8.8.8.8",
		"traceroute to google.com (142.250.1.2), 30 hops max":                 "142.250.1.2",
		"Tracing the path to 8.8.8.8 on TCP port 80 (http), 30 hops max":      "8.8.8.8",
		"Tracing the path to google.com (142.250.1.2) on TCP port 80":         "142.250.1.2",
		"traceroute to fd00:12::2 (fd00:12::2), 30 hops max, 72 byte packets": "fd00:12::2",
		" 1  10.5.0.1  0.5 ms": "", // not a banner
		"Selected device eth0, address 10.6.0.2, port 33 for out": "", // not a banner
	}
	for line, want := range cases {
		if got := ParseTraceTarget(line); got != want {
			t.Errorf("ParseTraceTarget(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestParseMtrReport(t *testing.T) {
	raw := []byte(`{"report":{"mtr":{"src":"ue-0","dst":"8.8.8.8"},"hubs":[
		{"count":1,"host":"10.5.0.1","Loss%":0.0,"Snt":5,"Last":0.5,"Avg":0.6,"Best":0.4,"Wrst":1.2,"StDev":0.3,"Gmean":0.55},
		{"count":2,"host":"???","Loss%":100.0,"Snt":5,"Last":0.0,"Avg":0.0,"Best":0.0,"Wrst":0.0,"StDev":0.0,"Gmean":0.0},
		{"count":3,"host":"8.8.8.8","Loss%":0.0,"Snt":5,"Last":2.0,"Avg":2.1,"Best":1.9,"Wrst":2.5,"StDev":0.2,"Gmean":2.05}
	]}}`)
	hubs, err := ParseMtrReport(raw)
	if err != nil {
		t.Fatalf("ParseMtrReport error: %v", err)
	}
	if len(hubs) != 3 {
		t.Fatalf("got %d hubs, want 3", len(hubs))
	}
	h0 := hubs[0]
	if h0.Host != "10.5.0.1" || h0.Loss != 0 || h0.Sent != 5 || h0.Avg != 0.6 ||
		h0.Best != 0.4 || h0.Worst != 1.2 || h0.Last != 0.5 || h0.StDev != 0.3 || h0.Gmean != 0.55 {
		t.Errorf("hub[0] mismatch: %+v", h0)
	}
	if hubs[1].Host != "???" || hubs[1].Loss != 100 {
		t.Errorf("hub[1] (timeout) mismatch: %+v", hubs[1])
	}
	if hubs[2].Host != "8.8.8.8" {
		t.Errorf("hub[2] host = %q, want 8.8.8.8", hubs[2].Host)
	}
	if _, err := ParseMtrReport([]byte("not json")); err == nil {
		t.Errorf("ParseMtrReport(garbage) should error")
	}
}

func TestAnnotateTraceHop(t *testing.T) {
	idx := map[string]TraceIPNode{"10.5.0.1": {Pod: "router-0", Iface: "eth2"}}
	adj := map[string][]string{
		"host-0":   {"switch-0"},
		"switch-0": {"host-0", "router-0"},
		"router-0": {"switch-0"},
	}

	// Resolved + topology path through the switch → "link".
	kind, node, iface, seg, path := AnnotateTraceHop("10.5.0.1", "host-0", idx, adj, nil)
	if kind != "l3" || node != "router-0" || iface != "eth2" || seg != "link" ||
		!equalSlice(path, []string{"host-0", "switch-0", "router-0"}) {
		t.Errorf("l3/link mismatch: kind=%s node=%s iface=%s seg=%s path=%v", kind, node, iface, seg, path)
	}

	// Resolved but no topology path from the previous pod → "tunnel".
	kind, node, _, seg, path = AnnotateTraceHop("10.5.0.1", "gnb", idx, adj, nil)
	if kind != "l3" || node != "router-0" || seg != "tunnel" || !equalSlice(path, []string{"gnb", "router-0"}) {
		t.Errorf("tunnel mismatch: kind=%s node=%s seg=%s path=%v", kind, node, seg, path)
	}

	// Unresolved IP → "external".
	kind, node, _, _, _ = AnnotateTraceHop("9.9.9.9", "host-0", idx, adj, nil)
	if kind != "external" || node != "" {
		t.Errorf("external mismatch: kind=%s node=%s", kind, node)
	}

	// Unresolved IP inside the cluster infrastructure → "cluster".
	_, podNet, _ := net.ParseCIDR("10.244.2.0/24")
	cluster := &TraceClusterInfo{nets: []*net.IPNet{podNet}, ips: map[string]bool{"10.208.103.21": true}}
	if kind, _, _, _, _ = AnnotateTraceHop("10.244.2.1", "host-0", idx, adj, cluster); kind != "cluster" {
		t.Errorf("pod-cidr hop should be cluster, got %s", kind)
	}
	if kind, _, _, _, _ = AnnotateTraceHop("10.208.103.21", "host-0", idx, adj, cluster); kind != "cluster" {
		t.Errorf("node-ip hop should be cluster, got %s", kind)
	}
	if kind, _, _, _, _ = AnnotateTraceHop("10.208.0.1", "host-0", idx, adj, cluster); kind != "external" {
		t.Errorf("lan gateway should stay external, got %s", kind)
	}
}

func TestBuildMtrCommand(t *testing.T) {
	icmp := BuildMtrCommand("8.8.8.8", "icmp", 5)
	if icmp[0] != "mtr" || !contains(icmp, "--json") || !contains(icmp, "5") || icmp[len(icmp)-1] != "8.8.8.8" {
		t.Errorf("icmp mtr command = %v", icmp)
	}
	if contains(icmp, "-u") || contains(icmp, "-T") {
		t.Errorf("icmp mtr should carry no proto flag: %v", icmp)
	}
	if !contains(BuildMtrCommand("x", "udp", 5), "-u") {
		t.Errorf("udp mtr should contain -u")
	}
	tcp := BuildMtrCommand("x", "tcp", 5)
	if !contains(tcp, "-T") || !contains(tcp, "-P") || !contains(tcp, "80") {
		t.Errorf("tcp mtr should contain -T -P 80: %v", tcp)
	}
	if v6 := BuildMtrCommand("fd00:12::2", "icmp", 5); !contains(v6, "-6") || v6[len(v6)-1] != "fd00:12::2" {
		t.Errorf("v6 mtr should contain -6: %v", v6)
	}
	if contains(icmp, "-6") {
		t.Errorf("a v4 mtr should carry no -6: %v", icmp)
	}
	// Cycles clamped to [1, 60].
	if cyclesArg(BuildMtrCommand("x", "icmp", 0)) != "5" {
		t.Errorf("cycles 0 should default to 5")
	}
	if cyclesArg(BuildMtrCommand("x", "icmp", 999)) != "60" {
		t.Errorf("cycles 999 should clamp to 60")
	}
}

// cyclesArg returns the value passed to mtr's -c flag.
func cyclesArg(cmd []string) string {
	for i, a := range cmd {
		if a == "-c" && i+1 < len(cmd) {
			return cmd[i+1]
		}
	}
	return ""
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
