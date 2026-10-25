package main

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// A namespace or pod named like a static segment of a sibling route ("logs",
// "ws", "metrics") must still reach the parameterized handler: gin prefers
// the static branch and backtracks when the rest of the path does not fit.
// The routes are replayed on a bare engine so no handler runs.
func TestRoutesSurviveReservedNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	real := gin.New()
	SetupRoutes(real)
	shadow := gin.New()
	for _, r := range real.Routes() {
		shadow.Handle(r.Method, r.Path, func(c *gin.Context) { c.String(200, c.FullPath()) })
	}

	words := []string{"logs", "ws", "ips", "metrics", "counters", "restart", "tc", "operation", "summary",
		"history", "namespace", "pod", "run", "pcap", "packet", "clear", "export", "folder", "import",
		"rename", "positions", "links", "configure", "deploy-network", "default", "logstream"}
	type tc struct{ method, path, want string }
	var cases []tc
	for _, w := range words {
		cases = append(cases,
			tc{"GET", "/pods/" + w, "/pods/:namespace"},
			tc{"GET", "/pods/logs/" + w + "/" + w, "/pods/logs/:namespace/:podName"},
			tc{"GET", "/pods/logstream/" + w + "/" + w, "/pods/logstream/:namespace/:podName"},
			tc{"GET", "/pods/tc/" + w + "/" + w, "/pods/tc/:namespace/:podName"},
			tc{"GET", "/pods/tc/" + w + "/" + w + "/" + w, "/pods/tc/:namespace/:podName/:interface"},
			tc{"DELETE", "/namespaces/" + w, "/namespaces/:namespace"},
			tc{"GET", "/namespaces/summary/" + w, "/namespaces/summary/:namespace"},
			tc{"GET", "/drivers/history/" + w, "/drivers/history/:namespace"},
			tc{"GET", "/drivers/history/" + w + "/" + w, "/drivers/history/:namespace/:podName"},
			tc{"DELETE", "/drivers/history/namespace/" + w, "/drivers/history/namespace/:namespace"},
			tc{"DELETE", "/drivers/history/namespace/" + w + "/pod/" + w, "/drivers/history/namespace/:namespace/pod/:podName"},
			tc{"GET", "/files/" + w, "/files/:namespace"},
			tc{"GET", "/files/" + w + "/" + w + "/x.txt", "/files/:namespace/*filename"},
			tc{"GET", "/trace/run/" + w + "/" + w, "/trace/run/:namespace/:podName"},
			tc{"POST", "/network/configure/" + w, "/network/configure/:namespace"},
		)
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		shadow.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != 200 || w.Body.String() != c.want {
			t.Errorf("%s %s: matched %q (%d), want %s", c.method, c.path, w.Body.String(), w.Code, c.want)
		}
	}
}
