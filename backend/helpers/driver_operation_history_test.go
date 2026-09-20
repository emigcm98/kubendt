package helpers

import (
	"testing"
	"time"
)

func TestParseExecutedAt(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want time.Time
	}{
		{"2026-07-29T17:22:59.067761927Z", true, time.Date(2026, 7, 29, 17, 22, 59, 67761927, time.UTC)},
		{"2026-09-20T21:29:14Z", true, time.Date(2026, 9, 20, 21, 29, 14, 0, time.UTC)},
		{"2026-01-05 10:11:12", true, time.Date(2026, 1, 5, 10, 11, 12, 0, time.UTC)},
		{"not a date", false, time.Time{}},
	}
	for _, tc := range cases {
		got, ok := parseExecutedAt(tc.in)
		if ok != tc.ok || (ok && !got.Equal(tc.want)) {
			t.Fatalf("parseExecutedAt(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
