# KubeNDT: Concrete Implementation Mechanisms

## 1. Driver Architecture & Resolution

### 1.1 Generic Driver Registration System

**Files**: `backend/drivers/registry/registry.go`, `backend/drivers/register_all.go`

The system uses Go generics with a reflection-based registry:

```
Registry Pattern:
  - Register[T Driver](ctor func() T)    // Generic registration
  - NewByName(name string)               // Retrieve driver by name
  - ResolveDefaultForType(t string)      // Get default driver for type
  - ValidDriversForType(t string)        // List valid drivers for type
```

**Registry Structure**:

- `registry`: map[string]reflect.Type, driver name → concrete type
- `registryType`: map[string]string, driver name → logical type (host|switch|router)
- `DefaultDriverByType`: map specifies default driver per type

**Registration Process**:

1. Each driver has a `NewXxxDriver()` constructor returning `*XxxDriver` implementing `Driver` interface
2. Constructor called, extracts Name() and Type() from instance
3. Stores reflect.Type in registry with validation that Name/Type are non-empty
4. NewByName() uses reflection to instantiate new instances from stored type

**Registered Drivers** (in register_all.go):

- `BasicHostDriver` (type: host), basic IP manipulation
- `HostDriver` (type: host), L2, L3, TC with ReplaceIP override
- `LinuxSwitchDriver` (type: switch), Linux bridge operations
- `OpenVSwitchDriver` (type: switch), OVS-specific operations
- `LinuxRouterDriver` (type: router), IP routing + TC
- `FRRRouterDriver` (type: router), FRR router with OSPF capability via vtysh
- `VyOSRouterDriver` (type: router), VyOS-specific driver for QEMU-based VyOS nodes; implements L2, L3, NAT and OSPF via VyOS CLI; also auto-adds `/dev/kvm` device mount

### 1.2 Capability Interface Contracts

**Files**: `backend/capabilities/capabilities/*.go`

**Interfaces** (defining capability contracts):

- `L2Capable`: LinkUp(iface), LinkDown(iface)
- `L3Capable`: SetIP, ReplaceIP, RemoveIP, SetDefaultRoute, RemoveDefaultRoute, Add/RemoveStaticRoute, Add/RemoveDNSNameserver, Add/RemoveDNSSearch
- `NATCapable`: EnableSNAT, DisableSNAT, EnableDNAT, DisableDNAT
- `TCCapable`: AddQdisc, DelQdisc (qdisc = tc traffic control)
- `SwitchCapable`: SetupBridge, TeardownBridge, Add/RemoveInterfaceToBridge
- `OSPFCapable`: OSPFAddNetwork, OSPFRemoveNetwork, OSPFSetRouterID, OSPFRemoveRouterID, OSPFPassiveDefault, OSPFRemovePassiveDefault, OSPFNoPassive, OSPFRemoveNoPassive, OSPFOriginateDefault, OSPFRemoveOriginateDefault, OSPFMTUIgnore, OSPFRemoveMTUIgnore

**Capability Bases** (default implementations):

- `L2Base`: Returns `[][]string` with `ip link set <iface> up|down`
- `L3Base`: IP addr, route, DNS operations with idempotent shell commands
- `TCBase`: netem (network emulation) and tbf (token bucket filter) qdisc builders
- `SwitchBase`: Linux bridge commands (brctl)
- `NATBase`: iptables SNAT/DNAT rules

**Embedding Pattern**:

```go
type HostDriver struct {
  drivers_meta.Meta          // Provides Name(), Type()
  capabilities.L2Base        // Embedded, provides LinkUp/LinkDown
  capabilities.L3Base        // Embedded, provides IP operations
  capabilities.TCBase        // Embedded, provides AddQdisc/DelQdisc
}
```

Drivers can override methods (e.g., HostDriver overrides ReplaceIP with idempotent shell check).

### 1.3 Driver Resolution for Pods

**Files**: `backend/helpers/driver_resolver.go`, `backend/helpers/configurator.go`

**GetDriverForPod(namespace, podName) → interface{}**:

1. Fetches pod object from Kubernetes
2. Reads the `kubendt/driver` label for the driver name (every pod has one, including QEMU pods, since the runtime is now derived from the driver itself via `drivers_meta.RuntimeProvider`)
3. Calls `drivers_registry.NewByName(drvName)` to instantiate driver
4. Returns `interface{}` (to be type-asserted by consumers)

**ResolveDriverCommands(driver interface{}, action types.ActionEntry) → [][]string**:

1. **`custom` bypass** (checked first, before any driver/capability logic):
   - `action.type == "custom"` → reads `action.command` (string or array), returns `[["sh","-c","<cmd>"]]` or `[["arg0","arg1",...]]`
   - No driver label required on the pod; `driver_type` is empty in history records
2. Validates protected interface "eth0" (only SNAT/DNAT allowed)
3. **Type assertion chain**: tries each capability interface in order
   - L2Capable: routes link_up, link_down
   - L3Capable: routes IP operations
   - NATCapable: routes SNAT/DNAT
   - TCCapable: routes qdisc operations
   - SwitchCapable: routes bridge operations
   - OSPFCapable: routes ospf_add_network, ospf_set_router_id, ospf_passive_default, ospf_no_passive, ospf_mtu_ignore, ospf_originate_default, and their remove variants
4. Returns `[][]string` where each inner slice is a complete command (e.g., `["ip", "addr", "add", "10.0.0.1/24", "dev", "eth1"]`)

**Custom action type**:

- `type: "custom"` is a protected bypass that executes arbitrary commands via `kubectl exec` without requiring a driver label on the pod.
- `command` field accepts a **string** (wrapped automatically in `["sh", "-c", <cmd>]`, enabling pipes/redirects) or an **array of strings** (used as-is for exact arg control).
- Pod does not need the `kubendt/driver` label. A pod can mix `custom` actions with driver-based actions, custom ones always succeed (if the pod exists), driver-based ones fail if no driver is found.
- `driver_type` in history/response is empty (`""`) for custom actions.
- Example:
  ```json
  { "type": "custom", "command": "mongosh open5gs --eval 'db.subscribers.insertOne({imsi:\"001010000000001\"})'" }
  { "type": "custom", "command": ["sysctl", "-w", "net.ipv4.ip_forward=1"] }
  ```

**Driver Assignment** (ResolveDriversForNodes):

- QEMU nodes: error if explicit driver specified
- Non-QEMU nodes:
  - No driver → use default for node type (host/switch/router)
  - Explicit driver → validate registration and type compatibility
  - Returns error if driver unknown or type mismatch

---

## 2. Reconciliation Mechanism

### 2.1 Multi-Round Recovery Strategy

**File**: `backend/helpers/reconcile.go`, `ReconcileMissingInterfaces(namespace, nodes, links, maxRounds=2)`

**Overview**: Bounded recovery strategy that progressively restarts pods to recover missing interfaces.

**Algorithm**:

```
FOR round = 1 TO maxRounds:
  1. Build desired link state from input
  2. Collect issues: for each link, check if both endpoints have their interfaces
     - Query pod "ip a" output, parse interface list
     - Skip pods that are terminating or not found

  3. If no issues → SUCCESS

  4. For each link with missing interfaces:
     - Round 1: Restart only the missing side (avoid restart cascades)
     - Round >1: Restart both endpoints (force clear meshnet skip state)
     - If both sides missing: intelligently choose one endpoint (uses pod type + missing count)

  5. Sleep(2*round seconds) and retry

  6. On final round: validate again, return error if issues persist
```

### 2.2 Interface Checking

**Function**: `podHasInterfaceWithRetries(namespace, podName, ifName, retries=3)`

- Executes `ip a` in pod via kubectl exec
- Parses output with regex to extract interface names
- Filters out eth0, localhost, tap* interfaces
- Returns (bool, reason) where reason explains why missing (pod-not-found, pod-terminating, interface-missing)

**Decision Functions**:

- `shouldCountAsMissing(reason)`: returns false for pod-terminating, pod-not-found
- `chooseRestartEndpoint()`: when both sides of link are broken, picks endpoint with:
  - Fewest total missing interfaces (spreads restarts)
  - Priority to routers/switches over hosts

### 2.3 Reconciliation Triggers

**File**: `backend/handlers/network.go`, `DeployNetwork()` handler

- Called at end of deploy after StatefulSets created and pods ready
- Mod workflow calls `ReconcileMissingInterfaces` after topology setup
- Scale-up and Add now pre-declare both sides of every new link in the Topology CRDs AND pre-inject peer skip entries (`injectPeerSkipEntries`) before creating/patching StatefulSets. With both pieces in place the new pod's first CNI ADD materialises the veth on both ends in a single pass, so the post-modify reconciliation typically passes with zero restarts (see Section 3.2 for the per-phase details).

### 2.4 Driver Operation Replay

**File**: `backend/helpers/driver_operation_history.go`

**ReplayDriverOperationsForPod(namespace, podName)**:

1. List all persisted operations for pod (ordered by ID)
2. Resolve driver from pod labels
3. For each operation:
   - Call `ResolveDriverCommands()` to get shell commands
   - Execute commands via kubectl exec
   - If unsupported by current driver: delete stale entry
   - If execution fails: delete entry (assume manual fix needed)
   - If success: increment replayed counter
4. Return DriverReplayStats{Total, Replayed, Pruned}

**Persistence**: Operations stored in SQLite `driver_operation_history` table during modify/configure operations

- Fields: namespace, pod_name, driver_type, action_type, action_json (serialized ActionEntry)
- Indexed on (namespace, pod_name) and executed_at for efficient retrieval

---

## 3. Deploy & Modify Workflows

### 3.1 Deployment Workflow

**File**: `backend/handlers/network.go`, `DeployNetwork(c *gin.Context)`

**Complete Flow** (13 steps):

```
1. Parse JSON request (nodes + links)
   - Validate: nodes ≥ 2, links ≥ 1

2. Validate namespace (enabled flag in k8s annotation)

3. Acquire namespace operation lock (prevents concurrent deploys)

4. Check topology state (must be empty)

5. Validate node types (host|switch|router)

6. Resolve drivers for nodes
   - QEMU nodes → driver=""
   - Non-QEMU → assign defaults or validate explicit drivers

7. Normalize replicas (0→1, cap at 16)

8. Prepare link UIDs
   - Generate or retrieve unique UIDs per link from link_uid_registry
   - Same UID used for both endpoints (deterministic)

9. Create Topology CRDs
   - One Topology CRD per pod (node.Replicas times)
   - Topology.spec.links contains:
     {local_intf, local_ip, peer_pod, peer_intf, peer_ip, uid}
   - Link definitions resolved for replicas:
     "node-0" or "node-0" converted to actual pod names

10. Create ConfigMaps from mount files
    - Read from files/<namespace>/<filename>
    - Create ConfigMap named <nodename>-file-<sanitized-filename>
    - Skip if file doesn't exist (warn, continue)

11. Create StatefulSets for each node
    - Replicas = node.Replicas
    - Pod template includes labels:
      kubendt/type: node.Type
      kubendt/driver: node.Driver
      kubendt/runtime: "qemu" | "k8s-linux"   (derived from the driver via drivers_meta.RuntimeProvider)
      kubendt/qemu: "true" | "false"         (same source as runtime, kept for backwards compat with handlers/shell.go)
    - Annotation: k8s.v1.cni.cncf.io/networks = meshnet
    - QEMU pods: Stdin=true, TTY=true, /dev/kvm added, Privileged=true
    - Non-QEMU pods: NET_ADMIN capability
    - Routers/switches: ip_forward sysctl = 1

12. Wait for pods ready (180s timeout)

13. Reconcile missing interfaces (bounded, 2 rounds)
    - Detects and recovers missing network interfaces
    - Restarts pods as needed

14. Set namespace topology state = true (in SQLite)
```

### 3.2 Modify Workflow (Add / Delete / Scale)

**File**: `backend/handlers/network.go`, `ModifyNetwork(c *gin.Context)`

A single modify request can contain any combination of `add`, `delete` and `scale` sections. The handler runs them in a fixed order so the resulting topology state is deterministic regardless of the user's payload order:

```
1. delete       (remove nodes/links)
2. scale-down   (shrink existing StatefulSets)
3. scale-up     (grow existing StatefulSets)
4. add          (create new nodes/links)
```

#### Add Phase: `ApplyAddToExistingTopology()`

```
1. Validation:
   - New nodes don't exist in namespace
   - Driver resolution for new nodes
   - Link validation (nodes/interfaces exist)

2. Prepare:
   - Assign/generate link UIDs (consistent with existing)
   - Create ConfigMaps for new node mounts

3. Declare CRDs FIRST (avoids reconciliation restart):
   - Create Topology CRDs for new pods (new-pod side declared)
   - upsertAppendLinksInTopologies for all add.links (peer side declared)
   - injectPeerSkipEntries on every new pod (writes
     {link_uid, new_pod_name} to each peer.status.skipped so the
     new pod's first CNI ADD knows it must create the veth itself
     instead of waiting for the long-running peer to re-fire CNI)

4. Create infrastructure:
   - Create StatefulSets for new nodes
   - Wait for new pods Ready

5. Recovery:
   - Restart any existing QEMU peer that gained a new link
     (NICs are fixed at QEMU launch and require re-launch)
   - Replay driver operations on restarted pods
```

#### Delete Phase: `ApplyDeleteOnExistingTopology()`

```
1. Validation:
   - Nodes/links exist in topology
   - Links reference valid pods

2. Modify topologies:
   - Remove links from Topology CRDs
   - Remove Topology CRDs for deleted pods

3. Delete infrastructure:
   - Delete StatefulSets (cascade deletes Pods)

4. Cleanup:
   - Restart affected pods (peers of deleted pods)
   - Replay operations on restarted pods
```

#### Scale Phase: `ApplyScaleDowns()` and `ApplyScaleUps()`

**File**: `backend/helpers/network_scale.go`

`BuildScalePlans()` first validates the requested replica counts against the current StatefulSets, rejects overlaps with `add.nodes` / `delete.nodes` and drops no-op entries (a `scale` that already matches the current count is logged and skipped). The plans are then split by direction so scale-downs and scale-ups run in sequence with clean state in between.

**Scale-down**, `ApplyScaleDowns()`:

```
For each scale-down plan (new < current):
  1. Compute orphan pods (ordinals [new, current))
  2. RemoveLinksReferencingDeletedPods (strip peer-side link entries)
  3. Delete orphan Topology CRDs
  4. Patch StatefulSet.Spec.Replicas = new
     (k8s terminates pods from highest ordinal downward)
  5. DeleteDriverOperationHistoryForPod for each orphan
```

**Scale-up**, `ApplyScaleUps()`:

```
For each scale-up plan (new > current):
  1. Compute new pods (ordinals [current, new))
  2. Pick the add.links that touch the new pods (relevantLinks)
  3. Declare CRDs FIRST:
     - CreateTopologyForPod for each new pod with relevantLinks
     - upsertAppendLinksInTopologies for the peer side
     - injectPeerSkipEntries on each new pod (same rationale as
       in Add Phase, avoids the reconciliation restart)
  4. Patch StatefulSet.Spec.Replicas = new
  5. WaitForPodsReadyByName for the new ordinals only
  6. Return consumedUIDs so the handler strips these links from
     request.Add.Links before invoking ApplyAddToExistingTopology
     (otherwise ValidateInterfaceConflicts would see the interface
     as already in use on the just-materialised pod)
```

The scale phases reuse the same Topology / StatefulSet / driver primitives as Add and Delete, so reconciliation, soft-heal and replay all apply uniformly across the four lifecycle operations.

### 3.3 Soft-Heal Mechanism (Post-Modify)

**File**: `backend/handlers/network.go`, after add/delete

```
Soft-heal Phase (non-destructive reconciliation):
1. Collect restarted pods from add/delete operations
2. Build links from Topology CRDs
3. Filter links touching restarted pods
4. Expand nudge set to include all peers of restarted pods
5. For each nudged pod:
   - NudgePodReconcile: Update pod annotation "kubendt/reconcile-at" = current timestamp
   - NudgeTopologyReconcile: Update Topology annotation "kubendt/reconcile-at" = current timestamp
6. No pod restart, no operation replay (just hints to external controllers)

This avoids expensive reconciliation rounds by hinting to meshnet to re-process
these pods WITHOUT restarting them.
```

---

## 4. State Recovery

### 4.1 SQLite Schema

**File**: `backend/database/database.go`

All per-namespace tables carry a `cluster_id` column (the canonical cluster ID = `kube-system` namespace UID; see [ARCHITECTURE.md](ARCHITECTURE.md#cluster-scoping)) so identically-named namespaces in different clusters never collide. Queries filter by the active cluster's ID, resolved via `kubeclient.CurrentClusterID`.

The schema version is tracked with `PRAGMA user_version` and advanced by `applyMigrations`; the first released schema is version `1`.

**Tables**:

1. **node_positions**
   - (cluster_id, namespace, node_id) PRIMARY KEY
   - x, y: frontend visualization coordinates

2. **namespace_state**
   - (cluster_id, namespace) PRIMARY KEY
   - has_topology: 0|1 (whether topology deployed)
   - updated_at: timestamp

3. **namespace_operations**
   - (cluster_id, namespace) PRIMARY KEY (enforces single operation per namespace)
   - operation_type: "deploy-network" | "modify-network"
   - started_at: timestamp

4. **link_uid_registry**
   - id, cluster_id, uid, namespace, node_name, interface_name, peer_node_name, peer_interface_name
   - uid: generated or assigned UID per link (unique within a cluster)
   - Indexed on (cluster_id, uid) and (cluster_id, namespace)

5. **driver_operation_history**
   - id (primary), cluster_id, namespace, pod_name, driver_type, action_type
   - action_json: serialized types.ActionEntry (all action-specific fields)
   - executed_at: timestamp
   - Indexed on (cluster_id, namespace, pod_name) and executed_at

6. **namespace_file_meta**
   - (cluster_id, namespace, path) PRIMARY KEY
   - sensitive: 0|1 (materialise the file as a Secret instead of a ConfigMap)
   - updated_at: timestamp

7. **clusters**
   - cluster_id PRIMARY KEY
   - context_name, server, last_seen: readable registry of known clusters, mirrored to `FILES_BASE_PATH/clusters.json`

### 4.2 Operation History Flow

**Persistence**:

1. During `ConfigureNetwork()` handler: for each action executed successfully
   - Call `SaveDriverOperation(namespace, podName, driverType, actionEntry)`
   - Serializes actionEntry to JSON, inserts into driver_operation_history
   - Idempotent: no dedup, just appends

2. During pod restart (reconciliation):
   - Call `ReplayDriverOperationsForPods(namespace, []podNames)`
   - Lists operations from DB, resolves driver, re-executes commands
   - Handles failures: prunes stale entries

### 4.3 Pod Restart & Recovery Sequence

**When pod crashes or is deleted/restarted**:

1. Kubernetes restarts pod (StatefulSet ensures recreation)
2. Pod mounts receive Topology CRD (meshnet populates networks)
3. Network interfaces appear in pod (meshnet creates veth pairs)
4. Backend reconciliation detects missing interfaces (if partial)
   - Triggers RestartPod (kills + StatefulSet recreates)
5. Post-restart: ReplayDriverOperationsForPods
   - Reads history from driver_operation_history
   - Resolves driver from pod label
   - Replays all previous operations in order
   - Handles failures: removes entries or continues

### 4.4 Backend Restart Handling

**When backend restarts**:

1. Database persists all operations
2. Next modify/deploy call will replay operations on affected pods
3. Topology state stored in namespace_state (allows resume)
4. Operation locks released at deployment end (or timeout mechanism TBD)

---

## 5. VM-based Node Adaptation

### 5.1 QEMU Node Characteristics

**Definition**:

- A node is QEMU-based when its resolved driver implements `drivers_meta.RuntimeProvider` and returns `RuntimeQEMU` (e.g. `VyOSRouterDriver`). The internal `NodeSpec.Qemu` field is populated by `ResolveDriversForNodes` from that source; it is not user-input and is not exposed in the request/response JSON.
- Pod labels at deployment time:
  - `kubendt/qemu: "true"` and `kubendt/runtime: "qemu"` (both set from the same source, kept in parallel so legacy readers keep working)

### 5.2 Detection & Adaptation

**File**: `backend/handlers/shell.go`, `InteractiveShellWebSocket()`

**QEMU Detection**:

```go
isQemuPod := podObj.Labels["kubendt/qemu"] == "true" ||
            podObj.Labels["kubendt/runtime"] == "qemu"
```

**Shell Mode Adaptation**:

- `mode="serial"`: Uses k8s attach API (direct console, no pty allocation)
- `mode="sh"/"bash"`: Uses k8s exec API with bash/sh shell
- `mode="auto"` (default):
  - QEMU pods: use attach (serial console fallback)
  - Non-QEMU: use exec with bash/sh

### 5.3 Configuration Differences

**File**: `backend/helpers/pods.go`, `CreateNetworkStatefulSet()`

**QEMU-specific pod configuration**:

```
Container:
  - Stdin: true     # Enable STDIN for serial console
  - TTY: true       # Allocate TTY for terminal
  - Env injected via Downward API:
    - POD_NAME: metadata.name
    - POD_NAMESPACE: metadata.namespace
    (Used for MAC generation inside VM)

SecurityContext:
  - Privileged: true    (required for VM hypervisor)
  - NET_ADMIN: added

DeviceMount:
  - /dev/kvm         (auto-added if not present)
  - Additional devices from node.Devices

Volume:
  - HostPath mounts for all devices

PodSecurityContext:
  - Sysctls: net.ipv4.ip_forward=1 (for routing if needed)
```

**Non-QEMU pods**:

```
Container:
  - Stdin: false
  - TTY: false
  - SecurityContext: NET_ADMIN only (not Privileged)

DeviceMount:
  - Only explicit devices from node.Devices
```

### 5.4 Driver Handling for QEMU

**Files**: `backend/helpers/driver_resolver.go`, `backend/drivers/meta/runtime.go`

The execution runtime (QEMU vs native Linux process) is declared by the driver, not by the user. Drivers that need a guest VM implement the `RuntimeProvider` interface:

```go
type RuntimeProvider interface {
    Runtime() string  // RuntimeQEMU | RuntimeNative
}
```

`VyOSRouterDriver` returns `RuntimeQEMU`. Every other driver omits the interface and defaults to native execution.

`ResolveDriversForNodes` resolves the driver name first (assigning the type default when empty, validating registration and type consistency), then sets `node.Qemu = (driver implements RuntimeProvider && Runtime() == RuntimeQEMU)`. Downstream code (StatefulSet labels, ConfigMap publisher, restart logic) reads `node.Qemu` exactly as before, only its source changed.

**Rationale**: the runtime is a property of the driver, not of the topology. The user picks a driver and the driver dictates whether QEMU is needed. This eliminates the previous error class where a topology could declare `qemu: true` with an incompatible driver, and it stops leaking an implementation detail into the public API.

### 5.5 Reconciliation Considerations

**QEMU pods in reconciliation**:

- Still participate in interface validation (if links reference them)
- Interfaces detected via `ip a` exec in the QEMU container
- Restart via pod deletion (no special handling)
- Post-restart: QEMU pod boots OS, meshnet configures networks
- Driver actions ARE replayed on QEMU pods after restart (every QEMU pod has a driver now)

---

## 6. Summary of Key Data Flows

### Deploy Flow (Simplified)

```
User Request (Nodes + Links)
  ↓
Namespace Lock (prevent concurrent ops)
  ↓
Validate nodes/types, Resolve drivers, Normalize replicas
  ↓
Create Topology CRDs (per pod)
  ↓
Create ConfigMaps (mounts + kubendt-internal-iface-counts for QEMU pods)
  ↓
Create StatefulSets (labels: driver + qemu/runtime derived from driver's RuntimeProvider)
  ↓
Wait pods ready (180s)
  ↓
ReconcileMissingInterfaces (2 rounds, restart+replay ops)
  ↓
Set namespace_state.has_topology = 1
  ↓
Return success
```

### Action Execution Flow (Modify)

```
User Action (e.g., SetIP on pod)
  ↓
GetDriverForPod (read label, instantiate)
  ↓
ResolveDriverCommands (type-assert capability, get shell cmds)
  ↓
Execute via Exec (kubectl exec, capture output)
  ↓
Success? → SaveDriverOperation (SQLite insert)
  ↓
Return result
```

### Pod Restart Recovery Flow

```
Pod deleted/crashed
  ↓
StatefulSet recreates pod
  ↓
Meshnet populates networks
  ↓
Reconciliation detects missing interfaces
  ↓
Restart pod again (soft-heal)
  ↓
ReplayDriverOperations (query history, resolve driver, re-exec)
  ↓
Interfaces operational
```

---

## 7. Key Implementation Details

### 7.1 Pod Naming Convention

- Node name: "router1" (base)
- Pod names: "router1-0", "router1-1", ... (for replicas)
- Link references can use base name (auto → -0) or full pod name
- Link UID generated once, stored in link_uid_registry

### 7.2 Idempotency

- **L3 operations**: Shell scripts check state before modifying (e.g., "grep -q || add")
- **Operation replay**: Re-executes operations, idempotent because commands use replace/sync
- **Configuration**: ConfigMaps mounted read-only, same content on restart
- **Driver operations**: Multiple executions of same action should reach same state

### 7.3 Protected Interface

- eth0: Default Kubernetes network, cannot be manipulated
- Driver commands protect eth0 via check in ResolveDriverCommands
- SNAT/DNAT allowed on eth0 for external connectivity

### 7.4 Namespace Locking

- Single operation at a time per namespace (SQLite PRIMARY KEY on namespace)
- Locks released at operation end (or via explicit endpoint)
- Prevents race conditions in multi-tenant scenarios

### 7.5 Link UID assignments

- UIDs: 1,000,000 - 10,000,000 (random generation)
- Stored in link_uid_registry for persistence
- Same UID used for both endpoints (canonical)
- Used for interface naming in meshnet (via UID mapping)

---

## 8. Technical Dependencies

**Kubernetes Concepts**:

- StatefulSets: pod lifecycle, scaling, deletion
- Pods: networking, labels, annotations
- ConfigMaps: file distribution
- Custom Resources (Topology CRD): optional, meshnet-operated

**External Controllers**:

- meshnet: CNI plugin, creates/manages network namespaces & veth pairs
- Kubernetes API: pod exec, pod deletion, resource creation

**Runtime**:

- kubectl exec: remote command execution
- SQL: state persistence
- Go reflection: driver instantiation

---

## 9. Error Handling & Edge Cases

### 9.1 Missing Interfaces Recovery

- Retry with exponential backoff (round-based)
- Bounded by maxRounds (default 2)
- Logs detailed reasons for each missing interface
- Returns error if after maxRounds issues persist

### 9.2 Operation Failures

- Unsupported driver action: pruned from history
- Execution failure: stale entry deleted, operation not retried
- Replay failure: logged, operation removed from history

### 9.3 Namespace Conflicts

- Second deploy on occupied namespace: 409 Conflict
- Concurrent modify ops: 409 Conflict (operation lock)
- Missing topology on modify: 400 Bad Request

### 9.4 QEMU vs Container Constraints

- QEMU nodes without an explicit driver: unmanaged, network configuration must be applied inside the guest OS manually.
- QEMU nodes with `VyOSRouterDriver`: fully managed via VyOS CLI; the driver translates the same declarative actions (L2, L3, NAT, OSPF) into VyOS-specific commands executed in the container.
- Require `/dev/kvm` on the host (not all systems). `VyOSRouterDriver` adds this device automatically.
- Serial console mode requires kernel support (attach API).

---

## 10. Extensibility Points

1. **New Drivers**: Embed capability bases, register with RegisterAllDrivers()
2. **New Capabilities**: Define interface, create base with default implementation, add type assertions in ResolveDriverCommands()
3. **New Actions**: Extend types.ActionEntry fields, add mapping in capability Interface & L3/NAT/TC/SwitchMethods maps
4. **Reconciliation Strategy**: Modify ReconcileMissingInterfaces algorithm (restart logic, retry count)
5. **State Recovery**: Alternative backends possible (Etcd, gRPC) by replacing SQLite DB layer

## 11. Packet Capture

Live per-interface capture surfaced in the UI as a Wireshark-like panel (packet list, protocol-tree detail, display filter, `.pcap` export).

### 11.1 Ephemeral-container model

There is no dedicated capture pod. An **ephemeral container** is injected into the _target pod_ and runs `tshark` there. Since all containers in a pod share the network namespace, it sees the pod's data-plane interfaces (`ethN` from Meshnet, `tapN` created by the QEMU entrypoint) — a single code path covers both `k8s-linux` and QEMU nodes, and capture is always pod-side (`ethN`/`tapN`), never inside a guest VM. See `helpers.InjectCaptureContainer` / `EnsureCaptureContainer`.

Image: anything with `tshark`/`dumpcap` + a shell. Default `nicolaka/netshoot` (no build step); override with `KUBENDT_CAPTURE_IMAGE`; slim image in `deploy/custom_images/capture/`. The container is granted `NET_RAW`+`NET_ADMIN`. Requires cluster support for ephemeral containers (GA since k8s 1.25).

### 11.2 One tshark, two outputs

A single `tshark` per run both writes the full `.pcap` (`-F pcap`, classic format) and prints the standard Wireshark columns (No/Time/Source/Destination/ Protocol/Length/Info) as line-buffered TSV (`-T fields` with `_ws.col.*`). The backend execs it into the container and forwards each stdout line to the browser over a WebSocket. Cancelling the exec context (socket close / Stop) kills `tshark` — that is the clean stop. See `helpers.BuildCaptureCommand`, `handlers.CaptureWebSocket`.

- **Detail**: on click, one packet is re-dissected from the saved pcap on demand (`tshark -r … -Y "frame.number==N" -T json`) — Wireshark's real tree.
- **Export all**: `cat` the pcap. **Export filtered**: `editcap -r … <ranges>` for the frame numbers currently matching the display filter.

### 11.3 One pcap file per run

A reused container hosts successive runs (start/stop/reconnect/clear); each run writes its own `/tmp/kubendt-capture-<runid>.pcap`. Separate files prevent a freshly started `tshark` from sharing a path with a previous one that hasn't fully exited — two writers on one path produce a sparse, half-zeroed, corrupt pcap. The run id reaches the client in the WebSocket `meta` frame and is echoed back on download/detail/clear as `?pcap=<runid>`.

### 11.4 Connection & lifecycle

WebSocket keepalive (server pings + read deadline; the browser auto-pongs) keeps an idle-interface capture alive; on a drop the panel reconnects, reusing the same ephemeral container. The capture process dies on stop/close; the idle container husk cannot be removed (a Kubernetes limitation), is bounded by a self-terminating sleeper, and is garbage-collected with the pod/topology.

Endpoints live under `/capture/*` (Swagger tag **capture**): `ws` (stream), `pcap` (download), `clear`, `packet` (detail). Code: `backend/helpers/capture.go`, `backend/handlers/capture.go`, `backend/routes.go`; UI: `frontend/src/components/CapturePanel.js` with entry points in `EdgeContextMenu.js` and `LinkInfoPanel.js`.

## 12. Traceroute / Packet-Path Visualization

Traces the real L3 path from a source pod to any IP or hostname and animates it on the graph. An envelope hops node by node along the lit edges, with step and scrub controls, a red ✕ when it is dropped or a green ✓ when it arrives, and an optional per-hop latency and loss table. The pods run a real dataplane (Meshnet) so this is the actual path the traffic takes, not a simulation.

### 12.1 Shared debug container

The probe does not run in the node image. It runs in an ephemeral `nicolaka/netshoot` container injected into the source pod's network namespace, so any image works (busybox, FRR, mongo) with nothing to install. Trace and capture share one debug container per pod. `helpers.EnsureDebugContainer` reuses a running `capture-*` husk (`FindRunningDebugContainer`) when there is one, otherwise it injects a fresh one. The container gets `NET_RAW` and `NET_ADMIN` for the ICMP sockets and for `tcptraceroute`.

### 12.2 Resolving hops to topology nodes

Every hop IP is mapped back to a node. `helpers.BuildTraceIPIndex` builds the index up front from the live IPv4 addresses of every pod (`ip -o -4 addr show`, which keeps the MAC-less TUN devices like `ogstun` and `uesimtun0` that the normal interface listing drops, so tunnel endpoints resolve) plus the IPs declared on the topology links. `helpers.AnnotateTraceHop` tags each hop.

- `l3`, resolved to a node. The path from the previous hop is rebuilt with a BFS over the pod adjacency (`BuildPodAdjacency`, `BFSPath`), so the packet animates through the switches on that segment.
- `tunnel`, resolved but with no topology path from the previous hop, like a GTP-U tunnel from UE to UPF. Drawn as a dashed overlay edge. The frontend refines this. When both ends sit on the same external-network node they share a physical L2 segment, so it reroutes through the grey node instead.
- `external`, a real IP outside the topology. Private ranges show as "external network", public ones as the internet (the packet flies outward).
- `timeout`, no reply for that TTL.

### 12.3 Modes, probes and verdict

Trace mode runs `traceroute`, ICMP by default (`-I`), UDP, or TCP through `tcptraceroute` (BusyBox `traceroute` has no `-T`). It streams line by line for the live animation and gives up after 3 consecutive no-replies (`MaxConsecutiveTraceTimeouts`) rather than grinding all the way to `-m 30`.

Metrics mode runs `mtr -n --json -c <cycles>` once and parses the report into the same hops, adding loss, avg, best, worst, last, jitter (StDev) and gmean.

The verdict is `unreachable` when a router answers ICMP `!N` or `!H` (red ✕), `delivered` when the destination replies (green ✓), or `unreached` when replies just stop. That last one stays amber because it might be a host filtering ICMP rather than a real drop.

Both modes go through one shared core, `helpers.RunTrace`, with an `emit(hop)` callback. The WebSocket forwards each hop live and the REST handler collects them into one document.

### 12.4 Interfaces and endpoints

Two front-ends sit over the same core (Swagger tag **trace**). The source node must be L3-capable, so a switch is rejected.

- `GET /trace/ws/{namespace}/{podName}` (WebSocket) streams `meta`, `status`, `hop` and `error` frames for the animated `TracePanel`. It is one-shot and closing the socket cancels the probe.
- `GET /trace/run/{namespace}/{podName}?dest=&method=&metrics=&cycles=` runs the same probe and returns it as one JSON document (`source`, `destination`, `method`, `mode`, `outcome`, `startedAt`, `finishedAt`, `hops`), the same shape as the panel's JSON export.

Code lives in `backend/helpers/trace.go`, `backend/handlers/trace.go` and `backend/routes.go`. The UI is `frontend/src/components/TracePanel.js` plus the packet and edge overlay in `InnerGraph.js`, launched from the node `ContextMenu.js`.

### 12.5 Limitations

IPv4 only (`ip -o -4`, no `-6`). One trace at a time, unlike capture. TCP probes hit port 80. Loss and latency on intermediate hops can mislead because routers often rate-limit ICMP while still forwarding, so only the final hop's numbers are fully trustworthy. The idle debug-container husk cannot be removed, the same Kubernetes limitation as capture.
