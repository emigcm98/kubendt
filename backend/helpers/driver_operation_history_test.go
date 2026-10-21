package helpers

import (
	"kubendt/types"
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

func TestAddressInverse(t *testing.T) {
	cases := map[string][]string{
		"remove_ip":  {"set_ip", "replace_ip"},
		"set_ip":     {"remove_ip"},
		"replace_ip": {"remove_ip"},
		"link_down":  nil,
	}
	for typ, want := range cases {
		got := addressInverse(types.ActionEntry{Type: typ})
		if len(got) != len(want) {
			t.Errorf("%s: got %v, want %v", typ, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: got %v, want %v", typ, got, want)
			}
		}
	}
}

func TestUndoneAfter(t *testing.T) {
	op := func(typ, iface, cidr, at string) PersistedDriverOperation {
		return PersistedDriverOperation{ActionType: typ, Action: types.ActionEntry{Type: typ, Iface: iface, CIDR: cidr}, ExecutedAt: at}
	}
	ops := []PersistedDriverOperation{
		op("set_ip", "eth2", "10.0.1.9/24", "2026-10-21T11:36:55.100Z"),
		op("remove_ip", "eth2", "10.0.1.9/24", "2026-10-21T11:36:55.200Z"),
		op("remove_ip", "eth2", "10.0.1.77/24", "2026-10-21T11:36:55.300Z"),
	}
	setAt, _ := parseExecutedAt("2026-10-21T11:36:55.100Z")
	if !undoneAfter(ops, addressInverse(types.ActionEntry{Type: "set_ip"}), "eth2", "10.0.1.9/24", setAt) {
		t.Error("the remove after the set undoes it")
	}
	removeAt, _ := parseExecutedAt("2026-10-21T11:36:55.200Z")
	if undoneAfter(ops, addressInverse(types.ActionEntry{Type: "remove_ip"}), "eth2", "10.0.1.9/24", removeAt) {
		t.Error("nothing sets the address after its remove")
	}
	if undoneAfter(ops, addressInverse(types.ActionEntry{Type: "set_ip"}), "eth2", "10.0.1.50/24", setAt) {
		t.Error("another address's remove does not count")
	}
}
