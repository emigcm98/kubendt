// pkg/drivers/register_all.go
package drivers_base

import (
	drivers_devices "kubendt/drivers/devices"
	drivers "kubendt/drivers/drivers"
	drivers_registry "kubendt/drivers/registry"
	"kubendt/types"
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
	drivers_devices.RegisterDevices("VyOSRouterDriver", []types.DeviceSpec{{Path: "/dev/kvm"}})
	drivers_devices.RegisterDevices("XRDRouterDriver", []types.DeviceSpec{{Path: "/dev/net/tun"}, {Path: "/dev/fuse"}})
}
