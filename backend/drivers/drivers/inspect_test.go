package drivers

import (
	"testing"

	"kubendt/actions"
)

// What each driver offers to read, by capability.
func TestInspectionsPerDriver(t *testing.T) {
	want := map[string]map[string][]string{
		"HostDriver":        {"L3Capable": {"neighbors", "routes"}},
		"LinuxSwitchDriver": {"SwitchCapable": {"bridge_fdb"}},
		"OpenVSwitchDriver": {"SwitchCapable": {"bridge_fdb"}},
		"LinuxRouterDriver": {"L3Capable": {"neighbors", "routes"}, "NATCapable": {"nat_rules"}},
		"FRRRouterDriver":   {"L3Capable": {"neighbors", "routes"}, "NATCapable": {"nat_rules"}, "OSPFCapable": {"ospf_interfaces", "ospf_neighbors"}, "OSPF6Capable": {"ospf6_neighbors"}, "RIPCapable": {"rip_routes"}, "BGPCapable": {"bgp_summary"}, "ISISCapable": {"isis_neighbors"}},
		"VyOSRouterDriver":  {"L3Capable": {"neighbors", "routes"}, "NATCapable": {"nat_rules"}, "OSPFCapable": {"ospf_interfaces", "ospf_neighbors"}, "OSPF6Capable": {"ospf6_neighbors"}, "RIPCapable": {"rip_routes"}, "BGPCapable": {"bgp_summary"}, "ISISCapable": {"isis_neighbors"}},
	}
	drivers := map[string]any{
		"HostDriver": NewHostDriver(), "LinuxSwitchDriver": NewLinuxSwitchDriver(), "OpenVSwitchDriver": NewOpenVSwitchDriver(),
		"LinuxRouterDriver": NewLinuxRouterDriver(), "FRRRouterDriver": NewFRRRouterDriver(), "VyOSRouterDriver": NewVyOSRouterDriver(),
	}
	for name, drv := range drivers {
		got := map[string][]string{}
		for _, c := range actions.ForDriver(drv) {
			for _, in := range c.Inspections {
				got[c.ID] = append(got[c.ID], in.ID)
			}
		}
		for capID, ids := range want[name] {
			if len(got[capID]) != len(ids) {
				t.Errorf("%s %s: got %v, want %v", name, capID, got[capID], ids)
				continue
			}
			for i := range ids {
				if got[capID][i] != ids[i] {
					t.Errorf("%s %s: got %v, want %v", name, capID, got[capID], ids)
					break
				}
			}
		}
		for capID := range got {
			if _, ok := want[name][capID]; !ok {
				t.Errorf("%s offers %v under %s, not expected", name, got[capID], capID)
			}
		}
	}
	if _, ok := actions.LookupInspection("ospf_neighbors"); !ok {
		t.Error("ospf_neighbors is not registered")
	}
}
