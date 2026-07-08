package main

import (
	"kubendt/handlers"

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// Define REST routes
func SetupRoutes(router *gin.Engine) {
	// Auth is the outer gate: reject unauthenticated requests first, then the
	// kubeconfig gate rejects cluster ops until a kubeconfig is loaded.
	router.Use(handlers.RequireAuth())
	router.Use(handlers.RequireKubeconfig())

	router.GET("/healthz", handlers.Healthz)
	router.GET("/readyz", handlers.Readyz)
	router.GET("/version", handlers.Version)

	authGroup := router.Group("/auth")
	{
		authGroup.POST("/login", handlers.Login)
		authGroup.POST("/logout", handlers.Logout)
		authGroup.GET("/me", handlers.AuthMe)

		// Token management: session or admin password only (never a bearer
		// token), so a leaked API token cannot mint or revoke tokens.
		tokens := authGroup.Group("/tokens")
		tokens.Use(handlers.RequireSessionOrPassword())
		{
			tokens.GET("", handlers.ListAPITokens)
			tokens.POST("", handlers.CreateAPIToken)
			tokens.DELETE("/:id", handlers.DeleteAPIToken)
		}
	}

	shellGroup := router.Group("/shell")
	{
		shellGroup.GET("/ws/:namespace/:podName", handlers.InteractiveShellWebSocket)
	}

	captureGroup := router.Group("/capture")
	{
		// Live packet capture on a pod interface (WebSocket stream)
		captureGroup.GET("/ws/:namespace/:podName/:interface", handlers.CaptureWebSocket)
		// Download the pcap a capture wrote (all, or ?frames= for a subset)
		captureGroup.GET("/pcap/:namespace/:podName/:container", handlers.DownloadCapturePcap)
		// Empty a stopped capture's pcap (Clear)
		captureGroup.POST("/clear/:namespace/:podName/:container", handlers.ClearCapture)
		// Full JSON dissection of one packet from a capture
		captureGroup.GET("/packet/:namespace/:podName/:container/:num", handlers.GetCapturePacketDetail)
	}

	podGroup := router.Group("/pods")
	{
		// 1. Get pod list
		podGroup.GET("/:namespace", handlers.ListPods)
		// 2. Restart pod (StatefulSet)
		podGroup.PATCH("/restart/:namespace/:podName", handlers.RestartPod)
		// 3. Get pod IP, MAC, NAT
		podGroup.GET("/ips/:namespace/:podName", handlers.GetInterfacesFromPod)
		// 4. Get Qdisc info
		podGroup.GET("/tc/:namespace/:podName/:interface", handlers.ShowQdisc)
		// 5. Get pod CPU/RAM metrics (requires metrics-server)
		podGroup.GET("/metrics/:namespace/:podName", handlers.GetPodMetrics)
	}

	nsGroup := router.Group("/namespaces")
	{
		// 1. List namespaces
		nsGroup.GET("/", handlers.ListNamespaces)
		nsGroup.GET("", handlers.ListNamespaces)
		// Namespace summary
		nsGroup.GET("/summary/:namespace", handlers.GetNamespaceSummary)
		// In-progress operation lock (cheap DB read; polled by the UI)
		nsGroup.GET("/operation/:namespace", handlers.GetNamespaceOperation)
		// Namespace CPU/RAM metrics (per-pod + aggregated, requires metrics-server)
		nsGroup.GET("/metrics/:namespace", handlers.GetNamespaceMetrics)
		// 2. Create namespace
		nsGroup.POST("/", handlers.CreateNamespace)
		// 3. Delete namespace
		nsGroup.DELETE("/:namespace", handlers.DeleteNamespace)
		// 4. Get pod interface status
		nsGroup.GET("/ips/:namespace", handlers.GetInterfacesInNamespace)
	}

	netGroup := router.Group("/network")
	{
		// Deploy Network
		netGroup.POST("/deploy-network/:namespace", handlers.DeployNetwork)
		// Clear all topology resources in namespace without deleting the namespace
		netGroup.DELETE("/clear-topology/:namespace", handlers.ClearTopology)
		// Modify Network (add/delete)
		netGroup.POST("/modify-network/:namespace", handlers.ModifyNetwork)
		// Get Network deployment
		netGroup.GET("/get-network/:namespace", handlers.GetNetwork)
		// Save node positions
		netGroup.POST("/positions/:namespace", handlers.SaveNodePositions)
		// Get node positions
		netGroup.GET("/positions/:namespace", handlers.GetNodePositions)
		// Configure Pods via DRIVERS (includes network config)
		netGroup.POST("/configure/:namespace", handlers.ConfigureNetwork)
	}

	// === File Control Routes ===
	router.POST("/file-ops/:namespace/folder", handlers.CreateFolder)
	router.POST("/file-ops/:namespace/import", handlers.ImportArchive)
	router.POST("/file-ops/:namespace/rename", handlers.RenameFile)
	router.GET("/file-ops/:namespace/export", handlers.ExportAsZip)

	// === File Content Routes ===
	router.GET("/files/:namespace", handlers.ListFiles)
	router.DELETE("/files/:namespace", handlers.DeleteAllNamespaceFiles) // delete every file in the namespace
	router.POST("/files/:namespace/", handlers.UploadFile)
	router.GET("/files/:namespace/*filename", handlers.GetFileContent)
	router.PUT("/files/:namespace/*filename", handlers.UpdateFileContent)
	router.PUT("/file-meta/:namespace/*filename", handlers.UpdateFileMeta)
	router.DELETE("/files/:namespace/*filename", handlers.DeleteFile)

	driversGroup := router.Group("/drivers")
	{
		// List available drivers
		driversGroup.GET("/", handlers.GetDrivers)
		// Full info for a single driver (same per-item shape as GET /drivers/).
		driversGroup.GET("/:driver", handlers.GetDriver)
		// List persisted driver operations for namespace
		driversGroup.GET("/history/:namespace", handlers.GetNamespaceDriverOperationHistory)
		// List persisted driver operations for pod
		driversGroup.GET("/history/:namespace/:podName", handlers.GetPodDriverOperationHistory)
		// Delete one persisted driver operation by ID
		driversGroup.DELETE("/history/:id", handlers.DeleteDriverOperationHistory)
		// Delete persisted driver operations for one pod
		driversGroup.DELETE("/history/namespace/:namespace/pod/:podName", handlers.DeletePodDriverOperationHistory)
		// Delete persisted driver operations for a whole namespace
		driversGroup.DELETE("/history/namespace/:namespace", handlers.DeleteNamespaceDriverOperationHistory)
	}

	clusterGroup := router.Group("/cluster")
	{
		// Get cluster status and nodes info
		clusterGroup.GET("/status", handlers.GetClusterStatus)
		// Get a single node's detailed info
		clusterGroup.GET("/nodes/:name", handlers.GetClusterNodeDetail)
	}

	kubeGroup := router.Group("/kube")
	{
		kubeGroup.GET("/config", handlers.GetKubeConfigInfo)
		kubeGroup.POST("/context", handlers.SetKubeContext)
		kubeGroup.POST("/config", handlers.LoadKubeConfig)
	}

	// Swagger UI, GET /swagger/index.html
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))

}
