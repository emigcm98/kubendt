package handlers

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"kubendt/auth"

	"github.com/gin-gonic/gin"
)

func cookieSecure() bool {
	// Set KUBENDT_COOKIE_SECURE=true when serving over HTTPS so the session
	// cookie is only sent over TLS. Default is false because the stock compose
	// serves plain HTTP behind nginx.
	return strings.EqualFold(os.Getenv("KUBENDT_COOKIE_SECURE"), "true")
}

func setSessionCookie(c *gin.Context, value string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     auth.CookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   cookieSecure(),
		MaxAge:   int(auth.SessionMaxAge().Seconds()),
	})
}

func clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   cookieSecure(),
		MaxAge:   -1,
	})
}

func bearerToken(c *gin.Context) (string, bool) {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")), true
	}
	return "", false
}

// principalFromRequest resolves a session cookie or bearer token to a principal.
func principalFromRequest(c *gin.Context) (*auth.Principal, bool) {
	if raw, err := c.Cookie(auth.CookieName); err == nil {
		if p, ok := auth.ValidateSession(raw); ok {
			return p, true
		}
	}
	if tok, ok := bearerToken(c); ok {
		if p, ok := auth.ValidateAPIToken(tok); ok {
			return p, true
		}
	}
	return nil, false
}

// ── Middlewares ───────────────────────────────────────────────────────────

// requireAuthAllowlist are paths reachable without authentication: health,
// version and the /auth endpoints (needed to log in / check status).
var requireAuthAllowlist = []string{"/healthz", "/readyz", "/version", "/auth"}

// RequireAuth gates every route (except the allowlist) behind a valid session
// cookie or bearer API token.
func RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !auth.Enabled() || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		for _, prefix := range requireAuthAllowlist {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				c.Next()
				return
			}
		}
		if p, ok := principalFromRequest(c); ok {
			c.Set("principal", p)
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "Authentication required.",
			"code":  "UNAUTHENTICATED",
		})
	}
}

// RequireSessionOrPassword protects API-token management: it accepts a session
// cookie or HTTP Basic password, but NOT a bearer token, so a leaked API token
// cannot mint or revoke tokens.
func RequireSessionOrPassword() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !auth.Enabled() {
			c.Next()
			return
		}
		if raw, err := c.Cookie(auth.CookieName); err == nil {
			if p, ok := auth.ValidateSession(raw); ok {
				c.Set("principal", p)
				c.Next()
				return
			}
		}
		if _, password, ok := c.Request.BasicAuth(); ok && auth.CheckPassword(password) {
			c.Set("principal", &auth.Principal{Identity: "admin", Roles: []string{"admin"}, Method: "password"})
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "A session or the admin password is required for token management.",
			"code":  "SESSION_OR_PASSWORD_REQUIRED",
		})
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────

// Login exchanges the admin password for a session cookie.
func Login(c *gin.Context) {
	if !auth.Enabled() {
		c.JSON(http.StatusOK, gin.H{"authenticated": true, "auth_disabled": true})
		return
	}

	ip := c.ClientIP()
	if ok, retry := auth.LoginAllowed(ip); !ok {
		c.Header("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "Too many login attempts. Try again later.",
			"code":  "RATE_LIMITED",
		})
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload"})
		return
	}
	if !auth.CheckPassword(req.Password) {
		auth.RegisterLoginFailure(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials", "code": "INVALID_CREDENTIALS"})
		return
	}
	auth.RegisterLoginSuccess(ip)
	raw, err := auth.CreateSession("admin", []string{"admin"})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not create session"})
		return
	}
	setSessionCookie(c, raw)
	c.JSON(http.StatusOK, gin.H{"authenticated": true, "identity": "admin", "roles": []string{"admin"}})
}

// Logout revokes the current session and clears the cookie.
func Logout(c *gin.Context) {
	if raw, err := c.Cookie(auth.CookieName); err == nil {
		_ = auth.DeleteSession(raw)
	}
	clearSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"authenticated": false})
}

// AuthMe reports the current authentication state (used by the frontend).
func AuthMe(c *gin.Context) {
	if !auth.Enabled() {
		c.JSON(http.StatusOK, gin.H{"enabled": false, "authenticated": true, "identity": "", "roles": []string{}})
		return
	}
	if p, ok := principalFromRequest(c); ok {
		c.JSON(http.StatusOK, gin.H{"enabled": true, "authenticated": true, "identity": p.Identity, "roles": p.Roles})
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": true, "authenticated": false})
}

// CreateAPIToken mints a token; its value is returned only once. An optional
// expires_in_days sets an expiry (0 or absent = never expires).
func CreateAPIToken(c *gin.Context) {
	var req struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A token name is required"})
		return
	}
	if req.ExpiresInDays < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expires_in_days cannot be negative"})
		return
	}
	var expiresAt int64
	if req.ExpiresInDays > 0 {
		expiresAt = time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour).Unix()
	}
	raw, err := auth.CreateAPIToken(strings.TrimSpace(req.Name), expiresAt)
	if err != nil {
		if errors.Is(err, auth.ErrTokenNameExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "A token with that name already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not create token"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"name": strings.TrimSpace(req.Name), "token": raw})
}

// ListAPITokens returns token metadata (never the secrets).
func ListAPITokens(c *gin.Context) {
	tokens, err := auth.ListAPITokens()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tokens": tokens})
}

// DeleteAPIToken revokes a token by id.
func DeleteAPIToken(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid token id"})
		return
	}
	removed, err := auth.DeleteAPIToken(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !removed {
		c.JSON(http.StatusNotFound, gin.H{"error": "Token not found"})
		return
	}
	c.Status(http.StatusNoContent)
}
