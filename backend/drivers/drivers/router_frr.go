package drivers

import (
	"fmt"

	"kubendt/capabilities/capabilities"
	drivers_meta "kubendt/drivers/meta"
)

type FRRRouterDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.L3Base
	capabilities.NATBase
}

func NewFRRRouterDriver() *FRRRouterDriver {
	return &FRRRouterDriver{
		Meta: drivers_meta.NewMeta("FRRRouterDriver", "router"),
	}
}

var _ capabilities.L2Capable = (*FRRRouterDriver)(nil)
var _ capabilities.L3Capable = (*FRRRouterDriver)(nil)
var _ capabilities.NATCapable = (*FRRRouterDriver)(nil)
var _ capabilities.OSPFCapable = (*FRRRouterDriver)(nil)

// ─── OSPFCapable ─────────────────────────────────────────────────────────────
// Uses vtysh FRR syntax to apply configuration directly to the running process.

func (FRRRouterDriver) OSPFAddNetwork(network, area string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", fmt.Sprintf("network %s area %s", network, area)}}
}

func (FRRRouterDriver) OSPFRemoveNetwork(network, area string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", fmt.Sprintf("no network %s area %s", network, area)}}
}

func (FRRRouterDriver) OSPFSetRouterID(routerID string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", fmt.Sprintf("ospf router-id %s", routerID)}}
}

func (FRRRouterDriver) OSPFRemoveRouterID() [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "no ospf router-id"}}
}

func (FRRRouterDriver) OSPFPassiveDefault() [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "passive-interface default"}}
}

func (FRRRouterDriver) OSPFRemovePassiveDefault() [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "no passive-interface default"}}
}

func (FRRRouterDriver) OSPFNoPassive(iface string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", fmt.Sprintf("no passive-interface %s", iface)}}
}

func (FRRRouterDriver) OSPFRemoveNoPassive(iface string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", fmt.Sprintf("passive-interface %s", iface)}}
}

func (FRRRouterDriver) OSPFOriginateDefault() [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "default-information originate always"}}
}

func (FRRRouterDriver) OSPFRemoveOriginateDefault() [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "no default-information originate"}}
}

func (FRRRouterDriver) OSPFMTUIgnore(iface string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", fmt.Sprintf("interface %s", iface), "-c", "ip ospf mtu-ignore"}}
}

func (FRRRouterDriver) OSPFRemoveMTUIgnore(iface string) [][]string {
	return [][]string{{"vtysh", "-c", "configure terminal", "-c", fmt.Sprintf("interface %s", iface), "-c", "no ip ospf mtu-ignore"}}
}
