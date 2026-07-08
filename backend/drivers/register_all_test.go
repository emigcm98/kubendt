package drivers_base

import (
	"testing"

	caps "kubendt/capabilities/capabilities"
	drivers_registry "kubendt/drivers/registry"
)

// TestRegisterAllDriversSatisfyContract ensures every real driver registers
// without tripping the per-type capability contract enforced in Register (which
// panics on violation), and pins the intended shape: all drivers speak L2,
// routers and hosts speak L3, switches provide bridge ops but are L2-only
// (no L3 — a switch must not pretend to route).
func TestRegisterAllDriversSatisfyContract(t *testing.T) {
	// Register panics on a contract violation; a clean call is itself the
	// baseline assertion.
	RegisterAllDrivers()

	for _, d := range drivers_registry.ListAll() {
		inst, err := drivers_registry.NewByName(d.Name)
		if err != nil {
			t.Fatalf("driver %q registered but NewByName failed: %v", d.Name, err)
		}

		if _, ok := inst.(caps.L2Capable); !ok {
			t.Errorf("driver %q (type %s) must be L2Capable", d.Name, d.Type)
		}

		_, isL3 := inst.(caps.L3Capable)
		_, isSwitch := inst.(caps.SwitchCapable)

		switch d.Type {
		case "router", "host":
			if !isL3 {
				t.Errorf("driver %q (type %s) must be L3Capable", d.Name, d.Type)
			}
		case "switch":
			if !isSwitch {
				t.Errorf("driver %q (type switch) must be SwitchCapable", d.Name)
			}
			if isL3 {
				t.Errorf("driver %q is a switch but implements L3Capable; switches are expected to be L2-only", d.Name)
			}
		default:
			t.Errorf("driver %q has unexpected type %q", d.Name, d.Type)
		}
	}
}
