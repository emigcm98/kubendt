package drivers

import (
	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
)

// LinuxSwitchDriver routes too: a Linux bridge with addresses on its VLAN
// interfaces is a layer 3 switch, the same box without NAT.
type LinuxSwitchDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.L3Base
	capabilities.SubinterfaceBase
	capabilities.SwitchBase
	capabilities.VLANBase
	capabilities.STPBase
}

func NewLinuxSwitchDriver() *LinuxSwitchDriver {
	return &LinuxSwitchDriver{
		Meta: drivers_meta.NewMeta("LinuxSwitchDriver", "switch"),
	}
}

var _ capabilities.L2Capable = (*LinuxSwitchDriver)(nil)
var _ capabilities.L3Capable = (*LinuxSwitchDriver)(nil)
var _ capabilities.SubinterfaceCapable = (*LinuxSwitchDriver)(nil)
var _ capabilities.SwitchCapable = (*LinuxSwitchDriver)(nil)
var _ capabilities.VLANCapable = (*LinuxSwitchDriver)(nil)
var _ capabilities.STPCapable = (*LinuxSwitchDriver)(nil)

var _ drivers_meta.KnownImagesProvider = (*LinuxSwitchDriver)(nil)

// KnownImages are the Alpine the examples bridge with and the Ubuntu
// tried next to it, bridging through the toolbox's ip.
func (LinuxSwitchDriver) KnownImages() []string {
	return []string{"alpine:3.24.2", "ubuntu:26.04"}
}
