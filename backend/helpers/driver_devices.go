package helpers

import (
	"strings"

	drivers_meta "kubendt/drivers/meta"
	drivers_registry "kubendt/drivers/registry"
	"kubendt/types"
)

// ResolveEffectiveDevicesForNode merges the devices the topology lists with
// what the driver declares, deduplicated by path. A qemu node without a
// driver still gets /dev/kvm.
func ResolveEffectiveDevicesForNode(node types.NodeSpec) []types.DeviceSpec {
	effective := append([]types.DeviceSpec{}, node.Devices...)
	seen := map[string]bool{}
	for _, dev := range effective {
		if p := strings.TrimSpace(dev.Path); p != "" {
			seen[p] = true
		}
	}
	appendDevice := func(path string) {
		p := strings.TrimSpace(path)
		if p == "" || seen[p] {
			return
		}
		effective = append(effective, types.DeviceSpec{Path: p})
		seen[p] = true
	}
	for _, p := range DriverDevices(node.Driver) {
		appendDevice(p)
	}
	if node.Qemu {
		appendDevice("/dev/kvm")
	}
	return effective
}

// DriverDevices lists the devices a driver declares, none for an unknown one.
func DriverDevices(driverName string) []string {
	inst, err := drivers_registry.NewByName(strings.TrimSpace(driverName))
	if err != nil {
		return nil
	}
	if dp, ok := inst.(drivers_meta.DeviceProvider); ok {
		return dp.Devices()
	}
	return nil
}
