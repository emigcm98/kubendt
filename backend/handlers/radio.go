package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// RadioProxy forwards /radio/* to the radio service: the companion that
// builds radio zones (a 3D scene from OpenStreetMap buildings, the road
// network and ray-traced coverage maps) on a host with a GPU. The service
// has no authentication of its own; it is reached only through here, behind
// the session or token every other route needs. KUBENDT_RADIO_URL names it,
// and without it the routes answer 503 so the dashboard can say why.
func RadioProxy() gin.HandlerFunc {
	target := strings.TrimSpace(os.Getenv("KUBENDT_RADIO_URL"))
	if target == "" {
		return func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "The radio service is not configured. Set KUBENDT_RADIO_URL on the backend.",
				"code":  "NO_RADIO_SERVICE",
			})
		}
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "KUBENDT_RADIO_URL is not a valid URL.", "code": "NO_RADIO_SERVICE"})
		}
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(gin.H{"error": "The radio service does not answer: " + err.Error(), "code": "RADIO_SERVICE_DOWN"})
	}
	return func(c *gin.Context) {
		r := c.Request
		r.URL.Path = radioServicePath(c.Param("path"))
		r.URL.RawPath = ""
		r.Host = u.Host
		// The session stays here: the service never sees the caller's credentials.
		r.Header.Del("Cookie")
		r.Header.Del("Authorization")
		proxy.ServeHTTP(c.Writer, r)
	}
}

// radioServicePath is the path the service sees for a /radio/<path> request.
func radioServicePath(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}
