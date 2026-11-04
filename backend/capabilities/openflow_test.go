package capabilities

import "testing"

func TestFlowMatch(t *testing.T) {
	cases := map[string]string{
		"priority=100,in_port=1,actions=drop":                          "priority=100,in_port=1",
		"priority=100,in_port=1 actions=drop":                          "priority=100,in_port=1",
		"in_port=1,dl_dst=ff:ff:ff:ff:ff:ff,actions=output:2,output:3": "in_port=1,dl_dst=ff:ff:ff:ff:ff:ff",
		"priority=0,actions=NORMAL":                                    "priority=0",
		"priority=100,in_port=1":                                       "priority=100,in_port=1",
	}
	for flow, want := range cases {
		if got := FlowMatch(flow); got != want {
			t.Errorf("FlowMatch(%q) = %q, want %q", flow, got, want)
		}
	}
}
