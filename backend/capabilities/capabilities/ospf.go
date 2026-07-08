package capabilities

type OSPFCapable interface {
	OSPFAddNetwork(network, area string) [][]string
	OSPFRemoveNetwork(network, area string) [][]string
	OSPFSetRouterID(routerID string) [][]string
	OSPFRemoveRouterID() [][]string
	OSPFPassiveDefault() [][]string
	OSPFRemovePassiveDefault() [][]string
	OSPFNoPassive(iface string) [][]string
	OSPFRemoveNoPassive(iface string) [][]string
	OSPFOriginateDefault() [][]string
	OSPFRemoveOriginateDefault() [][]string
	OSPFMTUIgnore(iface string) [][]string
	OSPFRemoveMTUIgnore(iface string) [][]string
}

var OSPFMethods = map[string]string{
	"ospf_add_network":              "OSPFAddNetwork",
	"ospf_remove_network":           "OSPFRemoveNetwork",
	"ospf_set_router_id":            "OSPFSetRouterID",
	"ospf_remove_router_id":         "OSPFRemoveRouterID",
	"ospf_passive_default":          "OSPFPassiveDefault",
	"ospf_remove_passive_default":   "OSPFRemovePassiveDefault",
	"ospf_no_passive":               "OSPFNoPassive",
	"ospf_remove_no_passive":        "OSPFRemoveNoPassive",
	"ospf_originate_default":        "OSPFOriginateDefault",
	"ospf_remove_originate_default": "OSPFRemoveOriginateDefault",
	"ospf_mtu_ignore":               "OSPFMTUIgnore",
	"ospf_remove_mtu_ignore":        "OSPFRemoveMTUIgnore",
}
