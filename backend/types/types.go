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

// GuestProbeProvider is an optional interface for drivers whose dataplane
// lives inside a guest VM. Network probes (traceroute, mtr) run there,
// wrapped with the returned command prefix, instead of in the pod's debug
// container, whose netns has no connectivity nor the guest routing table.
type GuestProbeProvider interface {
	GuestProbeWrapper() []string
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

// DefaultTerminationGracePeriodSeconds is what a node gets when the topology
// does not set one. Kubernetes would default to 30 s, but emulated nodes are
// stateless (their configuration is replayed after a restart) and the usual
// `sh -c "... && sleep infinity"` entrypoint ignores SIGTERM anyway, so waiting
// only delays the SIGKILL and every pod-recreating operation by ~30 s.
const DefaultTerminationGracePeriodSeconds int64 = 2

// OperationTimeline is the per-pod lifecycle record attached to deploy,
// modify and restart responses. It exists so the platform's own cost can be
// told apart from the substrate's: the Kubernetes stamps are read from the
// Pod object as Kubernetes wrote them, the observed ones are the backend's
// clock. Pods progress in parallel, so the total is a critical path, not the
// sum of the phases.
type OperationTimeline struct {
	// When the backend started handling the request, RFC3339 with ms.
	RequestStartedAt string `json:"request_started_at" example:"2026-09-20T12:47:47.120Z"`
	// Backend phases in ms since request_started_at. Only the ones that
	// apply to the operation are present.
	BackendMs BackendPhasesMs `json:"backend_ms"`
	Pods      []PodTimeline   `json:"pods"`
}

// BackendPhasesMs holds the duration of each backend phase in milliseconds.
type BackendPhasesMs struct {
	// Input validation and driver resolution (deploy).
	Validation *int64 `json:"validation,omitempty" example:"12"`
	// Topology CRDs, ConfigMaps and StatefulSets created (deploy).
	ResourceCreation *int64 `json:"resource_creation,omitempty" example:"470"`
	// Everything before pods start being deleted (restart) or before the
	// wait begins (modify): topology updates, peer cleanup, delete calls.
	Prepare *int64 `json:"prepare,omitempty" example:"1180"`
	// From the first pod delete/create until every affected pod is Ready.
	WaitReady *int64 `json:"wait_ready,omitempty" example:"5300"`
	// Replay of the persisted operation history on recreated pods.
	Replay *int64 `json:"replay,omitempty" example:"40"`
	// Interface validation and healing after the pods are Ready.
	Heal  *int64 `json:"heal,omitempty" example:"380"`
	Total int64  `json:"total" example:"6760"`
}

// PodTimeline is one pod's lifecycle during the operation.
type PodTimeline struct {
	Pod string `json:"pod" example:"router1-0"`
	// Stamps written by Kubernetes on the Pod object, RFC3339, 1 s
	// resolution. Empty when the cluster does not report a condition.
	Kubernetes PodKubernetesStamps `json:"kubernetes"`
	// Transitions as the backend saw them, in ms since request_started_at.
	// A step that had already happened when the backend started watching
	// the pod is omitted.
	Observed PodObservedStamps `json:"observed_ms"`
}

// PodKubernetesStamps are read from metadata and status of the Pod.
type PodKubernetesStamps struct {
	// metadata.creationTimestamp, when the StatefulSet controller created the pod.
	Created string `json:"created,omitempty" example:"2026-09-20T12:47:51Z"`
	// PodScheduled condition, a worker was chosen.
	Scheduled string `json:"scheduled,omitempty" example:"2026-09-20T12:47:51Z"`
	// PodReadyToStartContainers condition, sandbox created and CNI attachment
	// done (this is where Meshnet wires the interfaces). Needs Kubernetes 1.29+.
	SandboxReady string `json:"sandbox_ready,omitempty" example:"2026-09-20T12:47:52Z"`
	// containerStatuses[0].state.running.startedAt, image pulled and process started.
	ContainerStarted string `json:"container_started,omitempty" example:"2026-09-20T12:47:52Z"`
	// Ready condition, the readiness probe passed.
	Ready string `json:"ready,omitempty" example:"2026-09-20T12:47:53Z"`
}

// PodObservedStamps are milliseconds since request_started_at on the backend clock.
type PodObservedStamps struct {
	// The backend asked Kubernetes to delete the previous pod (restart, modify).
	DeleteIssued *int64 `json:"delete_issued,omitempty" example:"1180"`
	// The previous pod object disappeared, termination complete (restart, modify).
	OldPodGone       *int64 `json:"old_pod_gone,omitempty" example:"4210"`
	Scheduled        *int64 `json:"scheduled,omitempty" example:"4300"`
	SandboxReady     *int64 `json:"sandbox_ready,omitempty" example:"5100"`
	ContainerStarted *int64 `json:"container_started,omitempty" example:"5900"`
	// The pod became Ready and the backend's watch delivered it. Against
	// kubernetes.ready this is the platform's detection lag.
	ReadySeen *int64 `json:"ready_seen,omitempty" example:"6480"`
}

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
	// Seconds Kubernetes waits for the pod to exit on SIGTERM before it is
	// killed. Bounds restart, delete and scale-down latency. Defaults to
	// DefaultTerminationGracePeriodSeconds, must be >= 1 when set.
	TerminationGracePeriodSeconds int64 `json:"terminationGracePeriodSeconds,omitempty" example:"2"`
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
	Missing   bool   `json:"missing,omitempty" example:"false"`     // file is mounted but no longer present in the namespace file manager
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
