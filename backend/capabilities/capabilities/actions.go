package capabilities

// ActionCapability maps an action type (e.g. "set_ip") to the capability that
// owns it (e.g. "L3"), and reports whether the action is known at all. Lets
// callers separate an unknown action (typo) from a known action whose
// capability the driver lacks.
func ActionCapability(actionType string) (capability string, known bool) {
	for capName, methods := range map[string]map[string]string{
		"L2":     L2Methods,
		"L3":     L3Methods,
		"Switch": SwitchMethods,
		"TC":     TCMethods,
		"NAT":    NATMethods,
		"OSPF":   OSPFMethods,
	} {
		if _, ok := methods[actionType]; ok {
			return capName, true
		}
	}
	return "", false
}
