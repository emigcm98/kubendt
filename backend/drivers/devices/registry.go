package drivers_devices

import (
	"strings"
	"sync"

	"kubendt/types"
)

var (
	mu       sync.RWMutex
	registry = map[string][]types.DeviceSpec{}
)

// RegisterDevices sets default devices for a driver name.
// Existing entries for the same driver are replaced.
func RegisterDevices(driverName string, devices []types.DeviceSpec) {
	name := strings.TrimSpace(driverName)
	if name == "" {
		return
	}

	normalized := make([]types.DeviceSpec, 0, len(devices))
	seen := map[string]bool{}
	for _, d := range devices {
		p := strings.TrimSpace(d.Path)
		if p == "" || seen[p] {
			continue
		}
		normalized = append(normalized, types.DeviceSpec{Path: p})
		seen[p] = true
	}

	mu.Lock()
	defer mu.Unlock()
	registry[name] = normalized
}

// DevicesForDriver returns default devices declared for the given driver.
func DevicesForDriver(driverName string) []types.DeviceSpec {
	mu.RLock()
	defer mu.RUnlock()

	devices, ok := registry[strings.TrimSpace(driverName)]
	if !ok {
		return nil
	}
	out := make([]types.DeviceSpec, len(devices))
	copy(out, devices)
	return out
}
