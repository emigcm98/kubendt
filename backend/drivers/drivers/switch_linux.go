package drivers

import (
	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
)

type LinuxSwitchDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.SwitchBase
}

func NewLinuxSwitchDriver() *LinuxSwitchDriver {
	return &LinuxSwitchDriver{
		Meta: drivers_meta.NewMeta("LinuxSwitchDriver", "switch"),
	}
}

var _ capabilities.L2Capable = (*LinuxSwitchDriver)(nil)
var _ capabilities.SwitchCapable = (*LinuxSwitchDriver)(nil)

var _ drivers_meta.KnownImagesProvider = (*LinuxSwitchDriver)(nil)

// KnownImages is the Alpine the examples bridge with.
func (LinuxSwitchDriver) KnownImages() []string {
	return []string{"alpine:3.24.2"}
}
