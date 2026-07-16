package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"kubendt/helpers"
	"kubendt/kubeclient"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// How long to wait for the debug container to come up. The first image pull
	// dominates this.
	traceStartTimeout = 120 * time.Second
	// Cap on a whole run. A total black hole would stall for ~60s with -m 30
	// -w 2, and 90s leaves headroom for DNS and slow first hops.
	traceRunTimeout = 90 * time.Second
	traceWriteWait  = 10 * time.Second
)

// traceHopFrame is the wire shape of a hop. It embeds TraceHop and adds the
// "type" field the frontend switches on.
type traceHopFrame struct {
	Type string `json:"type"`
	helpers.TraceHop
}

// TraceWebSocket runs a one-shot traceroute from a source pod toward a
// destination and streams each hop, already resolved to a topology node, to the
// browser, which animates the packet across the graph.
//
// It sends JSON text frames tagged by "type": a "meta" frame, "status" frames
// (starting, resolving, tracing, done), one "hop" frame per hop, and "error".
// The probe runs in the shared debug container exec'd into the pod netns, so the
// source image needs no traceroute of its own. Closing the socket kills it.
func TraceWebSocket(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")
	dest := strings.TrimSpace(c.Query("dest"))
	method := strings.ToLower(strings.TrimSpace(c.DefaultQuery("method", "icmp")))
	metrics := c.Query("metrics") == "1"
	cycles, _ := strconv.Atoi(c.DefaultQuery("cycles", "5"))

	if method != "icmp" && method != "udp" && method != "tcp" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid trace method (expected icmp|udp|tcp)"})
		return
	}
	if !helpers.ValidTraceDest(dest) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid destination (expected an IPv4 address or hostname)"})
		return
	}

	pod, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	podObj, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), pod, metav1.GetOptions{})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pod not found"})
		return
	}
	// Only L3-capable nodes can originate a traceroute. A switch, or any driver
	// without L3Capable, is rejected before the socket is even upgraded.
	if !helpers.DriverIsL3Capable(podObj.Labels["kubendt/driver"]) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source node is not L3-capable (no traceroute origin)"})
		return
	}

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println("❌ Trace WebSocket upgrade error:", err)
		return
	}
	defer ws.Close()

	var writeMu sync.Mutex
	sendCtl := func(v any) {
		if b, err := json.Marshal(v); err == nil {
			writeMu.Lock()
			_ = ws.SetWriteDeadline(time.Now().Add(traceWriteWait))
			_ = ws.WriteMessage(websocket.TextMessage, b)
			writeMu.Unlock()
		}
	}

	container, reused, err := helpers.EnsureDebugContainer(namespace, pod)
	if err != nil {
		log.Printf("❌ Trace: ensure debug container failed for %s/%s: %v", namespace, pod, err)
		sendCtl(gin.H{"type": "error", "message": err.Error()})
		return
	}
	log.Printf("🧭 Trace: %s %s from %s/%s to %q (method=%s)",
		map[bool]string{true: "reusing", false: "injected"}[reused], container, namespace, pod, dest, method)
	sendCtl(gin.H{"type": "meta", "source": pod, "dest": dest, "method": method, "container": container, "reused": reused})

	if !reused {
		sendCtl(gin.H{"type": "status", "state": "starting"})
		if err := helpers.WaitEphemeralRunning(namespace, pod, container, traceStartTimeout); err != nil {
			log.Printf("❌ Trace: container %s did not start: %v", container, err)
			sendCtl(gin.H{"type": "error", "message": err.Error()})
			return
		}
	}

	// Build the IP-to-node index and the adjacency up front so hops can be
	// annotated as they arrive.
	sendCtl(gin.H{"type": "status", "state": "resolving"})
	ipIndex, err := helpers.BuildTraceIPIndex(namespace)
	if err != nil {
		log.Printf("⚠️ Trace: could not build IP index: %v (hops will be unresolved)", err)
		ipIndex = map[string]helpers.TraceIPNode{}
	}
	adjacency := helpers.BuildPodAdjacency(namespace)

	ctx, cancel := context.WithTimeout(context.Background(), traceRunTimeout)
	defer cancel()

	// Client disconnect cancels the exec, in either mode.
	go func() {
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	// Probe via the shared core, forwarding each hop to the browser as it comes.
	sendCtl(gin.H{"type": "status", "state": map[bool]string{true: "measuring", false: "tracing"}[metrics]})
	outcome, runErr := helpers.RunTrace(ctx, namespace, pod, container, dest, method, metrics, cycles, ipIndex, adjacency,
		func(hop helpers.TraceHop) { sendCtl(traceHopFrame{Type: "hop", TraceHop: hop}) })
	if runErr != nil && ctx.Err() == nil {
		log.Printf("❌ Trace stream ended with error (%s): %v", container, runErr)
		sendCtl(gin.H{"type": "error", "message": runErr.Error()})
		return
	}
	sendCtl(gin.H{"type": "status", "state": "done", "outcome": outcome})
}

// TraceReport runs the same probe as TraceWebSocket but returns the whole
// result as one JSON document, for scripting and automation with no socket. It
// blocks until the probe finishes.
func TraceReport(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")
	dest := strings.TrimSpace(c.Query("dest"))
	method := strings.ToLower(strings.TrimSpace(c.DefaultQuery("method", "icmp")))
	metrics := c.Query("metrics") == "1"
	cycles, _ := strconv.Atoi(c.DefaultQuery("cycles", "5"))

	if method != "icmp" && method != "udp" && method != "tcp" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid trace method (expected icmp|udp|tcp)"})
		return
	}
	if !helpers.ValidTraceDest(dest) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid destination (expected an IPv4 address or hostname)"})
		return
	}
	pod, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	podObj, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), pod, metav1.GetOptions{})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pod not found"})
		return
	}
	if !helpers.DriverIsL3Capable(podObj.Labels["kubendt/driver"]) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source node is not L3-capable (no traceroute origin)"})
		return
	}

	container, reused, err := helpers.EnsureDebugContainer(namespace, pod)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !reused {
		if err := helpers.WaitEphemeralRunning(namespace, pod, container, traceStartTimeout); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	ipIndex, err := helpers.BuildTraceIPIndex(namespace)
	if err != nil {
		ipIndex = map[string]helpers.TraceIPNode{}
	}
	adjacency := helpers.BuildPodAdjacency(namespace)

	ctx, cancel := context.WithTimeout(context.Background(), traceRunTimeout)
	defer cancel()

	startedAt := time.Now()
	hops := make([]helpers.TraceHop, 0)
	outcome, runErr := helpers.RunTrace(ctx, namespace, pod, container, dest, method, metrics, cycles, ipIndex, adjacency,
		func(hop helpers.TraceHop) { hops = append(hops, hop) })
	finishedAt := time.Now()
	if runErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": runErr.Error()})
		return
	}

	mode := "trace"
	if metrics {
		mode = "metrics"
	}
	resp := gin.H{
		"source":      pod,
		"destination": dest,
		"method":      method,
		"mode":        mode,
		"outcome":     outcome,
		"startedAt":   startedAt.UTC().Format(time.RFC3339),
		"finishedAt":  finishedAt.UTC().Format(time.RFC3339),
		"hops":        hops,
	}
	if metrics {
		resp["cycles"] = cycles
	}
	c.JSON(http.StatusOK, resp)
}
