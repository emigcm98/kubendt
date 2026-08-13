package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"kubendt/helpers"
	"kubendt/kubeclient"
	"kubendt/types"

	"github.com/gin-gonic/gin"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ListPods(c *gin.Context) {
	namespace := c.Param("namespace")

	pods, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(c.Request.Context(), metav1.ListOptions{})
	if err != nil {
		log.Printf("Error fetching pods: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// First, count replicas by baseName
	replicaCountMap := make(map[string]int)
	for _, pod := range pods.Items {
		parts := strings.Split(pod.Name, "-")
		if len(parts) >= 2 {
			base := strings.Join(parts[:len(parts)-1], "-")
			replicaCountMap[base]++
		}
	}

	var podList []map[string]interface{}
	for _, pod := range pods.Items {
		createdAt := pod.CreationTimestamp.Time
		uptime := time.Since(createdAt)

		status := string(pod.Status.Phase)
		if pod.DeletionTimestamp != nil {
			status = "Terminating"
		} else {
			_, detailedStatus := helpers.GetPodStatus(&pod)
			switch detailedStatus {
			case "running-not-ready", "container-not-ready":
				status = detailedStatus
			}
		}

		name := pod.Name
		baseName := name
		replicaIndex := 0
		replicaCount := 1

		parts := strings.Split(name, "-")
		if len(parts) >= 2 {
			base := strings.Join(parts[:len(parts)-1], "-")
			indexPart := parts[len(parts)-1]
			if idx, err := strconv.Atoi(indexPart); err == nil {
				baseName = base
				replicaIndex = idx
				replicaCount = replicaCountMap[base]
			}
		}

		podInfo := map[string]interface{}{
			"name":         name,
			"baseName":     baseName,
			"replicaIndex": replicaIndex,
			"replicaCount": replicaCount,
			"type":         pod.Labels["kubendt/type"],
			"driver":       pod.Labels["kubendt/driver"],
			// Whether this node may originate a traceroute (drives the
			// "Traceroute from here" menu gate). True only for L3-capable drivers.
			"l3capable": helpers.DriverIsL3Capable(pod.Labels["kubendt/driver"]),
			"runtime": func() string {
				runtime := pod.Labels["kubendt/runtime"]
				if runtime == "" {
					return "k8s-linux"
				}
				return runtime
			}(),
			"shellMode": pod.Labels["kubendt/shell-mode"],
			"serialShell": func() bool {
				if pod.Labels["kubendt/qemu"] == "true" {
					return true
				}
				return pod.Labels["kubendt/shell-mode"] == "serial"
			}(),
			"namespace": pod.Namespace,
			"image":     pod.Spec.Containers[0].Image,
			"node":      pod.Spec.NodeName,
			"status":    status,
			"createdAt": createdAt.Format("2006-01-02 15:04:05"),
			"uptime": fmt.Sprintf("%d days, %d hours, %d minutes",
				int(uptime.Hours())/24, int(uptime.Hours())%24, int(uptime.Minutes())%60),
		}
		podList = append(podList, podInfo)
	}

	c.JSON(http.StatusOK, gin.H{"pods": podList})
}

func RestartPod(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")
	podName, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		log.Printf("❌ Invalid pod reference '%s': %v", podRef, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	nudgePodAndTopology := func(phase, target string) {
		if err := helpers.NudgePodReconcile(namespace, target); err != nil {
			log.Printf("⚠️ Restart %s-heal: could not nudge Pod '%s': %v", phase, target, err)
		} else {
			log.Printf("ℹ️ Restart %s-heal: nudged Pod '%s'", phase, target)
		}

		if err := helpers.NudgeTopologyReconcile(namespace, target); err != nil {
			log.Printf("⚠️ Restart %s-heal: could not nudge Topology '%s': %v", phase, target, err)
		} else {
			log.Printf("ℹ️ Restart %s-heal: nudged Topology '%s'", phase, target)
		}
	}

	startedAt := time.Now()

	err = helpers.RestartPod(namespace, podName)
	if err != nil {
		if strings.Contains(err.Error(), "no pertenece a un StatefulSet") {
			log.Printf("⚠️ %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else if strings.Contains(err.Error(), "not found") {
			log.Printf("❌ %v", err)
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		} else {
			log.Printf("❌ Error restarting pod %s: %v", podName, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not restart pod"})
		}
		return
	}

	// After manual restart, run only soft-heal nudges (no hard reconcile / extra restarts).
	if waitErr := helpers.WaitForPodsReadyByName(namespace, []string{podName}); waitErr != nil {
		log.Printf("⚠️ Pod '%s' restarted but not fully ready before reconcile: %v", podName, waitErr)
	}
	podRestartDur := time.Since(startedAt)
	replayAt := time.Now()

	replayStats, replayErr := helpers.ReplayDriverOperationsForPodWithStats(namespace, podName)
	if replayErr != nil {
		log.Printf("❌ Pod %s restarted but failed to replay persisted operations: %v", podName, replayErr)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Pod restarted but failed to replay persisted driver operations",
			"details": replayErr.Error(),
			"replay":  replayStats,
		})
		return
	}
	replayDur := time.Since(replayAt)

	nudgePodAndTopology("soft", podName)

	// Give meshnet a short window to process nudges.
	time.Sleep(1500 * time.Millisecond)

	log.Printf("✅ Pod %s restarted successfully in namespace %s", podName, namespace)
	c.JSON(http.StatusOK, gin.H{
		"message":             fmt.Sprintf("Pod %s restarted successfully", podName),
		"replayed_operations": replayStats.Replayed,
		"replay":              replayStats,
		"took_time": gin.H{
			"total":       fmt.Sprintf("%.2fs", time.Since(startedAt).Seconds()),
			"pod_restart": fmt.Sprintf("%.2fs", podRestartDur.Seconds()),
			"replay":      fmt.Sprintf("%.2fs", replayDur.Seconds()),
		},
	})
}

func GetInterfacesFromPod(c *gin.Context) {
	podRef := c.Param("podName")
	namespace := c.Param("namespace")
	requestedIntf := strings.TrimSpace(c.Query("intf"))
	pod, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		log.Printf("❌ Invalid pod reference '%s': %v", podRef, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Full-list requests are topology-scoped by default: the result is restricted
	// to the interfaces declared in the pod's Topology CRD (its link endpoints),
	// dropping the primary CNI iface, loopback and config-created devices like OVS
	// bridges. Use ?scope=all for the raw kernel list. Single-interface lookups
	// (?intf=) are never scoped (and never read the CRD). Fail open: if the
	// topology set can't be read, return the full list rather than nothing.
	topoScope := requestedIntf == "" && c.Query("scope") != "all"
	var topoSet map[string]bool
	if topoScope {
		if s, terr := helpers.GetTopologyInterfaceSetForPod(namespace, pod); terr != nil {
			log.Printf("⚠️ scope=topology: could not read Topology interfaces for %s/%s: %v (returning all)", namespace, pod, terr)
			topoScope = false
		} else {
			topoSet = s
		}
	}
	filterScope := func(list []map[string]string) []map[string]string {
		if !topoScope || topoSet == nil {
			return list
		}
		out := make([]map[string]string, 0, len(list))
		for _, it := range list {
			if topoSet[it["interface"]] {
				out = append(out, it)
			}
		}
		return out
	}

	// Use driver-aware interface inspection when the driver supports it.
	// This gives guest-level interface names, IPs, and MACs instead of the pod-level
	// "ip a" view. Falls back to the standard path if the driver doesn't implement
	// EffectiveInterfaceInspector or if the inspection fails.
	if drv, drvErr := helpers.GetDriverForPod(namespace, pod); drvErr == nil {
		if requestedIntf != "" {
			if singleInspector, ok := drv.(types.EffectiveSingleInterfaceInspector); ok {
				entry, inspErr := singleInspector.GetEffectiveInterface(namespace, pod, requestedIntf)
				if inspErr != nil {
					log.Printf("⚠️ EffectiveSingleInterfaceInspector failed for %s/%s intf=%s: %v, falling back", namespace, pod, requestedIntf, inspErr)
				} else {
					c.JSON(http.StatusOK, gin.H{"interfaces": []map[string]string{entry}})
					return
				}
			}
		}

		if inspector, ok := drv.(types.EffectiveInterfaceInspector); ok {
			result, inspErr := inspector.GetEffectiveInterfaces(namespace, pod)
			if inspErr != nil {
				log.Printf("⚠️ EffectiveInterfaceInspector failed for %s/%s: %v, falling back to ip a", namespace, pod, inspErr)
			} else {
				if requestedIntf != "" {
					filtered := make([]map[string]string, 0, 1)
					for _, it := range result {
						if it["interface"] == requestedIntf {
							filtered = append(filtered, it)
							break
						}
					}
					c.JSON(http.StatusOK, gin.H{"interfaces": filtered})
					return
				}
				c.JSON(http.StatusOK, gin.H{"interfaces": filterScope(result)})
				return
			}
		}
	}

	result, err := helpers.GetInterfacesFromPod(pod, namespace)
	if err != nil {
		log.Printf("❌ Error fetching pod interfaces '%s': %v", pod, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Error fetching interfaces: %v", err),
		})
		return
	}

	if requestedIntf != "" {
		filtered := make([]map[string]string, 0, 1)
		for _, it := range result {
			if it["interface"] == requestedIntf {
				filtered = append(filtered, it)
				break
			}
		}
		result = filtered
	}

	c.JSON(http.StatusOK, gin.H{
		"interfaces": filterScope(result),
	})
}

func ShowQdisc(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")
	iface := c.Param("interface")
	podName, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Runs `tc qdisc show dev <iface>` in the pod netns, via the pod's own tc or
	// an ephemeral toolbox when the image ships none (same path as apply).
	stdout, stderr, err := helpers.RunTC(namespace, podName, []string{"tc", "qdisc", "show", "dev", iface})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  err.Error(),
			"stderr": stderr,
			"stdout": stdout,
		})
		return
	}

	parsed, perr := helpers.ParseQdiscShowToMap(stdout)
	if perr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": perr.Error(), "raw": stdout})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"interface": iface,
		"tcparams":  parsed, // map[string]interface{} con qdisc, parent, delay, jitter, loss, duplicate, corrupt, limit, ...
	})
}

// GetPodMetrics returns CPU and memory usage for a single pod via the metrics-server API.
// Returns {"available": false} with HTTP 503 if metrics-server is not installed.
func GetPodMetrics(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")
	podName, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	rawBody, err := kubeclient.Clientset.RESTClient().Get().
		AbsPath(fmt.Sprintf("/apis/metrics.k8s.io/v1beta1/namespaces/%s/pods/%s", namespace, podName)).
		DoRaw(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"available": false,
			"error":     "metrics-server not available or not installed",
		})
		return
	}

	var metricsData map[string]interface{}
	if jsonErr := json.Unmarshal(rawBody, &metricsData); jsonErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"available": false, "error": "failed to parse metrics response"})
		return
	}

	containers, _ := metricsData["containers"].([]interface{})
	cpuMilli := int64(0)
	memBytes := int64(0)

	for _, ct := range containers {
		ctMap, ok := ct.(map[string]interface{})
		if !ok {
			continue
		}
		usage, ok := ctMap["usage"].(map[string]interface{})
		if !ok {
			continue
		}
		if cpuStr, ok := usage["cpu"].(string); ok {
			q, perr := resource.ParseQuantity(cpuStr)
			if perr == nil {
				cpuMilli += q.MilliValue()
			}
		}
		if memStr, ok := usage["memory"].(string); ok {
			q, perr := resource.ParseQuantity(memStr)
			if perr == nil {
				memBytes += q.Value()
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"available":    true,
		"pod":          podName,
		"cpu_milli":    cpuMilli,
		"memory_bytes": memBytes,
		"memory_mib":   memBytes / (1024 * 1024),
	})
}

// GetNamespaceMetrics returns CPU and memory usage for every pod in a namespace,
// both per-pod and aggregated, via the metrics-server API.
// Returns {"available": false} with HTTP 503 if metrics-server is not installed.
func GetNamespaceMetrics(c *gin.Context) {
	namespace := c.Param("namespace")

	rawBody, err := kubeclient.Clientset.RESTClient().Get().
		AbsPath(fmt.Sprintf("/apis/metrics.k8s.io/v1beta1/namespaces/%s/pods", namespace)).
		DoRaw(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"available": false,
			"error":     "metrics-server not available or not installed",
		})
		return
	}

	var list struct {
		Items []map[string]interface{} `json:"items"`
	}
	if jsonErr := json.Unmarshal(rawBody, &list); jsonErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"available": false, "error": "failed to parse metrics response"})
		return
	}

	pods := make([]types.NamespacePodMetrics, 0, len(list.Items))
	var totalCPUMilli, totalMemBytes int64

	for _, item := range list.Items {
		podName := ""
		if meta, ok := item["metadata"].(map[string]interface{}); ok {
			if name, ok := meta["name"].(string); ok {
				podName = name
			}
		}

		containers, _ := item["containers"].([]interface{})
		var cpuMilli, memBytes int64
		for _, ct := range containers {
			ctMap, ok := ct.(map[string]interface{})
			if !ok {
				continue
			}
			usage, ok := ctMap["usage"].(map[string]interface{})
			if !ok {
				continue
			}
			if cpuStr, ok := usage["cpu"].(string); ok {
				if q, perr := resource.ParseQuantity(cpuStr); perr == nil {
					cpuMilli += q.MilliValue()
				}
			}
			if memStr, ok := usage["memory"].(string); ok {
				if q, perr := resource.ParseQuantity(memStr); perr == nil {
					memBytes += q.Value()
				}
			}
		}

		pods = append(pods, types.NamespacePodMetrics{
			Pod:         podName,
			CPUMilli:    cpuMilli,
			MemoryBytes: memBytes,
			MemoryMiB:   memBytes / (1024 * 1024),
		})
		totalCPUMilli += cpuMilli
		totalMemBytes += memBytes
	}

	// Deterministic ordering by pod name for stable output.
	sort.Slice(pods, func(i, j int) bool { return pods[i].Pod < pods[j].Pod })

	c.JSON(http.StatusOK, types.NamespaceMetricsResponse{
		Available:        true,
		Namespace:        namespace,
		PodCount:         len(pods),
		Pods:             pods,
		TotalCPUMilli:    totalCPUMilli,
		TotalMemoryBytes: totalMemBytes,
		TotalMemoryMiB:   totalMemBytes / (1024 * 1024),
	})
}
