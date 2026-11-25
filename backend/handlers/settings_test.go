package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kubendt/auth"

	"github.com/gin-gonic/gin"
)

func settingsRouter(p *auth.Principal) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("principal", p)
		c.Next()
	})
	r.PUT("/settings", SaveSettings)
	return r
}

func putSettings(r *gin.Engine, body string) (int, map[string]any) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// Admins read the settings; only the owner saves them, and a value the
// setting's own check refuses never gets saved.
func TestSettingsAreTheOwners(t *testing.T) {
	t.Setenv("KUBENDT_AUTH_DISABLED", "")
	if !auth.Enabled() {
		t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw")
		_ = auth.Init()
	}
	admin := &auth.Principal{Identity: "adm", Role: auth.RoleAdmin, Roles: []string{auth.RoleAdmin}}
	owner := &auth.Principal{Identity: auth.OwnerAccount, Role: auth.RoleOwner, Roles: []string{auth.RoleOwner}}

	if code, out := putSettings(settingsRouter(admin), `{"values":{"KUBENDT_LINK_MTU":"1400"}}`); code != 403 || out["code"] != "OWNER_ONLY" {
		t.Fatalf("an admin saving: %d %v", code, out)
	}
	if code, out := putSettings(settingsRouter(owner), `{"values":{"KUBENDT_ADMIN_PASSWORD":"x"}}`); code != 400 || out["code"] != "INVALID_SETTING" {
		t.Fatalf("a variable the portal may not change: %d %v", code, out)
	}
	if code, out := putSettings(settingsRouter(owner), `{"values":{"KUBENDT_LINK_MTU":"9"}}`); code != 400 || out["key"] != "KUBENDT_LINK_MTU" {
		t.Fatalf("an MTU below the floor: %d %v", code, out)
	}
}
