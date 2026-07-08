// Package auth handles password login, browser sessions (opaque tokens stored
// hashed in SQLite, sent as an HttpOnly cookie) and API tokens for scripts.
// Credential checks go through the Authenticator interface so another backend
// (e.g. LDAP) can be added later.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"kubendt/database"

	"golang.org/x/crypto/bcrypt"
)

// CookieName is the session cookie the browser carries (also sent on the
// WebSocket handshake, which is why cookie-based auth covers shells/capture).
const CookieName = "kubendt_session"

const apiTokenPrefix = "kdt_"

// Principal is the authenticated identity attached to a request.
type Principal struct {
	Identity string
	Roles    []string
	Method   string // "session" | "token" | "password"
}

// HasRole reports whether the principal carries the given role. Authorization
// (RBAC) can build on this later; v1 grants every authenticated user "admin".
func (p *Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

var (
	enabled     bool
	adminHash   []byte
	idleTimeout time.Duration
	maxLifetime time.Duration
)

// Authenticator verifies a username/password pair. LocalAuthenticator is the
// only implementation today; an LDAP one would satisfy the same interface.
type Authenticator interface {
	Authenticate(username, password string) (*Principal, error)
}

// LocalAuthenticator checks against the single admin password.
type LocalAuthenticator struct{}

func (LocalAuthenticator) Authenticate(username, password string) (*Principal, error) {
	if !CheckPassword(password) {
		return nil, errors.New("invalid credentials")
	}
	return &Principal{Identity: "admin", Roles: []string{"admin"}, Method: "password"}, nil
}

// Init loads the auth configuration. It must be called after database.InitDB.
func Init() error {
	if strings.EqualFold(os.Getenv("KUBENDT_AUTH_DISABLED"), "true") {
		enabled = false
		log.Printf("⚠️  AUTH DISABLED (KUBENDT_AUTH_DISABLED=true): the API is UNAUTHENTICATED. Run only on a trusted network.")
		return nil
	}
	enabled = true
	idleTimeout = hoursFromEnv("KUBENDT_SESSION_IDLE_HOURS", 12*time.Hour)
	maxLifetime = hoursFromEnv("KUBENDT_SESSION_MAX_HOURS", 7*24*time.Hour)

	if err := resolveAdminPassword(); err != nil {
		return err
	}

	// Sessions do not survive a restart: clear them so a bounce forces re-login
	// (this also covers a password change). API tokens are left untouched.
	if n, err := InvalidateAllSessions(); err != nil {
		return err
	} else if n > 0 {
		log.Printf("🔒 Cleared %d session(s) on startup; users must sign in again", n)
	}
	return nil
}

// resolveAdminPassword sets adminHash from KUBENDT_ADMIN_PASSWORD (persisting it
// as the source of truth), a previously stored hash, or a freshly generated
// password on first run.
func resolveAdminPassword() error {
	stored, err := loadPasswordHash()
	if err != nil {
		return err
	}

	if pw := os.Getenv("KUBENDT_ADMIN_PASSWORD"); pw != "" {
		// Reuse the stored hash when the password is unchanged (avoids
		// rewriting it on every boot); otherwise persist the new one.
		if stored != "" && bcrypt.CompareHashAndPassword([]byte(stored), []byte(pw)) == nil {
			adminHash = []byte(stored)
			return nil
		}
		h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if err := savePasswordHash(string(h)); err != nil {
			return err
		}
		adminHash = h
		return nil
	}

	if stored != "" {
		adminHash = []byte(stored)
		return nil
	}

	pw, err := randomToken(18)
	if err != nil {
		return err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := savePasswordHash(string(h)); err != nil {
		return err
	}
	adminHash = h
	log.Printf("🔑 Generated admin password (shown once; set KUBENDT_ADMIN_PASSWORD to override):")
	log.Printf("      user: admin   password: %s", pw)
	return nil
}

// InvalidateAllSessions revokes every browser session. API tokens are
// unaffected; revoke those individually.
func InvalidateAllSessions() (int64, error) {
	res, err := database.DB.Exec(`DELETE FROM sessions`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Enabled reports whether authentication is active.
func Enabled() bool { return enabled }

// SessionMaxAge is the absolute session lifetime, used as the cookie Max-Age.
func SessionMaxAge() time.Duration { return maxLifetime }

// CheckPassword verifies a candidate password against the admin hash.
func CheckPassword(password string) bool {
	if len(adminHash) == 0 {
		return false
	}
	return bcrypt.CompareHashAndPassword(adminHash, []byte(password)) == nil
}

// ── Sessions ────────────────────────────────────────────────────────────────

// CreateSession issues a new browser session and returns the raw token to be
// placed in the cookie. Only its hash is stored.
func CreateSession(identity string, roles []string) (string, error) {
	raw, err := randomToken(32)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	if _, err := database.DB.Exec(
		`INSERT INTO sessions (token_hash, identity, roles, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
		hashToken(raw), identity, strings.Join(roles, ","), now, now,
	); err != nil {
		return "", err
	}
	return raw, nil
}

// ValidateSession checks a session token, enforcing idle and absolute timeouts,
// and slides the idle window forward on success.
func ValidateSession(raw string) (*Principal, bool) {
	if raw == "" {
		return nil, false
	}
	h := hashToken(raw)
	var identity, roles string
	var createdAt, lastSeen int64
	if err := database.DB.QueryRow(
		`SELECT identity, roles, created_at, last_seen_at FROM sessions WHERE token_hash = ?`, h,
	).Scan(&identity, &roles, &createdAt, &lastSeen); err != nil {
		return nil, false
	}

	now := time.Now()
	if now.Sub(time.Unix(lastSeen, 0)) > idleTimeout || now.Sub(time.Unix(createdAt, 0)) > maxLifetime {
		_ = DeleteSession(raw)
		return nil, false
	}

	_, _ = database.DB.Exec(`UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`, now.Unix(), h)
	return &Principal{Identity: identity, Roles: splitRoles(roles), Method: "session"}, true
}

// DeleteSession revokes a session (logout).
func DeleteSession(raw string) error {
	_, err := database.DB.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(raw))
	return err
}

// ── API tokens ──────────────────────────────────────────────────────────────

// TokenMeta describes an API token without exposing its secret.
type TokenMeta struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at,omitempty"`
	ExpiresAt  *int64 `json:"expires_at,omitempty"`
}

// ErrTokenNameExists is returned when creating a token whose name is taken.
var ErrTokenNameExists = errors.New("a token with that name already exists")

// CreateAPIToken mints a token and returns the raw value (shown once).
// expiresAt is a unix timestamp, or 0 for a non-expiring token. Token names
// must be unique.
func CreateAPIToken(name string, expiresAt int64) (string, error) {
	var count int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE name = ?`, name).Scan(&count); err != nil {
		return "", err
	}
	if count > 0 {
		return "", ErrTokenNameExists
	}

	tok, err := randomToken(32)
	if err != nil {
		return "", err
	}
	raw := apiTokenPrefix + tok
	var exp interface{}
	if expiresAt > 0 {
		exp = expiresAt
	}
	if _, err := database.DB.Exec(
		`INSERT INTO api_tokens (token_hash, name, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hashToken(raw), name, time.Now().Unix(), exp,
	); err != nil {
		return "", err
	}
	return raw, nil
}

// ValidateAPIToken checks a bearer token and records its last use.
func ValidateAPIToken(raw string) (*Principal, bool) {
	if !strings.HasPrefix(raw, apiTokenPrefix) {
		return nil, false
	}
	h := hashToken(raw)
	var id int64
	var expiresAt sql.NullInt64
	if err := database.DB.QueryRow(
		`SELECT id, expires_at FROM api_tokens WHERE token_hash = ?`, h,
	).Scan(&id, &expiresAt); err != nil {
		return nil, false
	}
	if expiresAt.Valid && time.Now().Unix() > expiresAt.Int64 {
		return nil, false
	}
	_, _ = database.DB.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return &Principal{Identity: "admin", Roles: []string{"admin"}, Method: "token"}, true
}

// ListAPITokens returns metadata for all tokens (never the secrets).
func ListAPITokens() ([]TokenMeta, error) {
	rows, err := database.DB.Query(
		`SELECT id, name, created_at, last_used_at, expires_at FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tokens := []TokenMeta{}
	for rows.Next() {
		var t TokenMeta
		var lastUsed, expires sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt, &lastUsed, &expires); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			t.LastUsedAt = &lastUsed.Int64
		}
		if expires.Valid {
			t.ExpiresAt = &expires.Int64
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// DeleteAPIToken revokes a token by id; reports whether a row was removed.
func DeleteAPIToken(id int64) (bool, error) {
	res, err := database.DB.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ── helpers ─────────────────────────────────────────────────────────────────

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func splitRoles(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func hoursFromEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if h, err := strconv.ParseFloat(v, 64); err == nil && h > 0 {
			return time.Duration(h * float64(time.Hour))
		}
	}
	return def
}

func loadPasswordHash() (string, error) {
	var h string
	err := database.DB.QueryRow(`SELECT password_hash FROM auth_config WHERE id = 1`).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return h, nil
}

func savePasswordHash(h string) error {
	_, err := database.DB.Exec(`INSERT OR REPLACE INTO auth_config (id, password_hash) VALUES (1, ?)`, h)
	return err
}
