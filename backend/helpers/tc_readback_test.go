package helpers

import (
	"reflect"
	"testing"

	"kubendt/types"
)

// Samples captured from `tc -j qdisc show` on the cluster (iproute2 6.15,
// 6.19 and 7.1 print the same shapes).
const (
	tcJSONNetemFull = `[{"kind":"netem","handle":"8052:","root":true,"refcnt":2,"options":{"limit":500,"delay":{"delay":0.05,"jitter":0.01,"correlation":0.25},"loss-random":{"loss":0.01,"correlation":0},"duplicate":{"duplicate":0.005,"correlation":0},"corrupt":{"corrupt":0.001,"correlation":0},"seed":12280009765276374062,"ecn":false,"gap":0}}]`
	tcJSONNetemRate = `[{"kind":"netem","handle":"8054:","root":true,"refcnt":2,"options":{"limit":1000,"delay":{"delay":0.03,"jitter":0,"correlation":0},"reorder":{"reorder":0.25,"correlation":0.5},"rate":{"rate":250000,"packetoverhead":0,"cellsize":0,"celloverhead":0},"seed":16990048779375876720,"ecn":false,"gap":1}}]`
	tcJSONGemodel   = `[{"kind":"netem","handle":"8056:","root":true,"refcnt":2,"options":{"limit":1000,"loss-gemodel":{"p":0.01,"r":0.1,"1-h":1,"1-k":0},"seed":14695546488333467696,"ecn":false,"gap":0}}]`
	tcJSONTBF       = `[{"kind":"tbf","handle":"8058:","root":true,"refcnt":2,"options":{"rate":62500,"burst":4096,"lat":50000}}]`
	tcJSONTree      = `[{"kind":"tbf","handle":"1:","root":true,"refcnt":2,"options":{"rate":250000,"burst":4096,"lat":50000}},{"kind":"netem","handle":"10:","parent":"1:1","options":{"limit":1000,"delay":{"delay":0.03,"jitter":0,"correlation":0},"seed":3398696887365618610,"ecn":false,"gap":0}}]`
	tcJSONDefault   = `[{"kind":"noqueue","handle":"0:","root":true,"refcnt":2,"options":{}}]`
	tcJSONAllDevs   = `[{"kind":"noqueue","handle":"0:","dev":"lo","root":true,"refcnt":2,"options":{}},{"kind":"noqueue","handle":"0:","dev":"eth0","root":true,"refcnt":2,"options":{}},{"kind":"netem","handle":"8036:","dev":"eth1","root":true,"refcnt":7,"options":{"limit":1000,"delay":{"delay":0.005,"jitter":0,"correlation":0},"loss-random":{"loss":0.02,"correlation":0},"seed":6687234951155528066,"ecn":false,"gap":0}},{"kind":"noqueue","handle":"0:","dev":"br0","root":true,"refcnt":2,"options":{}}]`
)

func intp(n int) *int { return &n }

// strip drops the fields a test does not assert on.
func strip(s *types.QdiscState) *types.QdiscState {
	c := *s
	c.Raw = ""
	c.Children = nil
	return &c
}

func TestParseTCQdiscJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want *types.QdiscState
	}{
		{"netem full", tcJSONNetemFull, &types.QdiscState{Qdisc: "netem", Shaped: true, Handle: "8052", Parent: "root",
			Delay: "50ms", Jitter: "10ms", DelayCorrelation: "25%", Loss: "1%", Duplicate: "0.5%", Corrupt: "0.1%", Limit: intp(500)}},
		{"netem rate and reorder", tcJSONNetemRate, &types.QdiscState{Qdisc: "netem", Shaped: true, Handle: "8054", Parent: "root",
			Delay: "30ms", Reorder: "25%", ReorderCorrelation: "50%", Rate: "2mbit", Limit: intp(1000)}},
		{"gemodel", tcJSONGemodel, &types.QdiscState{Qdisc: "netem", Shaped: true, Handle: "8056", Parent: "root",
			LossModel: "gemodel", Gemodel: map[string]string{"p": "1%", "r": "10%", "1-h": "100%", "1-k": "0%"}, Limit: intp(1000)}},
		{"tbf", tcJSONTBF, &types.QdiscState{Qdisc: "tbf", Shaped: true, Handle: "8058", Parent: "root",
			Rate: "500kbit", Burst: "4096b", Latency: "50ms"}},
		{"kernel default", tcJSONDefault, &types.QdiscState{Qdisc: "noqueue", Shaped: false, Handle: "0", Parent: "root"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			states, err := ParseTCQdiscJSON(tc.raw)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			got, ok := states[""]
			if !ok {
				t.Fatalf("no state keyed by empty dev, got %v", states)
			}
			if got.Raw == "" {
				t.Errorf("raw should carry what tc printed")
			}
			if !reflect.DeepEqual(strip(got), tc.want) {
				t.Errorf("state = %+v, want %+v", strip(got), tc.want)
			}
		})
	}
}

func TestParseTCQdiscJSONTree(t *testing.T) {
	states, err := ParseTCQdiscJSON(tcJSONTree)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	root := states[""]
	if root.Qdisc != "tbf" || root.Handle != "1" || root.Rate != "2mbit" {
		t.Fatalf("root = %+v", root)
	}
	if len(root.Children) != 1 {
		t.Fatalf("root should have one child, got %d", len(root.Children))
	}
	child := root.Children[0]
	if child.Qdisc != "netem" || child.Parent != "1:1" || child.Delay != "30ms" {
		t.Errorf("child = %+v", child)
	}
}

func TestParseTCQdiscJSONAllDevices(t *testing.T) {
	states, err := ParseTCQdiscJSON(tcJSONAllDevs)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(states) != 4 {
		t.Fatalf("expected 4 devices, got %d: %v", len(states), states)
	}
	if !states["eth1"].Shaped || states["eth1"].Delay != "5ms" || states["eth1"].Loss != "2%" {
		t.Errorf("eth1 = %+v", states["eth1"])
	}
	if states["eth0"].Shaped || states["br0"].Shaped {
		t.Errorf("eth0 and br0 should be unshaped")
	}
	if _, err := ParseTCQdiscJSON("qdisc noqueue 0: root"); err == nil {
		t.Errorf("text output must not parse as JSON")
	}
}

func TestTCLacksJSON(t *testing.T) {
	if !tcLacksJSON(`Option "-j" is unknown, try "tc -help".`) {
		t.Errorf("iproute2 rejection not recognised")
	}
	if !tcLacksJSON("tc: invalid option -- 'j'") {
		t.Errorf("busybox rejection not recognised")
	}
	if tcLacksJSON(`Cannot find device "eth9"`) {
		t.Errorf("a missing device is not a missing -j")
	}
}

func TestFmtTCRate(t *testing.T) {
	cases := map[float64]string{500000: "500kbit", 2e6: "2mbit", 1.5e6: "1500kbit", 1e9: "1gbit", 800: "800bit"}
	for bits, want := range cases {
		if got := fmtTCRate(bits); got != want {
			t.Errorf("fmtTCRate(%v) = %q, want %q", bits, got, want)
		}
	}
	if got := fmtTCMs(0.03 * 1000); got != "30ms" {
		t.Errorf("fmtTCMs float noise: %q", got)
	}
}
