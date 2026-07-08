package capabilities

type TCBase struct{}

func (TCBase) AddQdisc(iface, qdiscType string, params []string) [][]string {
	cmd := []string{"tc", "qdisc", "replace", "dev", iface, "root", qdiscType}
	cmd = append(cmd, params...)
	return [][]string{cmd}
}

func (TCBase) DelQdisc(iface string) [][]string {
	return [][]string{{"tc", "qdisc", "del", "dev", iface, "root"}}
}
