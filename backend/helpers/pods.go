package helpers

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	drvers_registry "kubendt/drivers/registry"
	"kubendt/kubeclient"
	"kubendt/types"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/ptr"
)

func ExecInPod(namespace, podName string, command []string) (stdout, stderr string, err error) {
	req := kubeclient.Clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
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
		return "", "", fmt.Errorf("exec init error: %w", err)
	}

	var outBuf, errBuf bytes.Buffer
	err = exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdout: &outBuf,
		Stderr: &errBuf,
	})
	return strings.TrimSpace(outBuf.String()), strings.TrimSpace(errBuf.String()), err
}

func GetInterfacesFromPod(podName, namespace string) ([]map[string]string, error) {
	stdout, stderr, err := ExecInPod(namespace, podName, []string{"ip", "a"})
	if err != nil {
		return nil, fmt.Errorf("error executing command in pod: %w\nstderr: %s", err, stderr)
	}

	scanner := bufio.NewScanner(strings.NewReader(stdout))
	var currentIntf, currentMac, currentIP, currentState string
	var result []map[string]string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if matches := regexp.MustCompile(`^\d+: ([^:@]+)[:@].*<([^>]*)>`).FindStringSubmatch(line); len(matches) > 2 {
			if currentIntf != "" && !strings.HasPrefix(currentIntf, "tap") && !types.IsPseudoInterface(currentIntf) && currentMac != "" {
				result = append(result, map[string]string{
					"interface": currentIntf,
					"mac":       currentMac,
					"ipv4":      currentIP,
					"state":     currentState,
				})
			}
			currentIntf = matches[1]
			currentMac = ""
			currentIP = ""
			flags := matches[2]
			if strings.Contains(flags, "UP") {
				currentState = "up"
			} else {
				currentState = "down"
			}
			continue
		}

		if matches := regexp.MustCompile(`link/ether ([0-9a-f:]{17})`).FindStringSubmatch(line); len(matches) > 1 {
			currentMac = matches[1]
		}

		if matches := regexp.MustCompile(`inet (\d+\.\d+\.\d+\.\d+/\d+)`).FindStringSubmatch(line); len(matches) > 1 {
			currentIP = matches[1]
		}
	}

	if currentIntf != "" && currentIntf != "eth0" && !strings.HasPrefix(currentIntf, "tap") && currentMac != "" {
		result = append(result, map[string]string{
			"interface": currentIntf,
			"mac":       currentMac,
			"ipv4":      currentIP,
			"state":     currentState,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning command output: %w", err)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i]["interface"] < result[j]["interface"]
	})

	return result, nil
}

func CreateEnvConfigMapFromNode(namespace string, node types.NodeSpec) error {
	configMapName := fmt.Sprintf("%s-env", node.Name)

	envVars := make(map[string]string)
	for key, value := range node.Env {
		envVars[key] = value
	}

	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
		},
		Data: envVars,
	}

	_, err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Create(context.TODO(), configMap, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			current, getErr := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Get(context.TODO(), configMapName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("error fetching existing ConfigMap %s: %w", configMapName, getErr)
			}
			current.Data = envVars
			if _, updateErr := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Update(context.TODO(), current, metav1.UpdateOptions{}); updateErr != nil {
				return fmt.Errorf("error updating ConfigMap %s: %w", configMapName, updateErr)
			}
			return nil
		}
		return fmt.Errorf("error creating ConfigMap %s: %w", configMapName, err)
	}

	return nil
}

// resolveDriverForProbe returns a driver instance for the given driver name,
// or nil if the name is empty or the driver is not registered.
// Used only to check for ReadinessProbeProvider at pod creation time.
func resolveDriverForProbe(driverName string) any {
	if driverName == "" {
		return nil
	}
	drv, err := drvers_registry.NewByName(driverName)
	if err != nil {
		return nil
	}
	return drv
}

// buildReadinessProbe returns a Kubernetes readiness probe tailored to the pod type.
// If the driver for the node implements types.ReadinessProbeProvider, its probe
// spec is used. Otherwise a lightweight default ("command -v ip") is returned.
func buildReadinessProbe(driver any) *v1.Probe {
	if provider, ok := driver.(types.ReadinessProbeProvider); ok {
		spec := provider.ReadinessProbeCommands()
		return &v1.Probe{
			ProbeHandler: v1.ProbeHandler{
				Exec: &v1.ExecAction{Command: spec.Command},
			},
			InitialDelaySeconds: spec.InitialDelaySeconds,
			PeriodSeconds:       spec.PeriodSeconds,
			TimeoutSeconds:      spec.TimeoutSeconds,
			FailureThreshold:    spec.FailureThreshold,
		}
	}
	return &v1.Probe{
		ProbeHandler: v1.ProbeHandler{
			Exec: &v1.ExecAction{
				Command: []string{"sh", "-c", "command -v ip"},
			},
		},
		InitialDelaySeconds: 5,
		PeriodSeconds:       5,
		FailureThreshold:    12,
	}
}

func CreateNetworkStatefulSet(namespace string, node types.NodeSpec, validMounts []types.MountSpec) error {
	var commands []string
	useCustomCommand := false
	shellMode := strings.ToLower(strings.TrimSpace(node.ShellMode))
	if shellMode != "serial" {
		shellMode = "sh"
	}
	if node.Qemu {
		shellMode = "serial"
	}
	useSerialShell := shellMode == "serial"
	if len(node.Commands) > 0 {
		commands = node.Commands
		useCustomCommand = true
	}

	// Create environment variables ConfigMap
	if err := CreateEnvConfigMapFromNode(namespace, node); err != nil {
		return err
	}

	configMapName := fmt.Sprintf("%s-env", node.Name)

	container := v1.Container{
		Name:  node.Name,
		Image: node.Image,
		EnvFrom: []v1.EnvFromSource{
			{
				ConfigMapRef: &v1.ConfigMapEnvSource{
					LocalObjectReference: v1.LocalObjectReference{
						Name: configMapName,
					},
				},
			},
		},
		SecurityContext: &v1.SecurityContext{
			Capabilities: &v1.Capabilities{
				Add: []v1.Capability{"NET_ADMIN"},
			},
		},
		ReadinessProbe: buildReadinessProbe(resolveDriverForProbe(node.Driver)),
	}

	if useSerialShell {
		container.Stdin = true
		container.TTY = true
	}

	if useCustomCommand {
		container.Command = commands
	}

	// --- Add user-defined mounts ---
	var volumeMounts []v1.VolumeMount
	var volumes []v1.Volume

	for _, mount := range validMounts {
		volumeName := SanitizeVolumeName(mount.File)
		dataKey := SanitizeConfigMapDataKey(mount.File)

		// SubPath mounts on ConfigMap/Secret are read-only in practice.
		volumeMounts = append(volumeMounts, v1.VolumeMount{
			Name:      volumeName,
			MountPath: mount.MountTo,
			SubPath:   dataKey,
			ReadOnly:  true,
		})

		resourceName, isSecret := MountResourceNameForFile(namespace, mount.File)
		if isSecret {
			volumes = append(volumes, v1.Volume{
				Name: volumeName,
				VolumeSource: v1.VolumeSource{
					Secret: &v1.SecretVolumeSource{
						SecretName: resourceName,
					},
				},
			})
		} else {
			volumes = append(volumes, v1.Volume{
				Name: volumeName,
				VolumeSource: v1.VolumeSource{
					ConfigMap: &v1.ConfigMapVolumeSource{
						LocalObjectReference: v1.LocalObjectReference{
							Name: resourceName,
						},
					},
				},
			})
		}
	}

	container.VolumeMounts = volumeMounts

	effectiveDevices := ResolveEffectiveDevicesForNode(node)

	// --- Add host devices ---
	for _, dev := range effectiveDevices {
		devName := filepath.Base(dev.Path) // take the volume name
		volumeName := SanitizeVolumeName(devName)
		volumes = append(volumes, v1.Volume{
			Name: volumeName,
			VolumeSource: v1.VolumeSource{
				HostPath: &v1.HostPathVolumeSource{
					Path: dev.Path,
					Type: new(v1.HostPathType), // optional: CharDevice
				},
			},
		})
		container.VolumeMounts = append(container.VolumeMounts, v1.VolumeMount{
			Name:      volumeName,
			MountPath: dev.Path,
		})
	}

	// QEMU pods need to know the exact number of dataplane interfaces they
	// should see before snapshotting and launching QEMU, otherwise a race
	// between meshnet wiring and our own deletePeerVethsForPod cleanup can
	// leave the entrypoint snapshotting a partial NIC list, baking a wrong
	// QEMU command line. We expose the per-pod expected count through a
	// namespace-scoped internal ConfigMap that the backend rebuilds after
	// every topology change (see helpers/iface_counts.go).
	//
	// Marked optional: the entrypoint falls back to a fixed sleep if the
	// file isn't there (e.g. first pod of a new namespace before the CM
	// is created), so pods never block on this volume.
	if node.Qemu {
		volumes = append(volumes, v1.Volume{
			Name: "kubendt-iface-counts",
			VolumeSource: v1.VolumeSource{
				ConfigMap: &v1.ConfigMapVolumeSource{
					LocalObjectReference: v1.LocalObjectReference{
						Name: IfaceCountsConfigMapName,
					},
					Optional: ptr.To(true),
				},
			},
		})
		container.VolumeMounts = append(container.VolumeMounts, v1.VolumeMount{
			Name:      "kubendt-iface-counts",
			MountPath: "/etc/kubendt/iface-counts",
			ReadOnly:  true,
		})
	}

	podSpec := v1.PodSpec{
		Containers: []v1.Container{container},
		Volumes:    volumes,
	}

	// if type==router, qemu, has devices, or is explicitly requested, activate privileges
	if node.Privileged || node.Type == "router" || node.Qemu || len(effectiveDevices) > 0 {
		podSpec.Containers[0].SecurityContext.Privileged = ptr.To(true)
	}

	// If it's a router or switch, activate ip_forward
	if node.Type == "router" || node.Type == "switch" {
		podSpec.SecurityContext = &v1.PodSecurityContext{
			Sysctls: []v1.Sysctl{
				{Name: "net.ipv4.ip_forward", Value: "1"},
			},
		}
	}

	podTemplate := v1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			// runtime labels let handlers/frontend decide shell mode and display model
			Labels: map[string]string{
				"kubendt/type":   node.Type,
				"kubendt/driver": node.Driver, // new driver!!
				"kubendt/shell-mode": func() string {
					return shellMode
				}(),
				"kubendt/runtime": func() string {
					if node.Qemu {
						return "qemu"
					}
					return "k8s-linux"
				}(),
				"kubendt/qemu": func() string {
					if node.Qemu {
						return "true"
					}
					return "false"
				}(),
				"app": node.Name,
			},
			Annotations: map[string]string{
				"k8s.v1.cni.cncf.io/networks": fmt.Sprintf(`[{"name":"meshnet", "namespace":"%s"}]`, namespace),
			},
		},
		Spec: podSpec,
	}

	replicas := int32(node.Replicas)

	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      node.Name,
			Namespace: namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:            &replicas,
			PodManagementPolicy: "Parallel",
			ServiceName:         node.Name,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": node.Name,
				},
			},
			Template: podTemplate,
		},
	}

	_, err := kubeclient.Clientset.AppsV1().StatefulSets(namespace).Create(context.TODO(), statefulSet, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating StatefulSet %s: %w", node.Name, err)
	}

	log.Printf("✅ StatefulSet %s [replicas=%d] deployed successfully in namespace %s", node.Name, node.Replicas, namespace)
	return nil
}

// sanitizeMountFileSuffix normalises a file path into a DNS-1123 fragment
// usable inside a ConfigMap or Secret name.
func sanitizeMountFileSuffix(name string) string {
	return strings.ToLower(
		strings.ReplaceAll(
			strings.ReplaceAll(
				strings.ReplaceAll(name, "_", "-"),
				".", "-"),
			"/", "-"),
	)
}

// SanitizeMountConfigMapName returns the ConfigMap name that backs a
// non-sensitive file mount. Shared across pods mounting the same file.
func SanitizeMountConfigMapName(name string) string {
	return fmt.Sprintf("kubendt-file-%s", sanitizeMountFileSuffix(name))
}

// SanitizeMountSecretName returns the Secret name that backs a sensitive
// file mount. Shared across pods mounting the same file.
func SanitizeMountSecretName(name string) string {
	return fmt.Sprintf("kubendt-secret-file-%s", sanitizeMountFileSuffix(name))
}

// MountResourceNameForFile resolves the K8s resource name backing a file
// mount based on its sensitive flag. Returns (name, isSecret).
func MountResourceNameForFile(namespace, fileName string) (string, bool) {
	meta, _ := GetFileMeta(namespace, fileName)
	if meta.Sensitive {
		return SanitizeMountSecretName(fileName), true
	}
	return SanitizeMountConfigMapName(fileName), false
}

func SanitizeVolumeName(name string) string {
	sanitizedFileName := strings.ToLower(
		strings.ReplaceAll(
			strings.ReplaceAll(
				strings.ReplaceAll(name, "_", "-"),
				".", "-"),
			"/", "-"),
	)
	return fmt.Sprintf("vol-%s", sanitizedFileName)
}

func SanitizeConfigMapDataKey(name string) string {
	// ConfigMap data keys allow: [-._a-zA-Z0-9]+
	// Replace / with _ to preserve path structure while staying valid
	return strings.ReplaceAll(name, "/", "_")
}

// MountFilePathAnnotation records, on a mount ConfigMap/Secret, the original
// file-manager path that created it. The data key sanitizes "/" to "_" (see
// SanitizeConfigMapDataKey), which is lossy, so once the source file is deleted
// the real path is otherwise unrecoverable. Persisting it here lets the pod
// panel show "web-server/index.html" instead of the mangled key even for a
// missing mount.
const MountFilePathAnnotation = "kubendt/mount-file-path"

// SyncMountResourceFromFile refreshes the ConfigMap or Secret backing a file
// mount if one already exists. Returns kind="ConfigMap"|"Secret" on success,
// synced=false with no error if nothing existed to sync.
//
// Pods already mounting the file need a restart to see the new content
// (SubPath bind mounts don't propagate updates).
func SyncMountResourceFromFile(namespace, fileName string) (synced bool, kind string, err error) {
	ctx := context.TODO()

	nsDir, err := namespaceFilesDir(namespace)
	if err != nil {
		return false, "", err
	}
	basePath := filepath.Join(nsDir, fileName)
	data, readErr := os.ReadFile(basePath)
	if readErr != nil {
		return false, "", fmt.Errorf("error reading file %s: %w", basePath, readErr)
	}
	if len(data) > MaxMountFileBytes {
		return false, "", fmt.Errorf("file %q is %d bytes; cannot sync (ConfigMaps/Secrets cap at ~1 MiB)", fileName, len(data))
	}
	if !utf8.Valid(data) {
		return false, "", fmt.Errorf("file %q contains non-UTF-8 bytes; cannot sync", fileName)
	}
	dataKey := SanitizeConfigMapDataKey(fileName)

	// Secret and ConfigMap are mutually exclusive per file after a deploy.
	secretName := SanitizeMountSecretName(fileName)
	if secret, err := kubeclient.Clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{}); err == nil {
		if secret.Data != nil && string(secret.Data[dataKey]) == string(data) && len(secret.Data) == 1 &&
			secret.Annotations[MountFilePathAnnotation] == fileName {
			return true, "Secret", nil
		}
		secret.Data = map[string][]byte{dataKey: data}
		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		secret.Annotations[MountFilePathAnnotation] = fileName
		if _, err := kubeclient.Clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return false, "", fmt.Errorf("error updating Secret %s: %w", secretName, err)
		}
		return true, "Secret", nil
	} else if !apierrors.IsNotFound(err) {
		return false, "", fmt.Errorf("error reading Secret %s: %w", secretName, err)
	}

	configMapName := SanitizeMountConfigMapName(fileName)
	if cm, err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Get(ctx, configMapName, metav1.GetOptions{}); err == nil {
		if cm.Data != nil && cm.Data[dataKey] == string(data) && len(cm.Data) == 1 &&
			cm.Annotations[MountFilePathAnnotation] == fileName {
			return true, "ConfigMap", nil
		}
		cm.Data = map[string]string{dataKey: string(data)}
		if cm.Annotations == nil {
			cm.Annotations = map[string]string{}
		}
		cm.Annotations[MountFilePathAnnotation] = fileName
		if _, err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
			return false, "", fmt.Errorf("error updating ConfigMap %s: %w", configMapName, err)
		}
		return true, "ConfigMap", nil
	} else if !apierrors.IsNotFound(err) {
		return false, "", fmt.Errorf("error reading ConfigMap %s: %w", configMapName, err)
	}

	return false, "", nil
}

// CountMountedPodsForFile counts pods in the namespace that mount the file
// (via either its ConfigMap or its Secret).
func CountMountedPodsForFile(namespace, fileName string) (int, error) {
	configMapName := SanitizeMountConfigMapName(fileName)
	secretName := SanitizeMountSecretName(fileName)
	pods, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("error listing pods: %w", err)
	}
	count := 0
	for _, pod := range pods.Items {
		for _, vol := range pod.Spec.Volumes {
			if vol.ConfigMap != nil && vol.ConfigMap.Name == configMapName {
				count++
				break
			}
			if vol.Secret != nil && vol.Secret.SecretName == secretName {
				count++
				break
			}
		}
	}
	return count, nil
}

// MaxMountFileBytes is the hard cap for a file backing a mount (etcd limit
// for ConfigMaps and Secrets).
const MaxMountFileBytes = 1 << 20

// CreateMountResourceForFile upserts the ConfigMap (default) or Secret
// (when the file is flagged sensitive) that backs a namespace-file mount.
// Every pod mounting that file in the same namespace shares the resource.
// Enforces the 1 MiB cap and UTF-8 encoding (mounts only support text).
// Returns the resource name and whether it is a Secret.
func CreateMountResourceForFile(namespace, fileName string) (name string, isSecret bool, err error) {
	nsDir, err := namespaceFilesDir(namespace)
	if err != nil {
		return "", false, err
	}
	basePath := filepath.Join(nsDir, fileName)
	data, readErr := os.ReadFile(basePath)
	if readErr != nil {
		return "", false, fmt.Errorf("error reading file %s: %w", basePath, readErr)
	}
	if len(data) > MaxMountFileBytes {
		return "", false, fmt.Errorf("file %q is %d bytes; mounts are limited to %d bytes (ConfigMaps/Secrets cap at ~1 MiB in etcd). Split the file or use a different mechanism", fileName, len(data), MaxMountFileBytes)
	}
	if !utf8.Valid(data) {
		return "", false, fmt.Errorf("file %q contains non-UTF-8 bytes; mounts only support text files. Use a different mechanism for binaries", fileName)
	}

	meta, _ := GetFileMeta(namespace, fileName)
	dataKey := SanitizeConfigMapDataKey(fileName)
	ctx := context.TODO()

	if meta.Sensitive {
		secretName := SanitizeMountSecretName(fileName)
		desiredData := map[string][]byte{dataKey: data}

		existing, getErr := kubeclient.Clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
		if getErr == nil {
			if existing.Data != nil && string(existing.Data[dataKey]) == string(data) && len(existing.Data) == 1 &&
				existing.Annotations[MountFilePathAnnotation] == fileName {
				return secretName, true, nil
			}
			existing.Data = desiredData
			if existing.Annotations == nil {
				existing.Annotations = map[string]string{}
			}
			existing.Annotations[MountFilePathAnnotation] = fileName
			if _, updateErr := kubeclient.Clientset.CoreV1().Secrets(namespace).Update(ctx, existing, metav1.UpdateOptions{}); updateErr != nil {
				return "", true, fmt.Errorf("error updating Secret %s: %w", secretName, updateErr)
			}
			return secretName, true, nil
		}
		if !apierrors.IsNotFound(getErr) {
			return "", true, fmt.Errorf("error reading Secret %s: %w", secretName, getErr)
		}

		// Drop any stale ConfigMap left from a previous non-sensitive period.
		if err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, SanitizeMountConfigMapName(fileName), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			log.Printf("⚠️ could not delete stale ConfigMap for sensitive file %s: %v", fileName, err)
		}

		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "kubendt-backend",
					"kubendt/mount-file":           "true",
					"kubendt/sensitive":            "true",
				},
				Annotations: map[string]string{
					MountFilePathAnnotation: fileName,
				},
			},
			Type: v1.SecretTypeOpaque,
			Data: desiredData,
		}
		if _, err := kubeclient.Clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			return "", true, fmt.Errorf("error creating Secret %s: %w", secretName, err)
		}
		return secretName, true, nil
	}

	// Non-sensitive: ConfigMap.
	configMapName := SanitizeMountConfigMapName(fileName)
	desiredData := map[string]string{dataKey: string(data)}

	existing, getErr := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Get(ctx, configMapName, metav1.GetOptions{})
	if getErr == nil {
		if existing.Data != nil && existing.Data[dataKey] == string(data) && len(existing.Data) == 1 &&
			existing.Annotations[MountFilePathAnnotation] == fileName {
			return configMapName, false, nil
		}
		existing.Data = desiredData
		if existing.Annotations == nil {
			existing.Annotations = map[string]string{}
		}
		existing.Annotations[MountFilePathAnnotation] = fileName
		if _, updateErr := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Update(ctx, existing, metav1.UpdateOptions{}); updateErr != nil {
			return "", false, fmt.Errorf("error updating ConfigMap %s: %w", configMapName, updateErr)
		}
		return configMapName, false, nil
	}
	if !apierrors.IsNotFound(getErr) {
		return "", false, fmt.Errorf("error reading ConfigMap %s: %w", configMapName, getErr)
	}

	// Drop any stale Secret left from a previous "sensitive" period.
	if err := kubeclient.Clientset.CoreV1().Secrets(namespace).Delete(ctx, SanitizeMountSecretName(fileName), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		log.Printf("⚠️ could not delete stale Secret for non-sensitive file %s: %v", fileName, err)
	}

	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "kubendt-backend",
				"kubendt/mount-file":           "true",
			},
			Annotations: map[string]string{
				MountFilePathAnnotation: fileName,
			},
		},
		Data: desiredData,
	}
	if _, err := kubeclient.Clientset.CoreV1().ConfigMaps(namespace).Create(ctx, configMap, metav1.CreateOptions{}); err != nil {
		return "", false, fmt.Errorf("error creating ConfigMap %s: %w", configMapName, err)
	}
	return configMapName, false, nil
}

func RestartPod(namespace, podName string) error {
	pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("pod '%s' not found in namespace '%s'", podName, namespace)
	}

	// Only accept restarting if it's under a StatefulSet
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "StatefulSet" {
			// Refresh skip entries on peers so the next sandbox's CNI ADD
			// knows which side is the creator for each link.
			if prepErr := injectPeerSkipEntries(namespace, podName); prepErr != nil {
				log.Printf("⚠️ RestartPod: could not inject peer skip entries for %s: %v (continuing)", podName, prepErr)
			} else {
				log.Printf("ℹ️ RestartPod: injected peer skip entries for %s", podName)
			}

			// Pre-emptively delete the peer-side interfaces for every link
			// of this pod. Empirically (verified with meshnet master), the
			// peer end is NOT cleaned up by meshnet's cmdDel reliably, so
			// the next CNI ADD on the new sandbox tries to inject a veth
			// into the peer with the same name and dies with:
			//   failed to rename link kokoNNN -> ethX: file exists
			// That keeps the new pod stuck in ContainerCreating with
			// kubelet retrying forever. Removing the peer end here
			// guarantees a clean rename.
			//
			// The side effect, peer's pod-side ifindex changes when
			// meshnet recreates the veth, is then corrected by the
			// post-restart QEMU TC rewire pass in the modify handler
			// (RewireQemuPeersAfterRestart), so QEMU peers don't end up
			// with stale `mirred` redirect targets.
			deletePeerVethsForPod(namespace, podName)

			err := kubeclient.Clientset.CoreV1().Pods(namespace).Delete(context.TODO(), podName, metav1.DeleteOptions{})
			if err != nil {
				return fmt.Errorf("error deleting pod %s: %w", podName, err)
			}
			return nil
		}
	}

	return fmt.Errorf("pod '%s' does not belong to a StatefulSet and cannot be restarted", podName)
}

// GetTopologyInterfaceSetForPod returns the set of this pod's own interface
// names declared in its Meshnet Topology CRD (spec.links[].local_intf). These
// are the topology-defined data-plane interfaces, whatever they are named, and
// naturally exclude the primary CNI interface (eth0), loopback and any
// config-created devices (e.g. OVS bridges) that are not link endpoints.
func GetTopologyInterfaceSetForPod(namespace, podName string) (map[string]bool, error) {
	set := map[string]bool{}
	obj, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return set, nil
	}
	links, ok := spec["links"].([]interface{})
	if !ok {
		return set, nil
	}
	for _, l := range links {
		link, ok := l.(map[string]interface{})
		if !ok {
			continue
		}
		if li, _ := link["local_intf"].(string); li != "" {
			set[li] = true
		}
	}
	return set, nil
}

// deletePeerVethsForPod removes the peer-side veth/vxlan endpoints held by
// peer pods that are connected to podName via meshnet links. Must be called
// BEFORE deleting podName itself: meshnet's cmdDel does not always clean up
// the peer end of the link, and the next CNI ADD on the new sandbox would
// then fail with "rename link ... file exists" (see the comment in
// RestartPod for context).
//
// Best-effort: a peer pod that's also being restarted concurrently may have
// its container already gone, in which case the exec returns "container
// not found", that's fine, the veth dies with the peer's old netns anyway.
func deletePeerVethsForPod(namespace, podName string) {
	obj, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		log.Printf("⚠️ deletePeerVeths: could not read Topology for %s: %v", podName, err)
		return
	}

	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return
	}
	links, ok := spec["links"].([]interface{})
	if !ok {
		return
	}

	for _, l := range links {
		link, ok := l.(map[string]interface{})
		if !ok {
			continue
		}
		peer, _ := link["peer_pod"].(string)
		peerIntf, _ := link["peer_intf"].(string)
		if peer == "" || peer == crdExternalPeerPod || peerIntf == "" {
			continue
		}

		_, stderr, execErr := ExecInPod(namespace, peer, []string{"ip", "link", "del", peerIntf})
		if execErr != nil {
			if strings.Contains(stderr, "Cannot find device") || strings.Contains(stderr, "does not exist") {
				log.Printf("ℹ️ deletePeerVeths: %s/%s already absent (peer of %s)", peer, peerIntf, podName)
			} else {
				log.Printf("⚠️ deletePeerVeths: could not delete %s/%s (peer of %s): %v", peer, peerIntf, podName, execErr)
			}
		} else {
			log.Printf("ℹ️ deletePeerVeths: deleted %s/%s (was connected to %s)", peer, peerIntf, podName)
		}
	}
}

// injectPeerSkipEntries writes {link_uid, podName} into the status.skipped
// list of each peer pod, signalling meshnet "podName will (re)create the
// veth, you should skip".
//
// CRITICAL: we only add the entry on the peer for links where podName is
// the *creator*, i.e. links that DO NOT already appear in podName's own
// status.skipped. If we added a skip on the peer for a link where podName
// is the non-creator (peer is the original creator), both sides would end
// up with a skip entry referencing the link, and meshnet's cmdAdd would
// skip wiring on BOTH ends, leaving the link broken.
//
// This is consistent with meshnet's skipped semantics: "I (this pod) will
// skip handling, the listed pod will create". Adding podName to a peer's
// skipped list only makes sense when podName is in fact going to create.
func injectPeerSkipEntries(namespace, podName string) error {
	obj, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error reading Topology for %s: %w", podName, err)
	}

	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	links, ok := spec["links"].([]interface{})
	if !ok {
		return nil
	}

	// Build the set of link UIDs for which podName is the non-creator
	// (links that ALREADY appear in podName's own status.skipped). For
	// these links we must NOT advertise podName as creator on the peer,
	// or we end up with both sides skipping.
	nonCreatorLinks := make(map[int64]struct{})
	if status, ok := obj.Object["status"].(map[string]interface{}); ok {
		if skipped, ok := status["skipped"].([]interface{}); ok {
			for _, entry := range skipped {
				m, ok := entry.(map[string]interface{})
				if !ok {
					continue
				}
				var uid int64
				switch v := m["link_id"].(type) {
				case float64:
					uid = int64(v)
				case int64:
					uid = v
				default:
					continue
				}
				nonCreatorLinks[uid] = struct{}{}
			}
		}
	}

	for _, l := range links {
		link, ok := l.(map[string]interface{})
		if !ok {
			continue
		}
		peer, _ := link["peer_pod"].(string)
		if peer == "" || peer == crdExternalPeerPod {
			continue
		}
		var linkUID int64
		switch v := link["uid"].(type) {
		case float64:
			linkUID = int64(v)
		case int64:
			linkUID = v
		default:
			continue
		}

		if _, nonCreator := nonCreatorLinks[linkUID]; nonCreator {
			// Peer is the creator of this link; leave their skipped
			// list untouched, otherwise both sides skip.
			log.Printf("ℹ️ injectPeerSkipEntries: %s is non-creator for link %d (peer %s); leaving peer's skipped untouched", podName, linkUID, peer)
			continue
		}

		if addErr := addTopologySkipEntry(namespace, peer, linkUID, podName); addErr != nil {
			log.Printf("⚠️ injectPeerSkipEntries: could not update peer %s: %v", peer, addErr)
		}
	}
	return nil
}

// addTopologySkipEntry appends {linkUID, forPod} to podName's Topology.status.skipped
// (via the status subresource) if not already present.
func addTopologySkipEntry(namespace, podName string, linkUID int64, forPod string) error {
	obj, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error reading Topology for %s: %w", podName, err)
	}

	status, ok := obj.Object["status"].(map[string]interface{})
	if !ok {
		status = map[string]interface{}{}
		obj.Object["status"] = status
	}

	var skipped []interface{}
	if s, ok := status["skipped"].([]interface{}); ok {
		skipped = s
	}
	// Check if entry already exists
	for _, entry := range skipped {
		m, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		var existingUID int64
		switch v := m["link_id"].(type) {
		case float64:
			existingUID = int64(v)
		case int64:
			existingUID = v
		}
		if existingUID == linkUID && m["pod_name"] == forPod {
			return nil // already present
		}
	}

	skipped = append(skipped, map[string]interface{}{
		"link_id":  linkUID,
		"pod_name": forPod,
	})
	status["skipped"] = skipped

	if _, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).UpdateStatus(context.TODO(), obj, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating skipped entries for Topology %s: %w", podName, err)
	}
	log.Printf("ℹ️ addTopologySkipEntry: added {link_id:%d, pod_name:%s} to %s.status.skipped", linkUID, forPod, podName)
	return nil
}

// NudgeTopologyReconcile forces meshnet to re-process a pod Topology without restarting the pod.
// It is intentionally non-destructive and only updates metadata annotations.
func NudgeTopologyReconcile(namespace, podName string) error {
	obj, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting Topology %s for reconcile nudge: %w", podName, err)
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["kubendt/reconcile-at"] = time.Now().UTC().Format(time.RFC3339Nano)
	obj.SetAnnotations(annotations)

	if _, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Update(context.TODO(), obj, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating Topology %s annotation for reconcile nudge: %w", podName, err)
	}

	return nil
}

// NudgePodReconcile triggers a pod update event (via annotations) without restarting the pod.
func NudgePodReconcile(namespace, podName string) error {
	pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting pod %s for reconcile nudge: %w", podName, err)
	}

	annotations := pod.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["kubendt/reconcile-at"] = time.Now().UTC().Format(time.RFC3339Nano)
	pod.SetAnnotations(annotations)

	if _, err := kubeclient.Clientset.CoreV1().Pods(namespace).Update(context.TODO(), pod, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating pod %s annotation for reconcile nudge: %w", podName, err)
	}

	return nil
}

// TopologyGVR defines the Topology resource in meshnet
var TopologyGVR = schema.GroupVersionResource{
	Group:    "networkop.co.uk",
	Version:  "v1beta1",
	Resource: "topologies",
}

// GetPodStatus returns the readiness flag and a detailed status string for a pod.
// Exported so handlers can use the same logic as the internal wait loop.
func GetPodStatus(pod *v1.Pod) (bool, string) {
	return isPodReady(pod)
}

// isPodReady checks termination timestamp, phase, and Ready condition.
func isPodReady(pod *v1.Pod) (bool, string) {
	if pod.DeletionTimestamp != nil {
		return false, "terminating"
	}

	switch pod.Status.Phase {
	case v1.PodRunning:
		// Besides phase, require Ready TRUE and all containers ready
		readyCond := false
		for _, c := range pod.Status.Conditions {
			if c.Type == v1.PodReady && c.Status == v1.ConditionTrue {
				readyCond = true
				break
			}
		}
		if !readyCond {
			return false, "running-not-ready"
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if !cs.Ready {
				return false, "container-not-ready"
			}
		}
		return true, "ready"

	case v1.PodPending:
		return false, "pending"
	case v1.PodSucceeded:
		return false, "succeeded"
	case v1.PodFailed:
		return false, "failed"
	default:
		return false, "unknown"
	}
}

func ParseQdiscShowToMap(output string) (map[string]interface{}, error) {
	// If not output, return nil qdisc and empty raw
	if strings.TrimSpace(output) == "" {
		return map[string]interface{}{
			"qdisc": nil,
			"raw":   output,
		}, nil
	}

	lines := strings.Split(output, "\n")

	// 1) Choose the most relevant line: we prefer the one that has "qdisc" and affects "root"
	var line string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || !strings.Contains(ln, "qdisc") {
			continue
		}
		if strings.Contains(ln, " root ") || strings.HasSuffix(ln, " root") || strings.Contains(ln, " parent root") {
			line = ln
			break
		}
	}
	// If we didn't find one with root, take the first one with qdisc
	if line == "" {
		for _, ln := range lines {
			if strings.Contains(ln, "qdisc") {
				line = strings.TrimSpace(ln)
				break
			}
		}
	}

	// If we still don't have a line, consider there's no useful qdisc
	if line == "" {
		return map[string]interface{}{
			"qdisc": nil,
			"raw":   output,
		}, nil
	}

	res := map[string]interface{}{
		"raw": line,
	}

	// 2) Type of qdisc
	if m := regexp.MustCompile(`\bqdisc\s+(\S+)`).FindStringSubmatch(line); len(m) > 1 {
		res["qdisc"] = m[1] // p.ej. netem, tbf, noqueue, pfifo_fast...
	}

	// 3) Handle:
	//   a) Style "handle 8001:"
	if m := regexp.MustCompile(`\bhandle\s+([0-9A-Fa-f]+):`).FindStringSubmatch(line); len(m) > 1 {
		res["handle"] = m[1]
	} else {
		//   b) Style "qdisc netem 8001: root ..."
		if m := regexp.MustCompile(`\bqdisc\s+\S+\s+([0-9A-Fa-f]+):`).FindStringSubmatch(line); len(m) > 1 {
			res["handle"] = m[1]
		}
	}

	// 4) Parent / root
	if m := regexp.MustCompile(`\bparent\s+(\S+)`).FindStringSubmatch(line); len(m) > 1 {
		res["parent"] = m[1]
	} else if strings.Contains(line, " root ") || strings.HasSuffix(line, " root") {
		res["parent"] = "root"
	}

	// 5) Generic limit (some qdiscs show it)
	if m := regexp.MustCompile(`\blimit\s+(\d+)`).FindStringSubmatch(line); len(m) > 1 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			res["limit"] = n
		}
	}

	qdiscType, _ := res["qdisc"].(string)

	// 6) netem: delay [jitter], loss, duplicate, corrupt, seed, limit
	if qdiscType == "netem" {
		// delay X [jitter]. The jitter token must start with a digit so trailing
		// keywords like "seed" or "loss" are not mistaken for a jitter value.
		if m := regexp.MustCompile(`\bdelay\s+([0-9a-zA-Z.]+)(?:\s+([0-9][0-9a-zA-Z.]*))?`).FindStringSubmatch(line); len(m) >= 2 {
			res["delay"] = m[1]
			if len(m) >= 3 && m[2] != "" {
				res["jitter"] = m[2]
			}
		}
		// loss %
		if m := regexp.MustCompile(`\bloss\s+([0-9.]+%)`).FindStringSubmatch(line); len(m) > 1 {
			res["loss"] = m[1]
		}
		// duplicate %
		if m := regexp.MustCompile(`\bduplicate\s+([0-9.]+%)`).FindStringSubmatch(line); len(m) > 1 {
			res["duplicate"] = m[1]
		}
		// corrupt %
		if m := regexp.MustCompile(`\bcorrupt\s+([0-9.]+%)`).FindStringSubmatch(line); len(m) > 1 {
			res["corrupt"] = m[1]
		}
		// seed (long integer)
		if m := regexp.MustCompile(`\bseed\s+([0-9]+)`).FindStringSubmatch(line); len(m) > 1 {
			res["seed"] = m[1]
		}
		// specific limit (if not captured earlier)
		if _, ok := res["limit"]; !ok {
			if m := regexp.MustCompile(`\blimit\s+(\d+)`).FindStringSubmatch(line); len(m) > 1 {
				if n, err := strconv.Atoi(m[1]); err == nil {
					res["limit"] = n
				}
			}
		}
	}

	// 7) tbf: rate, burst, latency/lat
	// Typical examples:
	//   "qdisc tbf 1: root refcnt 2 rate 10Mbit burst 32Kb lat 400.0ms"
	//   "qdisc tbf 1: root rate 10mbit burst 32kbit latency 400ms"
	if qdiscType == "tbf" {
		// rate <valor-unidad> (muy variado: 10Mbit, 10mbit, 100kbit, 1000bps, etc.)
		if m := regexp.MustCompile(`\brate\s+([0-9A-Za-z.]+)`).FindStringSubmatch(line); len(m) > 1 {
			res["rate"] = m[1]
		}
		// burst <valor-unidad> (p.ej. 32Kb, 32kbit, 1514b, etc.)
		if m := regexp.MustCompile(`\bburst\s+([0-9A-Za-z.]+)`).FindStringSubmatch(line); len(m) > 1 {
			res["burst"] = m[1]
		}
		// latency o lat <tiempo>
		if m := regexp.MustCompile(`\b(?:latency|lat)\s+([0-9A-Za-z.]+)`).FindStringSubmatch(line); len(m) > 1 {
			res["latency"] = m[1]
		}
		// limit si aparece (no siempre)
		if _, ok := res["limit"]; !ok {
			if m := regexp.MustCompile(`\blimit\s+(\d+)`).FindStringSubmatch(line); len(m) > 1 {
				if n, err := strconv.Atoi(m[1]); err == nil {
					res["limit"] = n
				}
			}
		}
	}

	// 8) noqueue and other qdiscs: we return what we have without specific parameters
	// (we already have "raw", "qdisc" and maybe "handle"/"parent"/"limit")

	return res, nil
}
