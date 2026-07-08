package drivers

import (
	"kubendt/capabilities/capabilities"
	drivers_meta "kubendt/drivers/meta"
)

type BasicHostDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.L3Base
}

func NewBasicHostDriver() *BasicHostDriver {
	return &BasicHostDriver{
		Meta: drivers_meta.NewMeta("BasicHostDriver", "host"),
	}
}

// Compile-time checks (opcionales)
var _ capabilities.L2Capable = (*BasicHostDriver)(nil)
var _ capabilities.L3Capable = (*BasicHostDriver)(nil)
