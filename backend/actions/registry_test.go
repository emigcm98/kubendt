package actions

import (
	"strings"
	"testing"

	"kubendt/types"
)

type pinger interface {
	Ping(iface, cidr string) [][]string
}

type fakeDriver struct{}

func (fakeDriver) Ping(iface, cidr string) [][]string { return [][]string{{"ping", iface, cidr}} }

type otherDriver struct{}

func init() {
	Register[pinger](
		Capability{ID: "PingCapable", Label: "Ping"},
		Action[pinger]{Type: "test_ping", Label: "Ping", Params: P(Iface, CIDR, Gateway.Optional(), ExternalPort.Optional()),
			Run: func(c pinger, a types.ActionEntry) ([][]string, error) { return c.Ping(a.Iface, a.CIDR), nil }},
		Action[pinger]{Type: "test_ping_eth0", Label: "Ping from eth0", Params: P(Iface), AllowProtectedIface: true,
			Run: func(c pinger, a types.ActionEntry) ([][]string, error) { return c.Ping(a.Iface, ""), nil }},
	)
}

func TestValidate(t *testing.T) {
	d, _ := Lookup("test_ping")
	cases := []struct {
		name   string
		action types.ActionEntry
		want   string // substring of the error, empty for a valid request
	}{
		{"valid", types.ActionEntry{Iface: "eth1", CIDR: "10.0.0.1/24"}, ""},
		{"bare address is a cidr", types.ActionEntry{Iface: "eth1", CIDR: "10.0.0.1"}, ""},
		{"optional absent", types.ActionEntry{Iface: "eth1", CIDR: "10.0.0.1/24", Gateway: ""}, ""},
		{"missing required", types.ActionEntry{Iface: "eth1"}, `missing required field "cidr"`},
		{"bad cidr", types.ActionEntry{Iface: "eth1", CIDR: "nope"}, `field "cidr" must be an address`},
		{"bad iface", types.ActionEntry{Iface: "eth/1", CIDR: "10.0.0.1/24"}, `field "iface"`},
		{"bad gateway", types.ActionEntry{Iface: "eth1", CIDR: "10.0.0.1/24", Gateway: "router"}, `field "gateway" must be an IP`},
		{"port out of range", types.ActionEntry{Iface: "eth1", CIDR: "10.0.0.1/24", ExternalPort: 70000}, `between 1 and 65535`},
		{"protected iface", types.ActionEntry{Iface: "eth0", CIDR: "10.0.0.1/24"}, "protected interface eth0 (only test_ping_eth0)"},
	}
	for _, c := range cases {
		c.action.Type = "test_ping"
		err := d.Validate(c.action)
		if c.want == "" && err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

func TestProtectedIfaceAllowed(t *testing.T) {
	d, _ := Lookup("test_ping_eth0")
	if err := d.Validate(types.ActionEntry{Type: "test_ping_eth0", Iface: "eth0"}); err != nil {
		t.Errorf("eth0 should be allowed here, got %v", err)
	}
}

func TestSupportsAndRun(t *testing.T) {
	d, ok := Lookup("test_ping")
	if !ok {
		t.Fatal("test_ping not registered")
	}
	if !d.Supports(fakeDriver{}) || d.Supports(otherDriver{}) {
		t.Errorf("Supports should follow the interface")
	}
	cmds, err := d.Run(fakeDriver{}, types.ActionEntry{Iface: "eth1", CIDR: "10.0.0.1/24"})
	if err != nil || len(cmds) != 1 || strings.Join(cmds[0], " ") != "ping eth1 10.0.0.1/24" {
		t.Errorf("Run: got %v, %v", cmds, err)
	}
	if _, err := d.Run(otherDriver{}, types.ActionEntry{}); err == nil {
		t.Errorf("Run on a driver without the capability should fail")
	}
	if _, ok := Lookup("no_such_action"); ok {
		t.Errorf("unknown action found")
	}
}

func TestForDriver(t *testing.T) {
	var ids []string
	for _, c := range ForDriver(fakeDriver{}) {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "PingCapable" {
		t.Errorf("ForDriver: got %v", ids)
	}
	if got := ForDriver(otherDriver{}); len(got) != 0 {
		t.Errorf("ForDriver on a driver without capabilities: got %v", got)
	}
}

func TestRegisterRejectsUnknownParam(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), `"nope"`) {
			t.Errorf("expected a panic naming the param, got %v", r)
		}
	}()
	Register[pinger](Capability{ID: "Bad"}, Action[pinger]{Type: "test_bad", Params: P(Param{Name: "nope"})})
}
