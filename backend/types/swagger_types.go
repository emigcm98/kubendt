package types

// ─── Common response wrappers ─────────────────────────────────────────────────

// NamespaceInfo is returned for each namespace in the list.
type NamespaceInfo struct {
	Name      string `json:"name" example:"prueba"`
	Status    string `json:"status" example:"Active"`
	CreatedAt string `json:"createdAt" example:"2026-01-15 10:30:00"`
	Uptime    string `json:"uptime" example:"5 days, 2 hours, 10 minutes"`
	// HasTopology reflects whether a KubeNDT topology is currently deployed
	// in this namespace (StatefulSets/Topology CRDs registered). Updated by
	// deploy / modify / clear flows and cheap to read (DB lookup).
	HasTopology bool `json:"has_topology" example:"true"`
}

// NamespacesListResponse is returned by GET /namespaces/.
type NamespacesListResponse struct {
	Namespaces []NamespaceInfo `json:"namespaces"`
}

// NamespaceSummarySection describes general namespace metadata.
type NamespaceSummarySection struct {
	Name      string `json:"name" example:"demo"`
	Status    string `json:"status" example:"Active"`
	CreatedAt string `json:"created_at" example:"2026-03-23T16:14:08+01:00"`
	Age       string `json:"age" example:"34m58s"`
}

// NamespaceSummaryTopologySection contains topology state counters.
type NamespaceSummaryTopologySection struct {
	HasTopology     bool `json:"has_topology" example:"true"`
	StatefulSets    int  `json:"statefulsets" example:"13"`
	TopologyCRDs    int  `json:"topology_crds" example:"18"`
	LogicalLinks    int  `json:"logical_links" example:"18"`
	LinksToExternal int  `json:"links_to_external" example:"1"`
}

// NamespaceSummaryNodesSection contains node/replica totals.
type NamespaceSummaryNodesSection struct {
	ByType        map[string]int `json:"by_type" swaggertype:"object,integer" example:"host:9,router:3,switch:6"`
	TotalReplicas int            `json:"total_replicas" example:"18"`
}

// NamespaceSummaryPodsSection contains pod readiness and phase distribution.
type NamespaceSummaryPodsSection struct {
	PhaseCounts map[string]int `json:"phase_counts" swaggertype:"object,integer" example:"Running:18"`
	Ready       int            `json:"ready" example:"18"`
	Restarting  int            `json:"restarting" example:"0"`
	Running     int            `json:"running" example:"18"`
	Total       int            `json:"total" example:"18"`
}

// NamespaceSummaryDriversSection contains counters by driver name.
type NamespaceSummaryDriversSection struct {
	ByDriver map[string]int `json:"by_driver" swaggertype:"object,integer" example:"BasicHostDriver:5,FRRRouterDriver:3,HostDriver:4,LinuxSwitchDriver:6"`
}

// NamespaceSummaryRuntimeSection contains counters by runtime.
type NamespaceSummaryRuntimeSection struct {
	ByRuntime map[string]int `json:"by_runtime" swaggertype:"object,integer" example:"k8s-linux:18"`
}

// NamespaceSummaryOperationsSection contains persisted operation counters.
type NamespaceSummaryOperationsSection struct {
	ByAction        map[string]int `json:"by_action" swaggertype:"object,integer" example:"add_dns_nameserver:4,add_dns_search:4,enable_dnat:3,enable_snat:1,remove_default_route:2,replace_ip:1,set_default_route:9,setup_bridge:6"`
	ByDriver        map[string]int `json:"by_driver" swaggertype:"object,integer" example:"*drivers.BasicHostDriver:5,*drivers.FRRRouterDriver:7,*drivers.HostDriver:12,*drivers.LinuxSwitchDriver:6"`
	LastExecutedAt  string         `json:"last_executed_at" example:"2026-03-23T15:15:25.402889011Z"`
	PodsWithHistory int            `json:"pods_with_history" example:"18"`
	TotalPersisted  int            `json:"total_persisted" example:"30"`
}

// NamespaceSummaryOperationLockSection mirrors an active namespace lock.
// This field is null when no operation is currently in progress.
type NamespaceSummaryOperationLockSection struct {
	Namespace     string `json:"namespace" example:"demo"`
	OperationType string `json:"operationType" example:"modify-network"`
	StartedAt     string `json:"startedAt" example:"2026-03-23T15:00:00Z"`
}

// NamespaceSummaryResponse is returned by GET /namespaces/summary/:namespace.
type NamespaceSummaryResponse struct {
	Namespace     NamespaceSummarySection               `json:"namespace"`
	Topology      NamespaceSummaryTopologySection       `json:"topology"`
	Nodes         NamespaceSummaryNodesSection          `json:"nodes"`
	Pods          NamespaceSummaryPodsSection           `json:"pods"`
	Drivers       NamespaceSummaryDriversSection        `json:"drivers"`
	Runtime       NamespaceSummaryRuntimeSection        `json:"runtime"`
	Operations    NamespaceSummaryOperationsSection     `json:"operations"`
	OperationLock *NamespaceSummaryOperationLockSection `json:"operation_lock"`
	TookTime      string                                `json:"took_time" example:"37ms"`
	TookSeconds   float64                               `json:"took_seconds" example:"0.037295681"`
}

// PodInfo describes a single KubeNDT-managed pod.
type PodInfo struct {
	Name      string `json:"name" example:"router1"`
	BaseName  string `json:"baseName" example:"router"`
	Runtime   string `json:"runtime" example:"k8s-linux"`
	Replicas  int    `json:"replicaCount" example:"2"`
	ReplicaID int    `json:"replicaIndex" example:"0"`
	Driver    string `json:"driver" example:"frr-router"`
	Type      string `json:"type" example:"router"`
	Namespace string `json:"namespace" example:"prueba"`
	Image     string `json:"image" example:"frrouting/frr:latest"`
	Node      string `json:"node" example:"k8s-worker-1"`
	Status    string `json:"status" example:"Running"`
	CreatedAt string `json:"createdAt" example:"2026-01-15 10:30:00"`
	Uptime    string `json:"uptime" example:"1 days, 4 hours, 5 minutes"`
}

// PodsListResponse is returned by GET /pods/:namespace.
type PodsListResponse struct {
	Pods []PodInfo `json:"pods"`
}

// PodInterfaceEntry holds one interface entry returned by GET /pods/ips/:ns/:pod.
type PodInterfaceEntry struct {
	Interface string `json:"interface" example:"eth1"`
	MAC       string `json:"mac" example:"aa:bb:cc:dd:ee:ff"`
	IPv4      string `json:"ipv4" example:"10.0.0.1/24"`
}

// PodInterfacesResponse is returned by GET /pods/ips/:ns/:pod.
type PodInterfacesResponse struct {
	Interfaces []PodInterfaceEntry `json:"interfaces"`
}

// DriverReplayStatsDoc summarizes replay outcomes for persisted driver operations.
type DriverReplayStatsDoc struct {
	Total    int `json:"total" example:"3"`
	Replayed int `json:"replayed" example:"2"`
	Pruned   int `json:"pruned" example:"1"`
}

// RestartTimingResponse breaks down the timing of a pod restart operation.
type RestartTimingResponse struct {
	Total      string `json:"total" example:"42.50s"`
	PodRestart string `json:"pod_restart" example:"40.30s"`
	Replay     string `json:"replay" example:"0.95s"`
}

// RestartPodResponseDoc is returned by PATCH /pods/restart/:namespace/:podName.
type RestartPodResponseDoc struct {
	Message            string                `json:"message" example:"Pod router1-0 restarted successfully"`
	ReplayedOperations int                   `json:"replayed_operations" example:"2"`
	Replay             DriverReplayStatsDoc  `json:"replay"`
	TookTime           RestartTimingResponse `json:"took_time"`
}

// InterfaceDetail holds IP/MAC info for a single interface.
type InterfaceDetail struct {
	IP  string `json:"ip" example:"10.0.0.1/24"`
	MAC string `json:"mac" example:"aa:bb:cc:dd:ee:ff"`
}

// NATRule describes a port-forwarding rule.
type NATRule struct {
	ExternalPort int    `json:"externalPort" example:"8080"`
	InternalIP   string `json:"internalIP" example:"10.0.0.5"`
	InternalPort int    `json:"internalPort" example:"80"`
	Protocol     string `json:"protocol" example:"tcp"`
}

// PodInterfaceInfo is returned for a single pod by GET /pods/ips/:ns/:pod
// and also appears as a value in the map returned by GET /namespaces/ips/:ns.
type PodInterfaceInfo struct {
	Interfaces map[string]InterfaceDetail `json:"interfaces"`
	NAT        []NATRule                  `json:"nat,omitempty"`
}

// QdiscInfo is returned by GET /pods/tc/:ns/:pod/:iface.
type QdiscInfo struct {
	Interface string `json:"interface" example:"eth1"`
	Qdisc     string `json:"qdisc" example:"netem"`
	Details   string `json:"details" example:"limit 1000 delay 100ms loss 1%"`
}

// QdiscResponse is returned by GET /pods/tc/:ns/:pod/:iface.
type QdiscResponse struct {
	Interface string                 `json:"interface" example:"eth1"`
	TCParams  map[string]interface{} `json:"tcparams"`
}

// PodMetricsResponse is returned by GET /pods/metrics/:ns/:pod when metrics-server is available.
type PodMetricsResponse struct {
	Available   bool   `json:"available" example:"true"`
	Pod         string `json:"pod" example:"router1-0"`
	CPUMilli    int64  `json:"cpu_milli" example:"145"`
	MemoryBytes int64  `json:"memory_bytes" example:"52428800"`
	MemoryMiB   int64  `json:"memory_mib" example:"50"`
}

// PodMetricsUnavailableResponse is returned by GET /pods/metrics/:ns/:pod when metrics-server is not installed.
type PodMetricsUnavailableResponse struct {
	Available bool   `json:"available" example:"false"`
	Error     string `json:"error" example:"metrics-server not available or not installed"`
}

// NamespacePodMetrics is the per-pod entry inside a NamespaceMetricsResponse.
type NamespacePodMetrics struct {
	Pod         string `json:"pod" example:"frr-router-0"`
	CPUMilli    int64  `json:"cpu_milli" example:"145"`
	MemoryBytes int64  `json:"memory_bytes" example:"52428800"`
	MemoryMiB   int64  `json:"memory_mib" example:"50"`
}

// NamespaceMetricsResponse is returned by GET /namespaces/metrics/:ns when metrics-server is available.
// It reports CPU (millicores) and memory (bytes/MiB) usage per pod and aggregated over the namespace.
type NamespaceMetricsResponse struct {
	Available        bool                  `json:"available" example:"true"`
	Namespace        string                `json:"namespace" example:"modify-ospf"`
	PodCount         int                   `json:"pod_count" example:"6"`
	Pods             []NamespacePodMetrics `json:"pods"`
	TotalCPUMilli    int64                 `json:"total_cpu_milli" example:"870"`
	TotalMemoryBytes int64                 `json:"total_memory_bytes" example:"314572800"`
	TotalMemoryMiB   int64                 `json:"total_memory_mib" example:"300"`
}

// ─── Network ─────────────────────────────────────────────────────────────────

// NetworkTopologyResponse is returned by GET /network/get-network/:ns.
type NetworkTopologyResponse struct {
	Nodes []NodeSpec `json:"nodes"`
	Links []LinkSpec `json:"links"`
}

// DeployTimingResponse breaks down deployment duration by phase.
type DeployTimingResponse struct {
	Total            string `json:"total" example:"51.43s"`
	ResourceCreation string `json:"resource_creation" example:"0.24s"`
	NodeRunning      string `json:"node_running" example:"45.01s"`
	Reconciliation   string `json:"reconciliation" example:"6.18s"`
}

// WarningDoc describes a non-fatal incident surfaced in deploy/modify responses.
// The operation still succeeded; the caller is expected to display these to
// the user so they know something they declared was not fully applied (e.g.
// a mount file that was not found in the namespace file manager).
type WarningDoc struct {
	Node   string `json:"node,omitempty" example:"web-server"`
	Kind   string `json:"kind" example:"mount_file_missing"`
	File   string `json:"file,omitempty" example:"web-server/index.html"`
	Detail string `json:"detail" example:"File \"web-server/index.html\" not found in namespace file manager. Mount skipped, the pod will start without it."`
}

// DeployNetworkResponse is returned by POST /network/deploy-network/:ns.
type DeployNetworkResponse struct {
	Message  string               `json:"message" example:"Network infrastructure deployed successfully"`
	TookTime DeployTimingResponse `json:"took_time"`
	Warnings []WarningDoc         `json:"warnings"`
}

// ClearTimingResponse holds the duration for a clear operation.
type ClearTimingResponse struct {
	Total string `json:"total" example:"1.20s"`
}

// ClearTopologyResponse is returned by DELETE /network/clear-topology/:ns.
type ClearTopologyResponse struct {
	Message  string              `json:"message" example:"Topology resources cleared successfully"`
	TookTime ClearTimingResponse `json:"took_time"`
}

// NetworkDeleteSpecDoc is the delete portion for modify-network payload.
type NetworkDeleteSpecDoc struct {
	Nodes []string   `json:"nodes"`
	Links []LinkSpec `json:"links"`
}

// NetworkModifyRequestDoc is the request body for POST /network/modify-network/:ns.
// All three sections are optional, but the request must include at least one
// of them with content. Execution order inside a single request is:
// delete → scale-down → scale-up → add.
type NetworkModifyRequestDoc struct {
	Add    DeployRequest        `json:"add"`
	Delete NetworkDeleteSpecDoc `json:"delete"`
	// Scale changes the replica count of existing nodes. Each entry takes
	// the absolute number of replicas the node should end with. Replicas
	// must be >= 1 (to remove a node entirely use delete.nodes).
	Scale []ScaleSpec `json:"scale"`
}

// ModifyTimingResponse breaks down modify duration by phase.
type ModifyTimingResponse struct {
	Total            string `json:"total" example:"5.23s"`
	ModifyOperations string `json:"modify_operations" example:"3.12s"`
	Reconciliation   string `json:"reconciliation" example:"2.11s"`
}

// ModifyNetworkResponse is returned by POST /network/modify-network/:ns.
type ModifyNetworkResponse struct {
	Message        string               `json:"message" example:"network-modify applied successfully"`
	TookTime       ModifyTimingResponse `json:"took_time"`
	DeletedNodes   []string             `json:"deleted_nodes"`
	RestartedPods  []string             `json:"restarted_pods"`
	ScaledUpPods   []string             `json:"scaled_up_pods"`
	ScaledDownPods []string             `json:"scaled_down_pods"`
	AddNodesCount  int                  `json:"add_nodes_count" example:"1"`
	AddLinksCount  int                  `json:"add_links_count" example:"1"`
	DelNodesCount  int                  `json:"del_nodes_count" example:"0"`
	DelLinksCount  int                  `json:"del_links_count" example:"0"`
	ScaleCount     int                  `json:"scale_count" example:"1"`
	Warnings       []WarningDoc         `json:"warnings"`
}

// ConfigureTimingResponse breaks down configure duration.
type ConfigureTimingResponse struct {
	Total                string `json:"total" example:"1.23s"`
	SequentialEquivalent string `json:"sequential_equivalent" example:"7.81s"`
}

// ConfigureNetworkResponse is returned by POST /network/configure/:ns.
type ConfigureNetworkResponse struct {
	Status    string                  `json:"status" example:"success"`
	Successes int                     `json:"successes" example:"8"`
	Failures  int                     `json:"failures" example:"0"`
	Skipped   int                     `json:"skipped" example:"0"`
	TookTime  ConfigureTimingResponse `json:"took_time"`
	Speedup   float64                 `json:"speedup" example:"6.35"`
}

// NodePosition holds the x/y canvas coordinates for a node.
type NodePosition struct {
	X float64 `json:"x" example:"120.5"`
	Y float64 `json:"y" example:"340.0"`
}

// NodePositionsRequest is the body for POST /network/positions/:ns.
type NodePositionsRequest struct {
	Positions map[string]NodePosition `json:"positions"`
}

// NodePositionsResponse is returned by GET /network/positions/:ns.
type NodePositionsResponse struct {
	Positions map[string]NodePosition `json:"positions"`
}

// ─── Files ────────────────────────────────────────────────────────────────────

// FileEntry is a single item returned in the file listing.
type FileEntry struct {
	Name    string `json:"name" example:"ospfd.conf"`
	Type    string `json:"type" example:"file"`
	Size    int64  `json:"size" example:"2048"`
	Path    string `json:"path" example:"/prueba/ospfd.conf"`
	ModTime string `json:"modTime" example:"2026-03-10 08:15:00"`
}

// FilesListResponse is returned by GET /files/:ns.
type FilesListResponse struct {
	Files []FileEntry `json:"files"`
}

// UpdateFileContentRequest is the body for PUT /files/:ns/:filename.
type UpdateFileContentRequest struct {
	Content string `json:"content" example:"hostname router1"`
}

// CreateFolderRequest is the body for POST /file-ops/:ns/folder.
type CreateFolderRequest struct {
	Path string `json:"path" example:"configs/routers"`
}

// RenameFileRequest is the body for POST /file-ops/:ns/rename.
type RenameFileRequest struct {
	OldPath string `json:"oldPath" example:"configs/old.conf"`
	NewPath string `json:"newPath" example:"configs/new.conf"`
}

// ─── Drivers ─────────────────────────────────────────────────────────────────

// InterfaceNameConstraintsDoc is the optional set of rules a driver imposes
// on pod-side interface names. Omitted when the driver places no extra
// constraints beyond the Linux kernel ones.
type InterfaceNameConstraintsDoc struct {
	Pattern      string   `json:"pattern" example:"^eth\\d+$"`
	PatternHuman string   `json:"patternHuman" example:"^eth\\d+$ (e.g. eth1, eth10, eth42)"`
	Reserved     []string `json:"reserved" example:"eth0"`
}

// DriverInfo is a single driver entry returned by GET /drivers/ and
// the full response of GET /drivers/{driver}.
type DriverInfo struct {
	Name                     string                       `json:"name" example:"frr-router"`
	Type                     string                       `json:"type" example:"router"`
	Executor                 string                       `json:"executor" example:"kubectl"`
	IsDefault                bool                         `json:"isDefault" example:"true"`
	Capabilities             []CapabilityEntry            `json:"capabilities"`
	InterfaceNameConstraints *InterfaceNameConstraintsDoc `json:"interfaceNameConstraints,omitempty"`
}

// DriversListResponse is returned by GET /drivers/.
type DriversListResponse struct {
	Drivers []DriverInfo `json:"drivers"`
}

// DriverMethodParamEntry describes one method parameter in driver capability docs.
type DriverMethodParamEntry struct {
	Name string `json:"name" example:"iface"`
	Type string `json:"type" example:"string"`
}

// DriverMethodEntry describes one callable driver method.
type DriverMethodEntry struct {
	Name   string                   `json:"name" example:"link_up"`
	Label  string                   `json:"label" example:"LinkUp"`
	Params []DriverMethodParamEntry `json:"params"`
}

// CapabilityEntry describes one capability of a driver.
type CapabilityEntry struct {
	ID          string              `json:"id" example:"L3Capable"`
	Label       string              `json:"label" example:"Level 3: IP addressing"`
	Description string              `json:"description" example:"Management of addressing and routes."`
	Methods     []DriverMethodEntry `json:"methods"`
}

// DriverOperationHistoryEntryDoc describes one persisted driver action execution.
type DriverOperationHistoryEntryDoc struct {
	ID         int64       `json:"id" example:"12"`
	PodName    string      `json:"pod_name" example:"router1-0"`
	DriverType string      `json:"driver_type" example:"*drivers.FRRRouterDriver"`
	ActionType string      `json:"action_type" example:"add_static_route"`
	ExecutedAt string      `json:"executed_at" example:"2026-03-22T11:25:44.113Z"`
	Action     ActionEntry `json:"action"`
	Commands   []string    `json:"commands,omitempty" example:"ip route add 10.10.0.0/16 via 10.0.0.1 dev net1"`
}

// PodDriverHistoryResponseDoc is returned by GET /drivers/history/:namespace/:podName.
type PodDriverHistoryResponseDoc struct {
	Namespace  string                           `json:"namespace" example:"test-small"`
	Pod        string                           `json:"pod" example:"router1-0"`
	Operations []DriverOperationHistoryEntryDoc `json:"operations"`
}

// NamespaceDriverHistoryResponseDoc is returned by GET /drivers/history/:namespace.
type NamespaceDriverHistoryResponseDoc struct {
	Namespace  string                           `json:"namespace" example:"test-small"`
	Operations []DriverOperationHistoryEntryDoc `json:"operations"`
}

// DeleteDriverHistoryByIDResponseDoc is returned by DELETE /drivers/history/:id.
type DeleteDriverHistoryByIDResponseDoc struct {
	Message string `json:"message" example:"operation deleted"`
	ID      int64  `json:"id" example:"42"`
}

// DeletePodDriverHistoryResponseDoc is returned by DELETE /drivers/history/namespace/:namespace/pod/:podName.
type DeletePodDriverHistoryResponseDoc struct {
	Message   string `json:"message" example:"pod history deleted"`
	Namespace string `json:"namespace" example:"test-small"`
	Pod       string `json:"pod" example:"router1-0"`
}

// DeleteNamespaceDriverHistoryResponseDoc is returned by DELETE /drivers/history/namespace/:namespace.
type DeleteNamespaceDriverHistoryResponseDoc struct {
	Message   string `json:"message" example:"namespace history deleted"`
	Namespace string `json:"namespace" example:"test-small"`
}

// ─── Cluster ─────────────────────────────────────────────────────────────────

// ClusterNodeInfo describes a single Kubernetes node.
type ClusterNodeInfo struct {
	Name             string   `json:"name" example:"k8s-worker-1"`
	Roles            []string `json:"roles" example:"worker"`
	Status           string   `json:"status" example:"Ready"`
	CPUCapacity      string   `json:"cpu_capacity" example:"8"`
	MemoryCapacity   string   `json:"memory_capacity" example:"15.56Gi"`
	CPUUsage         string   `json:"cpu_usage" example:"423m"`
	MemoryUsage      string   `json:"memory_usage" example:"4.32Gi"`
	CPUPercentage    float64  `json:"cpu_percentage" example:"5.28"`
	MemoryPercentage float64  `json:"memory_percentage" example:"27.77"`
	KubeletVersion   string   `json:"kubelet_version" example:"v1.31.0"`
}

// ClusterStatusResponse is returned by GET /cluster/status.
type ClusterStatusResponse struct {
	Nodes []ClusterNodeInfo `json:"nodes"`
	Ready int               `json:"ready" example:"1"`
	Total int               `json:"total" example:"1"`
	// Cluster-wide weighted averages computed as sum(usage)/sum(capacity)
	// across nodes that report metrics. Zero when metrics-server is
	// unavailable or no node reports metrics.
	AvgCPUPercentage    float64 `json:"avg_cpu_percentage" example:"5.28"`
	AvgMemoryPercentage float64 `json:"avg_memory_percentage" example:"27.77"`
}

// NodeAddressDoc is one address reported by the Kubernetes node object.
type NodeAddressDoc struct {
	Type    string `json:"type" example:"InternalIP"`
	Address string `json:"address" example:"10.0.0.5"`
}

// NodeConditionDoc is one condition reported by the Kubernetes node object.
type NodeConditionDoc struct {
	Type           string `json:"type" example:"Ready"`
	Status         string `json:"status" example:"True"`
	Reason         string `json:"reason" example:"KubeletReady"`
	Message        string `json:"message" example:"kubelet is posting ready status"`
	LastTransition string `json:"last_transition" example:"2026-04-28T12:05:00Z"`
}

// NodeTaintDoc is one taint applied to the node.
type NodeTaintDoc struct {
	Key       string `json:"key" example:"node-role.kubernetes.io/control-plane"`
	Value     string `json:"value,omitempty" example:""`
	Effect    string `json:"effect" example:"NoSchedule"`
	TimeAdded string `json:"time_added,omitempty" example:"2026-04-28T12:00:00Z"`
}

// NodeResourceQuantityDoc holds capacity/allocatable values in canonical units.
type NodeResourceQuantityDoc struct {
	CPUMilli    int64 `json:"cpu_milli" example:"6000"`
	MemoryBytes int64 `json:"memory_bytes" example:"7290974208"`
	Pods        int64 `json:"pods" example:"110"`
	StorageEph  int64 `json:"storage_ephemeral_bytes" example:"42949672960"`
}

// NodeOSInfoDoc describes the operating system reported by the kubelet.
type NodeOSInfoDoc struct {
	OperatingSystem string `json:"operating_system" example:"linux"`
	Architecture    string `json:"architecture" example:"amd64"`
	OSImage         string `json:"os_image" example:"Ubuntu 22.04.4 LTS"`
	KernelVersion   string `json:"kernel_version" example:"5.15.0-105-generic"`
}

// NodeVersionsInfoDoc describes the kubelet and container runtime versions.
type NodeVersionsInfoDoc struct {
	KubeletVersion          string `json:"kubelet_version" example:"v1.32.2"`
	ContainerRuntimeVersion string `json:"container_runtime_version" example:"containerd://1.7.20"`
}

// NodeDetailResponse is returned by GET /cluster/nodes/{name}. It exposes
// everything kubectl can derive about a node, pod CIDR, addresses, OS info,
// versions, conditions, taints, labels, annotations and (when metrics-server
// is reachable) the current resource usage.
type NodeDetailResponse struct {
	Name              string                  `json:"name" example:"k8s-worker-1"`
	Roles             []string                `json:"roles" example:"worker"`
	Status            string                  `json:"status" example:"Ready"`
	CreationTimestamp string                  `json:"creation_timestamp" example:"2026-04-28T12:00:00Z"`
	PodCIDR           string                  `json:"pod_cidr" example:"10.244.1.0/24"`
	PodCIDRs          []string                `json:"pod_cidrs" example:"10.244.1.0/24"`
	Addresses         []NodeAddressDoc        `json:"addresses"`
	OSInfo            NodeOSInfoDoc           `json:"os_info"`
	Versions          NodeVersionsInfoDoc     `json:"versions"`
	Capacity          NodeResourceQuantityDoc `json:"capacity"`
	Allocatable       NodeResourceQuantityDoc `json:"allocatable"`
	Conditions        []NodeConditionDoc      `json:"conditions"`
	Taints            []NodeTaintDoc          `json:"taints"`
	Labels            map[string]string       `json:"labels"`
	Annotations       map[string]string       `json:"annotations"`
	CPUMilliUsage     int64                   `json:"cpu_milli_usage" example:"423"`
	MemoryBytesUsage  int64                   `json:"memory_bytes_usage" example:"4633038848"`
	CPUPercentage     float64                 `json:"cpu_percentage" example:"5.28"`
	MemoryPercentage  float64                 `json:"memory_percentage" example:"27.77"`
}

// ─── System ──────────────────────────────────────────────────────────────────

// HealthResponse is returned by GET /healthz.
type HealthResponse struct {
	Status     string `json:"status" example:"ok"`
	Service    string `json:"service" example:"kubendt-backend"`
	Version    string `json:"version" example:"0.1.0-pre"`
	Commit     string `json:"commit" example:"abc1234"`
	BuildDate  string `json:"build_date" example:"2026-04-28T12:00:00Z"`
	ServerTime string `json:"server_time" example:"2026-04-28T12:05:00Z"`
}

// ReadinessResponse is returned by GET /readyz.
type ReadinessResponse struct {
	Status     string            `json:"status" example:"ready"`
	Checks     map[string]string `json:"checks" swaggertype:"object,string" example:"database:ok,kubernetes:ok"`
	ServerTime string            `json:"server_time" example:"2026-04-28T12:05:00Z"`
}

// VersionResponse is returned by GET /version.
type VersionResponse struct {
	Service   string `json:"service" example:"kubendt-backend"`
	Version   string `json:"version" example:"0.1.0-pre"`
	Commit    string `json:"commit" example:"abc1234"`
	BuildDate string `json:"build_date" example:"2026-04-28T12:00:00Z"`
}

// ─── Kubeconfig ───────────────────────────────────────────────────────────────

// KubeConfigInfo is returned by GET /kube/config and POST /kube/config.
type KubeConfigInfo struct {
	Configured     bool     `json:"configured" example:"true"`
	Path           string   `json:"path" example:"/home/user/.kube/config"`
	CurrentContext string   `json:"current_context" example:"my-cluster"`
	Contexts       []string `json:"contexts" example:"[\"my-cluster\",\"staging\"]"`
	// ContextClusterIDs maps each context name to its cluster's canonical ID
	// (the kube-system namespace UID). Contexts whose cluster is currently
	// unreachable are omitted.
	ContextClusterIDs map[string]string `json:"context_cluster_ids"`
}

// NamespaceOperationLockInfo describes an in-progress operation on a namespace.
type NamespaceOperationLockInfo struct {
	Namespace     string `json:"namespace" example:"open5gs"`
	OperationType string `json:"operationType" example:"deploy-network"`
	StartedAt     string `json:"startedAt" example:"2026-07-08T07:07:11Z"`
}

// NamespaceOperationResponse is returned by GET /namespaces/operation/{namespace}.
// operation_lock is null when no operation is running.
type NamespaceOperationResponse struct {
	OperationLock *NamespaceOperationLockInfo `json:"operation_lock"`
}

// SetContextRequest is the body for POST /kube/context.
type SetContextRequest struct {
	Context string `json:"context" example:"my-cluster"`
}

// SetKubeContextResponse is returned by POST /kube/context.
type SetKubeContextResponse struct {
	CurrentContext string `json:"current_context" example:"my-cluster"`
}

// NamespaceOperationConflictResponse is returned when an operation lock already exists.
type NamespaceOperationConflictResponse struct {
	Error            string `json:"error" example:"Namespace 'prueba' already has an operation in progress"`
	CurrentOperation string `json:"current_operation,omitempty" example:"modify-network"`
	StartedAt        string `json:"started_at,omitempty" example:"2026-03-17T16:40:12Z"`
}

// LoadKubeConfigRequest is a legacy docs helper type.
type LoadKubeConfigRequest struct {
	File string `json:"file" example:"<kubeconfig YAML content>"`
}

// ─── Namespace / simple requests ─────────────────────────────────────────────

// CreateNamespaceRequest is the body for POST /namespaces/.
type CreateNamespaceRequest struct {
	Namespace string `json:"namespace" example:"prueba"`
}

// ─── Auth ─────────────────────────────────────────────────────────────────────

// LoginRequest is the body for POST /auth/login.
type LoginRequest struct {
	Password string `json:"password" example:"admin123"`
}

// LoginResponse is returned on a successful login.
type LoginResponse struct {
	Authenticated bool     `json:"authenticated" example:"true"`
	Identity      string   `json:"identity" example:"admin"`
	Roles         []string `json:"roles" example:"admin"`
}

// AuthStatus is returned by GET /auth/me.
type AuthStatus struct {
	Enabled       bool     `json:"enabled" example:"true"`
	Authenticated bool     `json:"authenticated" example:"true"`
	Identity      string   `json:"identity" example:"admin"`
	Roles         []string `json:"roles" example:"admin"`
}

// CreateTokenRequest is the body for POST /auth/tokens.
type CreateTokenRequest struct {
	Name string `json:"name" example:"ci-pipeline"`
	// Days until the token expires; 0 or omitted means it never expires.
	ExpiresInDays int `json:"expires_in_days,omitempty" example:"30"`
}

// CreateTokenResponse returns the freshly minted token (shown only once).
type CreateTokenResponse struct {
	Name  string `json:"name" example:"ci-pipeline"`
	Token string `json:"token" example:"kdt_9f3a...."`
}

// APIToken is the metadata of an API token (never its secret).
type APIToken struct {
	ID         int64  `json:"id" example:"1"`
	Name       string `json:"name" example:"ci-pipeline"`
	CreatedAt  int64  `json:"created_at" example:"1782905018"`
	LastUsedAt int64  `json:"last_used_at,omitempty" example:"1782905090"`
	ExpiresAt  int64  `json:"expires_at,omitempty" example:"0"`
}

// APITokenList is returned by GET /auth/tokens.
type APITokenList struct {
	Tokens []APIToken `json:"tokens"`
}
