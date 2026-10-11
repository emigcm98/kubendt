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
