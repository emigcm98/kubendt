package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"kubendt/helpers"
	"kubendt/kubeclient"
	"kubendt/types"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func writeOperationLockConflict(c *gin.Context, namespace string, lock *helpers.NamespaceOperationLock) {
	errorMessage := fmt.Sprintf("Namespace '%s' already has an operation in progress", namespace)
	payload := gin.H{"error": errorMessage}
	if lock != nil {
		payload["current_operation"] = lock.OperationType
		payload["started_at"] = lock.StartedAt
	}
	c.JSON(http.StatusConflict, payload)
}

type NetworkDeleteSpec struct {
	Nodes []string         `json:"nodes"`
	Links []types.LinkSpec `json:"links"`
}

type NetworkModifyRequest struct {
	Add    types.DeployRequest `json:"add"`
	Delete NetworkDeleteSpec   `json:"delete"`
	Scale  []types.ScaleSpec   `json:"scale"`
}

func ModifyNetwork(c *gin.Context) {
	startedAt := time.Now()
	var request NetworkModifyRequest
	namespace := c.Param("namespace")

	// Strict JSON decoding: reject unknown fields so typos surface immediately.
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		log.Printf("❌ Error en la solicitud JSON (network-modify): %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hasAdd := len(request.Add.Nodes) > 0 || len(request.Add.Links) > 0
	hasDelete := len(request.Delete.Nodes) > 0 || len(request.Delete.Links) > 0
	hasScale := len(request.Scale) > 0
	if !hasAdd && !hasDelete && !hasScale {
		c.JSON(http.StatusBadRequest, gin.H{"error": "network-modify requires 'add', 'delete' and/or 'scale' with content"})
		return
	}

	if err := helpers.ValidateNamespaceEnabled(namespace); err != nil {
		log.Printf("❌ Invalid namespace: %v", err)
		switch {
		case strings.Contains(err.Error(), "does not exist"):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case strings.Contains(err.Error(), "is not enabled for KubeNDT"):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	// Meshnet gate. Any topology change (add, delete or scale) creates or
	// restarts pods that the CNI has to wire, so without a running dataplane
	// they come up unwired or stuck in ContainerCreating. Only clearing a
	// topology and deleting the namespace are exempt (full teardown, no
	// rewiring). Only "missing" blocks; ?force=true overrides.
	if c.Query("force") != "true" {
		if m := detectMeshnet(c.Request.Context()); m.State == "missing" {
			c.JSON(http.StatusPreconditionFailed, gin.H{
				"error":  "Meshnet CNI not detected in the cluster. Topology changes would leave pods unwired. Install Meshnet, clear the topology, or retry with force=true.",
				"reason": "meshnet_missing",
			})
			return
		}
	}

	// Driver-aware interface-name validation runs BEFORE we grab the operation
	// lock or sync namespace state. A request whose payload is malformed
	// shouldn't pay the cost of lock contention, ConfigMap reads or any other
	// "we own this namespace now" bookkeeping, those calls can take seconds
	// when the API server or SQLite is under pressure. Fetching existingNodes
	// here is a single StatefulSet List (cheap) and gets re-read after we hold
	// the lock so race conditions don't matter.
	if hasAdd && len(request.Add.Links) > 0 {
		preValStart := time.Now()
		preExistingNodes, err := helpers.GetExistingNodes(namespace)
		if err != nil {
			log.Printf("❌ pre-validation: could not list existing nodes: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read existing nodes"})
			return
		}
		if len(request.Add.Nodes) > 0 {
			if err := helpers.ResolveDriversForNodes(request.Add.Nodes); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			if err := helpers.ValidateNodeInputs(namespace, request.Add.Nodes); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		if err := helpers.ValidateLinkIPs(request.Add.Links); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		validationNodes := make([]types.NodeSpec, 0, len(preExistingNodes)+len(request.Add.Nodes))
		validationNodes = append(validationNodes, preExistingNodes...)
		validationNodes = append(validationNodes, request.Add.Nodes...)
		// A link IP requires an L3-capable endpoint (checked against existing +
		// newly-added nodes), so an IP toward a switch is rejected here too.
		if err := helpers.ValidateLinkIPCapabilities(request.Add.Links, validationNodes); err != nil {
			log.Printf("ℹ️ modify pre-validation rejected request after %s: %v", time.Since(preValStart), err)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := helpers.ValidateLinkInterfaceNamesForDrivers(request.Add.Links, validationNodes); err != nil {
			log.Printf("ℹ️ modify pre-validation rejected request after %s: %v", time.Since(preValStart), err)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// External-label consistency: every external link in the new payload
		// must agree with the live state of the namespace on the
		// peerLabel ↔ peerIntf mapping. Catches relabel attempts and label
		// reuse across distinct ifaces before any state mutation happens.
		if err := helpers.ValidateExternalLabelConsistency(namespace, request.Add.Links); err != nil {
			log.Printf("ℹ️ modify pre-validation rejected request after %s: %v", time.Since(preValStart), err)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	lock, acquired, err := helpers.AcquireNamespaceOperationLock(namespace, "modify-network")
	if err != nil {
		log.Printf("❌ Error acquiring lock for namespace '%s': %v", namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not acquire operation lock"})
		return
	}
	if !acquired {
		writeOperationLockConflict(c, namespace, lock)
		return
	}
	defer func() {
		if err := helpers.ReleaseNamespaceOperationLock(namespace); err != nil {
			log.Printf("⚠️ Could not release lock for namespace '%s': %v", namespace, err)
		}
	}()

	hasTopology, err := helpers.SyncNamespaceTopologyState(namespace)
	if err != nil {
		log.Printf("❌ Error checking namespace topology state: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read topology state"})
		return
	}
	if !hasTopology {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No previous topology exists in the namespace"})
		return
	}

	existingNodes, err := helpers.GetExistingNodes(namespace)
	if err != nil {
		log.Printf("❌ Error fetching existing nodes: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read existing nodes"})
		return
	}
	if len(existingNodes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No previous topology exists in the namespace"})
		return
	}

	// Track peer-restart pressure split by source phase: delete/scale-down need
	// every touched peer restarted (CNI DEL/ADD does VXLAN cleanup), while
	// add/scale-up should only restart QEMU peers (non-QEMU pods pick up the
	// new veth through Meshnet's reconciler, restarting them would conflict
	// with the just-pushed peer-side veth).
	peersToRestartAlways := make(map[string]struct{})
	peersToRestartIfQemu := make(map[string]struct{})
	// Pods created during this modify (scale-up + add.nodes). The unified
	// wait phase blocks on these alongside the restarted peers, so all the
	// k8s pod operations happen in parallel.
	newPodsToWait := make(map[string]struct{})
	deletedNodes := []string{}
	scaledUpPods := []string{}
	scaledDownPods := []string{}
	// Non-fatal incidents surfaced back to the caller (e.g. mount files that
	// don't exist in the namespace file manager, leading to skipped ConfigMaps).
	// The operation still succeeds; the frontend renders these in the success
	// modal so the user knows something they declared was not applied.
	warnings := []types.Warning{}

	// Validate scale cross-section BEFORE touching anything, so user input
	// errors (overlap with add/delete, unknown nodes, etc.) surface fast.
	addNodeNames := make(map[string]struct{}, len(request.Add.Nodes))
	for _, n := range request.Add.Nodes {
		addNodeNames[n.Name] = struct{}{}
	}
	deleteNodeNames := make(map[string]struct{}, len(request.Delete.Nodes))
	for _, n := range request.Delete.Nodes {
		deleteNodeNames[n] = struct{}{}
	}
	scalePlans, err := helpers.BuildScalePlans(request.Scale, existingNodes, addNodeNames, deleteNodeNames)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.HasPrefix(err.Error(), "VALIDATION:") {
			status = http.StatusBadRequest
			err = fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "VALIDATION:"))
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	scaleDowns, scaleUps := helpers.SplitScalePlans(scalePlans)

	// Phase 1, Delete (CRD-only; restarts deferred to the unified phase).
	if hasDelete {
		touchedPeers, deleted, err := helpers.ApplyDeleteOnExistingTopology(namespace, request.Delete.Nodes, request.Delete.Links, existingNodes)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.HasPrefix(err.Error(), "VALIDATION:") {
				status = http.StatusBadRequest
				err = fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "VALIDATION:"))
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		for _, p := range touchedPeers {
			peersToRestartAlways[p] = struct{}{}
		}
		deletedNodes = deleted

		existingNodes, err = helpers.GetExistingNodes(namespace)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not re-read nodes after delete"})
			return
		}
	}

	// Phase 2a, Scale-down (CRD/sts-only; restarts deferred).
	if len(scaleDowns) > 0 {
		touched, orphans, err := helpers.ApplyScaleDowns(namespace, scaleDowns)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, p := range touched {
			peersToRestartAlways[p] = struct{}{}
		}
		scaledDownPods = orphans

		existingNodes, err = helpers.GetExistingNodes(namespace)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not re-read nodes after scale-down"})
			return
		}
	}

	// Phase 2b, Scale-up. Pre-prepare add-link UIDs so the Topology CRDs
	// written here and the peer-side upsert in ApplyAdd share the same UID
	// before Meshnet CNI ADD fires.
	preparedAddLinks := request.Add.Links
	if len(scaleUps) > 0 {
		// Effective topology view = existing nodes (with scaled replicas) + add.nodes.
		afterScale := helpers.ApplyEffectiveReplicasToNodes(existingNodes, scaleUps)
		allNodesAfter := make([]types.NodeSpec, 0, len(afterScale)+len(request.Add.Nodes))
		allNodesAfter = append(allNodesAfter, afterScale...)
		allNodesAfter = append(allNodesAfter, request.Add.Nodes...)

		if len(request.Add.Links) > 0 {
			prepared, err := helpers.PrepareUniqueLinkUIDs(namespace, request.Add.Links)
			if err != nil {
				status := http.StatusInternalServerError
				if strings.HasPrefix(err.Error(), "VALIDATION:") {
					status = http.StatusBadRequest
					err = fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "VALIDATION:"))
				}
				c.JSON(status, gin.H{"error": err.Error()})
				return
			}
			preparedAddLinks = prepared
			request.Add.Links = prepared // ApplyAdd will see UIDs already set → idempotent
		}

		newPods, touchedPeers, consumedUIDs, err := helpers.ApplyScaleUps(namespace, scaleUps, preparedAddLinks, allNodesAfter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		scaledUpPods = newPods
		for _, p := range newPods {
			newPodsToWait[p] = struct{}{}
		}
		for _, p := range touchedPeers {
			peersToRestartIfQemu[p] = struct{}{}
		}

		// Drop links scale-up already wrote, otherwise the add-phase
		// validator sees the interface as already in use and rejects.
		if len(consumedUIDs) > 0 {
			filtered := make([]types.LinkSpec, 0, len(request.Add.Links))
			for _, link := range request.Add.Links {
				if link.UID != nil {
					if _, consumed := consumedUIDs[*link.UID]; consumed {
						continue
					}
				}
				filtered = append(filtered, link)
			}
			request.Add.Links = filtered
		}

		existingNodes, err = helpers.GetExistingNodes(namespace)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not re-read nodes after scale-up"})
			return
		}
	}

	if hasAdd {
		newAddPods, touchedPeers, addWarnings, err := helpers.ApplyAddToExistingTopology(namespace, request.Add, existingNodes)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.HasPrefix(err.Error(), "VALIDATION:") {
				status = http.StatusBadRequest
				err = fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "VALIDATION:"))
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		warnings = append(warnings, addWarnings...)
		for _, p := range newAddPods {
			newPodsToWait[p] = struct{}{}
		}
		for _, p := range touchedPeers {
			peersToRestartIfQemu[p] = struct{}{}
		}
	}

	// === Unified restart + wait + replay phase ===========================
	//
	// Every Apply* function above is CRD/sts-only now: it publishes the
	// topology changes and patches StatefulSets but does NOT trigger any
	// RestartPod or wait for Ready. We collect everything here so:
	//   - QEMU peers touched by several phases (e.g. scale-up adding a link
	//     to peer X, then add adding another link to X) are restarted ONCE,
	//     not once per phase.
	//   - Newly-created pods and restarted peers are waited on in parallel
	//     in a single WaitForPodsReadyByName call, no serial pile-up.
	// ─────────────────────────────────────────────────────────────────────

	finalNodes, err := helpers.GetExistingNodes(namespace)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not re-read nodes before final restart phase"})
		return
	}
	isQemuByBase := make(map[string]bool, len(finalNodes))
	for _, n := range finalNodes {
		isQemuByBase[n.Name] = n.Qemu
	}
	isQemu := func(podName string) bool {
		base, _, ok := helpers.SplitIndexedPodName(podName)
		if !ok {
			base = podName
		}
		return isQemuByBase[base]
	}

	// Build the effective restart set:
	//   - peersToRestartAlways → restart regardless of QEMU/non-QEMU.
	//   - peersToRestartIfQemu → restart only if QEMU.
	// "Always" wins on conflict.
	restartSet := make(map[string]struct{})
	for p := range peersToRestartAlways {
		restartSet[p] = struct{}{}
	}
	for p := range peersToRestartIfQemu {
		if _, already := restartSet[p]; already {
			continue
		}
		if isQemu(p) {
			restartSet[p] = struct{}{}
		}
	}
	// Don't restart pods that we just created (they're already coming up
	// fresh) or pods that were just deleted/scaled-down.
	for p := range newPodsToWait {
		delete(restartSet, p)
	}
	for _, p := range scaledDownPods {
		delete(restartSet, p)
	}

	restartedPods := make([]string, 0, len(restartSet))
	for p := range restartSet {
		restartedPods = append(restartedPods, p)
	}
	sort.Strings(restartedPods)

	// Publish the updated per-pod expected-iface-count ConfigMap BEFORE we
	// start tearing down pods. The new pods spun up by the StatefulSet
	// controller (and the recreated ones from RestartPod) will mount the
	// latest values when their sandboxes are created, so each QEMU
	// entrypoint waits for exactly the number of dataplane interfaces it
	// is supposed to see post-modify.
	if err := helpers.UpdateIfaceCountsConfigMap(namespace, finalNodes); err != nil {
		log.Printf("⚠️ Could not update iface counts ConfigMap: %v (entrypoints will fall back to fixed sleep)", err)
	}

	// Trigger restarts (RestartPod just disposes of the pod and returns;
	// the StatefulSet controller recreates it asynchronously).
	for _, pod := range restartedPods {
		if err := helpers.RestartPod(namespace, pod); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("could not restart pod %s: %v", pod, err)})
			return
		}
	}

	// One global wait for everything that was created or restarted in this
	// modify call: scale-up pods + add.nodes pods + restarted peers, all
	// in parallel.
	waitSet := make(map[string]struct{}, len(newPodsToWait)+len(restartSet))
	for p := range newPodsToWait {
		waitSet[p] = struct{}{}
	}
	for p := range restartSet {
		waitSet[p] = struct{}{}
	}
	if len(waitSet) > 0 {
		waitList := make([]string, 0, len(waitSet))
		for p := range waitSet {
			waitList = append(waitList, p)
		}
		sort.Strings(waitList)
		if err := helpers.WaitForPodsReadyByName(namespace, waitList); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	// Replay persisted driver operations only on the peers we actually
	// restarted, new pods have no history yet.
	if len(restartedPods) > 0 {
		if err := helpers.ReplayDriverOperationsForPods(namespace, restartedPods); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed replaying persisted operations after restart: %v", err)})
			return
		}
	}

	// === Post-restart QEMU TC rewire ===================================
	//
	// Safety net for the rare case where meshnet has to recreate a peer's
	// veth/vxlan device with a new ifindex (e.g. a pod's worker node
	// changed → intra-node link became cross-node, file-exists race in
	// meshnet, etc.). In that case the QEMU peer's pre-existing TC rules
	// still point at the dead ifindex and the guest is silently cut off
	// in one direction, exactly the bug we chased for hours.
	//
	// We refresh TC on every QEMU pod that is a *direct peer* of a pod we
	// just restarted, and that itself was NOT in our restart set (those
	// already ran their entrypoint fresh and have correct TC). The script
	// `/usr/local/bin/kubendt-rewire-tc` is generated by the QEMU image's
	// entrypoint at boot, knows the eth↔tap mapping, and is idempotent,
	// so calling it when nothing's wrong costs nothing.
	if len(restartedPods) > 0 {
		if err := helpers.RewireQemuPeersAfterRestart(namespace, restartedPods, finalNodes); err != nil {
			log.Printf("⚠️ Post-restart QEMU rewire: %v (continuing)", err)
		}
	}

	// Fast post-modify soft-heal: nudge Pod + Topology updates for impacted pods
	// and their direct peers, avoiding expensive global reconcile rounds.
	nudgedSet := make(map[string]struct{})
	for _, pod := range restartedPods {
		nudgedSet[pod] = struct{}{}
	}

	if len(restartedPods) > 0 {
		allLinks, err := helpers.BuildLinksFromTopologyCRDs(namespace)
		if err != nil {
			log.Printf("⚠️ Modify soft-heal: could not build links from Topology CRDs: %v", err)
		} else {
			scoped := helpers.FilterLinksByPods(allLinks, restartedPods)
			for _, l := range scoped {
				if l.Node != "" && l.Node != "external" {
					nudgedSet[l.Node] = struct{}{}
				}
				if l.PeerNode != "" && l.PeerNode != "external" {
					nudgedSet[l.PeerNode] = struct{}{}
				}
			}
		}
	}

	if len(nudgedSet) > 0 {
		nudgedPods := make([]string, 0, len(nudgedSet))
		for pod := range nudgedSet {
			nudgedPods = append(nudgedPods, pod)
		}
		sort.Strings(nudgedPods)

		for _, pod := range nudgedPods {
			if err := helpers.NudgePodReconcile(namespace, pod); err != nil {
				log.Printf("⚠️ Modify soft-heal: could not nudge Pod '%s': %v", pod, err)
			} else {
				log.Printf("ℹ️ Modify soft-heal: nudged Pod '%s'", pod)
			}

			if err := helpers.NudgeTopologyReconcile(namespace, pod); err != nil {
				log.Printf("⚠️ Modify soft-heal: could not nudge Topology '%s': %v", pod, err)
			} else {
				log.Printf("ℹ️ Modify soft-heal: nudged Topology '%s'", pod)
			}
		}
	}

	modifyOpsDur := time.Since(startedAt)
	reconcileAt := time.Now()

	// Post-modify reconcile: for link-only adds, Topology CRD updates and annotation
	// nudges don't trigger CNI ADD. Restart one endpoint per new link so veth pairs
	// are created via CNI ADD.
	if hasAdd && len(request.Add.Links) > 0 {
		currentNodes, nodesErr := helpers.GetExistingNodes(namespace)
		if nodesErr != nil {
			log.Printf("⚠️ Modify reconcile: could not get current nodes: %v", nodesErr)
		} else {
			log.Printf("🔁 Modify: running post-add interface reconciliation for %d new link(s)...", len(request.Add.Links))
			time.Sleep(2 * time.Second) // let pods/topology CRD updates settle before checking interfaces
			if reconcileErr := helpers.ReconcileMissingInterfaces(namespace, currentNodes, request.Add.Links, 2); reconcileErr != nil {
				log.Printf("⚠️ Modify reconcile: %v", reconcileErr)
			}
		}
	}

	reconciliationDur := time.Since(reconcileAt)

	if _, err := helpers.SyncNamespaceTopologyState(namespace); err != nil {
		log.Printf("⚠️ Could not refresh topology state after modify in namespace '%s': %v", namespace, err)
	}
	if err := helpers.SyncNamespaceLinkUIDRegistry(namespace); err != nil {
		log.Printf("⚠️ Could not refresh link UID registry after modify in namespace '%s': %v", namespace, err)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "network-modify applied successfully",
		"took_time": gin.H{
			"total":             fmt.Sprintf("%.2fs", time.Since(startedAt).Seconds()),
			"modify_operations": fmt.Sprintf("%.2fs", modifyOpsDur.Seconds()),
			"reconciliation":    fmt.Sprintf("%.2fs", reconciliationDur.Seconds()),
		},
		"deleted_nodes":    deletedNodes,
		"restarted_pods":   restartedPods,
		"scaled_up_pods":   scaledUpPods,
		"scaled_down_pods": scaledDownPods,
		"add_nodes_count":  len(request.Add.Nodes),
		"add_links_count":  len(request.Add.Links),
		"del_nodes_count":  len(request.Delete.Nodes),
		"del_links_count":  len(request.Delete.Links),
		"scale_count":      len(request.Scale),
		"warnings":         warnings,
	})
}

func DeployNetwork(c *gin.Context) {
	startedAt := time.Now()
	var request types.DeployRequest
	namespace := c.Param("namespace")

	// 1. Parse json and detect nodes and links (strict: reject unknown fields)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		log.Printf("❌ Error en la solicitud JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(request.Nodes) < 1 {
		log.Printf("❌ Invalid JSON: 'nodes' must have at least 1 entry.")
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "'nodes' must have at least 1 entry.",
		})
		return
	}

	// 2. Validate namespace
	if err := helpers.ValidateNamespaceEnabled(namespace); err != nil {
		log.Printf("❌ Invalid namespace: %v", err)
		switch {
		case strings.Contains(err.Error(), "does not exist"):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case strings.Contains(err.Error(), "is not enabled for KubeNDT"):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	// Meshnet gate: refuse to deploy when the CNI dataplane is missing, since
	// the Topology CRD would be created but its links never wired (a silent
	// failure). Only "missing" blocks. "degraded" (a pod restarting) and
	// "unknown" (no permission to check) are allowed through. Callers who know
	// better, or hit a false positive in detection, can override with
	// ?force=true. This runs for API clients too, not just the UI.
	if c.Query("force") != "true" {
		if m := detectMeshnet(c.Request.Context()); m.State == "missing" {
			c.JSON(http.StatusPreconditionFailed, gin.H{
				"error":  "Meshnet CNI not detected in the cluster. Topology links would not be wired. Install Meshnet, or retry with force=true to deploy anyway.",
				"reason": "meshnet_missing",
			})
			return
		}
	}

	lock, acquired, err := helpers.AcquireNamespaceOperationLock(namespace, "deploy-network")
	if err != nil {
		log.Printf("❌ Error acquiring lock for namespace '%s': %v", namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not acquire operation lock"})
		return
	}
	if !acquired {
		writeOperationLockConflict(c, namespace, lock)
		return
	}
	defer func() {
		if err := helpers.ReleaseNamespaceOperationLock(namespace); err != nil {
			log.Printf("⚠️ Could not release lock for namespace '%s': %v", namespace, err)
		}
	}()

	hasTopology, err := helpers.SyncNamespaceTopologyState(namespace)
	if err != nil {
		log.Printf("❌ Error checking namespace topology state: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read topology state"})
		return
	}
	if hasTopology {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("Namespace '%s' already has a deployed topology", namespace)})
		return
	}

	// 3. Validate node basics: non-empty name, no duplicates within the
	// payload, valid type. Without the duplicate check, two nodes sharing
	// a name would both try to create the same Topology CRD / StatefulSet
	// and fail later with a confusing K8s "AlreadyExists" 500.
	seenNodeNames := make(map[string]struct{}, len(request.Nodes))
	for i, node := range request.Nodes {
		if strings.TrimSpace(node.Name) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("nodes[%d]: name is required", i)})
			return
		}
		if _, dup := seenNodeNames[node.Name]; dup {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("nodes[%d]: duplicate node name '%s' in payload", i, node.Name)})
			return
		}
		seenNodeNames[node.Name] = struct{}{}

		typ := node.Type
		if typ != "host" && typ != "switch" && typ != "router" {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Node '%s' has invalid type '%s' (expected: host|switch|router).", node.Name, node.Type)})
			return
		}
	}

	// 4a. Validate semantic constraints in links (the per-name validation is
	// done after driver resolution so driver-specific rules can layer in).
	for i, link := range request.Links {
		// Both endpoints cannot be "external", at least one must be a real pod
		if link.Node == "external" && link.PeerNode == "external" {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("link[%d]: both 'node' and 'peerNode' cannot be 'external'; at least one must be a real pod", i)})
			return
		}
		// Validate optional link name
		if err := helpers.ValidateLinkName(link.Name, i); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	// 4. Validate that all drivers specified in nodes are known and can be used
	if err := helpers.ResolveDriversForNodes(request.Nodes); err != nil {
		log.Printf("❌ Driver resolution: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 4b. Validate interface names per link against the driver constraints of
	// each endpoint (Linux kernel rules apply for all; VyOS adds ^eth\d+$ etc.).
	if err := helpers.ValidateLinkInterfaceNamesForDrivers(request.Links, request.Nodes); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 4c. Validate that peerLabel ↔ peerIntf is a 1:1 mapping for any link
	// touching "external". Without this, two pods on the same host iface
	// could be labelled differently (visually two networks, technically one
	// broadcast domain) or two host ifaces could share a label (visually
	// one network, technically two). The rule is checked against the live
	// namespace state plus the incoming payload together.
	if err := helpers.ValidateExternalLabelConsistency(namespace, request.Links); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 4d. Validate that no two links in the request claim the same pod
	// interface. Without this, an accidentally duplicated link entry
	// silently slips through (the CRD's spec.links is a flat list and the
	// second write either dedupes by upsert key or piles up an unusable
	// duplicate); either way the user is not told the payload was wrong.
	if err := helpers.ValidateInterfaceConflicts(namespace, request.Links, request.Nodes); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 4e. Validate static node inputs (image present, env var names, mount
	// targets + file existence, device paths) and link L3 addresses before
	// creating any Kubernetes object.
	if err := helpers.ValidateNodeInputs(namespace, request.Nodes); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := helpers.ValidateLinkIPs(request.Links); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// A link IP requires an L3-capable endpoint; reject e.g. an IP on a switch.
	if err := helpers.ValidateLinkIPCapabilities(request.Links, request.Nodes); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 5. Normalize the Replicas field
	for i, node := range request.Nodes {
		switch {
		case node.Replicas == 0:
			log.Printf("ℹ️ Node '%s': 'replicas' not specified, defaulting to 1.", node.Name)
			request.Nodes[i].Replicas = 1
		case node.Replicas < 0:
			log.Printf("❌ Node '%s': invalid replicas count (%d).", node.Name, node.Replicas)
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Node '%s' has invalid replicas count (%d). Must be >= 1.", node.Name, node.Replicas),
			})
			return
		case node.Replicas > types.MaxReplicas:
			log.Printf("⚠️ Node '%s': replicas %d exceed maximum (%d). Forcing to %d.", node.Name, node.Replicas, types.MaxReplicas, types.MaxReplicas)
			request.Nodes[i].Replicas = types.MaxReplicas
		}
	}

	// 5b. Ensure each link has a stable UID for this whole deploy request.
	// This avoids generating different UIDs per endpoint when creating per-pod Topology CRDs.
	request.Links = helpers.SanitizeLinksForQemuNodes(request.Links, request.Nodes)
	request.Links, err = helpers.PrepareUniqueLinkUIDs(namespace, request.Links)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.HasPrefix(err.Error(), "VALIDATION:") {
			status = http.StatusBadRequest
			err = fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "VALIDATION:"))
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	resourcesAt := time.Now()

	// 6. Create topology for each real pod, with or without links
	for _, node := range request.Nodes {
		if err := helpers.CreateTopologyObject(namespace, node, request.Links, request.Nodes); err != nil {
			log.Printf("❌ Topology: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	// 7. Create mounts
	// Pre-pass: any mount declaring sensitive=true upgrades the file flag in
	// namespace_file_meta. JSON can only mark sensitive, never unmark (see
	// MountSpec.Sensitive).
	for _, node := range request.Nodes {
		for _, mount := range node.Mounts {
			if mount.Sensitive {
				if err := helpers.SetFileSensitive(namespace, mount.File, true); err != nil {
					log.Printf("⚠️ Could not mark file %q as sensitive: %v", mount.File, err)
				}
			}
		}
	}

	validMountsMap := make(map[string][]types.MountSpec)
	warnings := make([]types.Warning, 0)
	for _, node := range request.Nodes {
		for _, mount := range node.Mounts {
			_, _, err := helpers.CreateMountResourceForFile(namespace, mount.File)
			if err != nil {
				if strings.Contains(err.Error(), "error reading file") {
					log.Printf("⚠️ File %s does not exist. Skipping ConfigMap creation: %v", mount.File, err)
					warnings = append(warnings, types.Warning{
						Node:   node.Name,
						Kind:   "mount_file_missing",
						File:   mount.File,
						Detail: fmt.Sprintf("File %q not found in namespace file manager. Mount skipped, the pod will start without it.", mount.File),
					})
					continue
				} else {
					log.Printf("❌ Error creating ConfigMap for %s: %v", mount.File, err)
					c.JSON(http.StatusInternalServerError, gin.H{
						"error": fmt.Sprintf("Error creating ConfigMap for file %s on node %s", mount.File, node.Name),
					})
					return
				}

			} else {
				validMountsMap[node.Name] = append(validMountsMap[node.Name], mount)
			}
		}
	}

	// 7b. Publish the per-pod expected dataplane interface count ConfigMap
	// BEFORE creating the StatefulSets so that kubelet, when it mounts the
	// volume for new pods, already sees the correct counts. QEMU entrypoint
	// reads its own pod's count and waits until that many interfaces are
	// present before snapshotting and launching QEMU, closing the race
	// where deletePeerVethsForPod could otherwise destroy a peer-side veth
	// during the entrypoint's settle window.
	if err := helpers.UpdateIfaceCountsConfigMap(namespace, request.Nodes); err != nil {
		log.Printf("⚠️ Could not update iface counts ConfigMap: %v (entrypoints will fall back to fixed sleep)", err)
	}

	// 8. Create StatefulSet for each node
	for _, node := range request.Nodes {
		mounts := validMountsMap[node.Name]
		err := helpers.CreateNetworkStatefulSet(namespace, node, mounts)
		if err != nil {
			log.Printf("❌ Error creating StatefulSet %s: %v", node.Name, err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("Error creating StatefulSet %s", node.Name),
			})
			return
		}
	}

	resourceCreationDur := time.Since(resourcesAt)
	nodeWaitAt := time.Now()

	// 9. Wait until all Pods are ready. The error is already user-facing and
	// specific (e.g. an image-pull failure names the pod, image and reason), so
	// surface it rather than a generic timeout message.
	if err := helpers.WaitForPodsReady(namespace, request.Nodes); err != nil {
		log.Printf("❌ Pods did not become ready, rolling back created resources: %v", err)
		// A fresh deploy that can't bring all pods up is unusable, so roll back
		// what was created and leave the namespace clean (positions and files
		// are kept so the user can fix the input and re-import).
		helpers.RollbackNamespaceTopology(namespace)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":      err.Error(),
			"rolledBack": true,
		})
		return
	}
	log.Println("✅ All nodes are in Running state.")

	nodeRunningDur := time.Since(nodeWaitAt)
	reconcileAt := time.Now()

	// Small delay to allow pods to fully stabilize after K8s reports Ready
	log.Println("⏳ Waiting 5s for pods to stabilize before interface validation...")
	time.Sleep(5 * time.Second)
	// 10. Reconcile missing interfaces (auto-healing con resetTopologyStatusForRestart)
	if err := helpers.ReconcileMissingInterfaces(namespace, request.Nodes, request.Links, 2); err != nil {
		log.Printf("⚠️ Reconcile ended with remaining issues: %v", err)
		// Decide: return 500 or just warn. I would warn, not fail deploy.
	}

	reconciliationDur := time.Since(reconcileAt)

	if err := helpers.SetNamespaceHasTopology(namespace, true); err != nil {
		log.Printf("⚠️ Could not persist topology state for namespace '%s': %v", namespace, err)
	}
	if err := helpers.SyncNamespaceLinkUIDRegistry(namespace); err != nil {
		log.Printf("⚠️ Could not refresh link UID registry after deploy in namespace '%s': %v", namespace, err)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Network infrastructure deployed successfully",
		"took_time": gin.H{
			"total":             fmt.Sprintf("%.2fs", time.Since(startedAt).Seconds()),
			"resource_creation": fmt.Sprintf("%.2fs", resourceCreationDur.Seconds()),
			"node_running":      fmt.Sprintf("%.2fs", nodeRunningDur.Seconds()),
			"reconciliation":    fmt.Sprintf("%.2fs", reconciliationDur.Seconds()),
		},
		"warnings": warnings,
	})
}

func ClearTopology(c *gin.Context) {
	startedAt := time.Now()
	namespace := c.Param("namespace")

	if err := helpers.ValidateNamespaceEnabled(namespace); err != nil {
		log.Printf("❌ Invalid namespace: %v", err)
		switch {
		case strings.Contains(err.Error(), "does not exist"):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case strings.Contains(err.Error(), "is not enabled for KubeNDT"):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	lock, acquired, err := helpers.AcquireNamespaceOperationLock(namespace, "clear-topology")
	if err != nil {
		log.Printf("❌ Error acquiring lock for namespace '%s': %v", namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not acquire operation lock"})
		return
	}
	if !acquired {
		writeOperationLockConflict(c, namespace, lock)
		return
	}
	defer func() {
		if err := helpers.ReleaseNamespaceOperationLock(namespace); err != nil {
			log.Printf("⚠️ Could not release lock for namespace '%s': %v", namespace, err)
		}
	}()

	deletePositions := c.Query("deletePositions") == "true"
	deleteFiles := c.Query("deleteFiles") == "true"

	if err := helpers.ClearNamespaceTopologyResources(namespace, deletePositions, deleteFiles); err != nil {
		log.Printf("❌ Error clearing topology in namespace '%s': %v", namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not clear topology resources"})
		return
	}

	if _, err := helpers.SyncNamespaceTopologyState(namespace); err != nil {
		log.Printf("⚠️ Could not refresh topology state after clear in namespace '%s': %v", namespace, err)
	}
	if err := helpers.DeleteNamespaceLinkUIDRegistry(namespace); err != nil {
		log.Printf("⚠️ Could not cleanup link UID registry after clear in namespace '%s': %v", namespace, err)
	}
	if err := helpers.DeleteNamespaceDriverOperationHistory(namespace); err != nil {
		log.Printf("⚠️ Could not cleanup driver operation history after clear in namespace '%s': %v", namespace, err)
	}
	if err := helpers.DeleteIfaceCountsConfigMap(namespace); err != nil {
		log.Printf("⚠️ Could not delete iface counts ConfigMap after clear in namespace '%s': %v", namespace, err)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Topology resources cleared successfully",
		"took_time": gin.H{
			"total": fmt.Sprintf("%.2fs", time.Since(startedAt).Seconds()),
		},
	})
}

// resolveMountPath recovers the original file-manager path of a mounted file
// and whether it still exists. It prefers the path recorded on the backing
// ConfigMap/Secret annotation (correct even when the source file was deleted),
// and falls back to matching the sanitized data key against the file manager
// for mounts deployed before the annotation existed.
func resolveMountPath(namespace, resourceName string, isSecret bool, dataKey string) (string, bool) {
	if path := helpers.OriginalMountFilePath(namespace, resourceName, isSecret); path != "" {
		exists, _ := helpers.NamespaceFileExists(namespace, path)
		return path, exists
	}
	return helpers.ResolveMountedFileName(namespace, dataKey)
}

func GetNetwork(c *gin.Context) {
	namespace := c.Param("namespace")

	// Validate namespace
	if err := helpers.ValidateNamespaceEnabled(namespace); err != nil {
		log.Printf("❌ Invalid namespace: %v", err)
		switch {
		case strings.Contains(err.Error(), "does not exist"):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case strings.Contains(err.Error(), "is not enabled for KubeNDT"):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	// Get StatefulSets
	ctx := c.Request.Context()
	stsList, err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("❌ Error fetching StatefulSets in namespace %s: %v", namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not fetch StatefulSets"})
		return
	}

	// Get Pods to extract 'kubendt/type'
	podList, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("❌ Error fetching Pods in namespace %s: %v", namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not fetch Pods"})
		return
	}

	// Create PodName -> Labels map
	podLabels := make(map[string]map[string]string)
	for _, pod := range podList.Items {
		podLabels[pod.Name] = pod.Labels
	}

	var nodes []types.NodeSpec
	replicaCount := make(map[string]int)

	for _, sts := range stsList.Items {
		name := sts.Spec.Template.Labels["app"]
		if name == "" {
			name = sts.Name // fallback
		}

		replicas := 1
		if sts.Spec.Replicas != nil {
			replicas = int(*sts.Spec.Replicas)
		}
		replicaCount[name] = replicas

		// Search for a corresponding pod to extract runtime/type/driver labels
		var podType, driver, runtime, shellMode string
		for _, labels := range podLabels {
			if labels["app"] == name {
				podType = labels["kubendt/type"]
				driver = labels["kubendt/driver"]
				runtime = labels["kubendt/runtime"]
				shellMode = labels["kubendt/shell-mode"]
				break
			}
		}

		isQemu := runtime == "qemu"

		container := sts.Spec.Template.Spec.Containers[0]

		// Process Env vars
		envVars := make(map[string]string)
		for _, envFrom := range container.EnvFrom {
			if envFrom.ConfigMapRef != nil {
				cmName := envFrom.ConfigMapRef.Name
				cm, err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Get(ctx, cmName, metav1.GetOptions{})
				if err != nil {
					log.Printf("⚠️ Could not retrieve ConfigMap %s: %v", cmName, err)
					continue
				}
				for k, v := range cm.Data {
					envVars[k] = v
				}
			}
		}

		// Process Mounts (both ConfigMap- and Secret-backed). The Secret
		// case carries an extra Sensitive=true hint so the UI can show the
		// lock icon without having to cross-reference the file manager.
		var mounts []types.MountSpec
		for _, volumeMount := range container.VolumeMounts {
			for _, volume := range sts.Spec.Template.Spec.Volumes {
				if volume.Name != volumeMount.Name {
					continue
				}
				if volume.ConfigMap != nil {
					// Skip kubendt-internal ConfigMaps (e.g. the iface-counts
					// state): they are not user file-manager files and must not
					// surface in the pod's Mounted Files list.
					if volume.ConfigMap.Name == helpers.IfaceCountsConfigMapName {
						break
					}
					dataKey := volumeMount.SubPath
					if dataKey == "" {
						dataKey = volume.ConfigMap.Name
					}
					fileName, exists := resolveMountPath(namespace, volume.ConfigMap.Name, false, dataKey)
					mounts = append(mounts, types.MountSpec{
						File:    fileName,
						MountTo: volumeMount.MountPath,
						Missing: !exists,
					})
					break
				}
				if volume.Secret != nil {
					dataKey := volumeMount.SubPath
					if dataKey == "" {
						dataKey = volume.Secret.SecretName
					}
					fileName, exists := resolveMountPath(namespace, volume.Secret.SecretName, true, dataKey)
					mounts = append(mounts, types.MountSpec{
						File:      fileName,
						MountTo:   volumeMount.MountPath,
						Sensitive: true,
						Missing:   !exists,
					})
					break
				}
			}
		}

		privileged := false
		if container.SecurityContext != nil && container.SecurityContext.Privileged != nil {
			privileged = *container.SecurityContext.Privileged
		}

		nodes = append(nodes, types.NodeSpec{
			Name:       name,
			Image:      container.Image,
			Type:       podType,
			ShellMode:  shellMode,
			Qemu:       isQemu,
			Privileged: privileged,
			Commands:   container.Command,
			Env:        envVars,
			Mounts:     mounts,
			Replicas:   replicas,
			Driver:     driver,
		})
	}

	// Get topologies
	gvr := schema.GroupVersionResource{
		Group:    "networkop.co.uk",
		Version:  "v1beta1",
		Resource: "topologies",
	}
	topologyList, err := kubeclient.DynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("❌ Error fetching topologies: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not retrieve topology"})
		return
	}

	// Process links
	seenLinks := make(map[string]bool)
	var links []types.LinkSpec

	for _, topology := range topologyList.Items {
		spec, found, _ := unstructured.NestedSlice(topology.Object, "spec", "links")
		if !found {
			continue
		}

		// Read annotations for peerLabel and per-link names
		annotations := topology.GetAnnotations()
		peerLabel := annotations["kubendt/peerlabel"]

		// Parse optional per-link names: {"uid": "name", ...}
		linkNamesMap := make(map[string]string)
		if rawNames, ok := annotations["kubendt/linknames"]; ok && rawNames != "" {
			if err := json.Unmarshal([]byte(rawNames), &linkNamesMap); err != nil {
				log.Printf("⚠️ get-network: could not parse kubendt/linknames annotation on %s: %v", topology.GetName(), err)
			}
		}

		for _, linkData := range spec {
			linkMap := linkData.(map[string]interface{})
			pod := topology.GetName()
			// Translate CRD peer_pod ("localhost") → API layer ("external") at read time
			peerPod := helpers.FromCRDPeerPod(linkMap["peer_pod"].(string))

			pod = helpers.ConvertToNodeNameIfSingleReplica(pod, replicaCount)
			if peerPod != "external" {
				peerPod = helpers.ConvertToNodeNameIfSingleReplica(peerPod, replicaCount)
			}

			linkKey := fmt.Sprintf("%s-%s", pod, peerPod)
			reverseKey := fmt.Sprintf("%s-%s", peerPod, pod)
			if seenLinks[linkKey] || seenLinks[reverseKey] {
				continue
			}
			seenLinks[linkKey] = true

			uidValue := int(linkMap["uid"].(int64))
			link := types.LinkSpec{
				Node:      pod,
				LocalIntf: linkMap["local_intf"].(string),
				PeerNode:  peerPod,
				PeerIntf:  linkMap["peer_intf"].(string),
				UID:       &uidValue,
			}
			if localIP, ok := linkMap["local_ip"].(string); ok {
				link.LocalIP = localIP
			}
			if peerIP, ok := linkMap["peer_ip"].(string); ok {
				link.PeerIP = peerIP
			}

			if peerLabel != "" && link.PeerNode == "external" {
				link.PeerLabel = peerLabel
			}

			// Restore optional link name from annotation
			if name, ok := linkNamesMap[fmt.Sprintf("%d", uidValue)]; ok && name != "" {
				link.Name = name
			}

			links = append(links, link)
		}
	}

	// Return JSON
	response := types.DeployRequest{
		Nodes: nodes,
		Links: links,
	}

	responseJSON, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		log.Printf("❌ Error serializing JSON: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not generate JSON"})
		return
	}

	c.Data(http.StatusOK, "application/json", responseJSON)
}
