package handlers

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"kubendt/auth"
	"kubendt/helpers"

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

// sessionFromRequest validates the session cookie and, when the session
// earned a fresh cookie, sets it on the response.
func sessionFromRequest(c *gin.Context) (*auth.Principal, bool) {
	raw, err := c.Cookie(auth.CookieName)
	if err != nil {
		return nil, false
	}
	p, refreshed, ok := auth.ValidateSession(raw)
	if !ok {
		return nil, false
	}
	if refreshed != "" {
		setSessionCookie(c, refreshed)
	}
	return p, true
}

// principalFromRequest resolves a session cookie or bearer token to a principal.
func principalFromRequest(c *gin.Context) (*auth.Principal, bool) {
	if p, ok := sessionFromRequest(c); ok {
		return p, true
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
			if p.MustChangePassword && !passwordChangeAllowlisted(path) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error": "Change your password first (PUT /auth/password).",
					"code":  "PASSWORD_CHANGE_REQUIRED",
				})
				return
			}
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

// passwordChangeAllowlisted is what an account with a password to change may
// still call: who it is, the change itself, and leaving.
func passwordChangeAllowlisted(path string) bool {
	return path == "/auth/me" || path == "/auth/password" || path == "/auth/logout"
}

// PrincipalOf is the identity the middleware attached to the request, nil
// when auth is disabled.
func PrincipalOf(c *gin.Context) *auth.Principal {
	if v, ok := c.Get("principal"); ok {
		if p, ok := v.(*auth.Principal); ok {
			return p
		}
	}
	return nil
}

// RequireAdmin lets admins and the owner through, after RequireAuth or
// RequireSessionOrPassword put the principal on the request.
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !auth.Enabled() {
			c.Next()
			return
		}
		if p := PrincipalOf(c); p != nil && p.IsAdmin() {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "An admin is required.", "code": "FORBIDDEN"})
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
		p, ok := sessionFromRequest(c)
		if !ok {
			if username, password, has := c.Request.BasicAuth(); has {
				p, ok = auth.Authenticate(username, password)
			}
		}
		if ok {
			// A password to change blocks tokens too, or the change could be
			// walked around by minting one.
			if p.MustChangePassword && c.Request.URL.Path != "/auth/password" {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error": "Change your password first (PUT /auth/password).",
					"code":  "PASSWORD_CHANGE_REQUIRED",
				})
				return
			}
			c.Set("principal", p)
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
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload"})
		return
	}
	// No username is the owner account, so the login of every script and of
	// the UI before users keeps working.
	p, ok := auth.Authenticate(strings.TrimSpace(req.Username), req.Password)
	if !ok {
		auth.RegisterLoginFailure(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials", "code": "INVALID_CREDENTIALS"})
		return
	}
	auth.RegisterLoginSuccess(ip)
	raw, err := auth.CreateSession(p.Identity)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not create session"})
		return
	}
	setSessionCookie(c, raw)
	c.JSON(http.StatusOK, meOf(p))
}

// meOf is what the UI learns about the account it is signed in as.
func meOf(p *auth.Principal) gin.H {
	out := gin.H{
		"enabled":              true,
		"authenticated":        true,
		"identity":             p.Identity,
		"username":             p.Identity,
		"role":                 p.Role,
		"roles":                p.Roles,
		"must_change_password": p.MustChangePassword,
	}
	if u, ok := auth.GetUser(p.Identity); ok {
		out["can_create_namespaces"] = u.CanCreateNamespaces || p.IsAdmin()
	}
	return out
}

// ChangePassword is an account changing its own password: the current one
// must match, its other sessions end, and this one gets a fresh cookie.
func ChangePassword(c *gin.Context) {
	p := PrincipalOf(c)
	if p == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required.", "code": "UNAUTHENTICATED"})
		return
	}
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.New) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "current_password and new_password are required"})
		return
	}
	if err := auth.ChangePassword(p.Identity, req.Current, req.New); err != nil {
		if errors.Is(err, auth.ErrBadPassword) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "The current password does not match", "code": "INVALID_CREDENTIALS"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p.Method == "session" {
		if raw, err := auth.CreateSession(p.Identity); err == nil {
			setSessionCookie(c, raw)
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "password changed"})
}

// Logout clears the cookie; a session is the cookie itself, nothing is kept
// of it on the server.
func Logout(c *gin.Context) {
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
		c.JSON(http.StatusOK, meOf(p))
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
	p := PrincipalOf(c)
	if p == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required.", "code": "UNAUTHENTICATED"})
		return
	}
	raw, err := auth.CreateAPIToken(p.Identity, strings.TrimSpace(req.Name), expiresAt)
	if err != nil {
		if errors.Is(err, auth.ErrTokenNameExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "A token with that name already exists"})
			return
		}
		if errors.Is(err, auth.ErrNoStore) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Load a kubeconfig first, API tokens are kept in the cluster"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not create token"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"name": strings.TrimSpace(req.Name), "token": raw})
}

// ListAPITokens returns the account's token metadata (never the secrets);
// with ?all=true an admin also sees the plain users' and the owner everyone's.
func ListAPITokens(c *gin.Context) {
	user := ""
	p := PrincipalOf(c)
	if p != nil {
		user = p.Identity
		if c.Query("all") == "true" && p.IsAdmin() {
			user = ""
		}
	}
	tokens, err := auth.ListAPITokens(user)
	if err == nil && user == "" {
		kept := tokens[:0]
		for _, t := range tokens {
			if auth.MayManageTokensOf(p, t.User) {
				kept = append(kept, t)
			}
		}
		tokens = kept
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tokens": tokens})
}

// DeleteAPIToken revokes a token by id: one's own, a plain user's for an
// admin, anyone's for the owner.
func DeleteAPIToken(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid token id"})
		return
	}
	if p := PrincipalOf(c); p != nil && !auth.MayManageTokensOf(p, auth.TokenOwner(id)) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Token not found"})
		return
	}
	removed, err := auth.DeleteAPIToken(id)
	if err != nil {
		if errors.Is(err, auth.ErrNoStore) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Load a kubeconfig first, API tokens are kept in the cluster"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !removed {
		c.JSON(http.StatusNotFound, gin.H{"error": "Token not found"})
		return
	}
	c.Status(http.StatusNoContent)
}

// GET /auth/quota  -> the account's limits and what it has against them.
// Under /auth the gate does not run, so the account is read here.
func AuthQuota(c *gin.Context) {
	if !auth.Enabled() {
		c.JSON(http.StatusOK, gin.H{"quota": helpers.Quota{}, "usage": helpers.QuotaUsage{}})
		return
	}
	p, ok := principalFromRequest(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required.", "code": "UNAUTHENTICATED"})
		return
	}
	usage, err := helpers.UsageOf(p.Identity)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"username": p.Identity, "quota": helpers.QuotaFor(p.Identity), "usage": usage})
}
