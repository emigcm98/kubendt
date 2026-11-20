package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"kubendt/auth"

	"github.com/gin-gonic/gin"
)

// The middleware against a table of accounts and levels, the namespace's
// access answered by a stub instead of the cluster.
func accessRouter(t *testing.T, p *auth.Principal, levels map[string]string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	prevLevel, prevHistory := levelFor, historyNamespace
	levelFor = func(ns string, principal *auth.Principal) (string, error) {
		if principal.IsAdmin() {
			return auth.LevelOwner, nil
		}
		return levels[ns], nil
	}
	historyNamespace = func(id int64) (string, bool) {
		if id == 42 {
			return "lab", true
		}
		return "", false
	}
	t.Cleanup(func() { levelFor, historyNamespace = prevLevel, prevHistory })
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if p != nil {
			c.Set("principal", p)
		}
		c.Next()
	})
	r.Use(RequireAccess())
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	for _, rule := range routePermissions {
		r.Handle(rule.method, rule.path, ok)
	}
	r.GET("/unlisted/:namespace", ok)
	return r
}

func TestRequireAccessByLevelAndRole(t *testing.T) {
	t.Setenv("KUBENDT_AUTH_DISABLED", "")
	enabledBefore := auth.Enabled()
	if !enabledBefore {
		t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw")
		_ = auth.Init()
	}
	viewer := &auth.Principal{Identity: "vic", Role: auth.RoleUser, Roles: []string{auth.RoleUser}}
	operator := &auth.Principal{Identity: "ops", Role: auth.RoleUser, Roles: []string{auth.RoleUser}}
	editor := &auth.Principal{Identity: "ed", Role: auth.RoleUser, Roles: []string{auth.RoleUser}}
	owner := &auth.Principal{Identity: "own", Role: auth.RoleUser, Roles: []string{auth.RoleUser}}
	admin := &auth.Principal{Identity: "adm", Role: auth.RoleAdmin, Roles: []string{auth.RoleAdmin}}
	levels := map[string]map[string]string{
		"vic": {"lab": auth.LevelViewer}, "ops": {"lab": auth.LevelOperator},
		"ed": {"lab": auth.LevelEditor}, "own": {"lab": auth.LevelOwner}, "adm": {},
	}
	cases := []struct {
		who          *auth.Principal
		method, path string
		want         int
	}{
		{viewer, "GET", "/pods/lab", 200},
		{viewer, "GET", "/files/lab/notes.txt", 200},
		{viewer, "GET", "/shell/ws/lab/r1-0", 403},
		{viewer, "POST", "/network/configure/lab", 403},
		{viewer, "GET", "/pods/other", 403},
		{operator, "GET", "/shell/ws/lab/r1-0", 200},
		{operator, "GET", "/trace/run/lab/r1-0", 200},
		{operator, "PATCH", "/pods/restart/lab/r1-0", 403},
		{editor, "POST", "/network/configure/lab", 200},
		{editor, "PUT", "/files/lab/notes.txt", 200},
		{editor, "DELETE", "/drivers/history/42", 200},
		{editor, "DELETE", "/drivers/history/7", 404},
		{editor, "DELETE", "/namespaces/lab", 403},
		{editor, "DELETE", "/network/clear-topology/lab", 403},
		{owner, "DELETE", "/namespaces/lab", 200},
		{owner, "GET", "/users", 403},
		{owner, "GET", "/cluster/status", 403},
		{owner, "GET", "/state", 403},
		{owner, "GET", "/state/kubendt-system", 403},
		{owner, "POST", "/namespaces/", 403},
		{owner, "GET", "/namespaces/", 200},
		{owner, "GET", "/drivers/", 200},
		{admin, "GET", "/users", 200},
		{admin, "GET", "/state/kubendt-system", 200},
		{admin, "POST", "/namespaces/", 200},
		{admin, "DELETE", "/namespaces/anything", 200},
		{admin, "POST", "/kube/context", 200},
		{viewer, "GET", "/unlisted/lab", 403},
		{nil, "GET", "/healthz", 200},
	}
	for _, tc := range cases {
		r := accessRouter(t, tc.who, levels[identityOf(tc.who)])
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.want {
			t.Errorf("%s %s %s: %d, want %d (%s)", identityOf(tc.who), tc.method, tc.path, w.Code, tc.want, w.Body.String())
		}
	}
}

func identityOf(p *auth.Principal) string {
	if p == nil {
		return "nobody"
	}
	return p.Identity
}
