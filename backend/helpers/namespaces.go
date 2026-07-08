package helpers

import (
	"bufio"
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"kubendt/capabilities/capabilities"
	"kubendt/kubeclient"
	"kubendt/types"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// Sentinel errors from CreateNamespace, so handlers can map them to precise
// HTTP responses instead of string-matching. ErrNamespaceExistsEmpty and
// ErrNamespaceHasResources both mean "name is taken"; they differ only in
// whether the existing namespace already holds resources.
var (
	ErrNamespaceExistsEmpty  = stderrors.New("namespace already exists but is empty")
	ErrNamespaceHasResources = stderrors.New("namespace already exists and contains resources")
)

func CreateNamespaceFileDir(namespace string) error {
	path, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}
	return os.MkdirAll(path, 0755)
}

func DeleteNamespaceFileDir(namespace string) error {
	path, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}

	// If directory does not exist, nothing to do.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("could not stat namespace files dir %s: %w", path, err)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("could not read namespace files dir %s: %w", path, err)
	}

	// Only remove folder when empty. Never delete when it contains files/subfolders.
	if len(entries) > 0 {
		return nil
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("could not remove empty namespace files dir %s: %w", path, err)
	}

	return nil
}

// DeleteNamespaceFilesAll removes the entire namespace files directory and all its contents.
func DeleteNamespaceFilesAll(namespace string) error {
	path, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("could not remove namespace files dir %s: %w", path, err)
	}
	return nil
}

// NamespaceHasFiles returns true if the namespace files directory exists and contains any entries.
func NamespaceHasFiles(namespace string) bool {
	path, err := namespaceFilesDir(namespace)
	if err != nil {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

func isNamespaceNonEmpty(namespace string) (bool, error) {
	ctx := context.TODO()

	pods, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	if len(pods.Items) > 0 {
		return true, nil
	}

	// Thing to check (in the future):
	// deployments, services, etc.
	// Keeping these blocks here in case you want to enable them:

	// deployments, _ := kubeclient.Clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	// if len(deployments.Items) > 0 {
	//     return true, nil
	// }

	// services, _ := kubeclient.Clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	// if len(services.Items) > 0 {
	//     return true, nil
	// }

	return false, nil
}

func CreateNamespace(namespace string) error {
	// Check if the namespace already exists
	_, err := kubeclient.Clientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err == nil {
		// Namespace exists, checking whether it is empty
		nonEmpty, checkErr := isNamespaceNonEmpty(namespace)
		if checkErr != nil {
			return fmt.Errorf("error checking namespace %s content: %w", namespace, checkErr)
		}
		if nonEmpty {
			return fmt.Errorf("%s: %w", namespace, ErrNamespaceHasResources)
		}
		return fmt.Errorf("%s: %w", namespace, ErrNamespaceExistsEmpty)
	}

	// Create the namespace
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"kubendt/enabled": "true",
			},
		},
	}

	_, err = kubeclient.Clientset.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("could not create namespace %s: %w", namespace, err)
	}

	// Create files directory associated with namespace
	if err := CreateNamespaceFileDir(namespace); err != nil {
		return fmt.Errorf("could not create files directory for namespace %s: %w", namespace, err)
	}

	return nil
}

// DeleteNamespace deletes a KubeNDT-managed namespace and its associated resources.
// deletePositions: if true, also removes saved node positions from the database.
// deleteFiles: if true, removes all files stored in the namespace file-manager directory.
func DeleteNamespace(namespace string, deletePositions, deleteFiles bool) error {
	// Validate that it has the label "kubendt/enabled=true"
	ns, err := kubeclient.Clientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not get namespace %s: %w", namespace, err)
	}

	if ns.Labels["kubendt/enabled"] != "true" {
		return fmt.Errorf("namespace %s does not have the label 'kubendt/enabled=true'", namespace)
	}

	// Delete the namespace
	err = kubeclient.Clientset.CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("error deleting namespace %s: %w", namespace, err)
	}

	if deletePositions {
		if err := DeletePositionsByNamespace(namespace); err != nil {
			log.Printf("⚠️ Could not cleanup positions for %s: %v", namespace, err)
		}
	}

	if deleteFiles {
		if err := DeleteNamespaceFilesAll(namespace); err != nil {
			log.Printf("⚠️ Could not delete namespace files for %s: %v", namespace, err)
		}
	} else {
		// Always try to remove the directory if it is empty (cleanup).
		if err := DeleteNamespaceFileDir(namespace); err != nil {
			log.Printf("⚠️ Could not cleanup namespace files folder %s: %v", namespace, err)
		}
	}

	if err := DeleteNamespaceState(namespace); err != nil {
		log.Printf("⚠️ Could not cleanup namespace state for %s: %v", namespace, err)
	}

	if err := DeleteNamespaceLinkUIDRegistry(namespace); err != nil {
		log.Printf("⚠️ Could not cleanup link UID registry for %s: %v", namespace, err)
	}

	if err := DeleteNamespaceDriverOperationHistory(namespace); err != nil {
		log.Printf("⚠️ Could not cleanup driver operation history for %s: %v", namespace, err)
	}

	return nil
}

func GetInterfacesInNamespace(namespace string) (map[string]map[string]interface{}, error) {
	pods, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("error fetching pods in namespace %s: %w", namespace, err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	result := make(map[string]map[string]interface{})

	for _, pod := range pods.Items {
		wg.Add(1)

		go func(pod v1.Pod) {
			defer wg.Done()

			podName := pod.Name
			podType := pod.Labels["kubendt/type"]

			// Try via driver (VyOS: SSH to guest; others: default driver).
			drv, _ := GetDriverForListedPod(&pod)

			// Launch in parallel:
			//   - interface inspection (SSH show interfaces ~1.8s)
			//   - NAT/internet check (SSH show nat source rules ~1.8s)
			// Both are independent; running them sequentially would double the time.
			type intfResult struct {
				states map[string]bool
				err    error
			}
			type natResult struct {
				iface string
			}

			intfCh := make(chan intfResult, 1)
			natCh := make(chan natResult, 1)

			// Interface goroutine
			go func() {
				states := make(map[string]bool)
				if drv != nil {
					if inspector, ok := drv.(types.EffectiveInterfaceStateInspector); ok {
						s, inspectErr := inspector.GetEffectiveInterfaceStates(namespace, podName)
						if inspectErr != nil {
							log.Printf("⚠️ EffectiveInterfaceStateInspector failed for %s/%s: %v, falling back to ip a", namespace, podName, inspectErr)
						} else {
							intfCh <- intfResult{states: s}
							return
						}
					}
				}
				intfCh <- intfResult{states: states, err: nil}
			}()

			// NAT goroutine (routers with NATCapable only)
			go func() {
				if podType == "router" && drv != nil {
					if nat, ok := drv.(capabilities.NATCapable); ok {
						iface, _ := nat.GetSNATInterface(namespace, podName)
						natCh <- natResult{iface: iface}
						return
					}
				}
				natCh <- natResult{}
			}()

			// Collect interface result
			intfStates := make(map[string]bool)
			if r := <-intfCh; len(r.states) > 0 {
				intfStates = r.states
			}

			// Fallback to ip a if the driver returned nothing
			if len(intfStates) == 0 {
				intfReq := kubeclient.Clientset.CoreV1().RESTClient().
					Post().
					Resource("pods").
					Name(podName).
					Namespace(namespace).
					SubResource("exec").
					VersionedParams(&v1.PodExecOptions{
						Command: []string{"ip", "a"},
						Stdin:   false,
						Stdout:  true,
						Stderr:  true,
						TTY:     false,
					}, scheme.ParameterCodec)

				intfExec, err := remotecommand.NewSPDYExecutor(kubeclient.Config, "POST", intfReq.URL())
				if err != nil {
					log.Printf("❌ Executor failed for pod %s: %v", podName, err)
					<-natCh // drenar canal antes de salir
					return
				}

				var stdout, stderr bytes.Buffer
				err = intfExec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
					Stdout: &stdout,
					Stderr: &stderr,
				})
				if err != nil {
					log.Printf("❌ Exec failed for pod %s: %v", podName, err)
					<-natCh
					return
				}

				scanner := bufio.NewScanner(strings.NewReader(stdout.String()))
				var currentIntf string
				var isUp bool

				for scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())

					if matches := regexp.MustCompile(`^\d+: ([^:@]+)[:@].*<(.*?)>`).FindStringSubmatch(line); len(matches) > 2 {
						if currentIntf != "" && currentIntf != "eth0" && currentIntf != "lo" && !strings.HasPrefix(currentIntf, "tap") {
							intfStates[currentIntf] = isUp
						}
						currentIntf = matches[1]
						flags := matches[2]
						isUp = strings.Contains(flags, "UP")
					}
				}

				if currentIntf != "" && currentIntf != "eth0" && currentIntf != "lo" && !strings.HasPrefix(currentIntf, "tap") {
					intfStates[currentIntf] = isUp
				}
			}

			// Recoger resultado de NAT
			internetIface := (<-natCh).iface

			data := map[string]interface{}{
				"interfaces": intfStates,
			}
			if podType == "router" {
				if internetIface != "" {
					data["internet"] = internetIface
				} else {
					data["internet"] = false
				}
			}

			mu.Lock()
			result[podName] = data
			mu.Unlock()
		}(pod)
	}

	wg.Wait()
	return result, nil
}

func ValidateNamespaceEnabled(namespace string) error {
	ns, err := kubeclient.Clientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("namespace '%s' does not exist", namespace)
		}
		return fmt.Errorf("error validating namespace '%s': %w", namespace, err)
	}

	if ns.DeletionTimestamp != nil {
		return fmt.Errorf("namespace '%s' does not exist or is terminating", namespace)
	}

	if ns.Labels["kubendt/enabled"] != "true" {
		return fmt.Errorf("namespace '%s' is not enabled for KubeNDT", namespace)
	}

	return nil
}
