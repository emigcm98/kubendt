package handlers

import (
	"io"
	"log"
	"net/http"
	"os"
	"sort"

	"kubendt/helpers"
	"kubendt/kubeclient"

	"github.com/gin-gonic/gin"
)

type kubeConfigInfoResponse struct {
	Configured     bool     `json:"configured"`
	Path           string   `json:"path"`
	CurrentContext string   `json:"current_context"`
	Contexts       []string `json:"contexts"`
	// ContextClusterIDs maps context name -> canonical cluster ID (kube-system
	// UID). Unreachable contexts are omitted. Lets the UI show which contexts
	// point at the same underlying cluster.
	ContextClusterIDs map[string]string `json:"context_cluster_ids"`
}

func GetKubeConfigInfo(c *gin.Context) {
	info, err := kubeclient.GetKubeConfigInfo()
	if err != nil {
		// No kubeconfig loaded yet: a valid state, not an error. The frontend
		// uses `configured` to prompt the user to load one before starting.
		c.JSON(http.StatusOK, kubeConfigInfoResponse{Configured: false, Contexts: []string{}})
		return
	}

	contexts := append([]string{}, info.Contexts...)
	sort.Strings(contexts)

	c.JSON(http.StatusOK, kubeConfigInfoResponse{
		Configured:        true,
		Path:              info.Path,
		CurrentContext:    info.CurrentContext,
		Contexts:          contexts,
		ContextClusterIDs: info.ContextClusterIDs,
	})
}

type setKubeContextRequest struct {
	Context string `json:"context"`
}

func SetKubeContext(c *gin.Context) {
	var req setKubeContextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload"})
		return
	}

	if req.Context == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Context cannot be empty"})
		return
	}

	info, err := kubeclient.GetKubeConfigInfo()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	valid := false
	for _, ctx := range info.Contexts {
		if ctx == req.Context {
			valid = true
			break
		}
	}
	if !valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Context not found in kubeconfig"})
		return
	}

	if err := kubeclient.SetKubeContext(req.Context); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if err := helpers.RecordActiveCluster(); err != nil {
		log.Printf("⚠️ Could not record active cluster after context switch: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{"current_context": req.Context})
}

func LoadKubeConfig(c *gin.Context) {
	// Get the file from the request
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file provided"})
		return
	}

	// Create tmp file to validate kubeconfig
	tmpFile, err := os.CreateTemp("", "kubeconfig-*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create temp file"})
		return
	}
	defer os.Remove(tmpFile.Name())

	// Copy uploaded file to tmp
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open uploaded file"})
		return
	}
	defer src.Close()

	if _, err := io.Copy(tmpFile, src); err != nil {
		tmpFile.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write temp file"})
		return
	}
	tmpFile.Close()

	// Load the kubeconfig from the temp file
	if err := kubeclient.LoadKubeConfigFromPath(tmpFile.Name()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := helpers.RecordActiveCluster(); err != nil {
		log.Printf("⚠️ Could not record active cluster after kubeconfig load: %v", err)
	}

	// Get updated info
	info, err := kubeclient.GetKubeConfigInfo()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	contexts := append([]string{}, info.Contexts...)
	sort.Strings(contexts)

	c.JSON(http.StatusOK, kubeConfigInfoResponse{
		Configured:        true,
		Path:              info.Path,
		CurrentContext:    info.CurrentContext,
		Contexts:          contexts,
		ContextClusterIDs: info.ContextClusterIDs,
	})
}
