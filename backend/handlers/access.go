package handlers

import (
	"log"
	"net/http"
	"strconv"

	"kubendt/auth"
	"kubendt/helpers"

	"github.com/gin-gonic/gin"
)

// Every route and the permission it needs, next to the routes themselves. A
// route absent from here fails a test, so nothing ships unguarded. The
// permission is asked of the account's level in the namespace the route
// names, or of its platform role when the route names none.
type routeRule struct {
	method, path string
	perm         auth.Permission
}

var routePermissions = []routeRule{
	// Reachable before signing in, or by any account.
	{"GET", "/healthz", auth.PermAny},
	{"GET", "/readyz", auth.PermAny},
	{"GET", "/version", auth.PermAny},
	{"GET", "/swagger/*any", auth.PermAny},
	{"POST", "/auth/login", auth.PermAny},
	{"POST", "/auth/logout", auth.PermAny},
	{"GET", "/auth/me", auth.PermAny},
	{"PUT", "/auth/password", auth.PermAny},
	{"GET", "/auth/tokens", auth.PermAny},
	{"POST", "/auth/tokens", auth.PermAny},
	{"DELETE", "/auth/tokens/:id", auth.PermAny},
	{"GET", "/drivers/", auth.PermAny},
	{"GET", "/drivers/:driver", auth.PermAny},
	{"GET", "/namespaces/", auth.PermAny},
	{"GET", "/namespaces", auth.PermAny},
	{"POST", "/namespaces/", auth.PermCreateNamespace},

	// The platform: the cluster, the kubeconfig, the users, the state of
	// every namespace.
	{"GET", "/users", auth.PermAdmin},
	{"POST", "/users", auth.PermAdmin},
	{"PUT", "/users/:name", auth.PermAdmin},
	{"DELETE", "/users/:name", auth.PermAdmin},
	{"POST", "/users/:name/password", auth.PermAdmin},
	{"GET", "/cluster/status", auth.PermAdmin},
	{"GET", "/cluster/nodes/:name", auth.PermAdmin},
	{"GET", "/kube/config", auth.PermAdmin},
	{"POST", "/kube/context", auth.PermAdmin},
	{"POST", "/kube/config", auth.PermAdmin},
	{"GET", "/state", auth.PermAdmin},

	// Seeing a namespace.
	{"GET", "/namespaces/summary/:namespace", auth.PermView},
	{"GET", "/namespaces/operation/:namespace", auth.PermView},
	{"GET", "/namespaces/metrics/:namespace", auth.PermView},
	{"GET", "/namespaces/ips/:namespace", auth.PermView},
	{"GET", "/namespaces/access/:namespace", auth.PermView},
	{"GET", "/pods/:namespace", auth.PermView},
	{"GET", "/pods/ips/:namespace/:podName", auth.PermView},
	{"GET", "/pods/tc/:namespace/:podName/:interface", auth.PermView},
	{"GET", "/pods/tc/:namespace/:podName", auth.PermView},
	{"GET", "/pods/metrics/:namespace/:podName", auth.PermView},
	{"GET", "/pods/counters/:namespace/:podName", auth.PermView},
	{"GET", "/pods/inspect/:namespace/:podName", auth.PermView},
	{"GET", "/pods/logs/:namespace/:podName", auth.PermView},
	{"GET", "/pods/logstream/:namespace/:podName", auth.PermView},
	{"GET", "/network/get-network/:namespace", auth.PermView},
	{"GET", "/network/links/:namespace", auth.PermView},
	{"GET", "/network/positions/:namespace", auth.PermView},
	{"GET", "/network/layout/:namespace", auth.PermView},
	{"GET", "/network/export-conf/:namespace", auth.PermView},
	{"GET", "/files/:namespace", auth.PermView},
	{"GET", "/files/:namespace/*filename", auth.PermView},
	{"GET", "/file-ops/:namespace/export", auth.PermView},
	{"GET", "/drivers/history/:namespace", auth.PermView},
	{"GET", "/drivers/history/:namespace/:podName", auth.PermView},
	{"GET", "/state/:namespace", auth.PermView},
	{"GET", "/state/:namespace/:kind/:name", auth.PermView},

	// Working on its pods without changing the lab.
	{"GET", "/shell/ws/:namespace/:podName", auth.PermOperate},
	{"GET", "/capture/ws/:namespace/:podName/:interface", auth.PermOperate},
	{"GET", "/capture/pcap/:namespace/:podName/:container", auth.PermOperate},
	{"POST", "/capture/clear/:namespace/:podName/:container", auth.PermOperate},
	{"GET", "/capture/packet/:namespace/:podName/:container/:num", auth.PermOperate},
	{"GET", "/trace/ws/:namespace/:podName", auth.PermOperate},
	{"GET", "/trace/run/:namespace/:podName", auth.PermOperate},
	{"GET", "/iperf/ws/:namespace/:podName", auth.PermOperate},
	{"GET", "/iperf/run/:namespace/:podName", auth.PermOperate},

	// Changing the lab.
	{"PATCH", "/pods/restart/:namespace/:podName", auth.PermEdit},
	{"POST", "/network/deploy-network/:namespace", auth.PermEdit},
	{"POST", "/network/modify-network/:namespace", auth.PermEdit},
	{"POST", "/network/positions/:namespace", auth.PermEdit},
	{"PUT", "/network/layout/:namespace", auth.PermEdit},
	{"POST", "/network/configure/:namespace", auth.PermEdit},
	{"POST", "/file-ops/:namespace/folder", auth.PermEdit},
	{"POST", "/file-ops/:namespace/import", auth.PermEdit},
	{"POST", "/file-ops/:namespace/rename", auth.PermEdit},
	{"DELETE", "/files/:namespace", auth.PermEdit},
	{"POST", "/files/:namespace/", auth.PermEdit},
	{"PUT", "/files/:namespace/*filename", auth.PermEdit},
	{"PUT", "/file-meta/:namespace/*filename", auth.PermEdit},
	{"DELETE", "/files/:namespace/*filename", auth.PermEdit},
	{"DELETE", "/drivers/history/:id", auth.PermEdit},
	{"DELETE", "/drivers/history/namespace/:namespace/pod/:podName", auth.PermEdit},
	{"DELETE", "/drivers/history/namespace/:namespace", auth.PermEdit},

	// Disposing of it.
	{"DELETE", "/namespaces/:namespace", auth.PermOwn},
	{"PUT", "/namespaces/access/:namespace", auth.PermOwn},
	{"DELETE", "/network/clear-topology/:namespace", auth.PermOwn},
}

// RouteRule is one line of the table, for the test that checks it against
// the registered routes.
type RouteRule struct {
	Method, Path string
	Permission   auth.Permission
}

// RouteRules is the table as declared.
func RouteRules() []RouteRule {
	out := make([]RouteRule, 0, len(routePermissions))
	for _, r := range routePermissions {
		out = append(out, RouteRule{Method: r.method, Path: r.path, Permission: r.perm})
	}
	return out
}

// RoutePermission is what a route needs, by method and registered pattern.
func RoutePermission(method, path string) (auth.Permission, bool) {
	for _, r := range routePermissions {
		if r.method == method && r.path == path {
			return r.perm, true
		}
	}
	return "", false
}

// levelFor tells an account's level in a namespace, a variable so the
// tests can answer without a cluster.
var levelFor = helpers.LevelForNamespace

// historyNamespace is the namespace an operation id belongs to.
var historyNamespace = helpers.HistoryEntryNamespace

// RequireAccess checks, after RequireAuth, that the account may do what the
// route does: a platform permission against its role, a namespace one
// against its level in the namespace the route names. The system namespace
// is the admins' through every route. Every request that changes something
// leaves a line in the log with who did it.
func RequireAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !auth.Enabled() || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		pattern := c.FullPath()
		if pattern == "" {
			c.Next()
			return
		}
		perm, known := RoutePermission(c.Request.Method, pattern)
		if !known {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "This route has no permission declared.", "code": "FORBIDDEN"})
			return
		}
		p := PrincipalOf(c)
		if p == nil {
			// The allowlist of RequireAuth: nothing to check without an account.
			c.Next()
			return
		}
		namespace, ok := namespaceOfRequest(c, pattern)
		if !ok {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "Not found."})
			return
		}
		allowed := false
		level := ""
		switch {
		case namespace != "" && namespace == helpers.SystemNamespace():
			allowed = p.GrantsPlatform(auth.PermAdmin)
		case namespace != "":
			var err error
			if level, err = levelFor(namespace, p); err != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			allowed = auth.LevelGrants(level, perm) || p.GrantsPlatform(perm)
		default:
			allowed = p.GrantsPlatform(perm)
		}
		if !allowed {
			if namespace != "" && level == "" && !p.IsAdmin() {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "No access to this namespace.", "code": "FORBIDDEN"})
				return
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Not allowed for this account.", "code": "FORBIDDEN"})
			return
		}
		if c.Request.Method != http.MethodGet {
			who := p.Identity
			if level != "" {
				who += " (" + level + ")"
			} else {
				who += " (" + p.Role + ")"
			}
			log.Printf("👤 %s %s %s", who, c.Request.Method, c.Request.URL.Path)
		}
		c.Next()
	}
}

// namespaceOfRequest is the namespace a route works on: its :namespace
// parameter, or the one the history entry of a DELETE by id belongs to; ""
// for a route of the platform. The second value is false when the route
// names a thing that is not there.
func namespaceOfRequest(c *gin.Context, pattern string) (string, bool) {
	if ns := c.Param("namespace"); ns != "" {
		return ns, true
	}
	if pattern == "/drivers/history/:id" {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			return "", true
		}
		ns, found := historyNamespace(id)
		if !found {
			return "", false
		}
		return ns, true
	}
	return "", true
}
