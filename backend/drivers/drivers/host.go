package drivers

import (
	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
)

// HostDriver is the one host driver, L2 and L3 on any Linux image.
type HostDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.L3Base
}

func NewHostDriver() *HostDriver {
	return &HostDriver{
		Meta: drivers_meta.NewMeta("HostDriver", "host"),
	}
}

var _ capabilities.L2Capable = (*HostDriver)(nil)
var _ capabilities.L3Capable = (*HostDriver)(nil)
