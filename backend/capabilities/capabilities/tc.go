package capabilities

type TCCapable interface {
	AddQdisc(iface, qdiscType string, params []string) [][]string
	DelQdisc(iface string) [][]string
}

var TCMethods = map[string]string{
	"add_qdisc": "AddQdisc",
	"del_qdisc": "DelQdisc",
}
