package capabilities_base

import (
	caps "kubendt/capabilities/capabilities"
	cap_registry "kubendt/capabilities/registry"
)

// Called from main.go to register all capabilities in the registry.
func RegisterAllCapabilities() {

	// L2Capable
	cap_registry.RegisterCapability[caps.L2Capable](
		"L2Capable",
		"Level 2: links",
		"Up & down network interfaces",
		caps.L2Methods,
	)

	// L3Capable
	cap_registry.RegisterCapability[caps.L3Capable](
		"L3Capable",
		"Level 3: IP addressing",
		"Management of addressing and routes.",
		caps.L3Methods,
	)

	// SwitchCapable
	cap_registry.RegisterCapability[caps.SwitchCapable](
		"SwitchCapable",
		"Switching",
		"Management of switching functions.",
		caps.SwitchMethods,
	)

	// NATCapable
	cap_registry.RegisterCapability[caps.NATCapable](
		"NATCapable",
		"Network Address Translation (NAT)",
		"Management of NAT rules.",
		caps.NATMethods,
	)

	// OSPFCapable
	cap_registry.RegisterCapability[caps.OSPFCapable](
		"OSPFCapable",
		"OSPF Routing",
		"Management of OSPF routing protocol configuration.",
		caps.OSPFMethods,
	)

}
