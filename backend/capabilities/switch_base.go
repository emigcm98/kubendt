package capabilities

type SwitchBase struct{}

func (SwitchBase) SetupBridge(bridge string, ifaces []string) [][]string {
	cmds := [][]string{
		{"sh", "-c", "ip link show " + bridge + " || ip link add " + bridge + " type bridge"},
		{"ip", "link", "set", bridge, "up"},
	}
	for _, iface := range ifaces {
		cmds = append(cmds, []string{"ip", "link", "set", iface, "master", bridge})
	}
	return cmds
}

func (SwitchBase) TeardownBridge(bridge string) [][]string {
	return [][]string{
		{"ip", "link", "set", bridge, "down"},
		{"ip", "link", "del", bridge, "type", "bridge"},
	}
}

func (SwitchBase) AddInterfaceToBridge(iface, bridge string) [][]string {
	return [][]string{
		{"ip", "link", "set", iface, "master", bridge},
	}
}

func (SwitchBase) RemoveInterfaceFromBridge(iface, bridge string) [][]string {
	return [][]string{
		{"ip", "link", "set", iface, "nomaster"},
	}
}
