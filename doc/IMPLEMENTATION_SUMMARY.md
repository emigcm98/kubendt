# KubeNDT Implementation: Executive Summary

## Overview

KubeNDT implements network topology management for Kubernetes through five interconnected mechanisms. This document provides sufficient operational detail for comprehensive technical documentation.

---

## 1. Driver Architecture & Resolution

**Problem Solved**: Network configuration varies by node type (host/switch/router) and deployment model. A plugin-based system allows different implementations without core changes.

**Solution**:
- **Generic Driver Registry**: Reflection-based registration system stores `reflect.Type` for each driver, enables instantiation at runtime
- **Capability Interfaces**: Drivers advertise capabilities via interface embedding:
  - `L2Capable`: Link layer (up/down)
  - `L3Capable`: IP operations (address, routing, DNS)
  - `NATCapable`: NAT rules (SNAT/DNAT via iptables)
  - `TCCapable`: Traffic control (netem/tbf qdisc)
  - `SwitchCapable`: Bridge operations (brctl)
  - `OSPFCapable`: OSPF routing protocol (FRR vtysh, configures running process directly)
- **Type-Asserted Command Resolution**: `ResolveDriverCommands()` tries each interface in order, returns shell commands

**Concrete Drivers**:
- `BasicHostDriver`, `HostDriver`: L2+L3+TC (HostDriver overrides ReplaceIP for idempotency)
- `LinuxSwitchDriver`, `OpenVSwitchDriver`: L2+Bridge+TC
- `LinuxRouterDriver`, `FRRRouterDriver`: L2+L3+NAT+TC (FRR also implements OSPFCapable via vtysh)
- `VyOSRouterDriver`: L2+L3+NAT+OSPF for QEMU-based VyOS nodes; translates actions into VyOS CLI commands; auto-adds `/dev/kvm` device

**Pod Resolution**:
- Label `kubendt/driver` contains driver name
- `GetDriverForPod()` fetches pod, reads label, instantiates via `NewByName()`
- Called before every action, allows driver changes via pod recreation

---

## 2. Reconciliation Mechanism

**Problem Solved**: Network interfaces may not appear immediately in pods due to meshnet delays or partial veth attachment. System must detect and recover missing interfaces without manual intervention.

**Solution**:
- **Progressive Multi-Round Recovery**: Calls `ReconcileMissingInterfaces(maxRounds=2)` at deployment end and on-demand
- **Intelligent Endpoint Restart Selection**:
  - Round 1: Restart only missing endpoint (avoids cascade)
  - Round 2+: Restart both endpoints (clears meshnet skip state)
  - If both missing: choose by lowest missing count, prefer router/switch types
- **Deterministic Retries**: Sleeps 2×round seconds between iterations
- **Post-Restart Recovery**: `ReplayDriverOperationsForPods()` restores all previous operations from SQLite

**Interface Detection**:
- Queries `ip a` output from pod (via kubectl exec)
- Parses regex to extract interface names
- Filters eth0, localhost, tap* (protected/irrelevant)
- Records reason for missing (pod-not-found, pod-terminating, interface-missing)

**Bounded Guarantee**: After maxRounds, returns error if issues persist (never infinite loop)

---

## 3. Deploy & Modify Workflows

**Deployment** (13-step ordered process):
1. Parse & validate JSON (≥2 nodes, ≥1 links)
2. Validate namespace (enabled, empty topology)
3. Acquire operation lock (prevents concurrent deploys)
4. Check topology state (must be empty)
5. Validate node types (host|switch|router)
6. **Resolve drivers** (assign defaults, validate explicit drivers)
7. Normalize replicas (0→1, cap at 16)
8. Prepare link UIDs (deterministic, stored in registry)
9. Create Topology CRDs (per pod, meshnet watches)
10. Create ConfigMaps (from mount file specifications)
11. Create StatefulSets (with labels, devices, security context)
12. Wait for pods ready (180s timeout, readiness probe: `command -v ip`)
13. Reconcile missing interfaces (2 rounds, restarts & replays)

**Modification** (Add/Delete in single request):
- **Delete Phase**: Remove links from Topology CRDs, delete StatefulSets (cascades pods), restart peer pods
- **Add Phase**: Create new StatefulSets, append links to Topology CRDs, restart affected pods
- **Soft-Heal** (post-modify): Update pod/topology annotations with timestamps (nudges external controllers, no pod restart)
- **Operation Replay**: Restarts trigger replay of persisted driver operations

**Topology Delta Computation**:
- Links identified by (source_pod, dest_pod) pair
- UID (link identifier) deterministic across operations
- Topology CRD list computed per pod from links
- Changes detected by comparing Topology.spec.links before/after

**Pod Restart Decision**:
- All pods in affected links restarted (to ensure meshnet picks up link changes)
- Soft-heal nudges peers of restarted pods (annotation update, no restart)

---

## 4. State Recovery

**Persistence Model**:
- **SQLite database** (`kubendt.db`, configurable path)
- **Five tables**:
  - `namespace_state`: topology deployment flag per namespace
  - `namespace_operations`: operation lock (single row per namespace, prevents concurrent ops)
  - `link_uid_registry`: stable UIDs for links (deterministic across deploys)
  - `driver_operation_history`: audit trail of all pod actions (indexed by namespace, pod_name, timestamp)
  - `node_positions`: frontend visualization coordinates

**Operation History Flow**:
1. `SaveDriverOperation()`: After successful action execution, serialize ActionEntry to JSON and insert into `driver_operation_history`
2. `ListDriverOperationsForPod()`: Query all operations for a pod, ordered by ID (insertion order)
3. `ReplayDriverOperationsForPod()`: 
   - Get driver from pod label
   - For each operation: resolve commands, execute, handle failures
   - Unsupported actions: deleted from DB (pruned)
   - Failed executions: deleted from DB (assume offline fix)
   - Successful: count toward replay total

**Pod Restart Recovery**:
- Pod crashes → Kubernetes restarts it (StatefulSet ensures recreation)
- Meshnet rebuilds network namespace (Topology CRD still present)
- Reconciliation detects missing interfaces if only partial
- Restarts pod again if needed
- Replays all previous operations: `ReplayDriverOperationsForPods()`

**Backend Restart Handling**:
- All operations persisted in SQLite
- After backend restarts, next modify/deploy operation will replay ops on affected pods
- Namespace state preserved (allows resume of interrupted operations)
- Operation locks released at end (or via cleanup endpoint)

---

## 5. VM-based Node Adaptation

**QEMU Definition**:
- Derived from the driver: a node is QEMU-based when its resolved driver implements `drivers_meta.RuntimeProvider` and returns `RuntimeQEMU` (e.g. `VyOSRouterDriver`). The topology JSON does NOT carry a `qemu` field.
- Pod labeled: `kubendt/qemu: "true"` and `kubendt/runtime: "qemu"`

**Detection & Shell Adaptation**:
- `InteractiveShellWebSocket()` checks pods labels
- `mode="serial"`: Uses k8s attach API (QEMU serial console fallback)
- `mode="sh"/"bash"`: Uses k8s exec API with shell
- `mode="auto"` (default): Uses attach for QEMU pods, exec for others

**Pod Configuration Differences**:

| Feature | Container Pod | QEMU Pod |
|---------|---------------|----------|
| Stdin | false | **true** |
| TTY | false | **true** |
| Privileged | false (unless router) | **true** |
| /dev/kvm mount | No | **auto-added** |
| Driver | Yes (default/explicit) | **No (empty) by default; explicit driver allowed (e.g. VyOSRouterDriver)** |
| Sysctls (ip_forward) | If router/switch | If router/switch |
| Boot time | ~2-5s | ~10-30s |

**Shell Execution Difference**:
- Container: exec API with bash/sh command
- QEMU: attach API for serial console input/output (full TTY)

**Driver Handling**:
- `ResolveDriversForNodes()` allows QEMU nodes to have an explicit driver. Without one, the node is left unmanaged (routing/bridging handled inside the guest VM).
- With `VyOSRouterDriver`, QEMU-based VyOS nodes receive full driver management: the same declarative actions (IP, routes, NAT, OSPF) are translated into VyOS CLI commands executed inside the container.

**Reconciliation**:
- QEMU pods still participate in interface validation
- Interfaces detected via `ip a` exec (works same as containers)
- Restart via pod deletion (no special handling)

---

## Key Design Patterns

### 1. Reflection & Generics
- Driver registry uses `reflect.Type` to store and instantiate types at runtime
- Generic `Register[T Driver](ctor func() T)` ensures type safety at registration

### 2. Type Assertions for Plugin Architecture
- `ResolveDriverCommands()` tries type assertions in order
- Driver implements subset of capabilities → right interface supported
- Fallback: nil if no capability matches action

### 3. Idempotent Shell Commands
- L3 operations use shell `||` operator to check state first
- Example: `grep -q || add` (check if exists before adding)
- Replay of operations is safe: same command = same state

### 4. Bounded Reconciliation
- Multi-round with exponential sleep (2s, 4s, ...)
- Deterministic restart selection (avoid restart loops)
- Error after maxRounds guarantees termination

### 5. Namespace Operation Lock
- SQLite PRIMARY KEY on namespace prevents concurrent operations
- Acquired at start, released at end
- Enables multi-tenant isolation

### 6. Annotation-Based Soft-Heal
- Pod/Topology annotations updated with timestamp (non-breaking)
- External controllers watch annotations and react
- No pod restart needed from backend (meshnet handles change)

---

## Failure Modes & Resilience

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Missing interface (round 1) | `ip a` check | Restart pod, retry |
| Missing interface (round 2+) | Same | Restart both endpoints, retry |
| Missing after maxRounds | Final check | Error logged, deploy marked failed |
| Operation unsupported by driver | Replay detects nil | Prune entry from history |
| Operation execution failure | Exec returns error | Prune entry (assume manual fix) |
| Pod restart (crash) | Reconciliation | Auto-restart + replay operations |
| Concurrent deploys | Lock conflict | Return 409 Conflict, reject second deploy |
| Missing ConfigMap file | Deploy phase | Warn, skip mount, continue |

---

## Extensibility

### Adding New Driver
1. Define struct with embedded capability bases: `type MyDriver struct { L2Base; L3Base; ... }`
2. Implement `Name()` → "MyDriver", `Type()` → "host"|"switch"|"router"
3. Override any base methods as needed
4. Register in `RegisterAllDrivers()`: `drivers_registry.Register(NewMyDriver)`

### Adding New Capability
1. Define interface: `type MyCapable interface { Method(...) [][]string }`
2. Create base with default implementation: `type MyBase struct { ... }`
3. Add capability methods to drivers' embedded structs
4. Add type assertion in `ResolveDriverCommands()`: `if mc, ok := driver.(MyCapable) { ... }`
5. Add action mapping in capability constants

### Adding New Action
1. Extend `types.ActionEntry` with new fields (e.g., `CustomParam string`)
2. Add action type to relevant capability methods
3. Update `ResolveDriverCommands()` switch statements
4. Clients send action with Type and appropriate fields populated

---

## Performance Characteristics

| Operation | Latency | Reason |
|-----------|---------|--------|
| Deploy (2 pods, 1 link) | ~45-60s | Reconciliation roundtrips |
| Modify add (1 node) | ~20-30s | Pod creation, link update, soft-heal |
| Modify delete (1 node) | ~10-15s | StatefulSet deletion, cleanup |
| Action execution (set IP) | 1-2s | Exec + shell command |
| Operation replay (5 ops) | 5-10s | 5 sequential execs |
| Reconciliation (per round) | ~20-30s | Interface checks, restarts, waits |

---

## Security Considerations

1. **Namespace Isolation**: Operations scoped by namespace (lock prevents cross-namespace interference)
2. **Pod Protection**: eth0 (K8s default network) protected from manipulation (SNAT/DNAT allowed only)
3. **RBAC**: Requires K8s API access (RBAC enforced by K8s itself)
4. **Privileged Pods**: Routers/switches/QEMU nodes marked privileged (explicit intent)
5. **No Secret Exposure**: ConfigMaps used for non-secret configs only (files < 1MB typical)

---

## Conclusion

KubeNDT achieves resilient network topology management through:
1. **Plugin architecture** enabling diverse driver implementations
2. **Progressive reconciliation** recovering from transient network issues
3. **Persistent operation history** enabling full state recovery after pod restarts
4. **Hybrid container/VM support** via configuration differences and label-based detection
5. **Non-destructive updates** via soft-heal mechanism (annotation nudges)

The implementation emphasizes **idempotency** (safe replay), **determinism** (same conditions → same outcomes), and **bounded termination** (no infinite loops or retry storms).

---

## Reference Documentation

- **Detailed Implementation**: See `IMPLEMENTATION_DETAILS.md`
- **Architecture Flows**: See `ARCHITECTURE_FLOWS.md`
- **Source Code**: `backend/handlers/`, `backend/helpers/`, `backend/drivers/`, `backend/capabilities/`

