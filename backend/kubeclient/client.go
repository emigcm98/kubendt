package kubeclient

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// What built the client: a kubeconfig, which may hold other contexts to
// switch to, or the ServiceAccount of a backend running in its cluster,
// which is that cluster for good.
const (
	ModeKubeconfig = "kubeconfig"
	ModeInCluster  = "in-cluster"
)

var mode string

// Mode is ModeKubeconfig or ModeInCluster, empty with no client.
func Mode() string { return mode }

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

// client-go throttles at 5 requests/s (burst 10) unless told otherwise. We
// issue one or more requests per pod, so a 128-node topology would sit in
// that queue for tens of seconds during validation and configure alone. The
// API server has its own priority and fairness, so a generous client budget
// is safe. KUBENDT_K8S_QPS and KUBENDT_K8S_BURST override the defaults.
const (
	defaultK8sQPS   = 100
	defaultK8sBurst = 200
)

func applyRateLimits(cfg *rest.Config) {
	cfg.QPS = float32(envPositiveInt("KUBENDT_K8S_QPS", defaultK8sQPS))
	cfg.Burst = envPositiveInt("KUBENDT_K8S_BURST", defaultK8sBurst)
}

func envPositiveInt(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Printf("⚠️ %s=%q is not a positive integer, using %d", name, v, def)
		return def
	}
	return n
}

func applyConfig(cfg *rest.Config) error {
	applyRateLimits(cfg)
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

// IsConfigured reports whether a usable Kubernetes client is loaded. It is
// false when the server started with neither a kubeconfig nor a
// ServiceAccount, until it restarts with one.
func IsConfigured() bool {
	return Clientset != nil
}

// InitClient (re)initializes the Kubernetes connection. It is intentionally
// non-fatal: with no usable kubeconfig it clears the clients and returns the
// error, so the server still starts and tells the UI to mount one.
func InitClient() error {
	kubeconfigPath := resolveKubeconfigPath()
	kubeContext := os.Getenv("KUBE_CONTEXT")

	cfg, err := loadConfig(kubeconfigPath, kubeContext)
	if err != nil {
		Config, Clientset, DynamicClient, mode = nil, nil, nil, ""
		log.Printf("⚠️ No usable kubeconfig: %v (mount one and restart to begin)", err)
		return err
	}

	if err := applyConfig(cfg); err != nil {
		Config, Clientset, DynamicClient, mode = nil, nil, nil, ""
		log.Printf("⚠️ Could not initialize Kubernetes client: %v", err)
		return err
	}
	mode = ModeInCluster
	if kubeconfigPath != "" {
		mode = ModeKubeconfig
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

// ClientForContext is a client for one context of the kubeconfig that leaves
// the active one alone, to look at another cluster before switching to it.
func ClientForContext(contextName string) (*kubernetes.Clientset, error) {
	path := resolveKubeconfigPath()
	if path == "" {
		return nil, errors.New("kubeconfig not found")
	}
	cfg, err := loadConfig(path, contextName)
	if err != nil {
		return nil, err
	}
	cfg.Timeout = clusterIDResolveTimeout
	return kubernetes.NewForConfig(cfg)
}

// ContextOverride is the context set over the kubeconfig's own, empty when
// there is none, to come back to after a failed switch.
func ContextOverride() string { return os.Getenv("KUBE_CONTEXT") }

// RestoreContext goes back to the context a switch started from.
func RestoreContext(previous string) error {
	if previous == "" {
		os.Unsetenv("KUBE_CONTEXT")
		return InitClient()
	}
	return SetKubeContext(previous)
}

// SetKubeContext moves the client to another context of the kubeconfig. It
// lives in this process only, the file is never written, so a restart comes
// back to the configured context.
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
