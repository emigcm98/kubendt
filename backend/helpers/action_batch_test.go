package helpers

import (
	"strings"
	"testing"
)

func TestShellScriptRoundTrip(t *testing.T) {
	plans := []PlannedAction{
		{Commands: [][]string{{"ip", "addr", "replace", "10.0.0.1/24", "dev", "eth1"}, {"ip", "link", "set", "eth1", "up"}}},
		{Commands: [][]string{{"sh", "-c", "grep -q '^nameserver 8.8.8.8$' /etc/resolv.conf || echo 'nameserver 8.8.8.8' >> /etc/resolv.conf"}}},
		{Commands: nil},
	}
	script := shellScript(plans)
	want := "{ 'ip' 'addr' 'replace' '10.0.0.1/24' 'dev' 'eth1' && 'ip' 'link' 'set' 'eth1' 'up'; } 2>&1; echo \"__kubendt_rc__ 0 $?\"\n" +
		"{ 'sh' '-c' 'grep -q '\\''^nameserver 8.8.8.8$'\\'' /etc/resolv.conf || echo '\\''nameserver 8.8.8.8'\\'' >> /etc/resolv.conf'; } 2>&1; echo \"__kubendt_rc__ 1 $?\"\n" +
		"{ true; } 2>&1; echo \"__kubendt_rc__ 2 $?\"\n"
	if script != want {
		t.Fatalf("script:\n%s\nwant:\n%s", script, want)
	}

	out := "RTNETLINK answers: File exists\n__kubendt_rc__ 0 2\n__kubendt_rc__ 1 0\nsh: ip: not found\n__kubendt_rc__ 2 127\n"
	res := parseShellScript(out, 4)
	if res[0].RC != 2 || res[0].Output != "RTNETLINK answers: File exists" || res[0].err() == nil {
		t.Errorf("first action: %+v", res[0])
	}
	if res[1].RC != 0 || res[1].Output != "" || res[1].err() != nil {
		t.Errorf("second action: %+v", res[1])
	}
	if res[2].RC != 127 || !strings.Contains(res[2].err().Error(), "'ip' is not in the pod's image") {
		t.Errorf("a missing program names itself: %+v %v", res[2], res[2].err())
	}
	if res[3].Seen || res[3].err() == nil {
		t.Errorf("an action without a marker is an error: %+v", res[3])
	}
}
