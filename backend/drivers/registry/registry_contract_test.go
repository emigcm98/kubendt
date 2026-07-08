package drivers_registry

import (
	"strings"
	"testing"
)

// Capability stubs, embedded into fake drivers to grant a capability.

type l2stub struct{}

func (l2stub) LinkUp(string) [][]string   { return nil }
func (l2stub) LinkDown(string) [][]string { return nil }

type l3stub struct{}

func (l3stub) SetIP(string, string) [][]string                     { return nil }
func (l3stub) ReplaceIP(string, string) [][]string                 { return nil }
func (l3stub) RemoveIP(string, string) [][]string                  { return nil }
func (l3stub) SetDefaultRoute(string) [][]string                   { return nil }
func (l3stub) RemoveDefaultRoute() [][]string                      { return nil }
func (l3stub) AddStaticRoute(string, string, string) [][]string    { return nil }
func (l3stub) RemoveStaticRoute(string, string, string) [][]string { return nil }
func (l3stub) AddDNSNameserver(string) [][]string                  { return nil }
func (l3stub) RemoveDNSNameserver(string) [][]string               { return nil }
func (l3stub) AddDNSSearch(string) [][]string                      { return nil }
func (l3stub) RemoveDNSSearch(string) [][]string                   { return nil }

type switchstub struct{}

func (switchstub) SetupBridge(string, []string) [][]string             { return nil }
func (switchstub) TeardownBridge(string) [][]string                    { return nil }
func (switchstub) AddInterfaceToBridge(string, string) [][]string      { return nil }
func (switchstub) RemoveInterfaceFromBridge(string, string) [][]string { return nil }

type baseDrv struct{ name, typ string }

func (b baseDrv) Name() string { return b.name }
func (b baseDrv) Type() string { return b.typ }

type routerOK struct {
	baseDrv
	l2stub
	l3stub
}
type routerMissingL3 struct {
	baseDrv
	l2stub
}
type switchOK struct {
	baseDrv
	l2stub
	switchstub
}
type switchMissingSwitch struct {
	baseDrv
	l2stub
}
type routerMissingL2 struct {
	baseDrv
	l3stub
}

func expectPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic containing %q, got none", want)
		}
		if msg, _ := r.(string); !strings.Contains(msg, want) {
			t.Fatalf("panic %q does not contain %q", r, want)
		}
	}()
	fn()
}

func TestRegisterEnforcesTypeContract(t *testing.T) {
	// Satisfying the contract must not panic.
	Register(func() *routerOK { return &routerOK{baseDrv: baseDrv{"TestRouterOK", "router"}} })
	Register(func() *switchOK { return &switchOK{baseDrv: baseDrv{"TestSwitchOK", "switch"}} })

	// Missing the type's required capability must panic.
	expectPanic(t, "must implement L3Capable", func() {
		Register(func() *routerMissingL3 { return &routerMissingL3{baseDrv: baseDrv{"TestRouterNoL3", "router"}} })
	})
	expectPanic(t, "must implement SwitchCapable", func() {
		Register(func() *switchMissingSwitch {
			return &switchMissingSwitch{baseDrv: baseDrv{"TestSwitchNoSwitch", "switch"}}
		})
	})
	expectPanic(t, "must implement L2Capable", func() {
		Register(func() *routerMissingL2 { return &routerMissingL2{baseDrv: baseDrv{"TestRouterNoL2", "router"}} })
	})
}
