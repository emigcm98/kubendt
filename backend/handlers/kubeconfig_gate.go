package handlers

import (
	"net/http"
	"strings"

	"kubendt/kubeclient"

	"github.com/gin-gonic/gin"
)

// kubeconfigGateAllowlist are the path prefixes that stay reachable even when
// no kubeconfig is loaded: infrastructure endpoints and the ones needed to
// actually load a kubeconfig. Everything else requires a configured client.
var kubeconfigGateAllowlist = []string{
	"/healthz",
	"/readyz",
	"/version",
	"/auth",
	"/kube",
	"/swagger",
}

// RequireKubeconfig blocks cluster-dependent requests with 503 NO_KUBECONFIG
// while the server has no usable Kubernetes client. Being an allowlist, any
// route added later is gated by default. CORS preflight (OPTIONS) is let
// through so the browser can still learn the real response.
func RequireKubeconfig() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions || kubeclient.IsConfigured() {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		for _, prefix := range kubeconfigGateAllowlist {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"error": "No kubeconfig configured. Load a kubeconfig to begin.",
			"code":  "NO_KUBECONFIG",
		})
	}
}
