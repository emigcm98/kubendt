package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"kubendt/kubeclient"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// CommandExecutor is the interface that all executor types must satisfy.
// Concrete implementations are built by embedding KubectlBase (see base.go)
// and registered via Register or MustRegister.

const DefaultExecutorName = "kubectl"

type CommandExecutor interface {
	Name() string
	ExecCommand(podName, namespace string, command Command) error
	ExecCommandAndGet(podName, namespace string, command Command) (string, error)
}

type driverExecutorNamer interface {
	ExecutorName() string
}

var (
	executorsMu sync.RWMutex
	executors   = map[string]CommandExecutor{}
)

func Register(executor CommandExecutor) error {
	if executor == nil {
		return fmt.Errorf("executor cannot be nil")
	}

	name := strings.TrimSpace(executor.Name())
	if name == "" {
		return fmt.Errorf("executor name cannot be empty")
	}

	executorsMu.Lock()
	defer executorsMu.Unlock()
	if _, exists := executors[name]; exists {
		return fmt.Errorf("executor %q already registered", name)
	}
	executors[name] = executor
	return nil
}

func MustRegister(executor CommandExecutor) {
	if err := Register(executor); err != nil {
		panic(err)
	}
}

func Get(name string) (CommandExecutor, error) {
	executorsMu.RLock()
	defer executorsMu.RUnlock()

	executor, ok := executors[name]
	if !ok {
		return nil, fmt.Errorf("executor %q not registered", name)
	}
	return executor, nil
}

func ResolveForDriver(driver any) (CommandExecutor, string, error) {
	if d, ok := driver.(driverExecutorNamer); ok {
		name := strings.TrimSpace(d.ExecutorName())
		if name != "" {
			executor, err := Get(name)
			if err != nil {
				return nil, "", err
			}
			return executor, name, nil
		}
	}

	executor, err := Get(DefaultExecutorName)
	if err != nil {
		return nil, "", err
	}
	return executor, DefaultExecutorName, nil
}

// execTimeout is the global kubectl-exec deadline. Override with the
// KUBECTL_EXEC_TIMEOUT_SECONDS environment variable when running against
// slow clusters.
//
// Sizing rationale: the SSH layer used by ssh_qemu already detects dead
// sessions on its own via ConnectTimeout(5) + ServerAliveInterval(3) ×
// ServerAliveCountMax(2) = 11s, so this deadline does NOT need to be a
// dead-session detector. Its job is to bound legitimately-long commands.
// VyOS batched commits (delete + set + commit covering many subsystems
// like interfaces, NAT, OSPF, DNS, firewall) routinely take 10-20s the
// first time after the guest boots, when configd caches are cold. 30s
// gives comfortable headroom for those without delaying real failures.
var execTimeout = func() time.Duration {
	if v := os.Getenv("KUBECTL_EXEC_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 30 * time.Second
}()

func execViaKubectl(podName, namespace string, prefix []string, command []string, executorName string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("empty command for executor %q", executorName)
	}

	effectiveCommand := make([]string, 0, len(prefix)+len(command))
	effectiveCommand = append(effectiveCommand, prefix...)
	effectiveCommand = append(effectiveCommand, command...)

	cmdText := strings.Join(effectiveCommand, " ")
	startedAt := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()

	req := kubeclient.Clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Command: effectiveCommand,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(kubeclient.Config, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("exec init error for executor %q: %w", executorName, err)
	}

	var outBuf, errBuf bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &outBuf,
		Stderr: &errBuf,
	})
	duration := time.Since(startedAt)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("⏱️ Exec TIMEOUT executor='%s' pod='%s' ns='%s' after %s cmd=%q", executorName, podName, namespace, duration.Round(time.Millisecond), cmdText)
			return "", fmt.Errorf("exec timeout after %s", duration.Round(time.Millisecond))
		}
		combined := strings.TrimSpace(outBuf.String() + "\n" + errBuf.String())
		log.Printf("❌ Exec failed executor='%s' pod='%s' ns='%s' took=%s cmd=%q err=%v", executorName, podName, namespace, duration.Round(time.Millisecond), cmdText, err)
		return "", fmt.Errorf("exec error: %v | output: %s", err, combined)
	}

	// Surface execs that succeed but eat most of the deadline. Helps catch
	// gradual slowdowns (busy API server, slow guest after boot, growing
	// commit fanout) before they start tripping the timeout.
	if duration > (execTimeout*7)/10 {
		log.Printf("⚠️ Slow exec executor='%s' pod='%s' ns='%s' took=%s (%.0f%% of %s deadline) cmd=%q",
			executorName, podName, namespace,
			duration.Round(time.Millisecond),
			float64(duration)/float64(execTimeout)*100,
			execTimeout,
			cmdText)
	}

	// log.Printf("✅ Exec ok executor='%s' pod='%s' ns='%s' took=%s cmd=%q", executorName, podName, namespace, duration.Round(time.Millisecond), cmdText)
	return outBuf.String(), nil
}

func Exec(podName, namespace string, command []string) error {
	executor, err := Get(DefaultExecutorName)
	if err != nil {
		return err
	}
	return executor.ExecCommand(podName, namespace, NewArgsCommand(command))
}

func ExecAndGet(namespace, pod string, command []string) ([]byte, error) {
	req := kubeclient.Clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Command: command,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(kubeclient.Config, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("exec init error: %w", err)
	}

	var outBuf, errBuf bytes.Buffer
	if err := exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdout: &outBuf,
		Stderr: &errBuf,
	}); err != nil {
		return nil, fmt.Errorf("exec error: %v\n%s", err, errBuf.String())
	}
	return outBuf.Bytes(), nil
}

func CopyToPod(pod, namespace, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("cannot open local file %q: %w", localPath, err)
	}
	defer f.Close()

	// Stream the file into the pod via: sh -c "cat > <remotePath>"
	req := kubeclient.Clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Command: []string{"sh", "-c", fmt.Sprintf("cat > %q", remotePath)},
			Stdin:   true,
			Stdout:  false,
			Stderr:  true,
			TTY:     false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(kubeclient.Config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("exec init error for cp: %w", err)
	}

	var errBuf bytes.Buffer
	if err := exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdin:  io.NopCloser(f),
		Stderr: &errBuf,
	}); err != nil {
		return fmt.Errorf("error copying to pod: %v\n%s", err, errBuf.String())
	}
	return nil
}
