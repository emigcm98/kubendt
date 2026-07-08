package handlers

import (
	"context"
	"net/http"
	"os"
	"time"

	"kubendt/database"
	"kubendt/kubeclient"

	"github.com/gin-gonic/gin"
)

func appVersion() string {
	version := os.Getenv("KUBENDT_VERSION")
	if version == "" {
		version = "dev"
	}
	return version
}

func appCommit() string {
	commit := os.Getenv("KUBENDT_COMMIT")
	if commit == "" {
		commit = "unknown"
	}
	return commit
}

func appBuildDate() string {
	buildDate := os.Getenv("KUBENDT_BUILD_DATE")
	if buildDate == "" {
		buildDate = "unknown"
	}
	return buildDate
}

func Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":      "ok",
		"service":     "kubendt-backend",
		"version":     appVersion(),
		"commit":      appCommit(),
		"build_date":  appBuildDate(),
		"server_time": time.Now().UTC().Format(time.RFC3339),
	})
}

func Version(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"service":    "kubendt-backend",
		"version":    appVersion(),
		"commit":     appCommit(),
		"build_date": appBuildDate(),
	})
}

func Readyz(c *gin.Context) {
	checks := map[string]string{
		"database":   "ok",
		"kubernetes": "ok",
	}

	ready := true

	if database.DB == nil {
		checks["database"] = "not_initialized"
		ready = false
	} else {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := database.DB.PingContext(ctx); err != nil {
			checks["database"] = "error: " + err.Error()
			ready = false
		}
	}

	if !kubeclient.IsConfigured() {
		checks["kubernetes"] = "no_kubeconfig"
		ready = false
	}

	status := http.StatusOK
	state := "ready"
	if !ready {
		status = http.StatusServiceUnavailable
		state = "not_ready"
	}

	c.JSON(status, gin.H{
		"status":      state,
		"checks":      checks,
		"server_time": time.Now().UTC().Format(time.RFC3339),
	})
}
