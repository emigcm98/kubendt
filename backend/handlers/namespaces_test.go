package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// A namespace named like a page of the UI could never be opened, so the
// handler refuses it before touching the cluster.
func TestCreateNamespaceRefusesReservedNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/namespaces/", CreateNamespace)
	for name := range reservedNamespaceNames {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/namespaces/", strings.NewReader(`{"namespace":"`+name+`"}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, w.Code)
		}
	}
}
