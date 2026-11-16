package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRadioProxyWithoutServiceSays503(t *testing.T) {
	t.Setenv("KUBENDT_RADIO_URL", "")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Any("/radio/*path", RadioProxy())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/radio/zones", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 without KUBENDT_RADIO_URL, got %d", w.Code)
	}
}

func TestRadioProxyForwardsPathAndDropsCredentials(t *testing.T) {
	var gotPath, gotCookie, gotAuth string
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotCookie, gotAuth = r.URL.Path, r.Header.Get("Cookie"), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer service.Close()
	t.Setenv("KUBENDT_RADIO_URL", service.URL)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Any("/radio/*path", RadioProxy())
	front := httptest.NewServer(r)
	defer front.Close()
	req, _ := http.NewRequest(http.MethodGet, front.URL+"/radio/zones/leganes/coverage.png", nil)
	req.Header.Set("Cookie", "kubendt_session=secret")
	req.Header.Set("Authorization", "Bearer secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || gotPath != "/zones/leganes/coverage.png" {
		t.Fatalf("status %d, service saw path %q", res.StatusCode, gotPath)
	}
	if gotCookie != "" || gotAuth != "" {
		t.Fatalf("credentials reached the service: cookie %q auth %q", gotCookie, gotAuth)
	}
}
