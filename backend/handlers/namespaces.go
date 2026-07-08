package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"log"
	"net/http"
	"time"

	"kubendt/helpers"
	"kubendt/kubeclient"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Listar Pods (aquellos con kubendt)
func ListNamespaces(c *gin.Context) {

	namespaces, err := kubeclient.Clientset.CoreV1().Namespaces().List(c.Request.Context(), metav1.ListOptions{
		LabelSelector: "kubendt/enabled=true",
	})
	if err != nil {
		log.Printf("Error fetching namespaces: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	namespaceList := []gin.H{}
	for _, namespace := range namespaces.Items {
		createdAt := namespace.CreationTimestamp.Time
		uptime := time.Since(createdAt)

		status := string(namespace.Status.Phase)
		if namespace.DeletionTimestamp != nil {
			status = "Terminating"
		}

		// Resolve against reality: DB row first, then Topology CRDs in
		// kube-api. This also refreshes the DB row so future reads via the
		// cheap NamespaceHasTopology() stay accurate (handy after a backend
		// restart or when topologies were created before the column existed).
		hasTopology, err := helpers.SyncNamespaceTopologyState(namespace.Name)
		if err != nil {
			log.Printf("⚠️ Could not sync topology state for '%s': %v", namespace.Name, err)
			hasTopology = false
		}

		namespaceList = append(namespaceList, gin.H{
			"name":      namespace.Name,
			"status":    status,
			"createdAt": createdAt.Format("2006-01-02 15:04:05"),
			"uptime": fmt.Sprintf("%d days, %d hours, %d minutes",
				int(uptime.Hours())/24, int(uptime.Hours())%24, int(uptime.Minutes())%60),
			"has_topology": hasTopology,
		})
	}

	c.JSON(http.StatusOK, gin.H{"namespaces": namespaceList})
}

func CreateNamespace(c *gin.Context) {
	var request struct {
		Namespace string `json:"namespace"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		log.Printf("❌ Error en la solicitud JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON format"})
		return
	}

	namespace := request.Namespace

	err := helpers.CreateNamespace(namespace)
	if err != nil {
		switch {
		case errors.Is(err, helpers.ErrNamespaceHasResources):
			log.Printf("ℹ️ Namespace '%s' already exists and contains resources", namespace)
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("Namespace '%s' already exists and contains resources. Choose a different name or open the existing one.", namespace)})
			return
		case errors.Is(err, helpers.ErrNamespaceExistsEmpty):
			log.Printf("ℹ️ Namespace '%s' already exists (empty)", namespace)
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("Namespace '%s' already exists. Choose a different name.", namespace)})
			return
		default:
			log.Printf("❌ Error creating or validating namespace '%s': %v", namespace, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Could not create or verify namespace %s", namespace)})
			return
		}
	}

	log.Printf("✅ Namespace '%s' created successfully", namespace)
	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Namespace %s created successfully", namespace)})
}

// DeleteNamespace elimina un namespace y todos sus recursos
func DeleteNamespace(c *gin.Context) {
	namespace := c.Param("namespace")

	deletePositions := c.Query("deletePositions") == "true"
	deleteFiles := c.Query("deleteFiles") == "true"

	err := helpers.DeleteNamespace(namespace, deletePositions, deleteFiles)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "not found"):
			log.Printf("❌ Namespace '%s' does not exist", namespace)
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Namespace %s does not exist", namespace)})
			return
		case strings.Contains(err.Error(), "does not have the label"):
			log.Printf("⚠️ Namespace '%s' cannot be deleted because it was not created by KubeNDT", namespace)
			c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf("Namespace %s cannot be deleted: it was not created by KubeNDT", namespace)})
			return
		case strings.Contains(err.Error(), "deleting positions"):
			log.Printf("⚠️ Could not delete positions for '%s' in database", namespace)
		default:
			log.Printf("❌ Error deleting namespace '%s': %v", namespace, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Could not delete namespace %s", namespace)})
			return
		}
	}

	log.Printf("✅ Namespace '%s' deleted successfully", namespace)
	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Namespace %s deleted successfully", namespace)})
}

func GetInterfacesInNamespace(c *gin.Context) {
	namespace := c.Param("namespace")
	interfaces, err := helpers.GetInterfacesInNamespace(namespace)
	if err != nil {
		log.Printf("❌ Error fetching interfaces for namespace '%s': %v", namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not retrieve interfaces information"})
		return
	}
	c.JSON(http.StatusOK, interfaces)
}

// GetNamespaceOperation returns the in-progress operation lock for a namespace,
// or null when idle. It is a single DB read (no cluster calls), so the UI can
// poll it cheaply to reflect a running deploy/modify/clear after a reload or
// navigation without hitting the heavy topology endpoints.
func GetNamespaceOperation(c *gin.Context) {
	namespace := c.Param("namespace")
	lock, err := helpers.GetNamespaceOperationLock(namespace)
	if err != nil {
		log.Printf("⚠️ Could not read operation lock for '%s': %v", namespace, err)
		c.JSON(http.StatusOK, gin.H{"operation_lock": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"operation_lock": lock})
}

func GetNamespaceSummary(c *gin.Context) {
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

	ctx := c.Request.Context()
	nsObj, err := kubeclient.Clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Could not retrieve namespace '%s'", namespace)})
		return
	}

	podList, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not fetch pods"})
		return
	}

	stsList, err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not fetch statefulsets"})
		return
	}

	gvr := schema.GroupVersionResource{Group: "networkop.co.uk", Version: "v1beta1", Resource: "topologies"}
	topologyList, err := kubeclient.DynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not fetch topologies"})
		return
	}

	links, err := helpers.BuildLinksFromTopologyCRDs(namespace)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not build links from topologies"})
		return
	}

	hasTopology, err := helpers.SyncNamespaceTopologyState(namespace)
	if err != nil {
		log.Printf("⚠️ Could not sync namespace topology state for '%s': %v", namespace, err)
		hasTopology = len(stsList.Items) > 0 || len(topologyList.Items) > 0
	}

	operationLock, err := helpers.GetNamespaceOperationLock(namespace)
	if err != nil {
		log.Printf("⚠️ Could not read operation lock for '%s': %v", namespace, err)
	}

	history, err := helpers.ListDriverOperationsForNamespace(namespace)
	if err != nil {
		log.Printf("⚠️ Could not read driver operation history for '%s': %v", namespace, err)
		history = nil
	}

	nodeTypeCounts := map[string]int{}
	driverCounts := map[string]int{}
	runtimeCounts := map[string]int{}
	replicasTotal := 0

	for _, sts := range stsList.Items {
		replicas := 1
		if sts.Spec.Replicas != nil {
			replicas = int(*sts.Spec.Replicas)
		}
		replicasTotal += replicas

		nodeType := sts.Spec.Template.Labels["kubendt/type"]
		if nodeType == "" {
			nodeType = "unknown"
		}
		nodeTypeCounts[nodeType] += replicas

		driver := sts.Spec.Template.Labels["kubendt/driver"]
		if driver == "" {
			driver = "unknown"
		}
		driverCounts[driver] += replicas

		runtime := sts.Spec.Template.Labels["kubendt/runtime"]
		if runtime == "" {
			if sts.Spec.Template.Labels["kubendt/qemu"] == "true" {
				runtime = "qemu"
			} else {
				runtime = "container"
			}
		}
		runtimeCounts[runtime] += replicas
	}

	podPhaseCounts := map[string]int{}
	podsReady := 0
	podsRunning := 0
	podsRestarting := 0

	for _, pod := range podList.Items {
		phase := string(pod.Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}
		podPhaseCounts[phase]++

		if pod.Status.Phase == "Running" {
			podsRunning++
		}

		ready := false
		for _, cond := range pod.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ready = true
				break
			}
		}
		if ready {
			podsReady++
		}

		restarting := false
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.RestartCount > 0 {
				restarting = true
				break
			}
		}
		if restarting {
			podsRestarting++
		}
	}

	linkWithLocalhost := 0
	for _, l := range links {
		if l.Node == "external" || l.PeerNode == "external" {
			linkWithLocalhost++
		}
	}

	historyByAction := map[string]int{}
	historyByDriver := map[string]int{}
	historyByPod := map[string]int{}
	recentExecutedAt := ""
	for _, op := range history {
		historyByAction[op.ActionType]++
		historyByDriver[op.DriverType]++
		historyByPod[op.PodName]++
		if op.ExecutedAt > recentExecutedAt {
			recentExecutedAt = op.ExecutedAt
		}
	}

	namespaceStatus := string(nsObj.Status.Phase)
	if nsObj.DeletionTimestamp != nil {
		namespaceStatus = "Terminating"
	}

	age := time.Since(nsObj.CreationTimestamp.Time)
	took := time.Since(startedAt)

	response := gin.H{
		"namespace": gin.H{
			"name":       namespace,
			"status":     namespaceStatus,
			"created_at": nsObj.CreationTimestamp.Format(time.RFC3339),
			"age":        age.Round(time.Second).String(),
		},
		"topology": gin.H{
			"has_topology":      hasTopology,
			"statefulsets":      len(stsList.Items),
			"topology_crds":     len(topologyList.Items),
			"logical_links":     len(links),
			"links_to_external": linkWithLocalhost,
		},
		"nodes": gin.H{
			"total_replicas": replicasTotal,
			"by_type":        nodeTypeCounts,
		},
		"pods": gin.H{
			"total":        len(podList.Items),
			"running":      podsRunning,
			"ready":        podsReady,
			"restarting":   podsRestarting,
			"phase_counts": podPhaseCounts,
		},
		"drivers": gin.H{
			"by_driver": driverCounts,
		},
		"runtime": gin.H{
			"by_runtime": runtimeCounts,
		},
		"operations": gin.H{
			"total_persisted":   len(history),
			"by_action":         historyByAction,
			"by_driver":         historyByDriver,
			"pods_with_history": len(historyByPod),
			"last_executed_at":  recentExecutedAt,
		},
		"operation_lock": operationLock,
		"took_time":      took.Round(time.Millisecond).String(),
		"took_seconds":   took.Seconds(),
	}

	responseJSON, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not generate summary JSON"})
		return
	}

	c.Data(http.StatusOK, "application/json", responseJSON)
}
