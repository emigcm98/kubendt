package kubeclient

import (
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Clientset is the global instance for accessing Kubernetes
var (
	Clientset     *kubernetes.Clientset
	DynamicClient dynamic.Interface
	Config        *rest.Config
)

type KubeConfigInfo struct {
	Path           string
	CurrentContext string
	Contexts       []string
	// ContextClusterIDs maps each context name to its cluster's canonical ID
	// (kube-system UID). Contexts whose cluster is currently unreachable are
	// omitted rather than blocking the response.
	ContextClusterIDs map[string]string
}

func resolveKubeconfigPath() string {
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		if home, homeErr := os.UserHomeDir(); homeErr == nil && home != "" {
			kubeconfigPath = filepath.Join(home, ".kube", "config")
		}
	}

	if kubeconfigPath != "" {
		if _, statErr := os.Stat(kubeconfigPath); statErr != nil {
			kubeconfigPath = ""
		}
	}

	return kubeconfigPath
}

func loadConfig(kubeconfigPath, kubeContext string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		overrides := &clientcmd.ConfigOverrides{}
		if kubeContext != "" {
			overrides.CurrentContext = kubeContext
		}

		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
		return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	}

	return rest.InClusterConfig()
}

func applyConfig(cfg *rest.Config) error {
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}

	Config = cfg
	Clientset = clientset
	DynamicClient = dynamicClient
	// The active cluster just changed: force the next CurrentClusterID() to
	// re-resolve against it instead of returning the previous cluster's UID.
	invalidateClusterID()
	return nil
}

// IsConfigured reports whether a usable Kubernetes client is currently loaded.
// It is false when the server started without a kubeconfig (or in-cluster
// config) and none has been loaded yet through the API.
func IsConfigured() bool {
	return Clientset != nil
}

// InitClient (re)initializes the Kubernetes connection. It is intentionally
// non-fatal: when no usable kubeconfig is available it clears the clients and
// returns the error, so the server can still start and expose the endpoints
// needed to load a kubeconfig. The error is returned for callers that want it.
func InitClient() error {
	kubeconfigPath := resolveKubeconfigPath()
	kubeContext := os.Getenv("KUBE_CONTEXT")

	cfg, err := loadConfig(kubeconfigPath, kubeContext)
	if err != nil {
		Config, Clientset, DynamicClient = nil, nil, nil
		log.Printf("⚠️ No usable kubeconfig yet: %v (load one via the API/UI to begin)", err)
		return err
	}

	if err := applyConfig(cfg); err != nil {
		Config, Clientset, DynamicClient = nil, nil, nil
		log.Printf("⚠️ Could not initialize Kubernetes client: %v", err)
		return err
	}

	log.Println("✅ Kubernetes client initialized successfully")
	return nil
}

func GetKubeConfigInfo() (*KubeConfigInfo, error) {
	// Try to get info from file first
	path := resolveKubeconfigPath()

	// If we have a kubeconfig file path, load from it
	if path != "" {
		config, err := clientcmd.LoadFromFile(path)
		if err == nil {
			current := config.CurrentContext
			if envContext := os.Getenv("KUBE_CONTEXT"); envContext != "" {
				current = envContext
			}

			contexts := []string{}
			for name := range config.Contexts {
				contexts = append(contexts, name)
			}

			return &KubeConfigInfo{
				Path:              path,
				CurrentContext:    current,
				Contexts:          contexts,
				ContextClusterIDs: ClusterIDsForContexts(contexts),
			}, nil
		}
		// If file load failed, log but continue to in-cluster fallback
		log.Printf("Failed to load kubeconfig from file at %s: %v", path, err)
	}

	// Fallback: use in-cluster config if available
	if Config != nil {
		// For in-cluster config, we can't show contexts or path in the same way
		return &KubeConfigInfo{
			Path:           "in-cluster",
			CurrentContext: "in-cluster",
			Contexts:       []string{"in-cluster"},
		}, nil
	}

	return nil, errors.New("no kubeconfig or in-cluster config available")
}

// ActiveContextName returns the name of the currently active context: the
// KUBE_CONTEXT override when set, otherwise the kubeconfig's current-context.
// Empty when running from in-cluster config or with no kubeconfig.
func ActiveContextName() string {
	if c := os.Getenv("KUBE_CONTEXT"); c != "" {
		return c
	}
	path := resolveKubeconfigPath()
	if path == "" {
		return ""
	}
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return ""
	}
	return cfg.CurrentContext
}

// ActiveClusterMeta bundles the identity of the active cluster for the cluster
// registry: its canonical ID (kube-system UID), the context name that reached
// it, and the API server URL.
func ActiveClusterMeta() (id, contextName, server string, err error) {
	id, err = CurrentClusterID()
	if err != nil {
		return "", "", "", err
	}
	if Config != nil {
		server = Config.Host
	}
	return id, ActiveContextName(), server, nil
}

func SetKubeContext(contextName string) error {
	path := resolveKubeconfigPath()
	if path == "" {
		return errors.New("kubeconfig not found")
	}

	config, err := loadConfig(path, contextName)
	if err != nil {
		return err
	}

	if err := applyConfig(config); err != nil {
		return err
	}

	return os.Setenv("KUBE_CONTEXT", contextName)
}

// resolveWritableKubeconfigPath returns where an uploaded kubeconfig is saved:
// the KUBECONFIG path when set, otherwise ~/.kube/config. This is the same
// location InitClient reads from, so a load takes effect immediately and
// persists (in the container KUBECONFIG points at a volume).
func resolveWritableKubeconfigPath() (string, error) {
	if p := os.Getenv("KUBECONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot determine home directory: " + err.Error())
	}
	return filepath.Join(home, ".kube", "config"), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// LoadKubeConfigFromPath validates an uploaded kubeconfig and, only if it is
// genuinely usable, persists it to the configured location and reloads the
// client. Validation is three-fold: it must parse, describe at least one
// context/cluster, and actually reach the cluster.
func LoadKubeConfigFromPath(kubeconfigPath string) error {
	if _, err := os.Stat(kubeconfigPath); err != nil {
		return errors.New("kubeconfig file not found at " + kubeconfigPath)
	}

	// 1. Must parse as a kubeconfig.
	parsed, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return errors.New("invalid kubeconfig: " + err.Error())
	}

	// 2. Must describe a cluster (rejects unrelated YAML/random files).
	if len(parsed.Contexts) == 0 || len(parsed.Clusters) == 0 {
		return errors.New("invalid kubeconfig: no contexts or clusters defined")
	}

	// 3. Must be reachable (rejects stale/unreachable/misconfigured files).
	probeCfg, err := loadConfig(kubeconfigPath, "")
	if err != nil {
		return errors.New("invalid kubeconfig: " + err.Error())
	}
	probeCfg.Timeout = 8 * time.Second
	probeClient, err := kubernetes.NewForConfig(probeCfg)
	if err != nil {
		return errors.New("invalid kubeconfig: " + err.Error())
	}
	if _, err := probeClient.Discovery().ServerVersion(); err != nil {
		return errors.New("could not reach the cluster with this kubeconfig: " + err.Error())
	}

	// Persist to the configured location (KUBECONFIG, or ~/.kube/config).
	target, err := resolveWritableKubeconfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return errors.New("could not create kubeconfig directory: " + err.Error())
	}
	if err := copyFile(kubeconfigPath, target); err != nil {
		return errors.New("could not save kubeconfig: " + err.Error())
	}
	// Kubeconfig holds credentials: keep it owner-only.
	if err := os.Chmod(target, 0600); err != nil {
		log.Printf("⚠️ Could not restrict permissions on %s: %v", target, err)
	}
	log.Printf("✅ Kubeconfig saved to %s", target)

	// Drop any stale context override and reload from the persisted location.
	// KUBECONFIG is intentionally left untouched: that path is the source of
	// truth for both reads and writes.
	os.Unsetenv("KUBE_CONTEXT")
	return InitClient()
}

func ResetKubeConfig() error {
	// Clear the environment variable
	os.Unsetenv("KUBECONFIG")
	os.Unsetenv("KUBE_CONTEXT")

	// Reload with default path
	return InitClient()
}
