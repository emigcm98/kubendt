package drivers_meta

// GuestMTUSetter is implemented by drivers whose dataplane is a guest VM. The
// platform sets the MTU of the pod's ethN and tapN itself; the guest's own
// interface is only reachable through the driver, so it hands back the
// commands and they run through the driver's executor. Not an action: the
// MTU is platform-owned and never enters the operation history.
type GuestMTUSetter interface {
	GuestMTUCommands(iface string, mtu int) [][]string
}
