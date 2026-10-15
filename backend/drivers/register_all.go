// pkg/drivers/register_all.go
package drivers_base

import (
	drivers "kubendt/drivers/drivers"
	drivers_registry "kubendt/drivers/registry"
)

func RegisterAllDrivers() {
	drivers_registry.Register(drivers.NewBasicHostDriver)
	drivers_registry.Register(drivers.NewHostDriver)
	drivers_registry.Register(drivers.NewLinuxSwitchDriver)
	drivers_registry.Register(drivers.NewOpenVSwitchDriver)
	drivers_registry.Register(drivers.NewLinuxRouterDriver)
	drivers_registry.Register(drivers.NewFRRRouterDriver)
	drivers_registry.Register(drivers.NewVyOSRouterDriver)
	drivers_registry.Register(drivers.NewXRDRouterDriver)

	// Declarative default host devices by driver.
}
