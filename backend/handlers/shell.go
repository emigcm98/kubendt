package handlers

import (
	"context"
	"encoding/json"
	"io"
	"kubendt/helpers"
	"kubendt/kubeclient"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type TerminalSize struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type WSMessage struct {
	Type string        `json:"type"` // "input" o "resize"
	Data string        `json:"data"` // Para input
	Size *TerminalSize `json:"size"` // Para resize
}

func InteractiveShellWebSocket(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")
	mode := strings.ToLower(strings.TrimSpace(c.DefaultQuery("mode", "auto")))
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

	isQemuPod := false
	isSerialShellPod := false
	if podObj.Labels != nil {
		isQemuPod = podObj.Labels["kubendt/qemu"] == "true" || podObj.Labels["kubendt/runtime"] == "qemu"
		isSerialShellPod = isQemuPod || podObj.Labels["kubendt/shell-mode"] == "serial"
	}

	if mode != "auto" && mode != "sh" && mode != "serial" && mode != "vtysh" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid shell mode"})
		return
	}

	if mode == "serial" && !isSerialShellPod {
		c.JSON(http.StatusBadRequest, gin.H{"error": "serial shell is not enabled for this pod"})
		return
	}

	useAttach := (mode == "serial") || (mode == "auto" && isSerialShellPod)

	containerName := ""
	if len(podObj.Spec.Containers) > 0 {
		containerName = podObj.Spec.Containers[0].Name
	}

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println("❌ Error WebSocket upgrade:", err)
		return
	}

	defer ws.Close()

	// Keep backwards-compatible "auto" mode while allowing explicit shell selection.
	shellToUse := "sh"
	var shellCmd []string
	if mode == "vtysh" {
		shellCmd = []string{"vtysh"}
	} else if !useAttach {
		checkReq := kubeclient.Clientset.CoreV1().RESTClient().
			Post().
			Resource("pods").
			Name(pod).
			Namespace(namespace).
			SubResource("exec").
			VersionedParams(&v1.PodExecOptions{
				Command: []string{"which", "bash"},
				Stdin:   false,
				Stdout:  true,
				Stderr:  true,
				TTY:     false,
			}, scheme.ParameterCodec)

		checkExec, err := remotecommand.NewSPDYExecutor(kubeclient.Config, "POST", checkReq.URL())
		if err == nil {
			checkOut := &strings.Builder{}
			checkErr := checkExec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
				Stdout: checkOut,
				Stderr: checkOut,
			})
			if checkErr == nil && strings.Contains(checkOut.String(), "bash") {
				shellToUse = "bash"
			}
		}

		if shellToUse == "bash" {
			shellCmd = []string{"bash", "-i"}
		} else {
			shellCmd = []string{"sh", "-i"}
		}
	}

	stdoutReader, stdoutWriter := io.Pipe()
	stdinReader, stdinWriter := io.Pipe()

	// Channel to handle resize changes
	sizeChan := make(chan remotecommand.TerminalSize, 10)

	runExecStream := func(command []string) error {
		req := kubeclient.Clientset.CoreV1().RESTClient().
			Post().
			Resource("pods").
			Name(pod).
			Namespace(namespace).
			SubResource("exec").
			VersionedParams(&v1.PodExecOptions{
				Command: command,
				Stdin:   true,
				Stdout:  true,
				Stderr:  true,
				TTY:     true,
			}, scheme.ParameterCodec)

		exec, err := remotecommand.NewSPDYExecutor(kubeclient.Config, "POST", req.URL())
		if err != nil {
			return err
		}

		return exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
			Stdin:             stdinReader,
			Stdout:            stdoutWriter,
			Stderr:            stdoutWriter,
			Tty:               true,
			TerminalSizeQueue: &sizeQueue{ch: sizeChan},
		})
	}

	runAttachStream := func() error {
		req := kubeclient.Clientset.CoreV1().RESTClient().
			Post().
			Resource("pods").
			Name(pod).
			Namespace(namespace).
			SubResource("attach").
			VersionedParams(&v1.PodAttachOptions{
				Container: containerName,
				Stdin:     true,
				Stdout:    true,
				Stderr:    true,
				TTY:       true,
			}, scheme.ParameterCodec)

		exec, err := remotecommand.NewSPDYExecutor(kubeclient.Config, "POST", req.URL())
		if err != nil {
			return err
		}

		return exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
			Stdin:             stdinReader,
			Stdout:            stdoutWriter,
			Stderr:            stdoutWriter,
			Tty:               true,
			TerminalSizeQueue: &sizeQueue{ch: sizeChan},
		})
	}

	// Channel to signal when client sends first size
	firstSizeReceivedChan := make(chan bool, 1)
	var firstSizeReceived bool

	// Receive data from user
	go func() {
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				// Distinguish a clean client close (Normal/GoingAway/NoStatus,
				// also our own 1000 sent from the frontend) from a real broken
				// connection. Only the latter is interesting in the logs.
				if websocket.IsCloseError(err,
					websocket.CloseNormalClosure,
					websocket.CloseGoingAway,
					websocket.CloseNoStatusReceived) {
					log.Printf("ℹ️ Shell closed by client (pod=%s, mode=%s)", pod, mode)
				} else if websocket.IsUnexpectedCloseError(err,
					websocket.CloseNormalClosure,
					websocket.CloseGoingAway,
					websocket.CloseNoStatusReceived,
					websocket.CloseAbnormalClosure) {
					log.Printf("🔴 Unexpected error reading WebSocket: %v", err)
				}
				stdinWriter.Write([]byte("exit\n"))
				stdinWriter.Close()
				return
			}

			// Intentar parsear como mensaje estructurado JSON
			var msg WSMessage
			if err := json.Unmarshal(data, &msg); err == nil {
				// Es un mensaje JSON estructurado
				if msg.Type == "resize" && msg.Size != nil {
					// Mark first size as received
					if !firstSizeReceived {
						firstSizeReceived = true
						firstSizeReceivedChan <- true
					}
					// Send to resize channel
					select {
					case sizeChan <- remotecommand.TerminalSize{
						Width:  msg.Size.Cols,
						Height: msg.Size.Rows,
					}:
					default:
						// Vaciar el canal y enviar el nuevo
						select {
						case <-sizeChan:
						default:
						}
						sizeChan <- remotecommand.TerminalSize{
							Width:  msg.Size.Cols,
							Height: msg.Size.Rows,
						}
					}
					continue
				} else if msg.Type == "input" {
					// Escribir el input
					if _, err := stdinWriter.Write([]byte(msg.Data)); err != nil {
						return
					}
					continue
				}
			}

			// Si no es JSON o el parse falla, tratarlo como input raw (compatibilidad)
			if _, err := stdinWriter.Write(data); err != nil {
				return
			}
		}
	}()

	// Send data from pod to user
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stdoutReader.Read(buf)
			if err != nil {
				return
			}
			if err := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				return
			}
		}
	}()

	// WAIT for client to send first size BEFORE starting stream
	select {
	case <-firstSizeReceivedChan:
	case <-time.After(3 * time.Second):
	}
	if useAttach {
		err = runAttachStream()
	} else {
		err = runExecStream(shellCmd)
	}
	if err != nil {
		// When the client closes the WebSocket we feed "exit\n" + EOF to the
		// pod's shell; depending on the timing the shell may terminate with
		// 127 (command not found via SIGPIPE) or 130 (SIGINT). Both are the
		// expected outcome of a client-initiated close, log them as info,
		// not as errors, to keep the operator logs clean.
		msg := err.Error()
		if strings.Contains(msg, "command terminated with exit code 127") ||
			strings.Contains(msg, "command terminated with exit code 130") ||
			strings.Contains(msg, "command terminated with exit code 129") {
			log.Printf("ℹ️ Interactive shell closed (client disconnect): %s", msg)
		} else {
			log.Println("❌ Error in interactive stream:", err)
			log.Printf("exec.Stream finished with error: %v", err)
		}
	}
}

// sizeQueue implementa remotecommand.TerminalSizeQueue
type sizeQueue struct {
	ch chan remotecommand.TerminalSize
}

func (s *sizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-s.ch
	if !ok {
		return nil
	}
	return &size
}
