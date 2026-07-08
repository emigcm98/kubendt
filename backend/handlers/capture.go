package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"kubendt/helpers"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	// captureStartTimeout bounds how long we wait for the ephemeral capture
	// container to come up. The first pull of the capture image dominates this.
	captureStartTimeout = 120 * time.Second

	// Keepalive: ping the client regularly and expect pong traffic back, so an
	// idle interface (no packets) doesn't let a proxy or the browser drop the
	// socket. The browser auto-replies to pings, resetting the read deadline.
	capturePongWait   = 70 * time.Second
	capturePingPeriod = 30 * time.Second
	captureWriteWait  = 10 * time.Second
)

// CaptureWebSocket streams a live packet capture of one pod interface to the
// browser. It injects (or reuses, on reconnect) an ephemeral tshark container
// in the pod, execs tshark inside it, and forwards the standard Wireshark
// columns as tab-separated text frames. Control messages (container id,
// status, errors) are sent as JSON text frames.
//
// Protocol on the wire:
//   - JSON object with a "type" field  → control ("meta"|"status"|"error")
//   - any other text line              → one packet row (TSV: No, Time, Src,
//     Dst, Protocol, Len, Info)
//
// Query params:
//   - filter:    optional BPF capture filter
//   - container: optional existing capture container to reuse (reconnect /
//     start-after-stop), instead of injecting a new one
//
// Closing the socket cancels the exec, which kills tshark; the pcap it wrote
// stays in the container for later download via DownloadCapturePcap.
func CaptureWebSocket(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")
	iface := c.Param("interface")
	bpf := strings.TrimSpace(c.Query("filter"))
	reuse := strings.TrimSpace(c.Query("container"))

	pod, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !helpers.ValidIfaceName(iface) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid interface name"})
		return
	}
	if !helpers.ValidCaptureFilter(bpf) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "capture filter too long"})
		return
	}

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println("❌ Capture WebSocket upgrade error:", err)
		return
	}
	defer ws.Close()

	// gorilla forbids concurrent writers; the packet pump, control frames and
	// keepalive pings all funnel through this mutex.
	var writeMu sync.Mutex
	safeWrite := func(mt int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(captureWriteWait))
		return ws.WriteMessage(mt, data)
	}
	sendCtl := func(v any) {
		if b, err := json.Marshal(v); err == nil {
			_ = safeWrite(websocket.TextMessage, b)
		}
	}

	container, reused, err := helpers.EnsureCaptureContainer(namespace, pod, reuse)
	if err != nil {
		log.Printf("❌ Capture: ensure container failed for %s/%s: %v", namespace, pod, err)
		sendCtl(gin.H{"type": "error", "message": err.Error()})
		return
	}
	// Each run writes its own pcap file so an overlapping previous tshark (one
	// that hasn't fully exited on reconnect/restart) can't corrupt this one.
	runID := helpers.NewCaptureRunID()
	pcapPath := helpers.CapturePcapPathFor(runID)
	log.Printf("📡 Capture: %s %s on %s/%s (iface=%s filter=%q run=%s)",
		map[bool]string{true: "reusing", false: "injected"}[reused], container, namespace, pod, iface, bpf, runID)
	sendCtl(gin.H{"type": "meta", "container": container, "pod": pod, "namespace": namespace, "iface": iface, "reused": reused, "pcap": runID})
	if !reused {
		sendCtl(gin.H{"type": "status", "state": "starting"})
		if err := helpers.WaitEphemeralRunning(namespace, pod, container, captureStartTimeout); err != nil {
			log.Printf("❌ Capture: container %s did not start: %v", container, err)
			sendCtl(gin.H{"type": "error", "message": err.Error()})
			return
		}
	}
	sendCtl(gin.H{"type": "status", "state": "capturing"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdoutReader, stdoutWriter := io.Pipe()

	// Keepalive: extend the read deadline on every pong.
	_ = ws.SetReadDeadline(time.Now().Add(capturePongWait))
	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(capturePongWait))
		return nil
	})
	go func() {
		t := time.NewTicker(capturePingPeriod)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := safeWrite(websocket.PingMessage, nil); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Detect client disconnect (or a missed pong via the read deadline) →
	// cancel the exec and unblock the scanner. Only ever reads.
	go func() {
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				if websocket.IsCloseError(err,
					websocket.CloseNormalClosure,
					websocket.CloseGoingAway,
					websocket.CloseNoStatusReceived) {
					log.Printf("ℹ️ Capture closed by client (pod=%s, iface=%s, container=%s)", pod, iface, container)
				} else {
					log.Printf("ℹ️ Capture stream ended (pod=%s, container=%s): %v", pod, container, err)
				}
				cancel()
				_ = stdoutWriter.Close()
				return
			}
		}
	}()

	// Packet pump: forwards each TSV line as a text frame.
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		defer stdoutReader.Close()
		sc := bufio.NewScanner(stdoutReader)
		sc.Buffer(make([]byte, 256*1024), 8*1024*1024)
		for sc.Scan() {
			if err := safeWrite(websocket.TextMessage, sc.Bytes()); err != nil {
				cancel()
				return
			}
		}
	}()

	cmd := helpers.BuildCaptureCommand(iface, bpf, pcapPath)
	execErr := helpers.ExecStreamIntoContainer(ctx, namespace, pod, container, cmd, stdoutWriter)
	_ = stdoutWriter.Close()
	cancel()
	<-streamDone

	if execErr != nil {
		log.Printf("❌ Capture stream ended with error (%s): %v", container, execErr)
		sendCtl(gin.H{"type": "error", "message": fmt.Sprintf("capture ended: %v", execErr)})
		return
	}
	sendCtl(gin.H{"type": "status", "state": "stopped"})
}

// DownloadCapturePcap streams a capture's pcap back to the client. With no
// "frames" query it returns the whole capture; with "frames" (a comma list of
// numbers/ranges, e.g. "1-5,9,12-20") it returns only those frames via editcap
// — used to export exactly the packets currently shown by a display filter.
func DownloadCapturePcap(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")
	container := c.Param("container")
	pcapID := strings.TrimSpace(c.Query("pcap"))
	framesQuery := strings.TrimSpace(c.Query("frames"))

	pod, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !helpers.ValidCaptureContainer(container) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid capture id"})
		return
	}
	if !helpers.ValidPcapId(pcapID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pcap id"})
		return
	}
	pcapPath := helpers.CapturePcapPathFor(pcapID)

	var command []string
	suffix := ""
	if framesQuery != "" {
		tokens, ok := helpers.ParseFrameRanges(framesQuery)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid frames selector"})
			return
		}
		command = helpers.FilteredPcapCommand(pcapPath, tokens)
		suffix = "-filtered"
	} else {
		command = helpers.CatCommand(pcapPath)
	}

	filename := fmt.Sprintf("%s-%s%s.pcap", pod, container, suffix)
	c.Header("Content-Type", "application/vnd.tcpdump.pcap")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	if err := helpers.ExecStreamIntoContainer(context.Background(), namespace, pod, container, command, c.Writer); err != nil {
		log.Printf("❌ Capture pcap download failed (%s): %v", container, err)
		// Headers are likely already sent; nothing more we can cleanly do.
	}
}

// ClearCapture empties a stopped capture's pcap so a subsequent export starts
// fresh. The frontend only calls this when not actively capturing (a live
// Clear restarts the capture instead, which truncates the file via tshark -w).
func ClearCapture(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")
	container := c.Param("container")
	pcapID := strings.TrimSpace(c.Query("pcap"))

	pod, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !helpers.ValidCaptureContainer(container) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid capture id"})
		return
	}
	if !helpers.ValidPcapId(pcapID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pcap id"})
		return
	}

	if _, stderr, err := helpers.ExecInContainer(namespace, pod, container, helpers.TruncateCaptureCommand(helpers.CapturePcapPathFor(pcapID))); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("clear failed: %v (%s)", err, stderr)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "cleared"})
}

// GetCapturePacketDetail re-dissects a single packet from a capture's saved
// pcap into a full JSON protocol tree (Wireshark's own dissection), for the
// detail pane when a row is selected.
func GetCapturePacketDetail(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")
	container := c.Param("container")
	num := c.Param("num")
	pcapID := strings.TrimSpace(c.Query("pcap"))

	pod, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !helpers.ValidCaptureContainer(container) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid capture id"})
		return
	}
	if !helpers.ValidPcapId(pcapID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pcap id"})
		return
	}
	if !helpers.ValidFrameNumber(num) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid frame number"})
		return
	}

	stdout, stderr, err := helpers.ExecInContainer(namespace, pod, container, helpers.PacketDetailCommand(helpers.CapturePcapPathFor(pcapID), num))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("dissection failed: %v (%s)", err, stderr)})
		return
	}
	if strings.TrimSpace(stdout) == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "packet not found"})
		return
	}
	c.Header("Content-Type", "application/json")
	c.String(http.StatusOK, stdout)
}
