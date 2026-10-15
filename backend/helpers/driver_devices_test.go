package helpers

import (
	"strings"
	"testing"

	drivers_base "kubendt/drivers"
	"kubendt/types"
)

func TestResolveEffectiveDevicesForNode(t *testing.T) {
	drivers_base.RegisterAllDrivers()
	paths := func(devs []types.DeviceSpec) string {
		out := []string{}
		for _, d := range devs {
			out = append(out, d.Path)
		}
		return strings.Join(out, ",")
	}
	if got := paths(ResolveEffectiveDevicesForNode(types.NodeSpec{Driver: "VyOSRouterDriver", Qemu: true})); got != "/dev/kvm" {
		t.Errorf("VyOS declares kvm once, got %s", got)
	}
	if got := paths(ResolveEffectiveDevicesForNode(types.NodeSpec{Driver: "FRRRouterDriver"})); got != "" {
		t.Errorf("FRR declares nothing, got %s", got)
	}
	node := types.NodeSpec{Driver: "VyOSRouterDriver", Qemu: true, Devices: []types.DeviceSpec{{Path: "/dev/net/tun"}}}
	if got := paths(ResolveEffectiveDevicesForNode(node)); got != "/dev/net/tun,/dev/kvm" {
		t.Errorf("the topology's devices come first, got %s", got)
	}
	if got := paths(ResolveEffectiveDevicesForNode(types.NodeSpec{Qemu: true})); got != "" {
		t.Errorf("only the driver adds devices, got %s", got)
	}
}
