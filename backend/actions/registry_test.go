package actions

import (
	"fmt"
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
		Action[pinger]{Type: "test_ping", Label: "Ping", Params: P(Iface, CIDR, Gateway.Optional(), ExternalPort.Optional(), Cost.Optional(), Priority.Optional()),
			Run: func(c pinger, a types.ActionEntry) ([][]string, error) { return c.Ping(a.Iface, a.CIDR), nil }},
		Action[pinger]{Type: "test_ping_eth0", Label: "Ping from eth0", Params: P(Iface), AllowProtectedIface: true,
			Run: func(c pinger, a types.ActionEntry) ([][]string, error) { return c.Ping(a.Iface, ""), nil }},
	)
	Register[any](
		Capability{ID: "test_universal", Label: "Universal"},
		Action[any]{Type: "test_echo", Label: "Echo", Params: P(Iface, Ifaces.Optional(), Executor), Executor: "kubectl", ExecutorFromRequest: true,
			Run: func(_ any, a types.ActionEntry) ([][]string, error) {
				return [][]string{append([]string{"echo", a.Iface}, a.Ifaces...)}, nil
			}},
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
		{"negative number", types.ActionEntry{Iface: "eth1", CIDR: "10.0.0.1/24", Cost: -1}, `must not be negative`},
		{"zero through a pointer is present and fine", types.ActionEntry{Iface: "eth1", CIDR: "10.0.0.1/24", Priority: new(int)}, ""},
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
	ids := func(driver any) string {
		var out []string
		for _, c := range ForDriver(driver) {
			out = append(out, c.ID)
		}
		return strings.Join(out, ",")
	}
	if got := ids(fakeDriver{}); got != "PingCapable,test_universal" {
		t.Errorf("ForDriver: got %v", got)
	}
	if got := ids(otherDriver{}); got != "test_universal" {
		t.Errorf("ForDriver on a driver without capabilities: got %v", got)
	}
}

func TestUniversal(t *testing.T) {
	d, _ := Lookup("test_echo")
	if !d.Universal || !d.Supports(nil) || !d.Supports(otherDriver{}) {
		t.Errorf("a capability declared against any must match every driver and none")
	}
	cmds, err := d.Run(nil, types.ActionEntry{Iface: "eth1"})
	if err != nil || strings.Join(cmds[0], " ") != "echo eth1" {
		t.Errorf("Run without a driver: got %v, %v", cmds, err)
	}
	if err := d.Validate(types.ActionEntry{Iface: "eth1", Executor: "nope"}); err == nil || !strings.Contains(err.Error(), `field "executor" must name a registered executor`) {
		t.Errorf("unknown executor should fail validation, got %v", err)
	}
	if err := d.Validate(types.ActionEntry{Iface: "eth1", Executor: "kubectl"}); err != nil {
		t.Errorf("a registered executor should pass, got %v", err)
	}
}

func TestMapInterfaces(t *testing.T) {
	d, _ := Lookup("test_echo")
	original := types.ActionEntry{Iface: "eth1", Ifaces: []string{"eth2", "eth3"}}
	mapped, err := d.MapInterfaces(original, func(name string) (string, error) { return "Gig-" + name, nil })
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Iface != "Gig-eth1" || strings.Join(mapped.Ifaces, ",") != "Gig-eth2,Gig-eth3" {
		t.Errorf("mapped: %+v", mapped)
	}
	if original.Iface != "eth1" || strings.Join(original.Ifaces, ",") != "eth2,eth3" {
		t.Errorf("the request must stay untouched: %+v", original)
	}
	if _, err := d.MapInterfaces(original, func(name string) (string, error) { return "", fmt.Errorf("no map for %s", name) }); err == nil {
		t.Errorf("a mapping error must surface")
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
