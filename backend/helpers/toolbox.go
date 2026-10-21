package helpers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"sync"
	"time"

	"kubendt/kubeclient"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// The toolbox is the ephemeral container capture, traceroute and traffic
// control inject into a pod when they need a tool its image lacks. It shares
// the pod's network namespace, one per pod, reused by every tool. Kubernetes
// never removes an ephemeral container, so the sleeper bounds its lifetime.

// DefaultToolboxImage ships tshark, traceroute, mtr, tc and iperf3 and pulls
// from Docker Hub. KUBENDT_TOOLBOX_IMAGE overrides it, for a mirror or the
// slim image under deploy/custom_images/toolbox.
const DefaultToolboxImage = "nicolaka/netshoot:v0.16"

const toolboxSleeperSeconds = "86400"

// toolboxStartTimeout bounds the wait for a fresh toolbox. The first pull of
// the image on a worker dominates it.
const toolboxStartTimeout = 120 * time.Second

// Containers injected before the rename are called capture-*; they stay
// reusable until their pods go away.
var toolboxNameRe = regexp.MustCompile(`^(toolbox|capture)-[a-z0-9]{8,16}$`)

// toolboxLocks serializes injection per pod, so concurrent tools converge on
// one container instead of each injecting its own.
var toolboxLocks sync.Map // "namespace/pod" -> *sync.Mutex

// ToolboxImage returns the image the toolbox runs.
func ToolboxImage() string {
	for _, name := range []string{"KUBENDT_TOOLBOX_IMAGE", "KUBENDT_CAPTURE_IMAGE"} {
		if img := os.Getenv(name); img != "" {
			return img
		}
	}
	return DefaultToolboxImage
}

// ValidToolboxName reports whether s is a toolbox container name we minted.
func ValidToolboxName(s string) bool { return toolboxNameRe.MatchString(s) }

func newToolboxName() string {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("toolbox-%x", time.Now().UnixNano()&0xffffffffff)
	}
	return "toolbox-" + hex.EncodeToString(b)
}

// EnsureToolbox returns a running toolbox of the pod, injecting one when there
// is none and waiting until it runs. onInject, when given, is called right
// after a fresh injection, before the wait, so a caller can report it.
func EnsureToolbox(namespace, podName string, onInject func()) (container string, reused bool, err error) {
	key := namespace + "/" + podName
	lkAny, _ := toolboxLocks.LoadOrStore(key, &sync.Mutex{})
	lk := lkAny.(*sync.Mutex)
	lk.Lock()
	defer lk.Unlock()

	if existing := findRunningToolbox(namespace, podName); existing != "" {
		return existing, true, nil
	}
	name, err := injectToolbox(namespace, podName)
	if err != nil {
		return "", false, err
	}
	if onInject != nil {
		onInject()
	}
	if err := waitToolboxRunning(namespace, podName, name, toolboxStartTimeout); err != nil {
		return "", false, err
	}
	return name, false, nil
}

// ToolboxRunning reports whether the named toolbox exists and runs.
func ToolboxRunning(namespace, podName, container string) bool {
	pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return false
	}
	for _, cs := range pod.Status.EphemeralContainerStatuses {
		if cs.Name == container {
			return cs.State.Running != nil
		}
	}
	return false
}

func findRunningToolbox(namespace, podName string) string {
	pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	for _, cs := range pod.Status.EphemeralContainerStatuses {
		if ValidToolboxName(cs.Name) && cs.State.Running != nil {
			return cs.Name
		}
	}
	return ""
}

// injectToolbox adds the sleeper container to the pod. NET_RAW opens the
// capture and ICMP sockets, NET_ADMIN covers tc and promiscuous mode.
func injectToolbox(namespace, podName string) (string, error) {
	ctx := context.TODO()
	pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("pod %s/%s not found: %w", namespace, podName, err)
	}
	name := newToolboxName()
	ec := v1.EphemeralContainer{
		EphemeralContainerCommon: v1.EphemeralContainerCommon{
			Name:            name,
			Image:           ToolboxImage(),
			ImagePullPolicy: v1.PullIfNotPresent,
			Command:         []string{"sleep", toolboxSleeperSeconds},
			SecurityContext: &v1.SecurityContext{
				Capabilities: &v1.Capabilities{Add: []v1.Capability{"NET_RAW", "NET_ADMIN"}},
			},
		},
	}
	pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, ec)
	if _, err := kubeclient.Clientset.CoreV1().Pods(namespace).UpdateEphemeralContainers(ctx, podName, pod, metav1.UpdateOptions{}); err != nil {
		return "", fmt.Errorf("could not inject the toolbox into %s/%s: %w", namespace, podName, err)
	}
	return name, nil
}

// waitToolboxRunning polls the pod until the container runs, or fails with
// the reason it will not (a pull error, an early exit, the timeout).
func waitToolboxRunning(namespace, podName, container string, timeout time.Duration) error {
	ctx := context.TODO()
	deadline := time.Now().Add(timeout)
	for {
		pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("error polling pod %s/%s: %w", namespace, podName, err)
		}
		for _, cs := range pod.Status.EphemeralContainerStatuses {
			if cs.Name != container {
				continue
			}
			switch {
			case cs.State.Running != nil:
				return nil
			case cs.State.Terminated != nil:
				return fmt.Errorf("toolbox container terminated early (%s)", cs.State.Terminated.Reason)
			case cs.State.Waiting != nil:
				switch cs.State.Waiting.Reason {
				case "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "CreateContainerError", "CrashLoopBackOff":
					return fmt.Errorf("toolbox container not starting: %s (%s)", cs.State.Waiting.Reason, cs.State.Waiting.Message)
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for toolbox container %s to start", container)
		}
		// A cached image starts in well under a second.
		time.Sleep(200 * time.Millisecond)
	}
}

// ExecStreamIntoContainer runs command in a container of the pod and streams
// its stdout into out. Cancelling ctx tears the exec down, which makes the
// kubelet kill the process: that is how a capture or a probe is stopped.
func ExecStreamIntoContainer(ctx context.Context, namespace, podName, container string, command []string, out io.Writer) error {
	req := kubeclient.Clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := kubeclient.NewExecutor("POST", req.URL())
	if err != nil {
		return fmt.Errorf("exec init error: %w", err)
	}

	var errBuf bytes.Buffer
	streamErr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: out,
		Stderr: &errBuf,
	})
	// A cancelled context is the normal stop, not an error.
	if streamErr != nil && ctx.Err() != nil {
		return nil
	}
	if streamErr != nil {
		if msg := errBuf.String(); msg != "" {
			return fmt.Errorf("%w: %s", streamErr, msg)
		}
		return streamErr
	}
	return nil
}

// ExecInContainer runs command in a container of the pod and returns its
// stdout and stderr, for short bounded calls.
func ExecInContainer(namespace, podName, container string, command []string) (stdout, stderr string, err error) {
	var outBuf, errBuf bytes.Buffer
	req := kubeclient.Clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := kubeclient.NewExecutor("POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("exec init error: %w", err)
	}
	err = exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdout: &outBuf,
		Stderr: &errBuf,
	})
	return outBuf.String(), errBuf.String(), err
}
