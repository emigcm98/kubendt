package types

import "encoding/json"

// ActionFlags controls how an action is handled by the configurator.
type ActionFlags struct {
	// Persist controls whether the action is saved to history and deduplicated.
	// Default is true.
	Persist bool
	// Get controls whether command output is captured and returned in the API response.
	// Default is false.
	Get bool
}

// DefaultActionFlags returns the default flags for an action: persist=true, get=false.
func DefaultActionFlags() ActionFlags {
	return ActionFlags{Persist: true, Get: false}
}

type ConfigureNetworkRequest struct {
	Targets []PodConfig `json:"targets"`
}

type PodConfig struct {
	Pod     string        `json:"pod" example:"router1"`
	Actions []ActionEntry `json:"actions"`
}

// ActionEntry describes a single configuration action applied to a pod interface.
type ActionEntry struct {
	Type    string         `json:"type" example:"ip"`
	Iface   string         `json:"iface,omitempty" example:"eth1"`
	CIDR    string         `json:"cidr,omitempty" example:"192.168.1.1/24"`
	Gateway string         `json:"gateway,omitempty" example:"192.168.1.254"`
	DstCIDR string         `json:"dst_cidr,omitempty" example:"10.0.0.0/8"`
	Device  string         `json:"device,omitempty" example:"/dev/net/tun"`
	Bridge  string         `json:"bridge,omitempty" example:"br0"`
	Ifaces  []string       `json:"ifaces,omitempty"`
	Options *ActionOptions `json:"options,omitempty"`
	// NAT
	ExternalPort int    `json:"externalPort,omitempty" example:"8080"`
	InternalIP   string `json:"internalIP,omitempty" example:"10.0.0.5"`
	InternalPort int    `json:"internalPort,omitempty" example:"80"`
	Protocol     string `json:"protocol,omitempty" example:"tcp"`

	// DNS
	DNSServer string `json:"dns_server,omitempty" example:"8.8.8.8"`
	DNSDomain string `json:"dns_domain,omitempty" example:"example.local"`

	// TC / Qdisc
	TCParams *TCParamEntry `json:"tcparams,omitempty"`

	// OSPF
	OSPFArea string `json:"ospf_area,omitempty" example:"0"`
	RouterID string `json:"router_id,omitempty" example:"10.0.255.1"`

	// Custom, accepts a string ("sh -c" wrapper) or an array of args.
	// Only used when type == "custom". No driver label required on the pod.
	Command json.RawMessage `json:"command,omitempty"`
}

type ActionOptions struct {
	// PersistHistory controls whether the action is persisted in driver history.
	PersistHistory *bool `json:"persist_history,omitempty" example:"false"`
	// CaptureOutput controls whether command output is returned in the response.
	CaptureOutput *bool `json:"capture_output,omitempty" example:"true"`
}

type TCParamEntry struct {
	Qdisc     string `json:"qdisc" example:"netem"`              // netem or tbf
	Delay     string `json:"delay,omitempty" example:"100ms"`    // optional (netem)
	Jitter    string `json:"jitter,omitempty" example:"10ms"`    // optional (netem)
	Loss      string `json:"loss,omitempty" example:"1%"`        // optional (netem)
	Duplicate string `json:"duplicate,omitempty" example:"0.5%"` // optional (netem)
	Corrupt   string `json:"corrupt,omitempty" example:"0.1%"`   // optional (netem)
	Limit     *int   `json:"limit,omitempty" example:"1000"`     // optional (netem)
	Rate      string `json:"rate,omitempty" example:"10mbit"`    // optional (tbf)
	Burst     string `json:"burst,omitempty" example:"32kbit"`   // optional (tbf)
	Latency   string `json:"latency,omitempty" example:"50ms"`   // optional (tbf)
}
