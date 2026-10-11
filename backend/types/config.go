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
	// Links shapes links by one of their ends. Each entry becomes add_qdisc
	// or del_qdisc actions on the interfaces of both ends, run with Targets.
	Links []LinkShaping `json:"links,omitempty"`
}

// LinkShaping shapes a link named by one of its ends. The peer end comes
// from the topology, so node and localIntf are enough; peerNode and peerIntf,
// when given, are checked against it.
type LinkShaping struct {
	Node      string `json:"node" example:"host-0"`
	LocalIntf string `json:"localIntf" example:"eth1"`
	PeerNode  string `json:"peerNode,omitempty" example:"switch-0"`
	PeerIntf  string `json:"peerIntf,omitempty" example:"eth1"`
	// Shaping goes on node's interface, the node to peer direction. Delay is one-way.
	Shaping *TCParamEntry `json:"shaping,omitempty"`
	// Symmetric puts the same shaping on the peer's interface. Reverse puts a
	// different one there instead. A link to the external network has no peer to shape.
	Symmetric bool          `json:"symmetric,omitempty" example:"true"`
	Reverse   *TCParamEntry `json:"reverse,omitempty"`
	// Remove clears the shaping on both ends.
	Remove bool `json:"remove,omitempty" example:"false"`
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

// TCParamEntry is the shaping request of add_qdisc. Values use tc units and
// are validated before anything runs: a value outside its range fails the
// action instead of being dropped. Times accept ms, us or s (a bare number is
// ms), percentages accept an optional "%", rates accept bit, kbit, mbit or
// gbit, sizes accept b, kb, mb, kbit or mbit. Zero impairments are omitted.
//
// With netem (the default) every field but burst and latency combines in one
// qdisc: delay, jitter, loss, duplicate, corrupt, reorder and a rate. tbf is
// kept for a plain token bucket and takes rate, burst and latency only.
type TCParamEntry struct {
	Qdisc string `json:"qdisc,omitempty" example:"netem"` // netem (default) or tbf
	// netem
	Delay            string `json:"delay,omitempty" example:"100ms"`           // 0 to 60000 ms
	Jitter           string `json:"jitter,omitempty" example:"10ms"`           // 0 to 10000 ms, needs a delay
	DelayCorrelation string `json:"delay_correlation,omitempty" example:"25%"` // needs a jitter
	// Distribution of the jitter: uniform (default), normal, pareto or
	// paretonormal. tc does not report it back, the history keeps it.
	Distribution       string       `json:"distribution,omitempty" example:"normal"`
	Loss               string       `json:"loss,omitempty" example:"1%"`
	LossCorrelation    string       `json:"loss_correlation,omitempty" example:"10%"`
	Gemodel            *GemodelSpec `json:"gemodel,omitempty"` // Gilbert-Elliott loss, instead of loss
	Duplicate          string       `json:"duplicate,omitempty" example:"0.5%"`
	Corrupt            string       `json:"corrupt,omitempty" example:"0.1%"`
	Reorder            string       `json:"reorder,omitempty" example:"25%"` // needs a delay
	ReorderCorrelation string       `json:"reorder_correlation,omitempty" example:"50%"`
	Limit              *int         `json:"limit,omitempty" example:"1000"` // queue length in packets, 1 to 1000000
	// Rate caps the bandwidth: netem's link rate emulation, or tbf's rate (required there).
	Rate string `json:"rate,omitempty" example:"10mbit"`
	// tbf only
	Burst   string `json:"burst,omitempty" example:"32kbit"` // defaults to 32kbit
	Latency string `json:"latency,omitempty" example:"50ms"` // 1 to 5000 ms, defaults to 50ms
}

// GemodelSpec is netem's Gilbert-Elliott loss model: p is the probability of
// entering the bad state, r of leaving it, 1-h the loss probability in the
// bad state (100% by default) and 1-k in the good state (0% by default).
// Average loss is p / (p + r) with the defaults.
type GemodelSpec struct {
	P string `json:"p" example:"1%"`
	R string `json:"r,omitempty" example:"10%"`
	H string `json:"1-h,omitempty" example:"100%"`
	K string `json:"1-k,omitempty" example:"0%"`
}
