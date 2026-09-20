package helpers

import "testing"

func TestLinkRealization(t *testing.T) {
	cases := []struct {
		name, a, b string
		external   bool
		want       string
	}{
		{"same worker", "w1", "w1", false, LinkRealizationVeth},
		{"different workers", "w1", "w2", false, LinkRealizationVxlan},
		{"host uplink", "w1", "", true, LinkRealizationExternal},
		{"peer not scheduled", "w1", "", false, LinkRealizationPending},
		{"neither scheduled", "", "", false, LinkRealizationPending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := linkRealization(tc.a, tc.b, tc.external); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
