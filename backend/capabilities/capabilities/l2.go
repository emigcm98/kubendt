package capabilities

type L2Capable interface {
	LinkUp(iface string) [][]string
	LinkDown(iface string) [][]string
}

var L2Methods = map[string]string{
	"link_up":   "LinkUp",
	"link_down": "LinkDown",
}
