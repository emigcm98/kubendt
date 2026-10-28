package main

// KubeNDT API, Swagger annotation stubs.
// These stub functions exist solely to carry swaggo annotations;
// they are never called at runtime.

import "kubendt/types"

// @title           KubeNDT API
// @version         1.0
// @description     REST API for KubeNDT.
// @BasePath        /

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
//	@Description  Returns the traffic control state of a pod interface, read with `tc -j qdisc show` and normalized (times in ms, percentages, rates in kbit/mbit, sizes in bytes). `shaped` is false on the kernel default qdisc.
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

// swaggerShowPodQdiscs godoc
//
//	@Summary      Show Qdisc (TC) info for every interface
//	@Description  Returns the traffic control state of every interface of a pod (loopback and kernel pseudo-interfaces excluded), read with one exec. Each entry has the same shape as the single-interface endpoint.
//	@Tags         pods
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        podName    path      string  true  "Pod name"
//	@Success      200        {object}  types.PodQdiscsResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /pods/tc/{namespace}/{podName} [get]
func swaggerShowPodQdiscs() {}

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
//	@Param        namespace  path      string                true   "Namespace name"
//	@Param        force      query     bool                  false  "Deploy even if the Meshnet CNI is not detected. Without it a missing Meshnet returns 412 and nothing is deployed."
//	@Param        body       body      types.DeployRequest   true   "Topology definition"
//	@Success      200        {object}  types.DeployNetworkResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      403        {object}  types.ErrorResponse
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      409        {object}  types.NamespaceOperationConflictResponse
//	@Failure      412        {object}  types.MeshnetGateResponse
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
//	@Description  Adds or removes nodes/links from an existing topology. Any change (add, delete or scale) returns 412 when the Meshnet CNI is not detected, since it restarts or creates pods that need rewiring. Bypass with force=true. Only clearing a topology is exempt.
//	@Tags         network
//	@Accept       json
//	@Produce      json
//	@Param        namespace  path      string                true   "Namespace name"
//	@Param        force      query     bool                  false  "Apply the change even if the Meshnet CNI is not detected."
//	@Param        body       body      types.NetworkModifyRequestDoc   true  "Delta topology (add/delete)"
//	@Success      200        {object}  types.ModifyNetworkResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      403        {object}  types.ErrorResponse
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      409        {object}  types.NamespaceOperationConflictResponse
//	@Failure      412        {object}  types.MeshnetGateResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /network/modify-network/{namespace} [post]
func swaggerModifyNetwork() {}

// swaggerGetLinks godoc
//
//	@Summary      Get link realization
//	@Description  Lists every link in the namespace as it exists on the cluster, with the worker behind each endpoint and how Meshnet realized it: veth when both pods share a worker, vxlan across workers, external for host uplinks, pending while an endpoint is not scheduled yet.
//	@Tags         network
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.LinksStatusResponse
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /network/links/{namespace} [get]
func swaggerGetLinks() {}

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
//	@Description  Applies driver-based configuration actions (routes, bridges, NAT, TC, etc.) to pods concurrently. A `links` section shapes links by one end: the peer comes from the topology, `symmetric` repeats the shaping on the peer's interface, `reverse` puts a different one there, `remove` clears both ends. Each entry expands to add_qdisc or del_qdisc actions on the two interfaces. Each action can include options: {"persist_history": false, "capture_output": true}. The response includes a sequential_equivalent time (sum of per-pod durations) and a speedup factor vs the concurrent wall time.
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

// swaggerExportArchive godoc
//
//	@Summary      Export as archive
//	@Description  Downloads all files for a namespace as an archive. The format is chosen with the `format` query param.
//	@Tags         files
//	@Produce      application/zip
//	@Produce      application/gzip
//	@Param        namespace  path      string  true   "Namespace name"
//	@Param        format     query     string  false  "Archive format"  Enums(zip, tar.gz)  default(zip)
//	@Success      200        {file}    binary  "Archive download"
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /file-ops/{namespace}/export [get]
func swaggerExportArchive() {}

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
//	@Description  Opens a WebSocket connection providing an interactive shell inside a pod. Query parameter `mode` picks the shell: `auto` (default, the serial console when the node has one, else the pod shell), `sh`, `serial`, or a mode the node's driver declares (`vtysh` on FRR, `vyos` on VyOS); the pod listing's `shells` field says which a node offers.
//	@Tags         shell
//	@Param        namespace  path  string  true  "Namespace name"
//	@Param        podName    path  string  true  "Pod name"
//	@Param        mode       query string  false "Shell mode: auto, sh, serial, or one the driver declares" default(auto)
//	@Router       /shell/ws/{namespace}/{podName} [get]
func swaggerInteractiveShellWebSocket() {}

// ─── Capture (WebSocket + pcap) ────────────────────────────────────────────────

// swaggerCaptureWebSocket godoc
//
//	@Summary      Live packet capture (WebSocket)
//	@Description  Runs tshark in the pod's toolbox (an ephemeral container sharing its network namespace) and streams a live capture of one interface. Control frames are JSON ({type:"meta"|"status"|"error"}); every other text frame is one packet row as tab-separated Wireshark columns (No, Time, Source, Destination, Protocol, Length, Info). Closing the socket stops the capture. Optional `filter` is a BPF capture filter; optional `container` reuses an existing capture container on reconnect.
//	@Tags         capture
//	@Param        namespace  path   string  true   "Namespace name"
//	@Param        podName    path   string  true   "Pod name"
//	@Param        interface  path   string  true   "Interface to capture (e.g. eth1)"
//	@Param        filter     query  string  false  "BPF capture filter"
//	@Param        container  query  string  false  "Toolbox to reuse, the one holding the previous run's pcap"
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
//	@Param        container  path   string  true   "Toolbox that holds the pcap"
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
//	@Param        container  path   string  true  "Toolbox that holds the pcap"
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
//	@Param        container  path   string  true  "Toolbox that holds the pcap"
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

// ─── Trace (traceroute / metrics) ────────────────────────────────────────────

// swaggerTraceReport godoc
//
//	@Summary      Traceroute / metrics report
//	@Description  Runs a traceroute (or mtr when metrics=1) from a source pod toward an IP or hostname and returns the whole result as one JSON document, each hop resolved to a topology node. Blocks until the probe finishes. The source node must be L3-capable. The probe runs in the pod's toolbox, so the pod image needs no traceroute of its own. A live animated version streams the same hops over WebSocket at /trace/ws/{namespace}/{podName}.
//	@Tags         trace
//	@Produce      json
//	@Param        namespace  path      string   true   "Namespace"
//	@Param        podName     path      string   true   "Source pod (base name or indexed pod name); must be L3-capable"
//	@Param        dest        query     string   true   "Destination IPv4 address or hostname"
//	@Param        method      query     string   false  "Probe method: icmp (default), udp or tcp"
//	@Param        metrics     query     integer  false  "Set 1 for metrics mode (mtr latency/loss) instead of a plain traceroute"
//	@Param        cycles      query     integer  false  "mtr probe rounds (metrics mode only; default 5)"
//	@Success      200  {object}  types.TraceReportResponse
//	@Failure      400  {object}  types.ErrorResponse
//	@Failure      404  {object}  types.ErrorResponse
//	@Failure      500  {object}  types.ErrorResponse
//	@Router       /trace/run/{namespace}/{podName} [get]
func swaggerTraceReport() {}

// ─── Iperf (throughput tests) ────────────────────────────────────────────────

// swaggerIperfReport godoc
//
//	@Summary      Throughput test report
//	@Description  Runs iperf3 from a source pod toward an address and returns the whole run as one JSON document: one entry per second and the end totals (bytes, bits per second, retransmissions for TCP, jitter and loss for UDP). By default the backend starts the server on the pod that owns the destination address, in its toolbox, on a port of its own; `server=none` uses a server the lab already runs at the destination (port 5201 unless `port` says otherwise) and `server=<pod>` names the pod to run it on. The source node must be L3-capable. A live version streams the same intervals over WebSocket at /iperf/ws/{namespace}/{podName}.
//	@Tags         iperf
//	@Produce      json
//	@Param        namespace  path      string   true   "Namespace"
//	@Param        podName    path      string   true   "Source pod (base name or indexed pod name); must be L3-capable"
//	@Param        dest       query     string   true   "Destination IPv4 address or hostname"
//	@Param        proto      query     string   false  "tcp (default) or udp"
//	@Param        duration   query     integer  false  "Seconds, 1 to 120 (default 10)"
//	@Param        bandwidth  query     string   false  "Offered rate in bits per second with an optional k, M or G, e.g. 10M (UDP default 1M, TCP unlimited)"
//	@Param        parallel   query     integer  false  "Parallel streams, 1 to 8 (default 1)"
//	@Param        reverse    query     integer  false  "Set 1 to have the server send and the client receive"
//	@Param        server     query     string   false  "auto (default), none, or the pod to run the server on"
//	@Param        port       query     integer  false  "Server port: the one to use with server=none (default 5201), or the one to listen on for a managed server"
//	@Success      200  {object}  types.IperfReportResponse
//	@Failure      400  {object}  types.ErrorResponse
//	@Failure      404  {object}  types.ErrorResponse
//	@Failure      500  {object}  types.ErrorResponse
//	@Router       /iperf/run/{namespace}/{podName} [get]
func swaggerIperfReport() {}

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
var _ = types.QdiscState{}
var _ = types.PodQdiscsResponse{}
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
var _ = types.MeshnetGateResponse{}
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

var _ = types.TraceReportResponse{}
var _ = types.TraceHopDoc{}
var _ = types.TraceMetricsDoc{}

// swaggerExportNetworkConf godoc
//
//	@Summary      Export the applied network configuration
//	@Description  Returns the operations persisted for the pods of a namespace as a configure request (one target per pod, actions in the order they were applied), ready to be loaded again with POST /network/configure.
//	@Tags         network
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Success      200        {object}  types.ConfigureNetworkRequest
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /network/export-conf/{namespace} [get]
func swaggerExportNetworkConf() {}

// swaggerInspectPod godoc
//
//	@Summary      Inspect a pod
//	@Description  Runs one live read of the pod's driver, named by `what` (routes, neighbors, nat_rules, bridge_fdb, ospf_neighbors, ospf_interfaces, ospf6_neighbors, rip_routes, bgp_summary, isis_neighbors, as GET /drivers lists them per capability), and returns its rows with their columns. Without `what` it lists the inspections the pod's driver offers. Nothing of it goes to the history.
//	@Tags         pods
//	@Produce      json
//	@Param        namespace  path      string  true   "Namespace name"
//	@Param        podName    path      string  true   "Pod name"
//	@Param        what       query     string  false  "Inspection ID; empty lists the available ones"
//	@Success      200        {object}  types.PodInspectionResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      404        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /pods/inspect/{namespace}/{podName} [get]
func swaggerInspectPod() {}

// swaggerGetPodInterfaceCounters godoc
//
//	@Summary      Interface counters of a pod
//	@Description  Returns the kernel counters (bytes, packets, errors, drops, per direction) of every data interface of a pod, read from /proc/net/dev with one exec. `at` is the read time in Unix milliseconds, so two reads give rates.
//	@Tags         pods
//	@Produce      json
//	@Param        namespace  path      string  true  "Namespace name"
//	@Param        podName    path      string  true  "Pod name"
//	@Success      200        {object}  types.PodCountersResponse
//	@Failure      400        {object}  types.ErrorResponse
//	@Failure      500        {object}  types.ErrorResponse
//	@Router       /pods/counters/{namespace}/{podName} [get]
func swaggerGetPodInterfaceCounters() {}

// swaggerGetPodLogs godoc
//
//	@Summary      Container log of a pod
//	@Description  Returns the log of one container of a pod as text, like kubectl logs, read from the kubelet through the API server (no exec). The main container by default. `tail` is 500 lines unless given, 0 reads everything. The same log can be followed over WebSocket at /pods/logstream/{namespace}/{podName} with the same query: a first text frame with the metadata (containers, restarts), one binary frame per line, and a final text frame when the stream ends.
//	@Tags         pods
//	@Produce      plain
//	@Param        namespace   path      string  true   "Namespace name"
//	@Param        podName     path      string  true   "Pod name"
//	@Param        container   query     string  false  "Container to read, the main one by default"
//	@Param        tail        query     int     false  "Lines from the end, 0 for the whole log"  default(500)
//	@Param        since       query     int     false  "Only lines newer than this many seconds"
//	@Param        timestamps  query     bool    false  "Prefix every line with its timestamp"
//	@Param        previous    query     bool    false  "Read the log of the previous instance of the container, after a restart"
//	@Success      200         {string}  string  "The log"
//	@Failure      400         {object}  types.ErrorResponse
//	@Failure      500         {object}  types.ErrorResponse
//	@Router       /pods/logs/{namespace}/{podName} [get]
func swaggerGetPodLogs() {}
