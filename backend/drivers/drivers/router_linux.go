package drivers

import (
	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
)

type LinuxRouterDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.L3Base
	capabilities.NATBase
	capabilities.NAT64Base
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
var _ capabilities.NAT64Capable = (*LinuxRouterDriver)(nil)

var _ drivers_meta.KnownImagesProvider = (*LinuxRouterDriver)(nil)

// KnownImages are the Alpine and the Ubuntu the Linux router was tried
// with; Ubuntu has no iproute2 or iptables, the toolbox brings them.
func (LinuxRouterDriver) KnownImages() []string {
	return []string{"alpine:3.24.2", "ubuntu:26.04"}
}
