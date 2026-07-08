# KubeNDT Architecture: Visual Flows & Data Structures

## 1. Driver Registration & Resolution Architecture

### 1.1 Driver Life Cycle

```
REGISTRATION TIME (Backend Startup):
│
├─ RegisterAllDrivers()
│  ├─ Register(NewBasicHostDriver) 
│  │  └─ Extracts Name="BasicHostDriver", Type="host"
│  │     Stores: registry["BasicHostDriver"] = *BasicHostDriver type
│  ├─ Register(NewHostDriver)
│  │  └─ Extracts Name="HostDriver", Type="host"
│  ├─ Register(NewLinuxSwitchDriver)
│  │  └─ Extracts Name="LinuxSwitchDriver", Type="switch"
│  ├─ Register(NewOpenVSwitchDriver)
│  │  └─ Extracts Name="OpenVSwitchDriver", Type="switch"
│  ├─ Register(NewLinuxRouterDriver)
│  │  └─ Extracts Name="LinuxRouterDriver", Type="router"
│  ├─ Register(NewFRRRouterDriver)
│  │  └─ Extracts Name="FRRRouterDriver", Type="router"
│  └─ Register(NewVyOSRouterDriver)
│     └─ Extracts Name="VyOSRouterDriver", Type="router"
│        Also registers /dev/kvm device for this driver
│
└─ DefaultDriverByType = {
     "host": "BasicHostDriver" (default; "HostDriver" is opt-in)
     "switch": "LinuxSwitchDriver"
     "router": "LinuxRouterDriver"
   }

DRIVER RESOLUTION (on pod action):
│
├─ GetDriverForPod(ns, pod)
│  ├─ Fetch pod object
│  ├─ Check pod.Labels["kubendt/driver"] = "FRRRouterDriver"
│  └─ Call NewByName("FRRRouterDriver")
│     └─ Instantiate using reflection from registry
│        → returns *FRRRouterDriver instance
│
└─ ResolveDriverCommands(driver, action)
   ├─ Type assert driver.(L2Capable) → ok?
   │  └─ Call Capability method (e.g., LinkUp(iface))
   │     → returns [][]string{"ip", "link", "set", "eth1", "up"}
   │
   ├─ OR Type assert driver.(L3Capable) → ok?
   │  └─ Call Capability method (e.g., SetIP(iface, cidr))
   │     → returns [][]string{"ip", "addr", "add", "10.0.0.1/24", "dev", "eth1"}
   │
   ├─ OR Type assert driver.(NATCapable) → ok?
   │  └─ Call Capability method (e.g., EnableSNAT(iface))
   │     → returns iptables SNAT rule
   │
   ├─ OR Type assert driver.(OSPFCapable) → ok?
   │  └─ Call Capability method (e.g., OSPFAddNetwork(network, area))
   │     → returns [][]string{"vtysh", "-c", "configure terminal", "-c", "router ospf", "-c", "network 10.0.0.0/30 area 0"}
   │
   └─ Return [][]string with command(s) or nil if unsupported
```

### 1.2 Driver Type Hierarchy

```
┌─────────────────────────────────────────────────────────────────┐
│                     Driver Interface                            │
│  Name() → string, Type() → string (host|switch|router)          │
└─────┬───────────────────────────────────────────────────────────┘
      │
      ├─────────────────────┬──────────────────────┐
      │                     │                      │              
  HostDriver           SwitchDriver           RouterDriver        
  ├─ L2Base            ├─ L2Base              ├─ L2Base            
  ├─ L3Base            ├─ SwitchBase          ├─ L3Base            
  ├─ TCBase            ├─ TCBase              ├─ TCBase            
  │                    │                      ├─ NATBase           
  │                    │                      └─ [OSPF via direct methods (FRR)]
  │                    │                                           
  │  Overrides:        │  Overrides:           Overrides:         
  │  ReplaceIP()       │  (none typical)       OSPFAddNetwork()   
  │                    │                       OSPFSetRouterID()  
```

### 1.3 Capability Method Resolution Chain

```
Input: action = {Type: "set_ip", Iface: "eth1", CIDR: "10.0.0.1/24"}
       driver = (*FRRRouterDriver instance)
│
├─ L2Capable? (driver.(capabilities.L2Capable))
│  ├─ "link_up" → LinkUp()
│  ├─ "link_down" → LinkDown()
│  └─ else continue
│
├─ L3Capable? (driver.(capabilities.L3Capable))
│  ├─ "set_ip" → L3.SetIP(eth1, 10.0.0.1/24) ✓ MATCH
│  │  └─ Returns: [["ip", "addr", "add", "10.0.0.1/24", "dev", "eth1"]]
│  ├─ "replace_ip" → ReplaceIP()
│  ├─ "remove_ip" → RemoveIP()
│  ├─ "set_default_route" → SetDefaultRoute()
│  ├─ ... (other L3 methods)
│  └─ else continue
│
├─ NATCapable? (driver.(capabilities.NATCapable))
│  ├─ "enable_snat" → EnableSNAT()
│  ├─ "disable_snat" → DisableSNAT()
│  ├─ ... (other NAT methods)
│  └─ else continue
│
├─ TCCapable? (driver.(capabilities.TCCapable))
│  ├─ "add_qdisc" → AddQdisc()
│  ├─ "del_qdisc" → DelQdisc()
│  └─ else continue
│
├─ SwitchCapable? (driver.(capabilities.SwitchCapable))
│  ├─ "setup_bridge" → SetupBridge()
│  ├─ ... (other bridge methods)
│  └─ else continue
│
├─ OSPFCapable? (driver.(capabilities.OSPFCapable))
│  ├─ "ospf_add_network" → OSPFAddNetwork()
│  ├─ "ospf_remove_network" → OSPFRemoveNetwork()
│  ├─ "ospf_set_router_id" → OSPFSetRouterID()
│  ├─ "ospf_passive_default" → OSPFPassiveDefault()
│  ├─ "ospf_no_passive" → OSPFNoPassive()
│  ├─ "ospf_originate_default" → OSPFOriginateDefault()
│  ├─ "ospf_mtu_ignore" → OSPFMTUIgnore()
│  └─ else continue
│
└─ Return nil (action unsupported by this driver)

Output: [["ip", "addr", "add", "10.0.0.1/24", "dev", "eth1"]]
```

---

## 2. Reconciliation Flow Diagram

### 2.1 Multi-Round Reconciliation Algorithm

```
ReconcileMissingInterfaces(namespace, nodes, links, maxRounds=2)
│
├─ Build DesiredLinks from input
│  └─ For each link: {PodA, IfA, PodB, IfB}
│
├─ FOR round = 1 TO maxRounds:
│  │
│  ├─ CollectLinkIssues()
│  │  ├─ FOR each desired_link:
│  │  │  ├─ podHasInterfaceWithRetries(ns, PodA, IfA)
│  │  │  │  ├─ kubectl exec PodA "ip a"
│  │  │  │  ├─ Parse regex: "interface" from "4: eth1@if19:"
│  │  │  │  ├─ Filter: exclude eth0, lo, tap*
│  │  │  │  └─ Return (hasIntf: bool, reason: string)
│  │  │  │
│  │  │  ├─ Same for PodB, IfB
│  │  │  │
│  │  │  └─ Record missing: {MissingA, ReasonA, MissingB, ReasonB}
│  │  │
│  │  └─ Increment missingCountByPod[PodA] and missingCountByPod[PodB]
│  │
│  ├─ IF no issues → RETURN success ✓
│  │
│  ├─ DecideRestarts()
│  │  ├─ FOR each broken_link (missing interfaces):
│  │  │  │
│  │  │  ├─ IF only PodA missing AND round==1:
│  │  │  │  └─ toRestartSet.add(PodA)  ← restart only missing side
│  │  │  │
│  │  │  ├─ ELSE IF only PodB missing AND round==1:
│  │  │  │  └─ toRestartSet.add(PodB)  ← restart only missing side
│  │  │  │
│  │  │  ├─ ELSE IF both missing OR round>1:
│  │  │  │  ├─ chooseRestartEndpoint(PodA, PodB, countA, countB, type)
│  │  │  │  │  ├─ Prefer pod with fewer total missing
│  │  │  │  │  ├─ Prefer routers > switches > hosts
│  │  │  │  │  └─ Return chosen pod
│  │  │  │  └─ toRestartSet.add(chosen)
│  │  │  │
│  │  │  └─ Record restart reason for debugging
│  │  │
│  │  └─ Sort toRestart deterministically
│  │
│  ├─ IF toRestartSet is empty:
│  │  ├─ Sleep(2*round seconds)
│  │  └─ Continue next round
│  │
│  ├─ RestartPods(toRestart)
│  │  ├─ FOR each pod in toRestart:
│  │  │  ├─ Delete pod (StatefulSet auto-recreates)
│  │  │  └─ Wait for pod Ready
│  │  │
│  │  └─ ReplayDriverOperationsForPods(ns, toRestart)
│  │     ├─ FOR each pod:
│  │     │  ├─ Query driver_operation_history WHERE pod_name=pod
│  │     │  ├─ GetDriverForPod(pod) → resolve driver
│  │     │  ├─ FOR each operation:
│  │     │  │  ├─ ResolveDriverCommands(driver, action)
│  │     │  │  ├─ kubectl exec pod "cmd1" "cmd2" ...
│  │     │  │  ├─ IF success → operations[replayed]++
│  │     │  │  ├─ ELSE → delete from history, operations[pruned]++
│  │     │  │
│  │     │  └─ Log replay summary
│  │     │
│  │     └─ Return DriverReplayStats{total, replayed, pruned}
│  │
│  ├─ Sleep(2*round seconds)
│  │
│  └─ [Next iteration of round]
│
└─ AFTER all rounds: 
   ├─ Final validation check
   └─ IF issues persist → RETURN error("reconciliation failed after "+maxRounds+" rounds")
```

---

## 3. Deploy Workflow State Machine

```
┌──────────────────────────────────────────────────────────────────────┐
│ DeployNetwork() Handler                                              │
└──────────────────────────────────────────────────────────────────────┘
        │
        ↓
 ┌─────────────────────────┐
 │ Parse + Validate Input  │
 │ - JSON nodes + links    │
 │ - nodes ≥ 2, links ≥ 1  │
 └────────┬────────────────┘
          │
          ↓
 ┌──────────────────────────┐
 │ Validate Namespace       │
 │ - Enabled? (k8s annot)   │
 │ - Empty topology?        │
 │ - Acquire lock           │
 └────────┬─────────────────┘
          │
          ↓
 ┌────────────────────────────┐
 │ Resolve Drivers            │
 │ - QEMU → no driver         │
 │ - Non-QEMU → default/valid │
 └────────┬───────────────────┘
          │
          ↓
 ┌──────────────────────┐
 │ Normalize Inputs     │
 │ - Replicas: 0→1, cap│
 │ - Link UIDs          │
 └──────┬───────────────┘
        │
        ↓
 ┌──────────────────────────────────┐
 │ Create Resources (K8s API)       │
 │                                  │
 │  FOR each node:                  │
 │    1. Create Topology CRDs       │
 │       └─ meshnet watches these   │
 │    2. Create ConfigMaps          │
 │    3. Create StatefulSets        │
 │       └─ Pods get labels:        │
 │         - kubendt/driver         │
 │         - kubendt/qemu           │
 │         - kubendt/runtime        │
 └──────┬───────────────────────────┘
        │
        ↓
 ┌─────────────────────────────┐
 │ Wait Pods Ready (180s)      │
 │ - Readiness probe: "ip a"   │
 │ - All replicas ready        │
 └──────┬──────────────────────┘
        │
        ↓
 ┌──────────────────────────────────────┐
 │ Reconcile Missing Interfaces         │
 │ - Progressive multi-round recovery   │
 │ - Restarts pods as needed            │
 │ - Replays driver operations          │
 └──────┬───────────────────────────────┘
        │
        ↓
 ┌──────────────────────────┐
 │ Persist State (SQLite)   │
 │ - namespace_state        │
 │ - link_uid_registry      │
 └──────┬───────────────────┘
        │
        ↓
 ┌──────────────────────────┐
 │ Release Lock             │
 │ - Other ops can proceed  │
 └──────┬───────────────────┘
        │
        ↓
 ┌────────────────────────┐
 │ Return 200 OK          │
 │ - Topology deployed    │
 │ - Ready for use        │
 └────────────────────────┘
```

---

## 4. Pod Lifecycle & State

### 4.1 Pod Phases & Driver Operations

```
CREATION:
  StatefulSet(node="router1", replicas=2)
      ↓
  Creates: router1-0, router1-1 pods
      ↓
  Kubernetes assigns:
    - UID, name, namespace
    - Labels from StatefulSet template:
      * kubendt/type: "router"
      * kubendt/driver: "FRRRouterDriver"
      * kubendt/qemu: "false"
      * kubendt/runtime: "k8s-linux"
    - Mounts ConfigMaps

NETWORK SETUP:
  Meshnet (CNI) watches Topology CRD
      ↓
  Creates network namespace
      ↓
  Attaches veth pairs to pod:
    - eth1 (to peer-pod)
    - eth2 (to another peer)
      ↓
  Interfaces appear in pod: "ip a"

USER ACTION (e.g., SetIP):
  SetIP eth1 10.0.0.1/24
      ↓
  GetDriverForPod → FRRRouterDriver
      ↓
  ResolveDriverCommands → L3.SetIP() → ["ip", "addr", "add", "10.0.0.1/24", "dev", "eth1"]
      ↓
  kubectl exec router1-0 "ip addr add 10.0.0.1/24 dev eth1"
      ↓
  SaveDriverOperation:
    {namespace: "test", pod_name: "router1-0", driver_type: "FRRRouterDriver",
     action_type: "set_ip", action_json: "{\"type\":\"set_ip\",\"iface\":\"eth1\",...}"}
      ↓
  Return result

POD RESTART (failure/manual restart):
  Pod deleted
      ↓
  StatefulSet creates new router1-0
      ↓
  Kubernetes assigns new UID, mounts same ConfigMaps
      ↓
  Meshnet recreates networks (Topology CRD still exists)
      ↓
  Interfaces appear (similar to creation)
      ↓
  Reconciliation detects via "ip a" in pod
      ↓
  If missing → recursive restart
      ↓
  ReplayDriverOperations:
    Query: SELECT * FROM driver_operation_history WHERE pod_name="router1-0" ORDER BY id
    For each row:
      - GetDriverForPod → FRRRouterDriver
      - ResolveDriverCommands → ["ip", "addr", "add", "10.0.0.1/24", "dev", "eth1"]
      - kubectl exec → SUCCESS
      - (if unsupported or failed, delete row)
      ↓
  State recovered
```

---

## 5. State Persistence Model

### 5.1 Database Schema Relationships

```
┌─────────────────────────────────────────────────────────────────┐
│                                                                 │
│  namespace_state                  link_uid_registry             │
│  ┌──────────────────────────┐     ┌──────────────────────────┐  │
│  │ PK: namespace            │     │ PK: id                   │  │
│  │ ├─ has_topology (0|1)    │     │ ├─ uid (int)             │  │
│  │ ├─ updated_at            │     │ ├─ namespace (FK→state)  │  │
│  │ └─ [state tracking]      │     │ ├─ node_name             │  │
│  └──────────────────────────┘     │ ├─ peer_node_name        │  │
│         ↑                         │ ├─ interface_name        │  │
│         │                         │ ├─ peer_interface_name   │  │
│         │                         │ └─ updated_at            │  │
│         │                         └──────────────────────────┘  │
│         │                                  ↑                    │
│         └──────────────────────────────────┘                    │
│              (namespace grouping)                               │
│                                                                 │
│  namespace_operations             driver_operation_history      │
│  ┌──────────────────────────┐     ┌──────────────────────────┐  │
│  │ PK: namespace            │     │ PK: id                   │  │
│  │ ├─ operation_type        │     │ ├─ namespace (FK→ops)    │  │
│  │ │  (deploy|modify)       │     │ ├─ pod_name              │  │
│  │ ├─ started_at            │     │ ├─ driver_type           │  │
│  │ └─ [concurrency lock]    │     │ ├─ action_type           │  │
│  └──────────────────────────┘     │ ├─ action_json (JSON)    │  │
│         ↑                         │ ├─ executed_at (indexed) │  │
│         │                         │ └─ [audit trail]         │  │
│         │                         └──────────────────────────┘  │
│         ├──────────────────────────────────┘                    │
│         │ (prevents concurrent deploys)                         │
│         │                                                       │
│  node_positions                                                 │
│  ┌──────────────────────────┐                                   │
│  │ PK: (namespace, node_id) │                                   │
│  │ ├─ x, y (floats)         │                                   │
│  │ └─ [frontend coords]     │                                   │
│  └──────────────────────────┘                                   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 5.2 Driver Operation History Replay Sequence

```
Step 1: Pod Restart Detected
  └─ Reconciliation detects missing interface
     → Restarts pod (delete + StatefulSet recreates)

Step 2: Query Operation History
  └─ SELECT * FROM driver_operation_history
     WHERE namespace = "test" AND pod_name = "router1-0"
     ORDER BY id ASC
  
  Result:
  ┌────┬───────────┬──────────────────┬───────────────────┬────────────┐
  │ id │ pod_name  │ driver_type      │ action            │ action_json│
  ├────┼───────────┼──────────────────┼───────────────────┼────────────┤
  │ 1  │ router1-0 │ FRRRouterDriver  │ set_ip            │   {...}    │
  │ 2  │ router1-0 │ FRRRouterDriver  │ set_default_route │   {...}    │
  │ 3  │ router1-0 │ FRRRouterDriver  │ add_dns  	      │   {...}    │
  └────┴───────────┴──────────────────┴───────────────────┴────────────┘

Step 3: For Each Operation
  ├─ GetDriverForPod("test", "router1-0")
  │  └─ Pod label "kubendt/driver" = "FRRRouterDriver"
  │     → drivers_registry.NewByName("FRRRouterDriver")
  │     → *FRRRouterDriver instance
  │
  ├─ Unmarshal action_json → ActionEntry{Type:"set_ip", Iface:"eth1", CIDR:"10.0.0.1/24"}
  │
  ├─ ResolveDriverCommands(driver, action)
  │  └─ Type assert driver.(L3Capable)
  │     → L3.SetIP("eth1", "10.0.0.1/24")
  │     → [["ip", "addr", "add", "10.0.0.1/24", "dev", "eth1"]]
  │
  ├─ kubectl exec router1-0 "ip" "addr" "add" "10.0.0.1/24" "dev" "eth1"
  │  └─ IF success: replayed++
  │  OR IF unsupported: DELETE FROM history WHERE id=1; pruned++
  │  OR IF failed: DELETE FROM history WHERE id=1; pruned++

Step 4: Repeat for ops 2, 3...

Step 5: Return Stats
  └─ {Total: 3, Replayed: 3, Pruned: 0}
     → All operations restored
```

---

## 6. QEMU vs Container Pod Differences

### 6.1 Configuration Matrix

```
                        CONTAINER POD              QEMU POD
────────────────────────────────────────────────────────────────
Label: kubendt/qemu     "false"                    "true"
Label: kubendt/runtime  "k8s-linux"                "qemu"
────────────────────────────────────────────────────────────────
Driver Assignment       Yes (default by type)      No (empty) by default;
                                                   explicit driver allowed
                                                   (e.g. VyOSRouterDriver)
Driver Operations       Replayed                   N/A (unmanaged) or
                                                   Replayed (VyOS driver)
────────────────────────────────────────────────────────────────
Container.Stdin         false                      true
Container.TTY           false                      true
SecurityContext.Priv    false (or if router)       true
────────────────────────────────────────────────────────────────
DeviceMount: /dev/kvm   No                         Yes (auto-add)
DeviceMount: specified  Yes                        Yes
────────────────────────────────────────────────────────────────
Shell mode detection    Tries bash, falls to sh    Uses serial
Shell API               exec (kubectl exec)        attach (serial)
────────────────────────────────────────────────────────────────
Network Interfaces      veth pairs from meshnet    veth + inside VM routing
IP Configuration        Applied via driver ops     Inside VM (manual)
Reconciliation          Validates interfaces       Validates interfaces
────────────────────────────────────────────────────────────────
Boot Time               ~2-10 seconds              ~30-60 seconds
Use Case                Lightweight network        Full VM with routing
                        simulation                 daemon/OS
```

### 6.2 QEMU Pod Startup Flow

```
StatefulSet creates router1-0 (with Qemu=true)
    ↓
K8s creates pod with:
  ├─ Stdin=true, TTY=true
  ├─ /dev/kvm mount (HostPath)
  ├─ Privileged=true
  └─ NET_ADMIN capability
    ↓
Container entrypoint runs (e.g., QEMU hypervisor command)
    ↓
Kernel boots inside container
    ↓
Once kernel ready, meshnet creates veth pairs in pod ns
    ↓
Inside VM kernel, interfaces appear (eth0 → K8s default, eth1, eth2...)
    ↓
user$ ip a
  1: lo
  2: eth0@if...      (meshnet managed)
  3: eth1@if...      (peer to router2-0)
  4: eth2@if...      (peer to switch1-0)
    ↓
User can configure routing inside VM manually
OR frontend can SSH in and run commands
    ↓
If restart needed:
  - Pod deleted
  - StatefulSet recreates pod
  - QEMU boots again
  - Meshnet recreates networks (same Topology CRD)
  - Interfaces auto-populate again
```

---

## 7. Sequence Diagrams

### 7.1 Setup Action Sequence

```
User
  │
  ├─→ POST /api/network/deploy-network/:namespace {nodes, links}
  │         │
  │         ├─→ DynamicClient.List StatefulSets      [check empty]
  │         ├─→ For each node:
  │         │    ├─→ Create Topology CRD             [meshnet watches]
  │         │    ├─→ Create ConfigMaps               [mounts]
  │         │    └─→ Create StatefulSet              [pods spawn]
  │         │
  │         └─→ WaitForPodsReady (180s timeout)
  │                ├─→ Poll pods until Ready=true
  │                └─→ ReconcileMissingInterfaces
  │                     ├─→ Query interfaces per pod
  │                     ├─→ Restart broken pods
  │                     └─→ Replay operations
  │
  └←─ 200 OK {message: "deployed"}

Kubernetes
  │
  ├─→ Create pods from StatefulSet
  │   ├─→ Assign UID, labels
  │   ├─→ Notify meshnet (CNI)
  │   └─→ Block pod until network ready
  │
  └─← Return pod Ready when network namespace populated

Meshnet (CNI Controller)
  │
  ├─ Watch Topology CRD created
  │   ├─→ Create network namespace
  │   ├─→ Allocate veth pairs
  │   ├─→ Map UID → interface names
  │   └─→ Signal pod Ready to K8s
  │
  └─ Continue monitoring Topology for changes
```

### 7.2 Modify (Add) Sequence

```
User
  │
  ├─→ POST /api/network/modify-network/:namespace {add: {nodes, links}, delete: {...}}
  │         │
  │         ├─→ AcquireNamespaceOperationLock
  │         │
  │         ├─→ ApplyDeleteOnExistingTopology     [if delete present]
  │         │    ├─→ Remove links from Topologies
  │         │    ├─→ Delete StatefulSets
  │         │    └─→ Restart affected peers
  │         │
  │         ├─→ ApplyAddToExistingTopology        [if add present]
  │         │    ├─→ Validate new nodes don't exist
  │         │    ├─→ Create StatefulSets
  │         │    ├─→ Append links to Topology CRDs
  │         │    ├─→ RestartPods (affected)
  │         │    └─→ ReplayDriverOperations
  │         │
  │         ├─→ SoftHeal:
  │         │    ├─→ NudgePodReconcile (annotation update)
  │         │    └─→ NudgeTopologyReconcile (annotation update)
  │         │
  │         ├─→ ReleaseNamespaceOperationLock
  │         │
  │         └─→ SyncNamespaceTopologyState
  │
  └←─ 200 OK {message: "modify applied", restarted_pods: [...]}

Meshnet (watching Topology updates)
  │
  ├─ Detect Topology CRD modified (spec.links changed)
  │   ├─→ Update veth pair attachments
  │   └─→ Notify pod (via annotation watch)
  │
  └─ Non-destructive update (no pod restart required from meshnet side)
```

---

## 8. Error Handling Flows

### 8.1 Missing Interface Recovery (Reconciliation Failure Path)

```
Reconciliation starts
    │
    ├─ Round 1: Detect eth1 missing on router1-0
    │   ├─ Restart router1-0
    │   ├─ Replay operations
    │   └─ Check again: still missing?
    │
    ├─ IF still missing AND round < maxRounds:
    │   │
    │   ├─ Round 2: Restart both endpoints of link
    │   │           (router1-0 and its peer)
    │   │
    │   ├─ Check: eth1 present now? YES ✓
    │   │
    │   └─ RETURN success
    │
    ├─ ELSE IF after maxRounds still missing:
    │   │
    │   ├─ Log detailed issue report:
    │   │   {link: "router1-0.eth1 ↔ router2-0.eth1",
    │   │    missingAttempts: 2,
    │   │    reason: ["interface-missing", "interface-missing"],
    │   │    podTypes: ["router", "router"]}
    │   │
    │   └─ RETURN error("reconciliation: interfaces still missing after 2 rounds")
    │
    ├─ Caller decides:
    │   ├─ Option 1: Return 500 to user (deploy failed)
    │   ├─ Option 2: Log warning, continue (current: option 2)
    │   └─ Option 3: Kick a manual recovery task (async)
    │
    └─ [end]
```

### 8.2 Operation Replay Failure Path

```
ReplayDriverOperations called for router1-0
    │
    ├─ Query history: op_id=42, action=set_ip
    │   │
    │   ├─ ResolveDriverCommands() returns nil (unsupported)
    │   │   ├─ Log: "Removed stale operation 42"
    │   │   ├─ DELETE FROM history WHERE id=42
    │   │   └─ Continue
    │   │
    │   ├─ ResolveDriverCommands() returns commands ✓
    │   │   │
    │   │   ├─ kubectl exec pod "ip" "addr" "add" ... → FAILED
    │   │   │   ├─ Log error
    │   │   │   ├─ DELETE FROM history WHERE id=42 (prune failed entry)
    │   │   │   └─ Continue (don't retry)
    │   │   │
    │   │   └─ kubectl exec pod "ip" "addr" "add" ... → SUCCESS ✓
    │   │       ├─ Increment replayed++
    │   │       └─ Continue
    │   │
    │   └─ [next operation]
    │
    ├─ Return Stats{Total: 5, Replayed: 4, Pruned: 1}
    │
    └─ [end] (even if some failed, don't error, state best-effort)
```

---

## 9. Key Data Structures

### 9.1 ActionEntry (types/types.go)

```go
type ActionEntry struct {
  Type string // "set_ip", "link_up", "enable_snat", ...
  
  // L2 actions
  Iface string // Interface name: "eth1"
  
  // L3 actions
  CIDR string    // "10.0.0.1/24"
  Gateway string // "10.0.0.254"
  DstCIDR string // Destination CIDR for routes
  Device string  // Device for routing
  DNSServer string // Nameserver IP
  DNSDomain string // Search domain
  
  // NAT actions
  ExternalPort int    // External port (iptables DNAT)
  InternalPort int    // Internal port
  InternalIP string   // Internal IP
  Protocol string     // "tcp", "udp"
  
  // Bridge actions
  Bridge string  // Bridge name
  Ifaces []string // Multiple interfaces
  
  // TC (qdisc) actions
  TCParams *TCParamEntry // Delay, loss, rate limiting params
}

type TCParamEntry struct {
  Qdisc string  // "netem" or "tbf"
  Delay string  // "100ms"
  Jitter string // "10ms"
  Loss string   // "5%"
  Duplicate string // "1%"
  Corrupt string // "0.5%"
  Limit *int    // Packet limit
  Rate string   // "1mbps"
  Burst string  // "10kb"
  Latency string // "50ms"
}
```

### 9.2 PersistedDriverOperation (helpers/driver_operation_history.go)

```go
type PersistedDriverOperation struct {
  ID         int64             // Auto-increment, PK
  Namespace  string            // Isolation
  PodName    string            // Pod reference (for replay targeting)
  DriverType string            // For logging (e.g., "FRRRouterDriver")
  ActionType string            // Action identifier (e.g., "set_ip")
  Action     types.ActionEntry // Deserialized action (all params)
  ExecutedAt string            // RFC3339Nano timestamp
}

// In SQLite:
// action_json = JSON serialization of ActionEntry
//   e.g., {"type":"set_ip","iface":"eth1","cidr":"10.0.0.1/24"}
```

### 9.3 Driver Type (drivers/registry/registry.go)

```go
type Driver interface {
  Name() string   // "FRRRouterDriver"
  Type() string   // "host"|"switch"|"router"
}

// Internal registry storage:
registry map[string]reflect.Type           // Name → Type
registryType map[string]string             // Name → LogicalType
DefaultDriverByType map[string]string      // LogicalType → DefaultName
```

---

## Summary

This KubeNDT architecture enables:
1. **Pluggable drivers** via generic registration & type assertions
2. **Resilient recovery** through bounded reconciliation with intelligent restart selection
3. **State persistence** via operation replay from SQLite
4. **Mixed workloads** supporting both lightweight containers and full VMs
5. **Non-destructive updates** via soft-heal mechanism (annotation nudges)

The implementation emphasizes idempotency, progressive failure recovery, and minimal disruption during topology changes.
