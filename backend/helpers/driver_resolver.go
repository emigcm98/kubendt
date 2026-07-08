package helpers

import (
	"fmt"
	drivers_meta "kubendt/drivers/meta"
	drivers_registry "kubendt/drivers/registry"
	"kubendt/types"
	"strings"
)

// ResolveDriversForNodes resolves each request node's Driver field (assigning
// the type default when empty, validating registration and type consistency)
// and derives the runtime (Qemu flag) from the resolved driver. The runtime
// is intentionally NOT user-input: users can no longer flag a pod as qemu;
// they pick a driver and that driver declares its execution model.
func ResolveDriversForNodes(requestNodes []types.NodeSpec) error {
	for i, n := range requestNodes {
		typ := n.Type

		// If no driver, assign the type default.
		if strings.TrimSpace(n.Driver) == "" {
			def, err := drivers_registry.ResolveDefaultForType(typ)
			if err != nil {
				return fmt.Errorf("node %q: %v", n.Name, err)
			}
			requestNodes[i].Driver = def
		} else {
			if !drivers_registry.IsRegistered(n.Driver) {
				valid := drivers_registry.ValidDriversForType(typ)
				return fmt.Errorf("node %q: driver %q not registered. Valid drivers for type=%q: %v",
					n.Name, n.Driver, n.Type, valid)
			}
			if driverType, ok := drivers_registry.TypeOf(n.Driver); ok && driverType != typ {
				validDrivers := drivers_registry.ValidDriversForType(typ)
				return fmt.Errorf("node %q: driver %q is for type=%q but node is type=%q. Valid drivers: %v",
					n.Name, n.Driver, driverType, typ, validDrivers)
			}
		}

		// Derive the runtime from the resolved driver. Drivers that don't
		// implement RuntimeProvider default to native execution.
		requestNodes[i].Qemu = false
		if inst, err := drivers_registry.NewByName(requestNodes[i].Driver); err == nil {
			if rp, ok := inst.(drivers_meta.RuntimeProvider); ok && rp.Runtime() == drivers_meta.RuntimeQEMU {
				requestNodes[i].Qemu = true
			}
		}
	}
	return nil
}
