package helpers

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"kubendt/types"
)

func TestBuildTCParams(t *testing.T) {
	limit, zero := 500, 0
	cases := []struct {
		name    string
		in      *types.TCParamEntry
		qdisc   string
		want    []string
		wantErr string
	}{
		{"nil params", nil, "", nil, "tcparams is required"},
		{"empty qdisc defaults to netem", &types.TCParamEntry{Delay: "10ms"}, "netem", []string{"delay", "10ms"}, ""},
		{"unsupported qdisc", &types.TCParamEntry{Qdisc: "htb"}, "", nil, `tcparams.qdisc "htb" is not supported`},
		{"netem full", &types.TCParamEntry{Qdisc: "netem", Delay: "50ms", Jitter: "10ms", Loss: "1%", Duplicate: "0.5%", Corrupt: "0.1%", Limit: &limit},
			"netem", []string{"delay", "50ms", "10ms", "loss", "1%", "duplicate", "0.5%", "corrupt", "0.1%", "limit", "500"}, ""},
		{"bare numbers get units", &types.TCParamEntry{Qdisc: "netem", Delay: "20", Loss: "5"}, "netem", []string{"delay", "20ms", "loss", "5%"}, ""},
		{"seconds and microseconds accepted", &types.TCParamEntry{Qdisc: "netem", Delay: "1.5s", Jitter: "500us"}, "netem", []string{"delay", "1.5s", "500us"}, ""},
		{"zero impairments are omitted", &types.TCParamEntry{Qdisc: "netem", Delay: "0ms", Jitter: "0ms", Loss: "0%", Corrupt: "0"}, "netem", []string{}, ""},
		{"delay out of range", &types.TCParamEntry{Qdisc: "netem", Delay: "99999ms"}, "", nil, `tcparams.delay "99999ms" is out of range (0 to 60000 ms)`},
		{"delay in seconds out of range", &types.TCParamEntry{Qdisc: "netem", Delay: "61s"}, "", nil, "tcparams.delay"},
		{"loss out of range", &types.TCParamEntry{Qdisc: "netem", Loss: "150%"}, "", nil, `tcparams.loss "150%" is out of range (0 to 100%)`},
		{"malformed time", &types.TCParamEntry{Qdisc: "netem", Delay: "fast"}, "", nil, `tcparams.delay "fast" is not a time`},
		{"negative percentage", &types.TCParamEntry{Qdisc: "netem", Loss: "-1%"}, "", nil, "tcparams.loss"},
		{"jitter without delay", &types.TCParamEntry{Qdisc: "netem", Jitter: "10ms"}, "", nil, "tcparams.jitter requires a delay"},
		{"limit out of range", &types.TCParamEntry{Qdisc: "netem", Limit: &zero}, "", nil, "tcparams.limit 0 is out of range"},
		{"tbf field on netem", &types.TCParamEntry{Qdisc: "netem", Delay: "10ms", Burst: "32kbit"}, "", nil, "tcparams.burst is a tbf parameter"},
		{"netem rate", &types.TCParamEntry{Qdisc: "netem", Delay: "20ms", Rate: "2mbit"}, "netem", []string{"delay", "20ms", "rate", "2mbit"}, ""},
		{"composite", &types.TCParamEntry{Qdisc: "netem", Delay: "50ms", Jitter: "10ms", Loss: "1%", Reorder: "5%", Rate: "10mbit", Limit: &limit},
			"netem", []string{"delay", "50ms", "10ms", "loss", "1%", "reorder", "5%", "rate", "10mbit", "limit", "500"}, ""},
		{"distribution normal", &types.TCParamEntry{Qdisc: "netem", Delay: "50ms", Jitter: "10ms", Distribution: "normal"}, "netem", []string{"delay", "50ms", "10ms", "distribution", "normal"}, ""},
		{"distribution uniform is tc's default", &types.TCParamEntry{Qdisc: "netem", Delay: "50ms", Jitter: "10ms", Distribution: "uniform"}, "netem", []string{"delay", "50ms", "10ms"}, ""},
		{"distribution without jitter", &types.TCParamEntry{Qdisc: "netem", Delay: "50ms", Distribution: "normal"}, "", nil, "tcparams.distribution requires a jitter"},
		{"unknown distribution", &types.TCParamEntry{Qdisc: "netem", Delay: "50ms", Jitter: "5ms", Distribution: "gaussian"}, "", nil, `tcparams.distribution "gaussian" is not supported`},
		{"delay correlation", &types.TCParamEntry{Qdisc: "netem", Delay: "50ms", Jitter: "10ms", DelayCorrelation: "25%"}, "netem", []string{"delay", "50ms", "10ms", "25%"}, ""},
		{"delay correlation without jitter", &types.TCParamEntry{Qdisc: "netem", Delay: "50ms", DelayCorrelation: "25%"}, "", nil, "tcparams.delay_correlation requires a jitter"},
		{"loss correlation", &types.TCParamEntry{Qdisc: "netem", Loss: "5%", LossCorrelation: "25%"}, "netem", []string{"loss", "5%", "25%"}, ""},
		{"reorder with correlation", &types.TCParamEntry{Qdisc: "netem", Delay: "10ms", Reorder: "25%", ReorderCorrelation: "50%"}, "netem", []string{"delay", "10ms", "reorder", "25%", "50%"}, ""},
		{"reorder without delay", &types.TCParamEntry{Qdisc: "netem", Reorder: "25%"}, "", nil, "tcparams.reorder requires a delay"},
		{"gemodel p and r", &types.TCParamEntry{Qdisc: "netem", Gemodel: &types.GemodelSpec{P: "1%", R: "10%"}}, "netem", []string{"loss", "gemodel", "1%", "10%"}, ""},
		{"gemodel full", &types.TCParamEntry{Qdisc: "netem", Gemodel: &types.GemodelSpec{P: "1", R: "10", H: "100", K: "0"}}, "netem", []string{"loss", "gemodel", "1%", "10%", "100%", "0%"}, ""},
		{"gemodel without p", &types.TCParamEntry{Qdisc: "netem", Gemodel: &types.GemodelSpec{R: "10%"}}, "", nil, "tcparams.gemodel.p is required"},
		{"gemodel 1-h without r", &types.TCParamEntry{Qdisc: "netem", Gemodel: &types.GemodelSpec{P: "1%", H: "50%"}}, "", nil, "tcparams.gemodel.1-h needs tcparams.gemodel.r"},
		{"loss and gemodel together", &types.TCParamEntry{Qdisc: "netem", Loss: "1%", Gemodel: &types.GemodelSpec{P: "1%"}}, "", nil, "exclusive"},
		{"reorder on tbf", &types.TCParamEntry{Qdisc: "tbf", Rate: "1mbit", Reorder: "5%"}, "", nil, "tcparams.reorder is a netem parameter"},
		{"tbf full", &types.TCParamEntry{Qdisc: "tbf", Rate: "5mbit", Burst: "32kbit", Latency: "50ms"}, "tbf", []string{"rate", "5mbit", "burst", "32kbit", "latency", "50ms"}, ""},
		{"tbf defaults burst and latency", &types.TCParamEntry{Qdisc: "tbf", Rate: "500kbit"}, "tbf", []string{"rate", "500kbit", "burst", "32kbit", "latency", "50ms"}, ""},
		{"tbf units are lowercased", &types.TCParamEntry{Qdisc: "tbf", Rate: "1Mbit", Burst: "32Kb", Latency: "400ms"}, "tbf", []string{"rate", "1mbit", "burst", "32kb", "latency", "400ms"}, ""},
		{"tbf bare burst is bytes", &types.TCParamEntry{Qdisc: "tbf", Rate: "1mbit", Burst: "1600"}, "tbf", []string{"rate", "1mbit", "burst", "1600", "latency", "50ms"}, ""},
		{"tbf without rate", &types.TCParamEntry{Qdisc: "tbf", Burst: "32kbit"}, "", nil, "tcparams.rate is required for tbf"},
		{"tbf zero rate", &types.TCParamEntry{Qdisc: "tbf", Rate: "0mbit"}, "", nil, "tcparams.rate must be greater than zero"},
		{"tbf malformed rate", &types.TCParamEntry{Qdisc: "tbf", Rate: "fast"}, "", nil, `tcparams.rate "fast" is not a rate`},
		{"tbf latency out of range", &types.TCParamEntry{Qdisc: "tbf", Rate: "1mbit", Latency: "0ms"}, "", nil, "tcparams.latency"},
		{"netem field on tbf", &types.TCParamEntry{Qdisc: "tbf", Rate: "1mbit", Delay: "10ms"}, "", nil, "tcparams.delay is a netem parameter"},
		{"limit on tbf", &types.TCParamEntry{Qdisc: "tbf", Rate: "1mbit", Limit: &limit}, "", nil, "tcparams.limit is a netem parameter"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			qdisc, got, err := buildTCParams(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got qdisc=%q args=%v", tc.wantErr, qdisc, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if qdisc != tc.qdisc {
				t.Errorf("qdisc = %q, want %q", qdisc, tc.qdisc)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("args = %v, want %v", got, tc.want)
			}
		})
	}
}

// Every apply resets the root first: `tc qdisc replace` over a qdisc of the
// same kind keeps the parameters the new request does not mention.
func TestQdiscCommandsStartFromACleanRoot(t *testing.T) {
	reset := []string{"tc", "qdisc", "replace", "dev", "eth1", "root", "noqueue"}

	got := buildAddQdiscCommand("eth1", "netem", []string{"delay", "10ms"})
	want := [][]string{reset, {"tc", "qdisc", "replace", "dev", "eth1", "root", "netem", "delay", "10ms"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("add commands = %v, want %v", got, want)
	}

	// The delete succeeds on an unshaped interface too, and leaves the
	// kernel default rather than an explicit noqueue.
	got = buildDelQdiscCommand("eth1")
	want = [][]string{reset, {"tc", "qdisc", "del", "dev", "eth1", "root"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("del commands = %v, want %v", got, want)
	}
}

func intPtr(n int) *int { return &n }

// An htb request becomes a reset, the root with its default class, the link
// class, one class per entry, their leaves and the flower filters, in that order.
func TestHTBCommands(t *testing.T) {
	p := &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{
		{Name: "voice", Rate: "2mbit", Prio: intPtr(0), Match: []types.TCMatch{{Proto: "udp", DstPort: intPtr(5060)}, {DSCP: intPtr(46)}},
			Shaping: &types.TCParamEntry{Delay: "5ms"}},
		{Name: "bulk", Rate: "8mbit", Ceil: "10mbit", Prio: intPtr(1), Default: true, Queue: "pfifo", Limit: intPtr(100)},
	}}
	got, err := buildShapingCommands("eth1", p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{
		{"tc", "qdisc", "replace", "dev", "eth1", "root", "noqueue"},
		{"tc", "qdisc", "replace", "dev", "eth1", "root", "handle", "1:", "htb", "default", "12"},
		{"tc", "class", "add", "dev", "eth1", "parent", "1:", "classid", "1:1", "htb", "rate", "10mbit", "ceil", "10mbit"},
		{"tc", "class", "add", "dev", "eth1", "parent", "1:1", "classid", "1:11", "htb", "rate", "2mbit", "ceil", "10mbit", "prio", "0"},
		{"tc", "class", "add", "dev", "eth1", "parent", "1:1", "classid", "1:12", "htb", "rate", "8mbit", "ceil", "10mbit", "prio", "1"},
		{"tc", "qdisc", "add", "dev", "eth1", "parent", "1:11", "handle", "11:", "netem", "delay", "5ms"},
		{"tc", "qdisc", "add", "dev", "eth1", "parent", "1:12", "handle", "12:", "pfifo", "limit", "100"},
		{"tc", "filter", "add", "dev", "eth1", "parent", "1:", "protocol", "ip", "prio", "1", "flower", "ip_proto", "udp", "dst_port", "5060", "flowid", "1:11"},
		{"tc", "filter", "add", "dev", "eth1", "parent", "1:", "protocol", "ip", "prio", "2", "flower", "ip_tos", "0xb8/0xfc", "flowid", "1:11"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands =\n%v\nwant\n%v", got, want)
	}
}

func TestHTBValidation(t *testing.T) {
	ok := types.TCClassEntry{Rate: "1mbit", Default: true}
	cases := []struct {
		name    string
		p       *types.TCParamEntry
		wantErr string
	}{
		{"no rate", &types.TCParamEntry{Qdisc: "htb", Classes: []types.TCClassEntry{ok}}, "tcparams.rate is required for htb"},
		{"no classes", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit"}, "tcparams.classes is required"},
		{"classes without htb", &types.TCParamEntry{Qdisc: "netem", Classes: []types.TCClassEntry{ok}}, "needs qdisc htb"},
		{"impairment on the root", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Delay: "5ms", Classes: []types.TCClassEntry{ok}}, "tcparams.delay does not apply to htb"},
		{"no default", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{{Rate: "1mbit", Match: []types.TCMatch{{Proto: "udp"}}}}}, "one class must have default"},
		{"two defaults", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{ok, ok}}, "only one class can be the default"},
		{"no match and not default", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{ok, {Rate: "1mbit"}}}, "classes[1] needs a match"},
		{"rate above link", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{{Rate: "20mbit", Default: true}}}, "above the link rate"},
		{"ceil below rate", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{{Rate: "5mbit", Ceil: "2mbit", Default: true}}}, "below its rate"},
		{"rates add up above link", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{{Rate: "6mbit", Default: true}, {Rate: "6mbit", Match: []types.TCMatch{{Proto: "udp"}}}}}, "add up to 12mbit"},
		{"prio out of range", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{{Rate: "1mbit", Default: true, Prio: intPtr(9)}}}, "prio 9 is out of range"},
		{"unknown queue", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{{Rate: "1mbit", Default: true, Queue: "codel"}}}, `queue "codel" is not supported`},
		{"shaping with a rate", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{{Rate: "1mbit", Default: true, Shaping: &types.TCParamEntry{Rate: "5mbit"}}}}, "shaping.rate does not apply"},
		{"shaping on a plain queue", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{{Rate: "1mbit", Default: true, Queue: "sfq", Shaping: &types.TCParamEntry{Delay: "5ms"}}}}, "shaping needs queue netem"},
		{"bad shaping value", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{{Rate: "1mbit", Default: true, Shaping: &types.TCParamEntry{Delay: "fast"}}}}, `shaping: tcparams.delay "fast"`},
		{"port without proto", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{ok, {Rate: "1mbit", Match: []types.TCMatch{{DstPort: intPtr(80)}}}}}, "dstPort needs proto tcp or udp"},
		{"bad cidr", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{ok, {Rate: "1mbit", Match: []types.TCMatch{{Dst: "10.0.0.0/33"}}}}}, "is not an IP address or CIDR"},
		{"empty match", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{ok, {Rate: "1mbit", Match: []types.TCMatch{{}}}}}, "needs at least one of"},
		{"unknown proto", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{ok, {Rate: "1mbit", Match: []types.TCMatch{{Proto: "sctp"}}}}}, `proto "sctp" is not supported`},
		{"dscp out of range", &types.TCParamEntry{Qdisc: "htb", Rate: "10mbit", Classes: []types.TCClassEntry{ok, {Rate: "1mbit", Match: []types.TCMatch{{DSCP: intPtr(64)}}}}}, "dscp 64 is out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildShapingCommands("eth1", tc.p)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

// A vlan match registers the filter under 802.1Q with the IP keys behind
// vlan_ethtype, a bare IP match under protocol ip.
func TestFlowerArgs(t *testing.T) {
	protocol, keys, err := flowerArgs("m", types.TCMatch{VLAN: intPtr(10), Proto: "tcp", DstPort: intPtr(443)})
	if err != nil || protocol != "802.1Q" || !slices.Equal(keys, []string{"vlan_id", "10", "vlan_ethtype", "ip", "ip_proto", "tcp", "dst_port", "443"}) {
		t.Errorf("vlan match = %q %v %v", protocol, keys, err)
	}
	protocol, keys, err = flowerArgs("m", types.TCMatch{Src: "192.168.1.0/24", DSCP: intPtr(0)})
	if err != nil || protocol != "ip" || !slices.Equal(keys, []string{"src_ip", "192.168.1.0/24", "ip_tos", "0x0/0xfc"}) {
		t.Errorf("ip match = %q %v %v", protocol, keys, err)
	}
}
