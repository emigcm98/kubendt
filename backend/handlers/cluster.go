package handlers

import (
	"context"
	"encoding/json"
	"kubendt/kubeclient"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NodeInfo struct {
	Name             string   `json:"name"`
	Roles            []string `json:"roles"`
	Status           string   `json:"status"`
	CPUCapacity      string   `json:"cpu_capacity"`
	MemoryCapacity   string   `json:"memory_capacity"`
	CPUUsage         string   `json:"cpu_usage"`
	MemoryUsage      string   `json:"memory_usage"`
	CPUPercentage    float64  `json:"cpu_percentage"`
	MemoryPercentage float64  `json:"memory_percentage"`
	KubeletVersion   string   `json:"kubelet_version"`
}

type ClusterStatusResponse struct {
	Nodes []NodeInfo `json:"nodes"`
	Ready int        `json:"ready"`
	Total int        `json:"total"`
	// Aggregated cluster-wide percentages, weighted by node capacity so a
	// 2-CPU node at 50% does not weigh the same as an 8-CPU node at 50%.
	// Computed as sum(usage)/sum(capacity) across nodes that report metrics.
	// Null (zero in JSON) when metrics-server is unavailable.
	AvgCPUPercentage    float64 `json:"avg_cpu_percentage"`
	AvgMemoryPercentage float64 `json:"avg_memory_percentage"`
}

// nodeMetrics holds parsed CPU (millicores) and memory (bytes) usage for one node.
type nodeMetrics struct {
	cpuMilli int64
	memBytes int64
}

// fetchNodeMetrics calls the metrics-server API and returns a map of nodeName -> metrics.
// Returns nil (not an error) if metrics-server is unavailable.
func fetchNodeMetrics(ctx context.Context) map[string]nodeMetrics {
	rawBody, err := kubeclient.Clientset.RESTClient().Get().
		AbsPath("/apis/metrics.k8s.io/v1beta1/nodes").
		DoRaw(ctx)
	if err != nil {
		return nil
	}

	var resp struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Usage struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"usage"`
		} `json:"items"`
	}
	if json.Unmarshal(rawBody, &resp) != nil {
		return nil
	}

	result := make(map[string]nodeMetrics, len(resp.Items))
	for _, item := range resp.Items {
		nm := nodeMetrics{}
		if q, err := resource.ParseQuantity(item.Usage.CPU); err == nil {
			nm.cpuMilli = q.MilliValue()
		}
		if q, err := resource.ParseQuantity(item.Usage.Memory); err == nil {
			nm.memBytes = q.Value()
		}
		result[item.Metadata.Name] = nm
	}
	return result
}

func GetClusterStatus(c *gin.Context) {
	ctx := c.Request.Context()
	nodes, err := kubeclient.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error fetching nodes: " + err.Error()})
		return
	}

	// Fetch all node metrics in one call (nil if metrics-server unavailable)
	metricsMap := fetchNodeMetrics(ctx)

	nodeInfos := []NodeInfo{}
	readyCount := 0

	// Aggregates: sum of raw usage and capacity across nodes that report
	// metrics. Using raw bytes/millicores gives a precise weighted average
	// without any string parsing on the frontend.
	var sumCPUMilliUsed, sumCPUMilliCap int64
	var sumMemBytesUsed, sumMemBytesCap int64

	for _, node := range nodes.Items {
		// Roles
		roles := []string{}
		if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
			roles = append(roles, "control-plane")
		}
		if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
			roles = append(roles, "master")
		}
		if len(roles) == 0 {
			roles = append(roles, "worker")
		}

		// Status
		status := "NotReady"
		for _, condition := range node.Status.Conditions {
			if condition.Type == "Ready" {
				if condition.Status == "True" {
					status = "Ready"
					readyCount++
				}
				break
			}
		}

		// Capacity
		cpuCapacity := node.Status.Capacity.Cpu().String()
		cpuCap := node.Status.Capacity.Cpu().MilliValue()
		memCap := node.Status.Capacity.Memory().Value()
		memCapGiB := float64(memCap) / (1024 * 1024 * 1024)
		kubeletVersion := node.Status.NodeInfo.KubeletVersion

		// Usage from metrics-server
		cpuUsageStr := "N/A"
		memUsageStr := "N/A"
		cpuPercentage := 0.0
		memPercentage := 0.0

		if metricsMap != nil {
			if nm, ok := metricsMap[node.Name]; ok {
				cpuUsageStr = strconv.FormatInt(nm.cpuMilli, 10) + "m"
				memUsageGiB := float64(nm.memBytes) / (1024 * 1024 * 1024)
				memUsageStr = strconv.FormatFloat(memUsageGiB, 'f', 2, 64) + "Gi"
				if cpuCap > 0 {
					cpuPercentage = float64(nm.cpuMilli) / float64(cpuCap) * 100
					sumCPUMilliUsed += nm.cpuMilli
					sumCPUMilliCap += cpuCap
				}
				if memCap > 0 {
					memPercentage = float64(nm.memBytes) / float64(memCap) * 100
					sumMemBytesUsed += nm.memBytes
					sumMemBytesCap += memCap
				}
			}
		}

		nodeInfos = append(nodeInfos, NodeInfo{
			Name:             node.Name,
			Roles:            roles,
			Status:           status,
			CPUCapacity:      cpuCapacity,
			MemoryCapacity:   strconv.FormatFloat(memCapGiB, 'f', 2, 64) + "Gi",
			CPUUsage:         cpuUsageStr,
			MemoryUsage:      memUsageStr,
			CPUPercentage:    cpuPercentage,
			MemoryPercentage: memPercentage,
			KubeletVersion:   kubeletVersion,
		})
	}

	avgCPU := 0.0
	if sumCPUMilliCap > 0 {
		avgCPU = float64(sumCPUMilliUsed) / float64(sumCPUMilliCap) * 100
	}
	avgMem := 0.0
	if sumMemBytesCap > 0 {
		avgMem = float64(sumMemBytesUsed) / float64(sumMemBytesCap) * 100
	}

	c.JSON(http.StatusOK, ClusterStatusResponse{
		Nodes:               nodeInfos,
		Ready:               readyCount,
		Total:               len(nodes.Items),
		AvgCPUPercentage:    avgCPU,
		AvgMemoryPercentage: avgMem,
	})
}

// ─── Detailed single-node view ──────────────────────────────────────────────

type NodeAddress struct {
	Type    string `json:"type"`    // InternalIP / ExternalIP / Hostname / ...
	Address string `json:"address"` // 10.0.0.5 / k8s-worker1.local / ...
}

type NodeCondition struct {
	Type           string `json:"type"`            // Ready / MemoryPressure / DiskPressure / PIDPressure / NetworkUnavailable
	Status         string `json:"status"`          // True / False / Unknown
	Reason         string `json:"reason"`          // KubeletReady / ...
	Message        string `json:"message"`         // Human-readable explanation
	LastTransition string `json:"last_transition"` // RFC3339
}

type NodeTaint struct {
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	Effect    string `json:"effect"` // NoSchedule / PreferNoSchedule / NoExecute
	TimeAdded string `json:"time_added,omitempty"`
}

type NodeResourceQuantity struct {
	CPUMilli    int64 `json:"cpu_milli"`    // millicores
	MemoryBytes int64 `json:"memory_bytes"` // bytes
	Pods        int64 `json:"pods"`
	StorageEph  int64 `json:"storage_ephemeral_bytes"`
}

type NodeOSInfo struct {
	OperatingSystem string `json:"operating_system"` // linux
	Architecture    string `json:"architecture"`     // amd64
	OSImage         string `json:"os_image"`         // Ubuntu 22.04.4 LTS
	KernelVersion   string `json:"kernel_version"`   // 5.15.0-105-generic
}

type NodeVersionsInfo struct {
	KubeletVersion          string `json:"kubelet_version"`           // v1.32.2
	ContainerRuntimeVersion string `json:"container_runtime_version"` // containerd://1.7.20
}

type NodeDetailResponse struct {
	Name              string               `json:"name"`
	Roles             []string             `json:"roles"`
	Status            string               `json:"status"`
	CreationTimestamp string               `json:"creation_timestamp"`
	PodCIDR           string               `json:"pod_cidr"`
	PodCIDRs          []string             `json:"pod_cidrs"`
	Addresses         []NodeAddress        `json:"addresses"`
	OSInfo            NodeOSInfo           `json:"os_info"`
	Versions          NodeVersionsInfo     `json:"versions"`
	Capacity          NodeResourceQuantity `json:"capacity"`
	Allocatable       NodeResourceQuantity `json:"allocatable"`
	Conditions        []NodeCondition      `json:"conditions"`
	Taints            []NodeTaint          `json:"taints"`
	Labels            map[string]string    `json:"labels"`
	Annotations       map[string]string    `json:"annotations"`
	// Live usage, when metrics-server is available. Zero otherwise.
	CPUMilliUsage    int64   `json:"cpu_milli_usage"`
	MemoryBytesUsage int64   `json:"memory_bytes_usage"`
	CPUPercentage    float64 `json:"cpu_percentage"`
	MemoryPercentage float64 `json:"memory_percentage"`
}

func resourceQuantity(rl v1.ResourceList) NodeResourceQuantity {
	q := NodeResourceQuantity{}
	if v, ok := rl[v1.ResourceCPU]; ok {
		q.CPUMilli = v.MilliValue()
	}
	if v, ok := rl[v1.ResourceMemory]; ok {
		q.MemoryBytes = v.Value()
	}
	if v, ok := rl[v1.ResourcePods]; ok {
		q.Pods = v.Value()
	}
	if v, ok := rl[v1.ResourceEphemeralStorage]; ok {
		q.StorageEph = v.Value()
	}
	return q
}

// GetClusterNodeDetail returns the full kubectl-derivable view of a single
// node. No host-network introspection, that would require a privileged
// DaemonSet, intentionally out of scope here.
func GetClusterNodeDetail(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node name is required"})
		return
	}

	ctx := c.Request.Context()
	node, err := kubeclient.Clientset.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found: " + err.Error()})
		return
	}

	// Roles (mirror logic from GetClusterStatus so the panel is consistent)
	roles := []string{}
	if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
		roles = append(roles, "control-plane")
	}
	if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
		roles = append(roles, "master")
	}
	if len(roles) == 0 {
		roles = append(roles, "worker")
	}

	// Status
	status := "NotReady"
	for _, cond := range node.Status.Conditions {
		if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue {
			status = "Ready"
			break
		}
	}

	// Addresses
	addresses := []NodeAddress{}
	for _, a := range node.Status.Addresses {
		addresses = append(addresses, NodeAddress{
			Type:    string(a.Type),
			Address: a.Address,
		})
	}

	// Conditions
	conditions := []NodeCondition{}
	for _, c := range node.Status.Conditions {
		conditions = append(conditions, NodeCondition{
			Type:           string(c.Type),
			Status:         string(c.Status),
			Reason:         c.Reason,
			Message:        c.Message,
			LastTransition: c.LastTransitionTime.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}

	// Taints
	taints := []NodeTaint{}
	for _, t := range node.Spec.Taints {
		nt := NodeTaint{
			Key:    t.Key,
			Value:  t.Value,
			Effect: string(t.Effect),
		}
		if t.TimeAdded != nil {
			nt.TimeAdded = t.TimeAdded.UTC().Format("2006-01-02T15:04:05Z")
		}
		taints = append(taints, nt)
	}

	// Live usage (best-effort, optional)
	var cpuMilliUsage, memBytesUsage int64
	var cpuPct, memPct float64
	if metricsMap := fetchNodeMetrics(ctx); metricsMap != nil {
		if nm, ok := metricsMap[node.Name]; ok {
			cpuMilliUsage = nm.cpuMilli
			memBytesUsage = nm.memBytes
			if cpuCap := node.Status.Capacity.Cpu().MilliValue(); cpuCap > 0 {
				cpuPct = float64(nm.cpuMilli) / float64(cpuCap) * 100
			}
			if memCap := node.Status.Capacity.Memory().Value(); memCap > 0 {
				memPct = float64(nm.memBytes) / float64(memCap) * 100
			}
		}
	}

	resp := NodeDetailResponse{
		Name:              node.Name,
		Roles:             roles,
		Status:            status,
		CreationTimestamp: node.CreationTimestamp.UTC().Format("2006-01-02T15:04:05Z"),
		PodCIDR:           node.Spec.PodCIDR,
		PodCIDRs:          node.Spec.PodCIDRs,
		Addresses:         addresses,
		OSInfo: NodeOSInfo{
			OperatingSystem: node.Status.NodeInfo.OperatingSystem,
			Architecture:    node.Status.NodeInfo.Architecture,
			OSImage:         node.Status.NodeInfo.OSImage,
			KernelVersion:   node.Status.NodeInfo.KernelVersion,
		},
		Versions: NodeVersionsInfo{
			KubeletVersion:          node.Status.NodeInfo.KubeletVersion,
			ContainerRuntimeVersion: node.Status.NodeInfo.ContainerRuntimeVersion,
		},
		Capacity:         resourceQuantity(node.Status.Capacity),
		Allocatable:      resourceQuantity(node.Status.Allocatable),
		Conditions:       conditions,
		Taints:           taints,
		Labels:           node.Labels,
		Annotations:      node.Annotations,
		CPUMilliUsage:    cpuMilliUsage,
		MemoryBytesUsage: memBytesUsage,
		CPUPercentage:    cpuPct,
		MemoryPercentage: memPct,
	}

	c.JSON(http.StatusOK, resp)
}
