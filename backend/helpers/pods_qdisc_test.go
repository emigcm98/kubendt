package helpers

import "testing"

func TestParseQdiscShowToMapNetem(t *testing.T) {
	cases := []struct {
		name       string
		output     string
		wantDelay  string
		wantJitter string // "" means the field must be absent
		wantLoss   string // "" means the field must be absent
	}{
		{
			name:       "delay and jitter",
			output:     "qdisc netem 8002: root refcnt 5 limit 1000 delay 50ms 10ms seed 5915600613449923129",
			wantDelay:  "50ms",
			wantJitter: "10ms",
		},
		{
			// Regression: the seed token used to be captured as the jitter value
			// when netem printed a delay without a jitter.
			name:      "delay without jitter, seed present",
			output:    "qdisc netem 8001: root refcnt 2 limit 1000 delay 80ms seed 8240525997128663624",
			wantDelay: "80ms",
		},
		{
			name:      "delay then loss, no jitter",
			output:    "qdisc netem 8003: root refcnt 2 limit 1000 delay 100ms loss 1% seed 42",
			wantDelay: "100ms",
			wantLoss:  "1%",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ParseQdiscShowToMap(tc.output)
			if err != nil {
				t.Fatalf("ParseQdiscShowToMap error: %v", err)
			}
			if got := res["delay"]; got != tc.wantDelay {
				t.Errorf("delay = %v, want %q", got, tc.wantDelay)
			}
			if tc.wantJitter == "" {
				if got, ok := res["jitter"]; ok {
					t.Errorf("jitter should be absent, got %v", got)
				}
			} else if got := res["jitter"]; got != tc.wantJitter {
				t.Errorf("jitter = %v, want %q", got, tc.wantJitter)
			}
			if tc.wantLoss == "" {
				if got, ok := res["loss"]; ok {
					t.Errorf("loss should be absent, got %v", got)
				}
			} else if got := res["loss"]; got != tc.wantLoss {
				t.Errorf("loss = %v, want %q", got, tc.wantLoss)
			}
		})
	}
}
