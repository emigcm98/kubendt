package helpers

import (
	"testing"

	"kubendt/types"
)

func TestNormalizeTerminationGracePeriod(t *testing.T) {
	cases := []struct {
		name    string
		in      int64
		want    int64
		wantErr bool
	}{
		{"unset takes the default", 0, types.DefaultTerminationGracePeriodSeconds, false},
		{"explicit value is kept", 30, 30, false},
		{"minimum is one second", 1, 1, false},
		{"negative is rejected", -1, -1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := types.NodeSpec{Name: "r1", TerminationGracePeriodSeconds: tc.in}
			err := NormalizeTerminationGracePeriod(&node)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if node.TerminationGracePeriodSeconds != tc.want {
				t.Fatalf("got %d, want %d", node.TerminationGracePeriodSeconds, tc.want)
			}
		})
	}
}
