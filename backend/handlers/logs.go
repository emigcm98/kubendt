package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kubendt/helpers"
	"kubendt/kubeclient"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// Container logs come from the kubelet through the API server, the same
// stream kubectl logs reads: no exec, no process in the pod.

// podLogQuery is what both log endpoints take from the query string.
type podLogQuery struct {
	container  string
	tail       int64 // 0 reads the whole log
	previous   bool
	timestamps bool
	since      int64 // seconds, 0 for no limit
}

func parseLogQuery(c *gin.Context) podLogQuery {
	q := podLogQuery{container: strings.TrimSpace(c.Query("container")), tail: 500}
	if raw := c.Query("tail"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v >= 0 {
			q.tail = v
		}
	}
	q.previous = c.Query("previous") == "1" || c.Query("previous") == "true"
	q.timestamps = c.Query("timestamps") == "1" || c.Query("timestamps") == "true"
	if v, err := strconv.ParseInt(c.Query("since"), 10, 64); err == nil && v > 0 {
		q.since = v
	}
	return q
}

// podLogMeta says what can be read: the containers of the pod, the main one
// first and then the ephemeral ones (the toolbox), and how many times
// the chosen container restarted inside this pod object, the only case in
// which "previous" has a log to show.
type podLogMeta struct {
	Pod        string   `json:"pod"`
	Container  string   `json:"container"`
	Containers []string `json:"containers"`
	Restarts   int32    `json:"restarts"`
}

func resolveLogTarget(namespace, podRef, container string) (podLogMeta, error) {
	podName, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		return podLogMeta{}, err
	}
	pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return podLogMeta{}, fmt.Errorf("pod '%s' not found in namespace '%s'", podName, namespace)
	}
	meta := podLogMeta{Pod: podName}
	for _, ct := range pod.Spec.Containers {
		meta.Containers = append(meta.Containers, ct.Name)
	}
	for _, ec := range pod.Spec.EphemeralContainers {
		meta.Containers = append(meta.Containers, ec.Name)
	}
	if len(meta.Containers) == 0 {
		return podLogMeta{}, fmt.Errorf("pod '%s' has no containers", podName)
	}
	if container == "" {
		container = meta.Containers[0]
	}
	known := false
	for _, name := range meta.Containers {
		known = known || name == container
	}
	if !known {
		return podLogMeta{}, fmt.Errorf("pod '%s' has no container '%s'", podName, container)
	}
	meta.Container = container
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == container {
			meta.Restarts = cs.RestartCount
		}
	}
	return meta, nil
}

func openLogStream(ctx context.Context, namespace string, meta podLogMeta, q podLogQuery, follow bool) (io.ReadCloser, error) {
	opts := &v1.PodLogOptions{Container: meta.Container, Follow: follow, Previous: q.previous, Timestamps: q.timestamps}
	if q.tail > 0 {
		opts.TailLines = ptr.To(q.tail)
	}
	if q.since > 0 {
		opts.SinceSeconds = ptr.To(q.since)
	}
	return kubeclient.Clientset.CoreV1().Pods(namespace).GetLogs(meta.Pod, opts).Stream(ctx)
}

// GET /pods/logs/:namespace/:podName -> the container log as text, like
// kubectl logs. Query: container, tail (default 500, 0 for all), since
// (seconds), timestamps, previous.
func GetPodLogs(c *gin.Context) {
	namespace := c.Param("namespace")
	q := parseLogQuery(c)
	meta, err := resolveLogTarget(namespace, c.Param("podName"), q.container)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	stream, err := openLogStream(ctx, namespace, meta, q, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer stream.Close()
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, stream); err != nil {
		log.Printf("⚠️ Log read of %s/%s stopped: %v", namespace, meta.Pod, err)
	}
}

// GET /pods/logs/ws/:namespace/:podName -> the tail of the log, then every
// new line as it is written, until the client closes. The first frame is a
// text frame with the metadata, log lines go as binary frames (one per line,
// raw bytes), and a final text frame says the stream ended, which happens
// when the container exits or when "previous" has been read through.
func PodLogsWebSocket(c *gin.Context) {
	namespace := c.Param("namespace")
	q := parseLogQuery(c)
	meta, err := resolveLogTarget(namespace, c.Param("podName"), q.container)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println("❌ Error WebSocket upgrade:", err)
		return
	}
	defer ws.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	control := func(kind string, payload any) error {
		frame, _ := json.Marshal(gin.H{"kind": kind, kind: payload})
		return ws.WriteMessage(websocket.TextMessage, frame)
	}
	if err := control("meta", meta); err != nil {
		return
	}

	stream, err := openLogStream(ctx, namespace, meta, q, true)
	if err != nil {
		_ = control("error", err.Error())
		return
	}
	defer stream.Close()

	// Reads from the client only serve to notice it is gone.
	go func() {
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := ws.WriteMessage(websocket.BinaryMessage, scanner.Bytes()); err != nil {
			return
		}
	}
	if ctx.Err() == nil {
		_ = control("end", true)
	}
}
