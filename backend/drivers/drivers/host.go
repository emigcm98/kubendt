package drivers

import (
	"kubendt/capabilities/capabilities"
	drivers_meta "kubendt/drivers/meta"
)

// HostDriver: L2 + L3, con un override de ReplaceIP como ejemplo
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

// OVERRIDE: ReplaceIP con idempotencia extra (ejemplo)
func (HostDriver) ReplaceIP(iface, cidr string) [][]string {
	// uses "ip addr show" to check and avoid work if already equal
	cmd := []string{
		"sh", "-c",
		`ip -o addr show dev ` + iface + ` | grep -q ' ` + cidr + ` ' || { ip addr flush dev ` + iface + `; ip addr add ` + cidr + ` dev ` + iface + `; }`,
	}
	return [][]string{cmd}
}
