package capabilities

type NATCapable interface {
	// Non-persistent query to get the SNAT interface for a given pod
	GetSNATInterface(namespace, podName string) (string, error)
	EnableSNAT(iface string) [][]string
	DisableSNAT(iface string) [][]string
	EnableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string) [][]string
	DisableDNAT(iface string, externalPort int, internalIP string, internalPort int, protocol string) [][]string
}

var NATMethods = map[string]string{
	"enable_snat":  "EnableSNAT",
	"disable_snat": "DisableSNAT",
	"enable_dnat":  "EnableDNAT",
	"disable_dnat": "DisableDNAT",
}
