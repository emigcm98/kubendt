package helpers

import (
	"testing"

	"kubendt/types"
)

func burstArg(args []string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "burst" {
			return args[i+1], true
		}
	}
	return "", false
}

func TestBuildTCParamsFromStructTBFBurst(t *testing.T) {
	cases := []struct {
		name  string
		burst string
		want  string // "" means the burst must be dropped
	}{
		{"lowercase kb", "32kb", "32kb"},
		{"capital Kb", "32Kb", "32Kb"},
		{"kbit unit", "32kbit", "32kbit"},
		{"bare bytes", "1600", "1600"},
		{"out of range", "999999kb", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &types.TCParamEntry{Qdisc: "tbf", Rate: "5mbit", Burst: tc.burst, Latency: "50ms"}
			got, ok := burstArg(buildTCParamsFromStruct(p))
			if tc.want == "" {
				if ok {
					t.Errorf("burst should be dropped, got %q", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Errorf("burst = %q (present=%v), want %q", got, ok, tc.want)
			}
		})
	}
}
