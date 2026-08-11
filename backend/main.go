package main

//	@title			KubeNDT API
//	@version		1.0
//	@description	REST API for KubeNDT.
//	@BasePath		/

import (
	"context"
	"kubendt/auth"
	capabilities_base "kubendt/capabilities"
	"kubendt/database"
	docs "kubendt/docs"
	drivers "kubendt/drivers"
	"kubendt/handlers"
	"kubendt/helpers"
	"kubendt/kubeclient"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func main() {
	// Set-up database
	database.InitDB()
	helpers.SetDB(database.DB)

	// Initialize authentication (loads/generates the admin password, or runs
	// unauthenticated if explicitly disabled).
	if err := auth.Init(); err != nil {
		log.Fatalf("❌ Could not initialize authentication: %v", err)
	}

	// Any namespace_operations row that survived a previous run is stale by
	// definition (no operation is in flight on a freshly-booted process).
	// Wiping them here covers Ctrl-C, OOM-kill, panics, and any other path
	// that skips the per-handler `defer release`, so a namespace never gets
	// stuck "operation in progress" across restarts.
	if cleared, err := helpers.ClearAllNamespaceOperationLocks(); err != nil {
		log.Printf("⚠️ Could not clear stale operation locks at startup: %v", err)
	} else if cleared > 0 {
		log.Printf("🧹 Cleared %d stale namespace operation lock(s) from previous run", cleared)
	}

	// Initialize Kubernetes client. This is non-fatal: with no kubeconfig the
	// server still starts and serves the endpoints needed to load one; the
	// gate middleware returns 503 for cluster operations until then.
	if err := kubeclient.InitClient(); err != nil {
		log.Printf("ℹ️ Starting without a Kubernetes connection; load a kubeconfig via the UI/API to begin")
	}

	// Cluster-dependent startup sync only runs when a kubeconfig is loaded. A
	// transient connection failure is a warning, not a crash.
	if kubeclient.IsConfigured() {
		if err := helpers.RecordActiveCluster(); err != nil {
			log.Printf("⚠️ Could not record active cluster in registry: %v", err)
		}
		if err := helpers.SyncLinkUIDRegistryFromCluster(); err != nil {
			log.Printf("⚠️ Could not sync link UID registry from cluster: %v", err)
		}
	}

	// Initialize capabilities and drivers registry
	capabilities_base.RegisterAllCapabilities()
	drivers.RegisterAllDrivers()

	// host and schemes are left blank in the annotations so the spec is not tied
	// to one deployment. Swagger UI then uses the page's own origin and protocol
	// (http in dev, https behind nginx). In production SWAGGER_BASE_PATH sets the
	// /api prefix.
	if basePath := os.Getenv("SWAGGER_BASE_PATH"); basePath != "" {
		docs.SwaggerInfo.BasePath = basePath
	}

	// Show the running build version in the Swagger badge instead of the static
	// annotation value, which never changes between releases.
	docs.SwaggerInfo.Version = handlers.AppVersion()

	// Create router (gin.New avoids the "already attached" warning from gin.Default)
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())

	// Skip /shell (websocket) and */export (ZIPs are already compressed).
	router.Use(gzip.Gzip(
		gzip.DefaultCompression,
		gzip.WithExcludedPaths([]string{"/shell"}),
		gzip.WithExcludedPathsRegexs([]string{`.*/export$`}),
	))

	router.MaxMultipartMemory = 2 << 20

	// Trust only direct connections (no reverse proxy in front)
	if err := router.SetTrustedProxies(nil); err != nil {
		log.Printf("⚠️ Failed to set trusted proxies: %v", err)
	}

	// Configure CORS
	allowedOrigins := []string{"http://localhost:3000"}
	if envOrigins := os.Getenv("CORS_ALLOWED_ORIGINS"); envOrigins != "" {
		parts := strings.Split(envOrigins, ",")
		allowedOrigins = []string{}
		for _, part := range parts {
			origin := strings.TrimSpace(part)
			if origin != "" {
				allowedOrigins = append(allowedOrigins, origin)
			}
		}
		if len(allowedOrigins) == 0 {
			allowedOrigins = []string{"http://localhost:3000"}
		}
	}

	router.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins, // Allowed frontend origins
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	SetupRoutes(router)

	// Start server on configured port
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Graceful shutdown: catch SIGINT/SIGTERM, stop accepting new requests
	// and give in-flight handlers a window to finish. Their `defer release`
	// on the operation lock then runs naturally, combined with the startup
	// wipe above, stale locks should never linger across restarts.
	// No WriteTimeout: deploy/modify/configure can legitimately take minutes.
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ HTTP server error: %v", err)
		}
	}()
	log.Printf("KubeNDT backend listening on :%s", port)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Printf("🛑 Shutdown signal received, draining in-flight requests (max 30s)...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("⚠️ Forced shutdown after timeout: %v", err)
	} else {
		log.Printf("✅ Clean shutdown complete")
	}
}
