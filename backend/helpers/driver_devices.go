package helpers

import (
	"strings"

	drivers_devices "kubendt/drivers/devices"
	"kubendt/types"
)

// ResolveEffectiveDevicesForNode merges user-declared node devices with driver defaults.
// Driver defaults are deduplicated by path and appended only when missing.
// Backward compatibility: qemu nodes without explicit driver still receive /dev/kvm.
func ResolveEffectiveDevicesForNode(node types.NodeSpec) []types.DeviceSpec {
	effective := append([]types.DeviceSpec{}, node.Devices...)
	seen := map[string]bool{}

	for _, dev := range effective {
		p := strings.TrimSpace(dev.Path)
		if p != "" {
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

	if strings.TrimSpace(node.Driver) != "" {
		for _, dev := range drivers_devices.DevicesForDriver(node.Driver) {
			appendDevice(dev.Path)
		}
	}

	if node.Qemu {
		appendDevice("/dev/kvm")
	}

	return effective
}
