package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"kubendt/helpers"
	"kubendt/kubeclient"
	"kubendt/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Throughput tests run iperf3 from a pod toward an address of the namespace,
// through the toolboxes of both ends. The WebSocket streams every interval
// for the panel, the REST endpoint returns the whole run for scripts.

// iperfSetupTimeout is added to the duration for the toolboxes and the server.
const iperfSetupTimeout = 60 * time.Second

const iperfWriteWait = 10 * time.Second

type iperfRequest struct {
	opts   helpers.IperfOptions
	server string // auto, none, or a pod reference
}

// iperfRun is a request resolved to pods. serverPod is empty when the lab
// runs its own server at the destination.
type iperfRun struct {
	source    string
	serverPod string
}

type iperfIntervalFrame struct {
	Type string `json:"type"`
	types.IperfInterval
}

type iperfSummaryFrame struct {
	Type string `json:"type"`
	types.IperfSummary
}

func parseIperfQuery(c *gin.Context) (iperfRequest, error) {
	q := iperfRequest{server: strings.TrimSpace(c.DefaultQuery("server", "auto"))}
	if q.server == "" {
		q.server = "auto"
	}
	q.opts = helpers.IperfOptions{
		Dest:      strings.TrimSpace(c.Query("dest")),
		Proto:     strings.ToLower(strings.TrimSpace(c.DefaultQuery("proto", "tcp"))),
		Duration:  10,
		Parallel:  1,
		Bandwidth: strings.TrimSpace(c.Query("bandwidth")),
		Reverse:   c.Query("reverse") == "1" || c.Query("reverse") == "true",
	}
	for name, dst := range map[string]*int{"duration": &q.opts.Duration, "parallel": &q.opts.Parallel, "port": &q.opts.Port} {
		raw := strings.TrimSpace(c.Query(name))
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return q, fmt.Errorf("%s must be a whole number", name)
		}
		*dst = n
	}
	return q, q.opts.Validate()
}

func getPod(namespace, name string) (*v1.Pod, error) {
	return kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
}

// resolveIperfRun names the source and the server pods. The source must be
// L3-capable. Guest-VM nodes are out for now: their toolbox sits in a pod
// netns with no connectivity, like the trace before its guest wrapper.
func resolveIperfRun(namespace, podRef string, q iperfRequest) (iperfRun, int, error) {
	pod, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		return iperfRun{}, http.StatusBadRequest, err
	}
	podObj, err := getPod(namespace, pod)
	if err != nil {
		return iperfRun{}, http.StatusNotFound, errors.New("pod not found")
	}
	if !helpers.DriverIsL3Capable(podObj.Labels["kubendt/driver"]) {
		return iperfRun{}, http.StatusBadRequest, errors.New("source node is not L3-capable")
	}
	if podObj.Labels["kubendt/runtime"] == "qemu" {
		return iperfRun{}, http.StatusBadRequest, guestVMError(pod, podObj, "run a throughput test")
	}
	run := iperfRun{source: pod}
	switch q.server {
	case "none":
		return run, 0, nil
	case "auto":
		index, err := helpers.BuildTraceIPIndex(namespace)
		if err != nil {
			return run, http.StatusInternalServerError, err
		}
		node, ok := index[q.opts.Dest]
		if !ok {
			return run, http.StatusBadRequest, fmt.Errorf("%q is not an address of a node in the namespace; run your own server there and pass server=none", q.opts.Dest)
		}
		run.serverPod = node.Pod
	default:
		run.serverPod, err = helpers.ResolvePodReference(namespace, q.server)
		if err != nil {
			return run, http.StatusBadRequest, err
		}
	}
	if run.serverPod == run.source {
		return run, http.StatusBadRequest, errors.New("the server must run on another pod")
	}
	srv, err := getPod(namespace, run.serverPod)
	if err != nil {
		return run, http.StatusNotFound, fmt.Errorf("server pod %q not found", run.serverPod)
	}
	if srv.Labels["kubendt/runtime"] == "qemu" {
		return run, http.StatusBadRequest, guestVMError(run.serverPod, srv, "host the server")
	}
	return run, 0, nil
}

// guestVMError names the node and its driver: the toolbox sits in a pod
// netns that a guest VM leaves without an IP stack.
func guestVMError(pod string, obj *v1.Pod, what string) error {
	driver := obj.Labels["kubendt/driver"]
	if driver == "" {
		driver = "its driver"
	}
	return fmt.Errorf("%s cannot %s yet: %s runs a guest VM and iperf3 is not reachable from the pod", pod, what, driver)
}

// prepareIperf readies the toolboxes of both ends and picks the server port.
func prepareIperf(namespace string, run iperfRun, q iperfRequest, onInject func()) (client string, server *helpers.IperfServer, err error) {
	client, _, err = helpers.EnsureToolbox(namespace, run.source, onInject)
	if err != nil {
		return "", nil, err
	}
	if run.serverPod == "" {
		return client, nil, nil
	}
	container, _, err := helpers.EnsureToolbox(namespace, run.serverPod, onInject)
	if err != nil {
		return "", nil, err
	}
	port := q.opts.Port
	if port == 0 {
		port = helpers.PickIperfPort()
	}
	return client, &helpers.IperfServer{Pod: run.serverPod, Container: container, Port: port}, nil
}

func iperfMeta(run iperfRun, q iperfRequest, server *helpers.IperfServer, client string) gin.H {
	meta := gin.H{
		"type": "meta", "source": run.source, "dest": q.opts.Dest, "proto": q.opts.Proto,
		"duration": q.opts.Duration, "bandwidth": q.opts.Bandwidth, "parallel": q.opts.Parallel,
		"reverse": q.opts.Reverse, "container": client, "server": nil,
	}
	if server != nil {
		meta["server"] = gin.H{"pod": server.Pod, "port": server.Port}
	}
	return meta
}

// IperfWebSocket streams a throughput test: a "meta" frame, "status" frames
// (starting, running, done), one "interval" frame per second, a "summary"
// frame, or "error". Closing the socket stops the test. A destination the
// backend cannot place is reported as an error frame, after the upgrade, so
// the panel can show why.
func IperfWebSocket(c *gin.Context) {
	namespace := c.Param("namespace")
	q, err := parseIperfQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println("❌ Iperf WebSocket upgrade error:", err)
		return
	}
	defer ws.Close()

	var writeMu sync.Mutex
	sendCtl := func(v any) {
		if b, err := json.Marshal(v); err == nil {
			writeMu.Lock()
			_ = ws.SetWriteDeadline(time.Now().Add(iperfWriteWait))
			_ = ws.WriteMessage(websocket.TextMessage, b)
			writeMu.Unlock()
		}
	}

	run, _, err := resolveIperfRun(namespace, c.Param("podName"), q)
	if err != nil {
		sendCtl(gin.H{"type": "error", "message": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(q.opts.Duration)*time.Second+iperfSetupTimeout)
	defer cancel()
	go func() {
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	client, server, err := prepareIperf(namespace, run, q, func() { sendCtl(gin.H{"type": "status", "state": "starting"}) })
	if err != nil {
		log.Printf("❌ Iperf: toolbox failed for %s/%s: %v", namespace, run.source, err)
		sendCtl(gin.H{"type": "error", "message": err.Error()})
		return
	}
	sendCtl(iperfMeta(run, q, server, client))
	sendCtl(gin.H{"type": "status", "state": "running"})
	log.Printf("📶 Iperf: %s from %s/%s to %s (server=%s)", q.opts.Proto, namespace, run.source, q.opts.Dest, run.serverPod)

	summary, err := helpers.RunIperf(ctx, namespace, run.source, client, server, q.opts, func(iv types.IperfInterval) {
		sendCtl(iperfIntervalFrame{Type: "interval", IperfInterval: iv})
	})
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		sendCtl(gin.H{"type": "error", "message": "the test timed out"})
		return
	case ctx.Err() != nil:
		return
	case err != nil:
		log.Printf("❌ Iperf from %s/%s failed: %v", namespace, run.source, err)
		sendCtl(gin.H{"type": "error", "message": err.Error()})
		return
	}
	sendCtl(iperfSummaryFrame{Type: "summary", IperfSummary: *summary})
	sendCtl(gin.H{"type": "status", "state": "done"})
}

// IperfReport runs the same test and returns it as one JSON document.
func IperfReport(c *gin.Context) {
	namespace := c.Param("namespace")
	q, err := parseIperfQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	run, code, err := resolveIperfRun(namespace, c.Param("podName"), q)
	if err != nil {
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	client, server, err := prepareIperf(namespace, run, q, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(q.opts.Duration)*time.Second+iperfSetupTimeout)
	defer cancel()

	startedAt := time.Now()
	intervals := make([]types.IperfInterval, 0)
	summary, err := helpers.RunIperf(ctx, namespace, run.source, client, server, q.opts, func(iv types.IperfInterval) {
		intervals = append(intervals, iv)
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp := types.IperfReportResponse{
		Source: run.source, Destination: q.opts.Dest, Proto: q.opts.Proto, Duration: q.opts.Duration,
		Bandwidth: q.opts.Bandwidth, Parallel: q.opts.Parallel, Reverse: q.opts.Reverse,
		StartedAt: startedAt.UTC().Format(time.RFC3339), FinishedAt: time.Now().UTC().Format(time.RFC3339),
		Intervals: intervals, Summary: summary,
	}
	if server != nil {
		resp.Server = &types.IperfServerDoc{Pod: server.Pod, Port: server.Port}
	}
	c.JSON(http.StatusOK, resp)
}
