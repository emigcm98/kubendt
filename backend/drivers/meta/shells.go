package drivers_meta

// ShellSpec is one interactive shell a node offers. Mode is what the shell
// endpoint takes, Label what the menu shows, Command the argv exec'd with a
// TTY in the pod's main container (nil for the pod shell, which probes for
// bash, and for the serial console, which attaches instead).
type ShellSpec struct {
	Mode    string   `json:"mode" example:"vtysh"`
	Label   string   `json:"label" example:"vtysh console"`
	Command []string `json:"-"`
}

// ShellProvider is implemented by drivers whose nodes have a console of
// their own besides the pod shell: vtysh on FRR, the CLI over ssh on VyOS.
type ShellProvider interface {
	Shells() []ShellSpec
}

// DeviceProvider is implemented by drivers whose pods need host devices,
// /dev/kvm for a VM, /dev/net/tun for a dataplane that opens taps. They are
// added to every node of the driver unless the topology lists them already.
type DeviceProvider interface {
	Devices() []string
}
