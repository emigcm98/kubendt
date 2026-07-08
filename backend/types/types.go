package types

// EffectiveInterfaceInspector is an optional driver capability for resolving
// guest-OS interface info (name, IP, MAC) instead of the pod's "ip a" view.
// Implementations must be read-only. Backend type-asserts at runtime:
//
//	if inspector, ok := drv.(types.EffectiveInterfaceInspector); ok { ... }
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

// EffectiveActionExecutionPlanResolver is an optional interface for drivers
// that need action-specific command plans and/or a non-default executor.
// When handled=true, the returned executorName+commands are used directly.
type EffectiveActionExecutionPlanResolver interface {
	ResolveActionExecutionPlan(namespace, podName string, action ActionEntry) (executorName string, commands [][]string, handled bool, err error)
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

// ErrorResponse is returned on all error responses.
type ErrorResponse struct {
	Error string `json:"error" example:"descriptive error message"`
}

// MessageResponse is returned on simple success responses.
type MessageResponse struct {
	Message string `json:"message" example:"ok"`
}

// Warning is a non-fatal incident surfaced in the response of a deploy or
// modify operation. The kind is a stable machine-readable identifier; the
// detail is human-readable and safe to display verbatim.
type Warning struct {
	Node   string `json:"node,omitempty" example:"web-server"`
	Kind   string `json:"kind" example:"mount_file_missing"`
	File   string `json:"file,omitempty" example:"web-server/index.html"`
	Detail string `json:"detail" example:"File not found in namespace file manager. Mount skipped, the pod will start without it."`
}

// MaxReplicas caps how many pods a single node may request. Shared by deploy,
// modify and scale validation.
const MaxReplicas = 128

// Structure for a Node (Statefulset pod) in the JSON request
type NodeSpec struct {
	Name      string `json:"name" example:"router1"`
	Image     string `json:"image" example:"frrouting/frr:latest"`
	Type      string `json:"type" example:"router"`
	ShellMode string `json:"shellMode,omitempty" example:"sh"`
	// Qemu is no longer accepted from request payloads; it is derived from
	// the resolved driver via drivers_meta.RuntimeProvider. The field remains
	// in the struct so downstream backend code that branches on runtime
	// keeps working transparently.
	Qemu       bool              `json:"-"`
	Privileged bool              `json:"privileged,omitempty" example:"true"`
	Replicas   int               `json:"replicas,omitempty" example:"1"`
	Commands   []string          `json:"commands,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Mounts     []MountSpec       `json:"mounts,omitempty"`
	Devices    []DeviceSpec      `json:"devices,omitempty"`
	Driver     string            `json:"driver,omitempty" example:"frr-router"`
}

// Structure for a network link (CRD Topology) in the JSON request
type LinkSpec struct {
	LocalIntf string `json:"localIntf" example:"eth1"`
	Node      string `json:"node" example:"router1"`
	PeerNode  string `json:"peerNode" example:"router2"` // use "external" as reserved name for host uplinks
	PeerIntf  string `json:"peerIntf" example:"eth1"`
	UID       *int   `json:"uid" example:"1"`
	LocalIP   string `json:"localIp" example:"10.0.0.1/24"`
	PeerIP    string `json:"peerIp" example:"10.0.0.2/24"`
	PeerLabel string `json:"peerLabel,omitempty" example:"router2-eth1"`
	Name      string `json:"name,omitempty" example:"N3"`
}

// Structure for mounting a backend file into a pod. Mounts are always
// read-only at the container level (ConfigMap/Secret + SubPath does not
// propagate writes). Edits to the file content go through the namespace
// file manager and re-sync the underlying resource on save.
//
// Sensitive is a per-file flag. On import, if any mount of a file declares
// sensitive=true, the file is marked sensitive in namespace_file_meta and
// materialised as a Kubernetes Secret instead of a ConfigMap. Cannot unmark
// via JSON (omitempty makes "absent" indistinguishable from "false"); use
// the file manager toggle to clear the flag.
type MountSpec struct {
	File      string `json:"file" example:"ospfd.conf"`             // file name inside namespace directory
	MountTo   string `json:"mountTo" example:"/etc/frr/ospfd.conf"` // absolute path in container
	Sensitive bool   `json:"sensitive,omitempty" example:"false"`   // marks the file as sensitive (Secret-backed)
}

// Main structure for the deployment request
type DeployRequest struct {
	Nodes []NodeSpec `json:"nodes"`
	Links []LinkSpec `json:"links"`
}

// ScaleSpec is one entry in the network-modify "scale" section.
// It addresses an existing node (by base name, not indexed pod name) and
// the absolute number of replicas it should end with after the operation.
// Replicas must be >= 1 (to remove the node entirely use delete.nodes).
type ScaleSpec struct {
	Name     string `json:"name" example:"host"`
	Replicas int    `json:"replicas" example:"4"`
}

type DeviceSpec struct {
	Path string `json:"path" example:"/dev/net/tun"` // absolute path on host
}
