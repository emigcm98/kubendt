package capabilities

import "kubendt/types"

// Keys of the state the actions declare, shared by both sides of a pair so
// a set and its undo meet in the history.

func keyIface(a types.ActionEntry) string { return a.Iface }

func keyAddress(a types.ActionEntry) string { return a.Iface + " " + a.CIDR }

// A default route is one per family: the one set names the family through
// its gateway, the one removed through its family param, 4 when absent.
func keyDefaultRouteSet(a types.ActionEntry) string {
	if IsIPv6(a.Gateway) {
		return "6"
	}
	return "4"
}

func keyDefaultRouteFamily(a types.ActionEntry) string {
	if a.Family == "6" {
		return "6"
	}
	return "4"
}

func keyStaticRoute(a types.ActionEntry) string { return a.DstCIDR + " " + a.Gateway + " " + a.Device }

func keyNameserver(a types.ActionEntry) string { return a.DNSServer }

func keySearch(a types.ActionEntry) string { return a.DNSDomain }

func keyBridge(a types.ActionEntry) string { return a.Bridge }

func keyBridgeMember(a types.ActionEntry) string { return a.Iface + " " + a.Bridge }

func keyPortForward(a types.ActionEntry) string {
	return a.Iface + " " + a.Protocol + " " + itoa(a.ExternalPort)
}

func keyNetworkArea(a types.ActionEntry) string { return a.CIDR + " " + a.OSPFArea }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
