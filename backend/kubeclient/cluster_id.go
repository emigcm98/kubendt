package kubeclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// A cluster's canonical identity is the UID of its kube-system namespace. That
// object is created once at cluster bootstrap and never recreated, so its UID
// is stable for the life of the cluster and unique across clusters (the same
// convention OpenTelemetry's k8s.cluster.uid and kube-state-metrics use). We
// use it to namespace every persisted row and file tree, so identically-named
// Kubernetes namespaces living in different clusters never collide.

const (
	clusterIDResolveTimeout = 8 * time.Second
	// Shorter budget for annotating the context picker: unreachable contexts
	// must not stall the panel, and lookups run concurrently.
	contextIDResolveTimeout = 4 * time.Second
)

var (
	clusterIDMu     sync.RWMutex
	cachedClusterID string

	// ctxIDMu guards ctxIDCache, a context-name -> cluster-id memo used to
	// annotate the context list in the UI. Cluster IDs are immutable, so a
	// successful lookup is cached forever; failures are not cached (the cluster
	// may just be transiently unreachable).
	ctxIDMu    sync.Mutex
	ctxIDCache = map[string]string{}
)

// invalidateClusterID clears the active-cluster ID cache. Called from
// applyConfig on every client (re)load so the next CurrentClusterID resolves
// against the newly-active cluster.
func invalidateClusterID() {
	clusterIDMu.Lock()
	cachedClusterID = ""
	clusterIDMu.Unlock()
}

// CurrentClusterID returns the canonical ID of the currently active cluster,
// resolving it from the live cluster on first use and caching it until the
// active client changes. It is the scoping key for all persisted state.
func CurrentClusterID() (string, error) {
	clusterIDMu.RLock()
	id := cachedClusterID
	clusterIDMu.RUnlock()
	if id != "" {
		return id, nil
	}

	if Clientset == nil {
		return "", errors.New("kubernetes client not configured")
	}

	id, err := fetchKubeSystemUIDWithTimeout(Clientset, clusterIDResolveTimeout)
	if err != nil {
		return "", fmt.Errorf("resolving cluster id (kube-system uid): %w", err)
	}

	clusterIDMu.Lock()
	cachedClusterID = id
	clusterIDMu.Unlock()
	return id, nil
}

// ClusterIDForContext resolves the canonical ID of an arbitrary context without
// disturbing the active client. Best-effort and memoized: used only to label
// the context picker. Returns an error (and caches nothing) when the context is
// unreachable.
func ClusterIDForContext(contextName string) (string, error) {
	ctxIDMu.Lock()
	if id, ok := ctxIDCache[contextName]; ok {
		ctxIDMu.Unlock()
		return id, nil
	}
	ctxIDMu.Unlock()

	path := resolveKubeconfigPath()
	if path == "" {
		return "", errors.New("kubeconfig not found")
	}

	cfg, err := loadConfig(path, contextName)
	if err != nil {
		return "", err
	}
	cfg.Timeout = contextIDResolveTimeout
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", err
	}

	id, err := fetchKubeSystemUIDWithTimeout(cs, contextIDResolveTimeout)
	if err != nil {
		return "", err
	}

	ctxIDMu.Lock()
	ctxIDCache[contextName] = id
	ctxIDMu.Unlock()
	return id, nil
}

// ClusterIDsForContexts resolves the canonical ID of every named context
// concurrently, returning a context-name -> cluster-id map. Contexts whose
// cluster is unreachable are simply absent from the result.
func ClusterIDsForContexts(names []string) map[string]string {
	out := make(map[string]string, len(names))
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for _, name := range names {
		wg.Add(1)
		go func(ctxName string) {
			defer wg.Done()
			id, err := ClusterIDForContext(ctxName)
			if err != nil {
				return
			}
			mu.Lock()
			out[ctxName] = id
			mu.Unlock()
		}(name)
	}
	wg.Wait()
	return out
}

func fetchKubeSystemUIDWithTimeout(cs kubernetes.Interface, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ns, err := cs.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	uid := string(ns.UID)
	if uid == "" {
		return "", errors.New("kube-system namespace has no UID")
	}
	return uid, nil
}
