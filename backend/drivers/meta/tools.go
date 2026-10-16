package drivers_meta

// Tools the platform runs on a node and that need its IP stack: the trace
// (traceroute, tcptraceroute, mtr) and the throughput test (iperf3). Capture,
// counters and tc watch the wire from the pod and need nothing from here.
const (
	ToolTrace = "trace"
	ToolIperf = "iperf"
)

// AllTools lists every tool, what a container node offers through the toolbox.
func AllTools() []string { return []string{ToolTrace, ToolIperf} }

// GuestToolProvider is implemented by drivers whose dataplane is a guest VM,
// where the pod netns has no IP stack for the toolbox to use. A tool the
// guest image ships runs inside the guest, its argv prefixed by GuestWrapper:
// an ssh-like channel that hands the guest's shell one line, so the runner
// quotes each argument for it. A driver that runs a VM and does not
// implement it offers no tool.
type GuestToolProvider interface {
	GuestWrapper() []string
	GuestTools() []string
}
