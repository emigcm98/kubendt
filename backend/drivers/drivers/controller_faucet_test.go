package drivers

import (
	"reflect"
	"strings"
	"testing"
)

const faucetSampleMetrics = `# HELP dp_status status of datapaths
# TYPE dp_status gauge
dp_status{dp_id="0x1",dp_name="sw1"} 1.0
dp_status{dp_id="0x2",dp_name="sw2"} 0.0
of_dp_connections_total{dp_id="0x1",dp_name="sw1"} 2.0
port_status{dp_id="0x1",dp_name="sw1",port="1"} 1.0
port_status{dp_id="0x1",dp_name="sw1",port="10"} 0.0
port_status{dp_id="0x1",dp_name="sw1",port="2"} 1.0
learned_macs{dp_id="0x1",dp_name="sw1",n="0",port="1",vlan="10"} 281907355859.0
learned_macs{dp_id="0x1",dp_name="sw1",n="1",port="1",vlan="10"} 0.0
faucet_pbr_version{version="1.10.12"} 1.0
odd_label{text="a, \"quoted\" one\\ here"} 3
`

func TestParsePromText(t *testing.T) {
	samples := parsePromText(faucetSampleMetrics)
	if len(samples) != 10 {
		t.Fatalf("got %d samples, want 10", len(samples))
	}
	last := samples[len(samples)-1]
	if last.name != "odd_label" || last.value != 3 || last.labels["text"] != `a, "quoted" one\ here` {
		t.Errorf("escaped label parsed as %+v", last)
	}
	if samples[0].labels["dp_name"] != "sw1" || samples[0].value != 1 {
		t.Errorf("first sample parsed as %+v", samples[0])
	}
}

func TestFaucetRows(t *testing.T) {
	samples := parsePromText(faucetSampleMetrics)

	dps := faucetDatapathRows(samples)
	want := []map[string]string{
		{"datapath": "sw1", "dp_id": "0x1", "status": "up", "connections": "2"},
		{"datapath": "sw2", "dp_id": "0x2", "status": "down", "connections": "0"},
	}
	if !reflect.DeepEqual([]map[string]string(dps), want) {
		t.Errorf("datapaths: got %v, want %v", dps, want)
	}

	ports := faucetPortRows(samples)
	if len(ports) != 3 || ports[0]["port"] != "1" || ports[1]["port"] != "2" || ports[2]["port"] != "10" || ports[2]["status"] != "down" {
		t.Errorf("ports: got %v", ports)
	}

	hosts := faucetHostRows(samples)
	if len(hosts) != 1 || hosts[0]["mac"] != "00:41:a2:fc:e0:d3" || hosts[0]["vlan"] != "10" || hosts[0]["port"] != "1" {
		t.Errorf("hosts: got %v", hosts)
	}
}

func TestFaucetApplyConfig(t *testing.T) {
	cmds := FaucetControllerDriver{}.ApplyConfig("vlans:\n  office: {vid: 10, description: \"it's\"}\n")
	if len(cmds) != 1 || len(cmds[0]) != 3 {
		t.Fatalf("got %v", cmds)
	}
	script := cmds[0][2]
	if !strings.Contains(script, `description: "it'\''s"`) || !strings.HasSuffix(script, "> /etc/faucet/faucet.yaml && kill -HUP 1") {
		t.Errorf("script: %s", script)
	}
}
