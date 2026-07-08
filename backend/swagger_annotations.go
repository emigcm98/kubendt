package main

// KubeNDT API, Swagger annotation stubs.
// These stub functions exist solely to carry swaggo annotations;
// they are never called at runtime.

import "kubendt/types"

// @title           KubeNDT API
// @version         1.0
// @description     REST API for KubeNDT, Kubernetes Network Digital Twin.
// @BasePath        /
// @schemes         http https

// ─── System ──────────────────────────────────────────────────────────────────

// swaggerHealthz godoc
//
//	@Summary      Liveness check
//	@Description  Returns basic service/process health and build metadata.
//	@Tags         system
//	@Produce      json
//	@Success      200  {object}  types.HealthResponse
//	@Router       /healthz [get]
func swaggerHealthz() {}

// swaggerReadyz godoc
//
//	@Summary      Readiness check
//	@Description  Returns readiness state for backend dependencies such as database and Kubernetes clients.
//	@Tags         system
//	@Produce      json
//	@Success      200  {object}  types.ReadinessResponse
//	@Failure      503  {object}  types.ReadinessResponse
//	@Router       /readyz [get]
func swaggerReadyz() {}

// swaggerVersion godoc
//
//	@Summary      Service version
//	@Description  Returns service name and build metadata.
//	@Tags         system
//	@Produce      json
//	@Success      200  {object}  types.VersionResponse
//	@Router       /version [get]
func swaggerVersion() {}

// ─── Namespaces ──────────────────────────────────────────────────────────────

// swaggerListNamespaces godoc
//
//	@Summary      List namespaces
//	@Description  Returns all Kubernetes namespaces labelled kubendt/enabled=true.
//	@Tags         namespaces
//	@Produce      json
//	@Success      200  {object}  types.NamespacesListResponse
//	@Failure      500  {object}  types.ErrorResponse
//	@Router       /namespaces/ [get]
func swaggerListNamespaces() {}

// swaggerCreateNamespace godoc
//
//	@Summary      Create namespace
//	@Description  Creates a new Kubernetes namespace and labels it for KubeNDT.
//	@Tags         namespaces
//	@Accept       json
//	@Produce      json
//	@Param        body  body      types.CreateNamespaceRequest  true  "Namespace to create"
//	@Success      200   {object}  types.MessageResponse
//	@Failure      400   {object}  types.ErrorResponse
//	@Failure      409   {object}  types.ErrorResponse  "Namespace name already exists (empty or with resources)"
//	@Failure      500   {object}  types.ErrorResponse
//	@Router       /namespaces/ [post]
func swaggerCreateNamespace() {}

// swaggerDeleteNamespace godoc
//
//	@Summary      Delete namespace
//	@Description  Deletes a KubeNDT namespace and all resources inside it.
//	@Tags         namespaces
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.MessageResponse
//	@Failure      403        {object}  types.ErrorResponse
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /namespaces/{namespace} [delete]
func swaggerDeleteNamespace() {}

// swaggerGetInterfacesInNamespace godoc
//
//	@Summary      Get interfaces for all pods in a namespace
//	@Description  Returns IP, MAC, and NAT configuration for every pod in the namespace.
//	@Tags         namespaces
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  map[string]types.PodInterfaceInfo
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /namespaces/ips/{namespace} [get]
func swaggerGetInterfacesInNamespace() {}

// swaggerGetNamespaceSummary godoc
//
//	@Summary      Get namespace summary
//	@Description  Returns an aggregated operational summary of the namespace: topology status, node/pod counts, type and driver distribution, persisted operation stats, and active namespace lock (if any).
//	@Tags         namespaces
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.NamespaceSummaryResponse
//	@Failure      403        {object}  types.ErrorResponse
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /namespaces/summary/{namespace} [get]
func swaggerGetNamespaceSummary() {}

// swaggerGetNamespaceOperation godoc
//
//	@Summary      Get in-progress operation
//	@Description  Returns the active operation lock (deploy/modify/clear) for the namespace, or null when idle. Cheap DB read; polled by the UI to reflect a running operation after a reload or navigation.
//	@Tags         namespaces
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.NamespaceOperationResponse
//	@Router       /namespaces/operation/{namespace} [get]
func swaggerGetNamespaceOperation() {}

// ─── Pods ─────────────────────────────────────────────────────────────────────

// swaggerListPods godoc
//
//	@Summary      List pods
//	@Description  Lists all KubeNDT-managed pods (StatefulSets) in a namespace.
//	@Tags         pods
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.PodsListResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /pods/{namespace} [get]
func swaggerListPods() {}

// swaggerRestartPod godoc
//
//	@Summary      Restart pod
//	@Description  Restarts a pod by deleting it (the StatefulSet controller recreates it),
//	@Description  waits for it to reach Running state, then replays all persisted driver
//	@Description  operations from its history. The response includes a took_time breakdown
//	@Description  separating pod_restart (time until Running) from replay (re-execution of
//	@Description  persisted actions).
//	@Tags         pods
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        podName    path      string  true  "Pod name"
//	@Success      200        {object}  types.RestartPodResponseDoc
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /pods/restart/{namespace}/{podName} [patch]
func swaggerRestartPod() {}

// swaggerGetInterfacesFromPod godoc
//
//	@Summary      Get interfaces from a pod
//	@Description  Returns interface list (name, MAC, IPv4) for a single pod. By
//	@Description  default the result is scoped to the interfaces declared in the
//	@Description  pod's topology (its link endpoints), excluding the reserved CNI
//	@Description  management interface, loopback and kernel pseudo-interfaces. Use
//	@Description  scope=all for the full set including the management interface.
//	@Tags         pods
//	@Produce      json
//	@Param        namespace  path      string  true   "Namespace name"
//	@Param        podName    path      string  true   "Pod name"
//	@Param        intf       query     string  false  "Return only this interface"
//	@Param        scope      query     string  false  "Interface scope: topology (default) or all"  Enums(topology, all)
//	@Success      200        {object}  types.PodInterfacesResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /pods/ips/{namespace}/{podName} [get]
func swaggerGetInterfacesFromPod() {}

// swaggerShowQdisc godoc
//
//	@Summary      Show Qdisc (TC) info
//	@Description  Returns Traffic Control qdisc information for a pod interface.
//	@Tags         pods
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        podName    path      string  true  "Pod name"
//	@Param        interface  path      string  true  "Interface name (e.g. eth1)"
//	@Success      200        {object}  types.QdiscResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /pods/tc/{namespace}/{podName}/{interface} [get]
func swaggerShowQdisc() {}

// swaggerGetPodMetrics godoc
//
//	@Summary      Get pod CPU/RAM metrics
//	@Description  Returns real-time CPU (millicores) and memory (bytes/MiB) usage for a single pod via the Kubernetes metrics-server API. Returns HTTP 503 with {"available": false} when metrics-server is not installed or not yet ready.
//	@Tags         pods
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        podName    path      string  true  "Pod name"
//	@Success      200        {object}  types.PodMetricsResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      503        {object}  types.PodMetricsUnavailableResponse
//	@Router       /pods/metrics/{namespace}/{podName} [get]
func swaggerGetPodMetrics() {}

// swaggerGetNamespaceMetrics godoc
//
//	@Summary      Get namespace CPU/RAM metrics
//	@Description  Returns real-time CPU (millicores) and memory (bytes/MiB) usage for every pod in a namespace, both per-pod and aggregated over the namespace, via the Kubernetes metrics-server API. Returns HTTP 503 with {"available": false} when metrics-server is not installed or not yet ready.
//	@Tags         namespaces
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.NamespaceMetricsResponse
//	@Failure      503        {object}  types.PodMetricsUnavailableResponse
//	@Router       /namespaces/metrics/{namespace} [get]
func swaggerGetNamespaceMetrics() {}

// ─── Network ──────────────────────────────────────────────────────────────────

// swaggerDeployNetwork godoc
//
//	@Summary      Deploy network topology
//	@Description  Deploys a full network topology (nodes + links) into a namespace. In links, use the reserved name "external" for either node or peerNode to create a host uplink (external network connection). Both ends of a link cannot be "external" simultaneously.
//	@Tags         network
//	@Accept       json
//	@Produce      json
//	@Param        namespace  path      string                true  "Namespace name"
//	@Param        body       body      types.DeployRequest   true  "Topology definition"
//	@Success      200        {object}  types.DeployNetworkResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      403        {object}  types.ErrorResponse
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      409        {object}  types.NamespaceOperationConflictResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /network/deploy-network/{namespace} [post]
func swaggerDeployNetwork() {}

// swaggerClearTopology godoc
//
//	@Summary      Clear namespace topology
//	@Description  Deletes all topology resources in a namespace without deleting the namespace itself.
//	@Tags         network
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.ClearTopologyResponse
//	@Failure      403        {object}  types.ErrorResponse
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      409        {object}  types.NamespaceOperationConflictResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /network/clear-topology/{namespace} [delete]
func swaggerClearTopology() {}

// swaggerModifyNetwork godoc
//
//	@Summary      Modify network topology
//	@Description  Adds or removes nodes/links from an existing topology.
//	@Tags         network
//	@Accept       json
//	@Produce      json
//	@Param        namespace  path      string                true  "Namespace name"
//	@Param        body       body      types.NetworkModifyRequestDoc   true  "Delta topology (add/delete)"
//	@Success      200        {object}  types.ModifyNetworkResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      403        {object}  types.ErrorResponse
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      409        {object}  types.NamespaceOperationConflictResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /network/modify-network/{namespace} [post]
func swaggerModifyNetwork() {}

// swaggerGetNetwork godoc
//
//	@Summary      Get network topology
//	@Description  Returns the current network topology deployed in a namespace.
//	@Tags         network
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.NetworkTopologyResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /network/get-network/{namespace} [get]
func swaggerGetNetwork() {}

// swaggerSaveNodePositions godoc
//
//	@Summary      Save node positions
//	@Description  Persists visual positions of nodes in the topology graph.
//	@Tags         network
//	@Accept       json
//	@Produce      json
//	@Param        namespace  path      string                    true  "Namespace name"
//	@Param        body       body      types.NodePositionsRequest  true  "Map of node name to {x, y} position"
//	@Success      200        {object}  types.MessageResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /network/positions/{namespace} [post]
func swaggerSaveNodePositions() {}

// swaggerGetNodePositions godoc
//
//	@Summary      Get node positions
//	@Description  Returns the saved visual positions for nodes in a namespace.
//	@Tags         network
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.NodePositionsResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /network/positions/{namespace} [get]
func swaggerGetNodePositions() {}

// swaggerConfigureNetwork godoc
//
//	@Summary      Configure network (drivers)
//	@Description  Applies driver-based configuration actions (routes, bridges, NAT, TC, etc.) to pods concurrently. Each action can include options: {"persist_history": false, "capture_output": true}. The response includes a sequential_equivalent time (sum of per-pod durations) and a speedup factor vs the concurrent wall time.
//	@Tags         network
//	@Accept       json
//	@Produce      json
//	@Param        namespace  path      string                         true  "Namespace name"
//	@Param        body       body      types.ConfigureNetworkRequest  true  "Configuration request"
//	@Success      200        {object}  types.ConfigureNetworkResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /network/configure/{namespace} [post]
func swaggerConfigureNetwork() {}

// ─── Files ────────────────────────────────────────────────────────────────────

// swaggerListFiles godoc
//
//	@Summary      List files
//	@Description  Lists files and directories in the namespace's file storage.
//	@Tags         files
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        path       query     string  false "Sub-path to list (default: root)"
//	@Success      200        {object}  types.FilesListResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /files/{namespace} [get]
func swaggerListFiles() {}

// swaggerGetFileContent godoc
//
//	@Summary      Get file content
//	@Description  Returns the raw content of a file stored for the namespace.
//	@Tags         files
//	@Produce      plain
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        filename   path      string  true  "File path (relative to namespace root)"
//	@Success      200        {string}  string  "File content"
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /files/{namespace}/{filename} [get]
func swaggerGetFileContent() {}

// swaggerUploadFile godoc
//
//	@Summary      Upload file
//	@Description  Uploads a file to the namespace's file storage (multipart/form-data).
//	@Tags         files
//	@Accept       multipart/form-data
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        file       formData  file    true  "File to upload"
//	@Param        path       formData  string  false "Target sub-path"
//	@Success      200        {object}  types.MessageResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /files/{namespace}/ [post]
func swaggerUploadFile() {}

// swaggerUpdateFileContent godoc
//
//	@Summary      Update file content
//	@Description  Overwrites the content of an existing file.
//	@Tags         files
//	@Accept       json
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        filename   path      string  true  "File path"
//	@Param        body       body      types.UpdateFileContentRequest  true  "New file content payload"
//	@Success      200        {object}  types.MessageResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /files/{namespace}/{filename} [put]
func swaggerUpdateFileContent() {}

// swaggerDeleteFile godoc
//
//	@Summary      Delete file
//	@Description  Deletes a file from namespace storage.
//	@Tags         files
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        filename   path      string  true  "File path"
//	@Success      200        {object}  types.MessageResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /files/{namespace}/{filename} [delete]
func swaggerDeleteFile() {}

// swaggerDeleteAllNamespaceFiles godoc
//
//	@Summary      Delete all namespace files
//	@Description  Permanently removes every file and folder inside the namespace file-manager directory, then recreates the empty directory.
//	@Tags         files
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.MessageResponse
//	@Failure      403        {object}  types.ErrorResponse
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /files/{namespace} [delete]
func swaggerDeleteAllNamespaceFiles() {}

// swaggerCreateFolder godoc
//
//	@Summary      Create folder
//	@Description  Creates a new directory inside namespace file storage.
//	@Tags         files
//	@Accept       json
//	@Produce      json
//	@Param        namespace  path      string                     true  "Namespace name"
//	@Param        body       body      types.CreateFolderRequest  true  "Folder path to create"
//	@Success      200        {object}  types.MessageResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /file-ops/{namespace}/folder [post]
func swaggerCreateFolder() {}

// swaggerImportArchive godoc
//
//	@Summary      Import archive
//	@Description  Imports a .zip or .tar.gz archive into namespace file storage.
//	@Tags         files
//	@Accept       multipart/form-data
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        file       formData  file    true  "Archive file (.zip or .tar.gz)"
//	@Param        path       formData  string  false "Target extraction sub-path"
//	@Success      200        {object}  types.MessageResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /file-ops/{namespace}/import [post]
func swaggerImportArchive() {}

// swaggerRenameFile godoc
//
//	@Summary      Rename / move file
//	@Description  Renames or moves a file within namespace storage.
//	@Tags         files
//	@Accept       json
//	@Produce      json
//	@Param        namespace  path      string                    true  "Namespace name"
//	@Param        body       body      types.RenameFileRequest   true  "Old and new paths"
//	@Success      200        {object}  types.MessageResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /file-ops/{namespace}/rename [post]
func swaggerRenameFile() {}

// swaggerExportAsZip godoc
//
//	@Summary      Export as ZIP
//	@Description  Downloads all files for a namespace as a .zip archive.
//	@Tags         files
//	@Produce      application/zip
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {file}    binary  "ZIP archive"
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /file-ops/{namespace}/export [get]
func swaggerExportAsZip() {}

// ─── Drivers ──────────────────────────────────────────────────────────────────

// swaggerGetDrivers godoc
//
//	@Summary      List available drivers
//	@Description  Returns all registered KubeNDT drivers (e.g. linux-switch, ovs-switch, frr-router).
//	@Tags         drivers
//	@Produce      json
//	@Success      200  {object}  types.DriversListResponse
//	@Failure      500  {object}  types.ErrorResponse
//	@Router       /drivers/ [get]
func swaggerGetDrivers() {}

// swaggerGetDriver godoc
//
//	@Summary      Get a single driver
//	@Description  Returns the full info (type, executor, capabilities and interface-name constraints) for one registered driver. Same per-item shape used by `GET /drivers/`.
//	@Tags         drivers
//	@Produce      json
//	@Param        driver  path      string  true  "Driver name"
//	@Success      200     {object}  types.DriverInfo
//	@Failure      404     {object}  types.ErrorResponse
//	@Failure      500     {object}  types.ErrorResponse
//	@Router       /drivers/{driver} [get]
func swaggerGetDriver() {}

// swaggerGetNamespaceDriverOperationHistory godoc
//
//	@Summary      List persisted driver history by namespace
//	@Description  Returns all persisted driver operations for the namespace across all pods in execution order (oldest first). Each entry includes action payload, execution timestamp, pod name, and equivalent shell commands reconstructed from current driver capabilities when available.
//	@Tags         drivers
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.NamespaceDriverHistoryResponseDoc
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /drivers/history/{namespace} [get]
func swaggerGetNamespaceDriverOperationHistory() {}

// swaggerGetPodDriverOperationHistory godoc
//
//	@Summary      List persisted driver history by pod
//	@Description  Returns persisted driver operations for a resolved pod in a namespace (supports base name and indexed pod name). Entries are ordered by insertion time and include action payload, timestamp, and reconstructed equivalent shell commands.
//	@Tags         drivers
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        podName    path      string  true  "Pod reference (e.g. router1 or router1-0)"
//	@Success      200        {object}  types.PodDriverHistoryResponseDoc
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /drivers/history/{namespace}/{podName} [get]
func swaggerGetPodDriverOperationHistory() {}

// swaggerDeleteDriverOperationHistory godoc
//
//	@Summary      Delete one persisted driver operation by ID
//	@Description  Removes a single persisted driver operation from history using its unique operation ID.
//	@Tags         drivers
//	@Produce      json
//	@Param        id   path      int  true  "Operation ID"
//	@Success      200  {object}  types.DeleteDriverHistoryByIDResponseDoc
//	@Failure      400  {object}  types.ErrorResponse
//	@Failure      404  {object}  types.ErrorResponse
//	@Failure      500  {object}  types.ErrorResponse
//	@Router       /drivers/history/{id} [delete]
func swaggerDeleteDriverOperationHistory() {}

// swaggerDeletePodDriverOperationHistory godoc
//
//	@Summary      Delete persisted driver history for one pod
//	@Description  Deletes all persisted driver operations for one resolved pod in a namespace (supports base name and indexed pod name).
//	@Tags         drivers
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        podName    path      string  true  "Pod reference (e.g. router1 or router1-0)"
//	@Success      200        {object}  types.DeletePodDriverHistoryResponseDoc
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /drivers/history/namespace/{namespace}/pod/{podName} [delete]
func swaggerDeletePodDriverOperationHistory() {}

// swaggerDeleteNamespaceDriverOperationHistory godoc
//
//	@Summary      Delete persisted driver history for a namespace
//	@Description  Deletes all persisted driver operations across all pods in the namespace.
//	@Tags         drivers
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.DeleteNamespaceDriverHistoryResponseDoc
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /drivers/history/namespace/{namespace} [delete]
func swaggerDeleteNamespaceDriverOperationHistory() {}

// ─── Cluster ─────────────────────────────────────────────────────────────────

// swaggerGetClusterStatus godoc
//
//	@Summary      Get cluster status
//	@Description  Returns connection status and node info for the Kubernetes cluster.
//	@Tags         cluster
//	@Produce      json
//	@Success      200  {object}  types.ClusterStatusResponse
//	@Failure      500  {object}  types.ErrorResponse
//	@Router       /cluster/status [get]
func swaggerGetClusterStatus() {}

// swaggerGetClusterNodeDetail godoc
//
//	@Summary      Get a node's detailed info
//	@Description  Returns the full kubectl-derivable view of a single node: pod CIDR, addresses, OS info, kubelet/runtime versions, capacity vs allocatable, conditions, taints, labels and annotations. Live usage is included when metrics-server is reachable.
//	@Tags         cluster
//	@Produce      json
//	@Param        name  path      string  true  "Node name"
//	@Success      200   {object}  types.NodeDetailResponse
//	@Failure      404   {object}  types.ErrorResponse
//	@Failure      500   {object}  types.ErrorResponse
//	@Router       /cluster/nodes/{name} [get]
func swaggerGetClusterNodeDetail() {}

// ─── Kubeconfig ───────────────────────────────────────────────────────────────

// swaggerGetKubeConfigInfo godoc
//
//	@Summary      Get kubeconfig info
//	@Description  Returns information about the currently loaded kubeconfig (contexts, current context, server URLs).
//	@Tags         kubeconfig
//	@Produce      json
//	@Success      200  {object}  types.KubeConfigInfo
//	@Failure      500  {object}  types.ErrorResponse
//	@Router       /kube/config [get]
func swaggerGetKubeConfigInfo() {}

// swaggerSetKubeContext godoc
//
//	@Summary      Set kubeconfig context
//	@Description  Switches the active Kubernetes context.
//	@Tags         kubeconfig
//	@Accept       json
//	@Produce      json
//	@Param        body  body      types.SetContextRequest  true  "Context name to activate"
//	@Success      200   {object}  types.SetKubeContextResponse
//	@Failure      400   {object}  types.ErrorResponse
//	@Failure      500   {object}  types.ErrorResponse
//	@Router       /kube/context [post]
func swaggerSetKubeContext() {}

// swaggerLoadKubeConfig godoc
//
//	@Summary      Load kubeconfig file
//	@Description  Loads kubeconfig from an uploaded file.
//	@Tags         kubeconfig
//	@Accept       multipart/form-data
//	@Produce      json
//	@Param        file  formData  file  true  "Kubeconfig file"
//	@Success      200   {object}  types.KubeConfigInfo
//	@Failure      400   {object}  types.ErrorResponse
//	@Failure      500   {object}  types.ErrorResponse
//	@Router       /kube/config [post]
func swaggerLoadKubeConfig() {}

// ─── Shell (WebSocket) ────────────────────────────────────────────────────────

// swaggerInteractiveShellWebSocket godoc
//
//	@Summary      Interactive shell (WebSocket)
//	@Description  Opens a WebSocket connection providing an interactive shell inside a pod. Query parameter `mode` controls shell type: `auto` (default), `sh`, or `serial`.
//	@Tags         shell
//	@Param        namespace  path  string  true  "Namespace name"
//	@Param        podName    path  string  true  "Pod name"
//	@Param        mode       query string  false "Shell mode: auto|sh|serial" Enums(auto,sh,serial) default(auto)
//	@Router       /shell/ws/{namespace}/{podName} [get]
func swaggerInteractiveShellWebSocket() {}

// ─── Capture (WebSocket + pcap) ────────────────────────────────────────────────

// swaggerCaptureWebSocket godoc
//
//	@Summary      Live packet capture (WebSocket)
//	@Description  Injects an ephemeral tshark container into the pod (sharing its network namespace) and streams a live capture of one interface. Control frames are JSON ({type:"meta"|"status"|"error"}); every other text frame is one packet row as tab-separated Wireshark columns (No, Time, Source, Destination, Protocol, Length, Info). Closing the socket stops the capture. Optional `filter` is a BPF capture filter; optional `container` reuses an existing capture container on reconnect.
//	@Tags         capture
//	@Param        namespace  path   string  true   "Namespace name"
//	@Param        podName    path   string  true   "Pod name"
//	@Param        interface  path   string  true   "Interface to capture (e.g. eth1)"
//	@Param        filter     query  string  false  "BPF capture filter"
//	@Param        container  query  string  false  "Existing capture container to reuse"
//	@Router       /capture/ws/{namespace}/{podName}/{interface} [get]
func swaggerCaptureWebSocket() {}

// swaggerDownloadCapturePcap godoc
//
//	@Summary      Download capture pcap
//	@Description  Streams the pcap a capture run wrote, as a file download. Without `frames` it returns the whole capture; with `frames` (a comma list of numbers/ranges, e.g. 1-5,9,12-20) it returns only those packets (used to export the current display filter).
//	@Tags         capture
//	@Produce      application/octet-stream
//	@Param        namespace  path   string  true   "Namespace name"
//	@Param        podName    path   string  true   "Pod name"
//	@Param        container  path   string  true   "Capture container id"
//	@Param        pcap       query  string  true   "Capture run id"
//	@Param        frames     query  string  false  "Frame numbers/ranges to export (e.g. 1-5,9,12-20)"
//	@Success      200  {file}    binary
//	@Failure      400  {object}  types.ErrorResponse
//	@Router       /capture/pcap/{namespace}/{podName}/{container} [get]
func swaggerDownloadCapturePcap() {}

// swaggerClearCapture godoc
//
//	@Summary      Clear capture pcap
//	@Description  Empties a stopped capture's pcap so a later export starts fresh.
//	@Tags         capture
//	@Produce      json
//	@Param        namespace  path   string  true  "Namespace name"
//	@Param        podName    path   string  true  "Pod name"
//	@Param        container  path   string  true  "Capture container id"
//	@Param        pcap       query  string  true  "Capture run id"
//	@Success      200  {object}  types.MessageResponse
//	@Failure      400  {object}  types.ErrorResponse
//	@Failure      500  {object}  types.ErrorResponse
//	@Router       /capture/clear/{namespace}/{podName}/{container} [post]
func swaggerClearCapture() {}

// swaggerGetCapturePacketDetail godoc
//
//	@Summary      Packet dissection
//	@Description  Re-dissects a single packet from a capture's pcap into a full JSON protocol tree (Wireshark's own dissection via tshark -T json).
//	@Tags         capture
//	@Produce      json
//	@Param        namespace  path   string  true  "Namespace name"
//	@Param        podName    path   string  true  "Pod name"
//	@Param        container  path   string  true  "Capture container id"
//	@Param        num        path   int     true  "Frame number"
//	@Param        pcap       query  string  true  "Capture run id"
//	@Success      200  {string}  string  "Wireshark JSON dissection"
//	@Failure      400  {object}  types.ErrorResponse
//	@Failure      404  {object}  types.ErrorResponse
//	@Router       /capture/packet/{namespace}/{podName}/{container}/{num} [get]
func swaggerGetCapturePacketDetail() {}

// ─── Auth ────────────────────────────────────────────────────────────────────

// swaggerLogin godoc
//
//	@Summary      Log in
//	@Description  Exchanges the admin password for an HttpOnly session cookie.
//	@Tags         auth
//	@Accept       json
//	@Produce      json
//	@Param        body  body  types.LoginRequest  true  "Admin password"
//	@Success      200  {object}  types.LoginResponse
//	@Failure      401  {object}  types.ErrorResponse
//	@Router       /auth/login [post]
func swaggerLogin() {}

// swaggerLogout godoc
//
//	@Summary      Log out
//	@Description  Revokes the current session and clears the cookie.
//	@Tags         auth
//	@Produce      json
//	@Success      200  {object}  types.AuthStatus
//	@Router       /auth/logout [post]
func swaggerLogout() {}

// swaggerAuthMe godoc
//
//	@Summary      Current auth state
//	@Description  Reports whether auth is enabled and whether the caller is authenticated.
//	@Tags         auth
//	@Produce      json
//	@Success      200  {object}  types.AuthStatus
//	@Router       /auth/me [get]
func swaggerAuthMe() {}

// swaggerListAPITokens godoc
//
//	@Summary      List API tokens
//	@Description  Lists API token metadata (never the secrets). Requires a session or the admin password.
//	@Tags         auth
//	@Produce      json
//	@Success      200  {object}  types.APITokenList
//	@Router       /auth/tokens [get]
func swaggerListAPITokens() {}

// swaggerCreateAPIToken godoc
//
//	@Summary      Create an API token
//	@Description  Mints a long-lived bearer token (returned only once). Requires a session or the admin password (not another token).
//	@Tags         auth
//	@Accept       json
//	@Produce      json
//	@Param        body  body  types.CreateTokenRequest  true  "Token name"
//	@Success      201  {object}  types.CreateTokenResponse
//	@Failure      400  {object}  types.ErrorResponse
//	@Failure      409  {object}  types.ErrorResponse
//	@Router       /auth/tokens [post]
func swaggerCreateAPIToken() {}

// swaggerDeleteAPIToken godoc
//
//	@Summary      Revoke an API token
//	@Description  Revokes an API token by id. Requires a session or the admin password.
//	@Tags         auth
//	@Param        id  path  int  true  "Token id"
//	@Success      204  "No Content"
//	@Failure      404  {object}  types.ErrorResponse
//	@Router       /auth/tokens/{id} [delete]
func swaggerDeleteAPIToken() {}

// ─── Type aliases used by Swagger (to force inclusion in generated docs) ─────

// Ensure referenced types appear in the generated OpenAPI schema.
var _ = types.DeployRequest{}
var _ = types.ConfigureNetworkRequest{}
var _ = types.NodeSpec{}
var _ = types.LinkSpec{}
var _ = types.MountSpec{}
var _ = types.DeviceSpec{}
var _ = types.PodConfig{}
var _ = types.ActionEntry{}
var _ = types.TCParamEntry{}
var _ = types.ErrorResponse{}
var _ = types.MessageResponse{}
var _ = types.DeleteDriverHistoryByIDResponseDoc{}
var _ = types.DeletePodDriverHistoryResponseDoc{}
var _ = types.DeleteNamespaceDriverHistoryResponseDoc{}
var _ = types.NamespaceInfo{}
var _ = types.NamespacesListResponse{}
var _ = types.PodInfo{}
var _ = types.PodsListResponse{}
var _ = types.PodInterfaceEntry{}
var _ = types.PodInterfacesResponse{}
var _ = types.PodInterfaceInfo{}
var _ = types.QdiscInfo{}
var _ = types.QdiscResponse{}
var _ = types.NetworkTopologyResponse{}
var _ = types.DeployNetworkResponse{}
var _ = types.ClearTopologyResponse{}
var _ = types.NetworkDeleteSpecDoc{}
var _ = types.NetworkModifyRequestDoc{}
var _ = types.ModifyNetworkResponse{}
var _ = types.ConfigureNetworkResponse{}
var _ = types.NodePositionsRequest{}
var _ = types.NodePositionsResponse{}
var _ = types.FileEntry{}
var _ = types.FilesListResponse{}
var _ = types.UpdateFileContentRequest{}
var _ = types.DriverInfo{}
var _ = types.DriversListResponse{}
var _ = types.CapabilityEntry{}
var _ = types.DriverOperationHistoryEntryDoc{}
var _ = types.NamespaceDriverHistoryResponseDoc{}
var _ = types.PodDriverHistoryResponseDoc{}
var _ = types.ClusterStatusResponse{}
var _ = types.HealthResponse{}
var _ = types.ReadinessResponse{}
var _ = types.VersionResponse{}
var _ = types.KubeConfigInfo{}
var _ = types.SetContextRequest{}
var _ = types.SetKubeContextResponse{}
var _ = types.NamespaceOperationConflictResponse{}
var _ = types.LoadKubeConfigRequest{}
var _ = types.CreateNamespaceRequest{}
var _ = types.CreateFolderRequest{}
var _ = types.RenameFileRequest{}
var _ = types.NamespaceSummaryResponse{}
var _ = types.NamespaceOperationResponse{}
var _ = types.PodMetricsResponse{}
var _ = types.PodMetricsUnavailableResponse{}
