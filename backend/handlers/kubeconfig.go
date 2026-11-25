package handlers

import (
	"log"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"

	"kubendt/auth"
	"kubendt/helpers"
	"kubendt/kubeclient"
	"kubendt/settings"

	"github.com/gin-gonic/gin"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

type kubeConfigInfoResponse struct {
	Configured bool `json:"configured"`
	// Mode is "kubeconfig", whose contexts the owner may switch between, or
	// "in-cluster", a backend on its cluster's ServiceAccount, fixed.
	Mode           string   `json:"mode"`
	Path           string   `json:"path"`
	CurrentContext string   `json:"current_context"`
	Contexts       []string `json:"contexts"`
	// ContextClusterIDs maps context name -> canonical cluster ID (kube-system
	// UID). Unreachable contexts are omitted. Lets the UI show which contexts
	// point at the same underlying cluster.
	ContextClusterIDs map[string]string `json:"context_cluster_ids"`
	// ClusterID is the active cluster's, empty while it cannot be reached.
	ClusterID string `json:"cluster_id"`
}

// GET /kube/config -> where the cluster comes from and the contexts the
// kubeconfig offers.
func GetKubeConfigInfo(c *gin.Context) {
	info, err := kubeclient.GetKubeConfigInfo()
	if err != nil {
		// No kubeconfig: a valid state, the UI asks to mount one and restart.
		c.JSON(http.StatusOK, kubeConfigInfoResponse{Configured: false, Contexts: []string{}})
		return
	}

	contexts := append([]string{}, info.Contexts...)
	sort.Strings(contexts)
	clusterID, _ := kubeclient.CurrentClusterID()
	c.JSON(http.StatusOK, kubeConfigInfoResponse{
		Configured:        true,
		Mode:              kubeclient.Mode(),
		Path:              info.Path,
		CurrentContext:    info.CurrentContext,
		Contexts:          contexts,
		ContextClusterIDs: info.ContextClusterIDs,
		ClusterID:         clusterID,
	})
}

// openStreams counts this backend's live shell, logs, capture, trace and
// iperf sockets, which run on the active client: a switch to another cluster
// waits until there are none.
var openStreams atomic.Int64

// trackStream counts a socket from when it opens until the returned func.
func trackStream() func() {
	openStreams.Add(1)
	return func() { openStreams.Add(-1) }
}

var switchMu sync.Mutex

type setKubeContextRequest struct {
	Context string `json:"context"`
	// Password is the owner's of the cluster switched to, needed when it is
	// another cluster than the active one.
	Password string `json:"password,omitempty"`
}

// POST /kube/context {"context": "kind-lab", "password": "…"}: switch to
// another context of the kubeconfig, the owner's alone. A context on another
// cluster is that cluster's KubeNDT: it takes its owner's password, brings
// its accounts and settings and signs the owner in there. One on the same
// cluster only changes the credentials. The kubeconfig is never written, so
// a restart comes back to the configured context.
func SetKubeContext(c *gin.Context) {
	if kubeclient.Mode() == kubeclient.ModeInCluster {
		c.JSON(http.StatusConflict, gin.H{"error": "This KubeNDT runs in its cluster, there is no other context to switch to.", "code": "IN_CLUSTER"})
		return
	}
	if !callerIsOwner(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only the owner switches the cluster.", "code": "OWNER_ONLY"})
		return
	}
	var req setKubeContextRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Context == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name the context to switch to"})
		return
	}

	info, err := kubeclient.GetKubeConfigInfo()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	known := false
	for _, ctx := range info.Contexts {
		if ctx == req.Context {
			known = true
			break
		}
	}
	if !known {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The kubeconfig has no context " + req.Context, "code": "CONTEXT_NOT_FOUND"})
		return
	}

	switchMu.Lock()
	defer switchMu.Unlock()

	secrets, target, err := resolveTarget(req.Context)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Could not reach the cluster of " + req.Context + ": " + err.Error(), "code": "CONTEXT_UNREACHABLE"})
		return
	}
	current, _ := kubeclient.CurrentClusterID()
	other := target != current

	if other {
		if !ownerOfTarget(c, req, secrets) {
			return
		}
		if n := openStreams.Load(); n > 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "Close the shells, logs, captures, traces and throughput tests first, they run on this cluster.", "code": "STREAMS_OPEN"})
			return
		}
		lock, err := helpers.HeldOperationLock()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if lock != nil {
			c.JSON(http.StatusConflict, gin.H{
				"error":             "Namespace '" + lock.Namespace + "' has an operation in progress, wait for it to end.",
				"code":              "OPERATION_RUNNING",
				"namespace":         lock.Namespace,
				"current_operation": lock.OperationType,
				"started_at":        lock.StartedAt,
			})
			return
		}
	}

	previous := kubeclient.ContextOverride()
	if err := kubeclient.SetKubeContext(req.Context); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	goBack := func(cause error) {
		if err := kubeclient.RestoreContext(previous); err != nil {
			log.Printf("⚠️ Could not go back to the previous context after a failed switch: %v", err)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "The switch was undone: " + cause.Error()})
	}
	if err := helpers.EnsureSystemNamespace(); err != nil {
		goBack(err)
		return
	}
	if err := auth.Rebind(kubeclient.Clientset.CoreV1().Secrets(kubeclient.SystemNamespace())); err != nil {
		goBack(err)
		return
	}
	settings.Reread()
	if other {
		helpers.ForgetClusterCaches()
	}
	if err := helpers.RecordActiveCluster(); err != nil {
		log.Printf("⚠️ Could not record active cluster after context switch: %v", err)
	}
	log.Printf("🔀 Switched to context %s (cluster %s)", req.Context, target)

	if other && auth.Enabled() {
		raw, err := auth.CreateSession(auth.OwnerAccount)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Switched, but could not sign in there: " + err.Error()})
			return
		}
		setSessionCookie(c, raw)
	}
	c.JSON(http.StatusOK, gin.H{"current_context": req.Context, "cluster_id": target, "cluster_changed": other})
}

// resolveTarget reaches a context's cluster with a throwaway client: the
// Secrets of its system namespace and its ID, read fresh since the switch
// decides on it, not from the picker's memo. A variable so the tests can
// hand it a fake cluster.
var resolveTarget = func(contextName string) (corev1client.SecretInterface, string, error) {
	cs, err := kubeclient.ClientForContext(contextName)
	if err != nil {
		return nil, "", err
	}
	id, err := kubeclient.ClusterIDOf(cs)
	if err != nil {
		return nil, "", err
	}
	return cs.CoreV1().Secrets(kubeclient.SystemNamespace()), id, nil
}

// ownerOfTarget checks the password against the owner of the cluster switched
// to, under the login's limits; it answers the request when it says no.
func ownerOfTarget(c *gin.Context, req setKubeContextRequest, secrets corev1client.SecretInterface) bool {
	if !auth.Enabled() {
		return true
	}
	ip := c.ClientIP()
	if ok, retry := auth.LoginAllowed(ip, auth.OwnerAccount); !ok {
		tooManyLogins(c, retry)
		return false
	}
	if req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "That context is another cluster, its owner's password is needed.", "code": "PASSWORD_REQUIRED"})
		return false
	}
	ok, err := auth.CheckOwnerOn(secrets, req.Password)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "code": "CONTEXT_UNREACHABLE"})
		return false
	}
	if !ok {
		auth.RegisterLoginFailure(ip, auth.OwnerAccount)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "That is not the password of the owner there.", "code": "INVALID_CREDENTIALS"})
		return false
	}
	auth.RegisterLoginSuccess(ip, auth.OwnerAccount)
	return true
}
