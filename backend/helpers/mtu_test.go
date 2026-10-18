package helpers

import (
	"strings"
	"testing"

	"kubendt/drivers/drivers"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/types"
)

func TestParsePlatformLinkMTU(t *testing.T) {
	cases := []struct {
		raw      string
		want     int
		explicit bool
		wantErr  bool
	}{
		{"", DefaultLinkMTU, false, false},
		{"  ", DefaultLinkMTU, false, false},
		{"1500", 1500, true, false},
		{"8950", 8950, true, false},
		{"67", 0, true, true},
		{"65536", 0, true, true},
		{"auto", 0, true, true},
		{"1450b", 0, true, true},
	}
	for _, tc := range cases {
		got, explicit, err := parsePlatformLinkMTU(tc.raw)
		if (err != nil) != tc.wantErr {
			t.Fatalf("%q: err=%v, wantErr=%v", tc.raw, err, tc.wantErr)
		}
		if err == nil && (got != tc.want || explicit != tc.explicit) {
			t.Fatalf("%q: got %d explicit=%v, want %d explicit=%v", tc.raw, got, explicit, tc.want, tc.explicit)
		}
		if err != nil && !strings.Contains(err.Error(), LinkMTUEnv) {
			t.Fatalf("%q: error should name the variable: %v", tc.raw, err)
		}
	}
}

func TestParseLinkMTUs(t *testing.T) {
	out := strings.Join([]string{
		`1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN mode DEFAULT group default qlen 1000\    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00`,
		`2: eth0@if123: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1450 qdisc noqueue state UP mode DEFAULT group default \    link/ether 3a:11:00:00:00:01 brd ff:ff:ff:ff:ff:ff link-netnsid 0`,
		`18554: eth1@if18555: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP mode DEFAULT group default qlen 1000\    link/ether 02:00:00:00:00:01 brd ff:ff:ff:ff:ff:ff link-netnsid 1`,
		`19: eth2: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1450 qdisc noqueue state UNKNOWN mode DEFAULT group default qlen 1000\    link/ether 02:00:00:00:00:02 brd ff:ff:ff:ff:ff:ff`,
		`20: tap2: <BROADCAST,MULTICAST,PROMISC,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UNKNOWN mode DEFAULT group default qlen 1000\    link/ether 02:00:00:00:00:03 brd ff:ff:ff:ff:ff:ff`,
		`garbage line`,
	}, "\n")
	got := parseLinkMTUs(out)
	want := map[string]int{"lo": 65536, "eth0": 1450, "eth1": 1500, "eth2": 1450, "tap2": 1500}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: got %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
	if len(parseLinkMTUs("")) != 0 {
		t.Fatalf("empty output must parse to nothing")
	}
}

func TestParseSysfsMTUs(t *testing.T) {
	got := parseSysfsMTUs("eth1 1500\neth2 1450\n\nbad\n")
	if len(got) != 2 || got["eth1"] != 1500 || got["eth2"] != 1450 {
		t.Fatalf("got %v", got)
	}
}

func TestTapFor(t *testing.T) {
	cases := map[string]string{"eth1": "tap1", "eth923": "tap923", "eth": "", "ens3": "", "net1": ""}
	for iface, want := range cases {
		if got := tapFor(iface); got != want {
			t.Fatalf("%s: got %q, want %q", iface, got, want)
		}
	}
}

func TestSettleMTU(t *testing.T) {
	ceilingMu.Lock()
	workerCeiling = map[string]int{"w1": 1450, "w2": 1430}
	ceilingMu.Unlock()
	defer func() {
		ceilingMu.Lock()
		workerCeiling = map[string]int{}
		ceilingMu.Unlock()
	}()

	cross := []mtuEnd{
		{Pod: "a-0", Iface: "eth1", Worker: "w1", Peer: "b-0", PeerIf: "eth1", Cross: true},
		{Pod: "b-0", Iface: "eth1", Worker: "w2", Peer: "a-0", PeerIf: "eth1", Cross: true},
	}
	same := []mtuEnd{
		{Pod: "a-0", Iface: "eth2", Worker: "w1", Peer: "c-0", PeerIf: "eth1"},
		{Pod: "c-0", Iface: "eth1", Worker: "w1", Peer: "a-0", PeerIf: "eth2"},
	}

	// The default is lowered to what the lowest worker carries, once per worker.
	eff, warnings, err := settleMTU(DefaultLinkMTU, false, append(cross, same...))
	if err != nil || eff != 1430 || len(warnings) != 1 {
		t.Fatalf("default: eff=%d warnings=%v err=%v", eff, warnings, err)
	}
	if w := warnings[0]; w.Kind != "link_mtu_lowered" || !strings.Contains(w.Detail, "w2") || !strings.Contains(w.Detail, "1430") {
		t.Fatalf("warning should name the worker and the value: %+v", w)
	}

	// An operator value that does not fit names the link, the worker and the value.
	_, _, err = settleMTU(1500, true, cross)
	if err == nil {
		t.Fatalf("explicit 1500 over a 1450 ceiling must fail")
	}
	for _, want := range []string{"KUBENDT_LINK_MTU=1500", "a-0:eth1", "b-0:eth1", "w1", "1450"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err.Error(), want)
		}
	}

	// A value that fits, explicit or not, passes untouched.
	if eff, warnings, err := settleMTU(1400, true, cross); err != nil || eff != 1400 || len(warnings) != 0 {
		t.Fatalf("explicit 1400: eff=%d warnings=%v err=%v", eff, warnings, err)
	}

	// Same-worker ends have no ceiling, so a namespace without VXLAN keeps the target.
	if eff, warnings, err := settleMTU(9000, true, same); err != nil || eff != 9000 || len(warnings) != 0 {
		t.Fatalf("veth only: eff=%d warnings=%v err=%v", eff, warnings, err)
	}

	// A worker whose ceiling is still unknown does not block anything.
	unknown := []mtuEnd{{Pod: "x-0", Iface: "eth1", Worker: "w9", Peer: "y-0", PeerIf: "eth1", Cross: true}}
	if eff, _, err := settleMTU(1500, true, unknown); err != nil || eff != 1500 {
		t.Fatalf("unknown ceiling: eff=%d err=%v", eff, err)
	}
}

func TestRecordWorkerCeilingKeepsTheLargest(t *testing.T) {
	ceilingMu.Lock()
	workerCeiling = map[string]int{}
	ceilingMu.Unlock()
	defer func() {
		ceilingMu.Lock()
		workerCeiling = map[string]int{}
		ceilingMu.Unlock()
	}()
	recordWorkerCeiling("w1", 1450) // the fresh device
	recordWorkerCeiling("w1", 1400) // after a pass lowered it
	recordWorkerCeiling("", 9000)
	recordWorkerCeiling("w1", 0)
	if got := WorkerVXLANCeiling("w1"); got != 1450 {
		t.Fatalf("got %d, want 1450", got)
	}
	if got := WorkerVXLANCeiling("w2"); got != 0 {
		t.Fatalf("unknown worker: got %d, want 0", got)
	}
}

func TestVyOSGuestMTUCommands(t *testing.T) {
	var setter drivers_meta.GuestMTUSetter = drivers.NewVyOSRouterDriver()
	got := setter.GuestMTUCommands("eth2", 1450)
	if len(got) != 1 || len(got[0]) != 1 || got[0][0] != "set interfaces ethernet eth2 mtu 1450" {
		t.Fatalf("got %v", got)
	}
}

func TestSetScriptAndFailures(t *testing.T) {
	script := setScript([]setItem{{"eth1", 1450}, {"tap1", 1450}, {"eth2", 1400}})
	want := "ip link set dev eth1 mtu 1450 2>/dev/null || echo FAIL eth1; ip link set dev tap1 mtu 1450 2>/dev/null || echo FAIL tap1; ip link set dev eth2 mtu 1400 2>/dev/null || echo FAIL eth2"
	if script != want {
		t.Fatalf("script:\n%s\nwant:\n%s", script, want)
	}
	failed := parseSetFailures("FAIL eth1\nsomething else\nFAIL tap1\n")
	if len(failed) != 2 || !failed["eth1"] || !failed["tap1"] {
		t.Fatalf("got %v", failed)
	}
	if len(parseSetFailures("")) != 0 {
		t.Fatalf("no output means no failure")
	}
}

func TestGuestNeedsMTU(t *testing.T) {
	ends := []mtuEnd{{Pod: "r-0", Iface: "eth1"}, {Pod: "r-0", Iface: "eth2"}}
	both := []map[string]string{{"interface": "eth1", "mtu": "1450"}, {"interface": "eth2", "mtu": "1450"}, {"interface": "eth4"}}
	if guestNeedsMTU(both, ends, 1450) {
		t.Fatalf("a guest already at the value needs no commit")
	}
	one := []map[string]string{{"interface": "eth1", "mtu": "1450"}, {"interface": "eth2"}}
	if !guestNeedsMTU(one, ends, 1450) {
		t.Fatalf("an interface without an mtu needs the commit")
	}
	other := []map[string]string{{"interface": "eth1", "mtu": "1500"}, {"interface": "eth2", "mtu": "1450"}}
	if !guestNeedsMTU(other, ends, 1450) {
		t.Fatalf("an interface at another value needs the commit")
	}
	if !guestNeedsMTU(nil, ends, 1450) {
		t.Fatalf("nothing reported means commit")
	}
}

func TestGuestLinkMTUEnv(t *testing.T) {
	t.Setenv(LinkMTUEnv, "")
	if env := guestLinkMTUEnv(types.NodeSpec{Name: "host", Qemu: false}); env != nil {
		t.Fatalf("a container node gets no env, got %v", env)
	}
	env := guestLinkMTUEnv(types.NodeSpec{Name: "router", Qemu: true})
	if len(env) != 1 || env[0].Name != LinkMTUEnv || env[0].Value != "1450" {
		t.Fatalf("a VM node gets the default, got %v", env)
	}
	t.Setenv(LinkMTUEnv, "8950")
	if env := guestLinkMTUEnv(types.NodeSpec{Name: "router", Qemu: true}); len(env) != 1 || env[0].Value != "8950" {
		t.Fatalf("a VM node gets the operator's value, got %v", env)
	}
	t.Setenv(LinkMTUEnv, "abc")
	if env := guestLinkMTUEnv(types.NodeSpec{Name: "router", Qemu: true}); env != nil {
		t.Fatalf("an unusable value hands nothing over, got %v", env)
	}
}

func TestForgetReads(t *testing.T) {
	reads := map[string]map[string]int{"a-0": {"eth1": 1450}, "b-0": {"eth1": 1450}, "c-0": {"eth1": 1450}, "d-0": {"eth1": 1450}}
	desired := []desiredLink{{PodA: "a-0", IfA: "eth1", PodB: "b-0", IfB: "eth1"}, {PodA: "c-0", IfA: "eth1", PodB: "d-0", IfB: "eth1"}}
	forgetReads(reads, []string{"a-0"}, desired)
	if _, ok := reads["a-0"]; ok {
		t.Fatalf("the restarted pod must be forgotten")
	}
	if _, ok := reads["b-0"]; ok {
		t.Fatalf("its neighbour must be forgotten, Meshnet rebuilt the facing end")
	}
	if len(reads) != 2 {
		t.Fatalf("the other link keeps its reads, got %v", reads)
	}
	mergeReads(reads, map[string]map[string]int{"a-0": {"eth1": 1500}})
	if reads["a-0"]["eth1"] != 1500 {
		t.Fatalf("merge adds the new read")
	}
}

func TestSettleMTUWithLinkOverrides(t *testing.T) {
	ceilingMu.Lock()
	workerCeiling = map[string]int{"w1": 1450, "w2": 1450}
	ceilingMu.Unlock()
	defer func() {
		ceilingMu.Lock()
		workerCeiling = map[string]int{}
		ceilingMu.Unlock()
	}()
	fits := []mtuEnd{
		{Pod: "a-0", Iface: "eth1", Worker: "w1", Peer: "b-0", PeerIf: "eth1", Cross: true, Want: 1400},
		{Pod: "b-0", Iface: "eth1", Worker: "w2", Peer: "a-0", PeerIf: "eth1", Cross: true, Want: 1400},
	}
	eff, warnings, err := settleMTU(1450, false, fits)
	if err != nil || eff != 1450 || len(warnings) != 0 {
		t.Fatalf("a fitting override leaves the namespace value alone: eff=%d warnings=%v err=%v", eff, warnings, err)
	}
	if targetOf(fits[0], eff) != 1400 || targetOf(mtuEnd{}, eff) != 1450 {
		t.Fatalf("targets: override 1400, plain end 1450")
	}
	tooBig := []mtuEnd{{Pod: "a-0", Iface: "eth1", Worker: "w1", Peer: "b-0", PeerIf: "eth1", Cross: true, Want: 1500}}
	_, _, err = settleMTU(1450, false, tooBig)
	if err == nil || !strings.Contains(err.Error(), "asks for mtu 1500") || !strings.Contains(err.Error(), "w1") {
		t.Fatalf("an override above the ceiling fails naming the link and the worker: %v", err)
	}
}

func TestGuestNeedsMTUWithOverride(t *testing.T) {
	ends := []mtuEnd{{Pod: "r-0", Iface: "eth1", Want: 1400}, {Pod: "r-0", Iface: "eth2"}}
	entries := []map[string]string{{"interface": "eth1", "mtu": "1400"}, {"interface": "eth2", "mtu": "1450"}}
	if guestNeedsMTU(entries, ends, 1450) {
		t.Fatalf("each interface at its own target needs no commit")
	}
	entries[0]["mtu"] = "1450"
	if !guestNeedsMTU(entries, ends, 1450) {
		t.Fatalf("an overridden interface at the platform value needs the commit")
	}
}
