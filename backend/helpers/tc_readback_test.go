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

// Captured from a tree built with `tc -b`: an htb root with two classes, a
// netem leaf and a pfifo leaf, and two flower filters.
const (
	tcJSONHTBQdiscs  = `[{"kind":"htb","handle":"1:","root":true,"refcnt":2,"options":{"r2q":10,"default":"0x12","direct_packets_stat":0,"direct_qlen":1000}},{"kind":"pfifo","handle":"12:","parent":"1:12","options":{"limit":100}},{"kind":"netem","handle":"11:","parent":"1:11","options":{"limit":1000,"delay":{"delay":0.005,"jitter":0,"correlation":0},"seed":15151744933504699061,"ecn":false,"gap":0}}]`
	tcJSONHTBClasses = `[{"class":"htb","handle":"1:11","parent":"1:1","leaf":"0x11","prio":0,"rate":125000,"ceil":1250000,"burst":1600,"cburst":1600},{"class":"htb","handle":"1:1","root":true,"rate":1250000,"ceil":1250000,"burst":1600,"cburst":1600},{"class":"htb","handle":"1:12","parent":"1:1","leaf":"0x12","prio":1,"rate":1125000,"ceil":1250000,"burst":1600,"cburst":1600}]`
	tcJSONHTBFilters = `[{"parent":"1:","protocol":"ip","pref":1,"kind":"flower","chain":0},{"parent":"1:","protocol":"ip","pref":1,"kind":"flower","chain":0,"options":{"handle":1,"classid":"1:11","keys":{"eth_type":"ipv4","ip_proto":"icmp"},"not_in_hw":true}},{"parent":"1:","protocol":"ip","pref":2,"kind":"flower","chain":0},{"parent":"1:","protocol":"ip","pref":2,"kind":"flower","chain":0,"options":{"handle":1,"classid":"1:11","keys":{"eth_type":"ipv4","ip_proto":"tcp","ip_tos":184,"ip_tos_mask":"fc","dst_ip":"192.168.3.0/24","dst_port":5201},"not_in_hw":true}}]`
)

func TestAttachClasses(t *testing.T) {
	states, err := ParseTCQdiscJSON(tcJSONHTBQdiscs)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	root := states[""]
	if err := attachClasses(root, tcJSONHTBClasses, tcJSONHTBFilters); err != nil {
		t.Fatalf("attach error: %v", err)
	}
	if root.Qdisc != "htb" || !root.Shaped || root.Rate != "10mbit" || root.DefaultClass != "1:12" {
		t.Fatalf("root = %+v", root)
	}
	if len(root.Children) != 0 {
		t.Errorf("leaves should hang from their classes, %d left on the root", len(root.Children))
	}
	if len(root.Classes) != 2 {
		t.Fatalf("expected 2 classes, got %+v", root.Classes)
	}
	voice, bulk := root.Classes[0], root.Classes[1]
	if voice.ClassID != "1:11" || voice.Rate != "1mbit" || voice.Ceil != "10mbit" || voice.Prio == nil || *voice.Prio != 0 || voice.Default {
		t.Errorf("voice class = %+v", voice)
	}
	if voice.Leaf == nil || voice.Leaf.Qdisc != "netem" || voice.Leaf.Delay != "5ms" {
		t.Errorf("voice leaf = %+v", voice.Leaf)
	}
	if len(voice.Match) != 2 || voice.Match[0].Proto != "icmp" || voice.Match[1].Dst != "192.168.3.0/24" ||
		voice.Match[1].DstPort == nil || *voice.Match[1].DstPort != 5201 || voice.Match[1].DSCP == nil || *voice.Match[1].DSCP != 46 {
		t.Errorf("voice matches = %+v", voice.Match)
	}
	if bulk.ClassID != "1:12" || bulk.Rate != "9mbit" || !bulk.Default || bulk.Leaf == nil || bulk.Leaf.Qdisc != "pfifo" || len(bulk.Match) != 0 {
		t.Errorf("bulk class = %+v leaf %+v", bulk, bulk.Leaf)
	}
}

func TestSplitJSONDocuments(t *testing.T) {
	docs, err := splitJSONDocuments("[1,2]\n[]\n[{\"a\":1}]\n")
	if err != nil || len(docs) != 3 || docs[1] != "[]" {
		t.Errorf("docs = %v err %v", docs, err)
	}
	if _, err := splitJSONDocuments("qdisc noqueue 0: root"); err == nil {
		t.Errorf("text must not split as JSON")
	}
}
