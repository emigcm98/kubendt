package capabilities

import (
	"reflect"
	"testing"

	"kubendt/actions"
	"kubendt/types"
)

// v4Only has exactly the L3Capable method set, promoted from the embedded
// interface, so it lacks RemoveDefaultRoute6.
type v4Only struct{ L3Capable }

func TestRemoveDefaultRouteFamily(t *testing.T) {
	d, ok := actions.Lookup("remove_default_route")
	if !ok {
		t.Fatal("remove_default_route is not registered")
	}
	cases := []struct {
		name   string
		driver any
		family string
		want   [][]string
		fails  bool
	}{
		{"v4 by default", L3Base{}, "", [][]string{{"ip", "route", "del", "default"}}, false},
		{"v4 explicit", L3Base{}, "4", [][]string{{"ip", "route", "del", "default"}}, false},
		{"v6", L3Base{}, "6", [][]string{{"ip", "-6", "route", "del", "default"}}, false},
		{"v6 on a driver without it", v4Only{L3Base{}}, "6", nil, true},
	}
	if err := d.Validate(types.ActionEntry{Type: "remove_default_route", Family: "5"}); err == nil {
		t.Error("family 5 should fail validation")
	}
	for _, c := range cases {
		got, err := d.Run(c.driver, types.ActionEntry{Type: "remove_default_route", Family: c.family})
		if c.fails {
			if err == nil {
				t.Errorf("%s: expected an error, got %v", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, got, c.want)
		}
	}
}

func TestIsIPv6(t *testing.T) {
	cases := map[string]bool{
		"fd00::1":        true,
		"fd00:1::2/64":   true,
		"::1":            true,
		"10.0.0.1":       false,
		"10.0.0.1/24":    false,
		"::ffff:1.2.3.4": false,
		"":               false,
		"eth1":           false,
	}
	for addr, want := range cases {
		if got := IsIPv6(addr); got != want {
			t.Errorf("IsIPv6(%q) = %v, want %v", addr, got, want)
		}
	}
}
