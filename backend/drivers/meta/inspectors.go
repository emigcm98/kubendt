package drivers_meta

// EffectiveInterfaceInspector is an optional driver capability for resolving
// guest-OS interface info (name, IP, MAC) instead of the pod's "ip a" view.
// Implementations must be read-only. Backend type-asserts at runtime.
type EffectiveInterfaceInspector interface {
	GetEffectiveInterfaces(namespace, podName string) ([]map[string]string, error)
}

// EffectiveSingleInterfaceInspector is an optional narrower interface for
// low-latency lookups when only one interface is needed (e.g. hover tooltip).
// Implementations must be read-only and must not persist state.
type EffectiveSingleInterfaceInspector interface {
	GetEffectiveInterface(namespace, podName, podInterface string) (map[string]string, error)
}

// EffectiveInterfaceStateInspector is an optional interface for drivers that can
// provide authoritative up/down states for effective pod interfaces (e.g. via
// a guest router CLI rather than host-side pod interfaces).
type EffectiveInterfaceStateInspector interface {
	GetEffectiveInterfaceStates(namespace, podName string) (map[string]bool, error)
}

// GuestInterfaceMapper is an optional interface for drivers whose dataplane
// names interfaces differently from the pod (a guest OS with its own naming).
// Every interface param of an action is passed through it before the action
// runs, so capability methods always receive the guest name.
type GuestInterfaceMapper interface {
	GuestInterface(namespace, podName, podInterface string) (string, error)
}

// SNATInterfaceInspector is an optional interface for drivers that can say
// which interface a pod masquerades through. Read-only, nothing persisted.
type SNATInterfaceInspector interface {
	GetSNATInterface(namespace, podName string) (string, error)
}

// ReadinessProbeProvider is an optional interface for drivers that require a
// custom Kubernetes readiness probe instead of the default ("command -v ip").
// The returned probe is used when creating the pod's StatefulSet spec.
// Typical use: QEMU-based drivers that need to verify the guest VM is reachable
// (e.g. SSH probe) before the pod is declared Ready and driver replay starts.
type ReadinessProbeProvider interface {
	// ReadinessProbeCommands returns the shell command to use as the readiness
	// probe exec action, along with timing parameters.
	ReadinessProbeCommands() ReadinessProbeSpec
}

// ReadinessProbeSpec carries the parameters for a custom readiness probe.
type ReadinessProbeSpec struct {
	Command             []string
	InitialDelaySeconds int32
	PeriodSeconds       int32
	TimeoutSeconds      int32
	FailureThreshold    int32
}
