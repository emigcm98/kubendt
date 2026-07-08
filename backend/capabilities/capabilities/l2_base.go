package capabilities

type L2Base struct{}

func (L2Base) LinkUp(iface string) [][]string { return [][]string{{"ip", "link", "set", iface, "up"}} }
func (L2Base) LinkDown(iface string) [][]string {
	return [][]string{{"ip", "link", "set", iface, "down"}}
}
