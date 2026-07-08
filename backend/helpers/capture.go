package helpers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"kubendt/kubeclient"
	"os"
	"regexp"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// CapturePcapPathFor returns the in-container path where a capture run writes
// its pcap. A reused container hosts successive runs (start/stop/reconnect);
// each run gets its OWN file (keyed by a run id) so a freshly started tshark
// can never share a file with a previous one that hasn't fully exited yet —
// two writers on one path produce a sparse, half-zeroed, corrupt pcap.
// Classic pcap (not pcapng) so the download opens anywhere.
func CapturePcapPathFor(runID string) string {
	return "/tmp/kubendt-capture-" + runID + ".pcap"
}

// captureSleeperSeconds bounds the lifetime of the injected ephemeral
// container. The capture process itself is killed the moment the client
// disconnects (the exec stream is torn down); this sleeper only keeps the
// pcap file reachable for later download. Kubernetes does not allow removing
// an ephemeral container from a pod's spec, so this backstop guarantees the
// (idle, tiny) husk disappears on its own even if the topology lives long.
const captureSleeperSeconds = "86400"

// DefaultCaptureImage ships tshark/dumpcap out of the box and is pullable from
// Docker Hub, so capture works with zero image-building. Override with the
// KUBENDT_CAPTURE_IMAGE env var (e.g. an air-gapped mirror or the slim image
// under deploy/custom_images/capture/).
const DefaultCaptureImage = "nicolaka/netshoot:latest"

var (
	ifaceNameRe        = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
	captureNameRe      = regexp.MustCompile(`^capture-[a-z0-9]{8,16}$`)
	pcapIDRe           = regexp.MustCompile(`^[a-z0-9]{8,20}$`)
	frameNumberRe      = regexp.MustCompile(`^[0-9]{1,12}$`)
	frameRangeRe       = regexp.MustCompile(`^[0-9]{1,12}(-[0-9]{1,12})?$`)
	maxCaptureFilexLen = 512
)

// CaptureImage returns the container image used for packet capture.
func CaptureImage() string {
	if img := os.Getenv("KUBENDT_CAPTURE_IMAGE"); img != "" {
		return img
	}
	return DefaultCaptureImage
}

// ValidIfaceName reports whether s is a safe interface name to hand to tshark.
func ValidIfaceName(s string) bool { return ifaceNameRe.MatchString(s) }

// ValidCaptureContainer reports whether s is a capture container name we minted.
func ValidCaptureContainer(s string) bool { return captureNameRe.MatchString(s) }

// ValidFrameNumber reports whether s is a plausible frame number.
func ValidFrameNumber(s string) bool { return frameNumberRe.MatchString(s) }

// ValidCaptureFilter reports whether a BPF capture filter is within bounds.
// The filter is always passed as a single argv element (never through a
// shell), so length is the only real concern.
func ValidCaptureFilter(s string) bool { return len(s) <= maxCaptureFilexLen }

// ParseFrameRanges validates a comma-separated list of frame numbers/ranges
// (e.g. "1-5,9,12-20") and returns the tokens as separate argv elements for
// editcap. Returns false if any token is malformed or the list is too large.
func ParseFrameRanges(s string) ([]string, bool) {
	if s == "" {
		return nil, false
	}
	tokens := strings.Split(s, ",")
	if len(tokens) > 4096 {
		return nil, false
	}
	for _, t := range tokens {
		if !frameRangeRe.MatchString(t) {
			return nil, false
		}
	}
	return tokens, true
}

func newCaptureContainerName() string {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		// rand failure is effectively impossible; fall back to a time-based id.
		return fmt.Sprintf("capture-%x", time.Now().UnixNano()&0xffffffffff)
	}
	return "capture-" + hex.EncodeToString(b)
}

// InjectCaptureContainer adds an ephemeral "sleeper" container to the target
// pod and returns its name. The container shares the pod network namespace, so
// every data-plane interface (ethN, and the QEMU-side tapN) is visible to it
// for capture. The actual tshark process is exec'd into this container later.
func InjectCaptureContainer(namespace, podName string) (string, error) {
	ctx := context.TODO()
	pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("pod %s/%s not found: %w", namespace, podName, err)
	}

	name := newCaptureContainerName()
	ec := v1.EphemeralContainer{
		EphemeralContainerCommon: v1.EphemeralContainerCommon{
			Name:            name,
			Image:           CaptureImage(),
			ImagePullPolicy: v1.PullIfNotPresent,
			// Keep the container alive so we can exec tshark/cat into it; the
			// capture lifecycle is owned by the exec streams, not this command.
			Command: []string{"sleep", captureSleeperSeconds},
			SecurityContext: &v1.SecurityContext{
				Capabilities: &v1.Capabilities{
					// NET_RAW is required to open the capture socket; NET_ADMIN
					// covers promiscuous-mode toggling on some setups.
					Add: []v1.Capability{"NET_RAW", "NET_ADMIN"},
				},
			},
		},
	}

	pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, ec)
	if _, err := kubeclient.Clientset.CoreV1().Pods(namespace).UpdateEphemeralContainers(ctx, podName, pod, metav1.UpdateOptions{}); err != nil {
		return "", fmt.Errorf("could not inject capture container into %s/%s: %w", namespace, podName, err)
	}
	return name, nil
}

// WaitEphemeralRunning blocks until the named ephemeral container reports
// Running, or returns an error describing why it didn't (image pull failure,
// early termination, or timeout). The first pull of the capture image can take
// a while, hence the generous default timeout chosen by callers.
func WaitEphemeralRunning(namespace, podName, container string, timeout time.Duration) error {
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
				return fmt.Errorf("capture container terminated early (%s)", cs.State.Terminated.Reason)
			case cs.State.Waiting != nil:
				switch cs.State.Waiting.Reason {
				case "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "CreateContainerError", "CrashLoopBackOff":
					return fmt.Errorf("capture container not starting: %s (%s)", cs.State.Waiting.Reason, cs.State.Waiting.Message)
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for capture container %s to start", container)
		}
		time.Sleep(time.Second)
	}
}

// BuildCaptureCommand returns the tshark argv that both writes the pcapng to
// disk (for later download / re-dissection) and streams the standard Wireshark
// columns (No/Time/Source/Destination/Protocol/Length/Info) as tab-separated
// lines on stdout. An optional BPF capture filter is appended as a single argv
// element, so it never touches a shell.
func BuildCaptureCommand(iface, bpf, pcapPath string) []string {
	cmd := []string{
		"tshark",
		"-i", iface,
		"-w", pcapPath, // write the pcap alongside the live stream
		"-F", "pcap", // classic pcap (not pcapng) so it opens anywhere
		"-P", // force per-packet printing even though -w is set
		"-l", // line-buffer stdout so rows arrive live
		"-n", // no name resolution (faster, no DNS noise)
		"-T", "fields",
		"-E", "separator=/t",
		"-e", "frame.number",
		"-e", "frame.time_relative",
		"-e", "_ws.col.Source",
		"-e", "_ws.col.Destination",
		"-e", "_ws.col.Protocol",
		"-e", "frame.len",
		"-e", "_ws.col.Info",
	}
	if bpf != "" {
		cmd = append(cmd, "-f", bpf)
	}
	return cmd
}

// PacketDetailCommand returns the tshark argv that re-dissects a single packet
// from the saved pcap into a full JSON protocol tree.
func PacketDetailCommand(pcapPath, frameNumber string) []string {
	return []string{
		"tshark",
		"-r", pcapPath,
		"-Y", "frame.number==" + frameNumber,
		"-T", "json",
	}
}

// FilteredPcapCommand returns the editcap argv that extracts the given frame
// numbers/ranges from the saved pcap and writes a pcap to stdout ("-").
func FilteredPcapCommand(pcapPath string, frameTokens []string) []string {
	cmd := []string{"editcap", "-F", "pcap", "-r", pcapPath, "-"}
	return append(cmd, frameTokens...)
}

// CatCommand streams a file to stdout (used to download a whole pcap).
func CatCommand(pcapPath string) []string {
	return []string{"cat", pcapPath}
}

// TruncateCaptureCommand empties the saved pcap so a later export reflects only
// what was captured after a Clear. Only safe while no tshark holds the file
// open (callers stop the capture first).
func TruncateCaptureCommand(pcapPath string) []string {
	return []string{"sh", "-c", ": > " + pcapPath}
}

// NewCaptureRunID returns a short random id identifying one capture run, used
// to name its pcap file so concurrent/overlapping runs never share a file.
func NewCaptureRunID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano()&0xffffffffffff)
	}
	return hex.EncodeToString(b)
}

// ValidPcapId reports whether s is a capture run id we minted.
func ValidPcapId(s string) bool { return pcapIDRe.MatchString(s) }

// EphemeralRunning reports whether the named ephemeral container currently
// exists and is in the Running state.
func EphemeralRunning(namespace, podName, container string) bool {
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

// EnsureCaptureContainer reuses an existing, still-running capture container
// when the client provides one (reconnect / start-after-stop), or injects a
// fresh one otherwise. Returns the container name and whether it was reused.
func EnsureCaptureContainer(namespace, podName, existing string) (string, bool, error) {
	if existing != "" && ValidCaptureContainer(existing) && EphemeralRunning(namespace, podName, existing) {
		return existing, true, nil
	}
	name, err := InjectCaptureContainer(namespace, podName)
	return name, false, err
}

// ExecStreamIntoContainer runs command in the given (ephemeral) container and
// streams its stdout into out. Cancelling ctx tears the exec stream down,
// which makes the kubelet kill the remote process — this is how a capture is
// stopped cleanly. stderr is captured for diagnostics and surfaced on error.
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

	exec, err := remotecommand.NewSPDYExecutor(kubeclient.Config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("exec init error: %w", err)
	}

	var errBuf bytes.Buffer
	streamErr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: out,
		Stderr: &errBuf,
	})
	// A context cancellation is the normal "stop capture" path, not an error.
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

// ExecInContainer runs command in the given container and returns its stdout
// and stderr as strings. Used for short, bounded calls (packet detail, cat).
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

	exec, err := remotecommand.NewSPDYExecutor(kubeclient.Config, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("exec init error: %w", err)
	}
	err = exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdout: &outBuf,
		Stderr: &errBuf,
	})
	return outBuf.String(), errBuf.String(), err
}
