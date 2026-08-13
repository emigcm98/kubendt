package drivers

import (
	"kubendt/capabilities/capabilities"
	drivers_meta "kubendt/drivers/meta"
)

type LinuxRouterDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.L3Base
	capabilities.NATBase
}

func NewLinuxRouterDriver() *LinuxRouterDriver {
	return &LinuxRouterDriver{
		Meta: drivers_meta.NewMeta("LinuxRouterDriver", "router"),
	}
}

// Compile-time checks
var _ capabilities.L2Capable = (*LinuxRouterDriver)(nil)
var _ capabilities.L3Capable = (*LinuxRouterDriver)(nil)
var _ capabilities.NATCapable = (*LinuxRouterDriver)(nil)
