// Package auth handles password login, browser sessions (a signed cookie, no
// server-side record) and API tokens for scripts. The admin hash, the tokens
// and the key the sessions are signed with are Secrets in the system
// namespace of the home cluster, so any backend on it serves the same
// logins. Credential checks go through the Authenticator interface so another
// backend (e.g. LDAP) can be added later.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// CookieName is the session cookie the browser carries (also sent on the
// WebSocket handshake, which is why cookie-based auth covers shells/capture).
const CookieName = "kubendt_session"

const (
	apiTokenPrefix = "kdt_"
	sessionPrefix  = "v1."
	// sessionRefreshEvery is how often a session in use gets a fresh cookie,
	// which is what slides its idle window; every request would be noise.
	sessionRefreshEvery = 5 * time.Minute
	// storeReloadEvery is how long what was read from the store is trusted
	// before a validation reads it again: a token another backend minted or
	// revoked, a password it changed, the sessions it ended, are seen within it.
	storeReloadEvery = 10 * time.Second
	// tokenTouchEvery is how often a token's last use is written back.
	tokenTouchEvery = 5 * time.Minute
)

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
	idleTimeout time.Duration
	maxLifetime time.Duration

	mu                 sync.RWMutex
	adminHash          []byte
	sessionsValidAfter int64
	sessionKey         []byte
	bound              bool
	tokens             map[string]*tokenEntry // by hash
	storeLoadedAt      time.Time
)

var randRead = rand.Read

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

// Init loads the auth configuration and, when a cluster is configured, binds
// to it. Without one the admin password comes from KUBENDT_ADMIN_PASSWORD or
// is generated, the sessions are signed with a key of this process, and the
// tokens wait for a cluster; Bind picks the store up once one is loaded.
func Init() error {
	if strings.EqualFold(os.Getenv("KUBENDT_AUTH_DISABLED"), "true") {
		enabled = false
		log.Printf("⚠️  AUTH DISABLED (KUBENDT_AUTH_DISABLED=true): the API is UNAUTHENTICATED. Run only on a trusted network.")
		return nil
	}
	enabled = true
	idleTimeout = hoursFromEnv("KUBENDT_SESSION_IDLE_HOURS", 12*time.Hour)
	maxLifetime = hoursFromEnv("KUBENDT_SESSION_MAX_HOURS", 7*24*time.Hour)

	mu.Lock()
	bound, tokens, adminHash, sessionsValidAfter = false, map[string]*tokenEntry{}, nil, 0
	key := make([]byte, 32)
	if _, err := randRead(key); err != nil {
		mu.Unlock()
		return err
	}
	sessionKey = key
	mu.Unlock()

	if err := Bind(); err != nil {
		return err
	}
	if !IsBound() {
		if err := resolveAdminPassword(nil); err != nil {
			return err
		}
		log.Printf("ℹ️ No cluster yet: the admin password is this process's, sessions end with it, API tokens wait for a kubeconfig")
	}
	return nil
}

// Bind takes the store of the configured cluster, once: the first cluster is
// the home cluster, and a context switch later does not move the admin or
// the tokens. It is what the kubeconfig endpoints call after a load.
func Bind() error {
	if !enabled || IsBound() {
		return nil
	}
	client := secretsOfCurrentCluster()
	if client == nil {
		return nil
	}
	homeSecrets = client
	if err := migrateFromDatabase(); err != nil {
		return err
	}
	stored, err := readAdmin()
	if err != nil {
		return err
	}
	if err := resolveAdminPassword(stored); err != nil {
		return err
	}
	key, err := loadOrCreateSessionKey()
	if err != nil {
		return err
	}
	mu.Lock()
	sessionKey = key
	bound = true
	mu.Unlock()
	return reloadStore()
}

// IsBound reports whether a cluster holds the store.
func IsBound() bool {
	mu.RLock()
	defer mu.RUnlock()
	return bound
}

// resolveAdminPassword sets the admin hash from KUBENDT_ADMIN_PASSWORD (the
// source of truth when set, written to the store when it changed, which also
// ends every session), from the stored hash, or from a password generated
// and printed once. stored is nil when no cluster holds one.
func resolveAdminPassword(stored *adminRecord) error {
	validAfter := int64(0)
	storedHash := ""
	if stored != nil {
		validAfter, storedHash = stored.SessionsValidAfter, stored.Hash
	}
	setAdmin := func(hash []byte) {
		mu.Lock()
		adminHash, sessionsValidAfter = hash, validAfter
		mu.Unlock()
	}

	if pw := os.Getenv("KUBENDT_ADMIN_PASSWORD"); pw != "" {
		if storedHash != "" && bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(pw)) == nil {
			setAdmin([]byte(storedHash))
			return nil
		}
		h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if stored != nil || homeSecrets != nil {
			if storedHash != "" {
				validAfter = nowUnix()
				log.Printf("🔒 Admin password changed, every session must sign in again")
			}
			if err := writeAdmin(adminRecord{Hash: string(h), SessionsValidAfter: validAfter}); err != nil {
				return err
			}
		}
		setAdmin(h)
		return nil
	}

	if storedHash != "" {
		setAdmin([]byte(storedHash))
		return nil
	}

	// A password of this process when nothing holds one: in memory until a
	// cluster is bound, written to it when there is one.
	mu.RLock()
	current := adminHash
	mu.RUnlock()
	if len(current) > 0 {
		if homeSecrets != nil {
			if err := writeAdmin(adminRecord{Hash: string(current)}); err != nil {
				return err
			}
		}
		setAdmin(current)
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
	if homeSecrets != nil {
		if err := writeAdmin(adminRecord{Hash: string(h)}); err != nil {
			return err
		}
	}
	setAdmin(h)
	log.Printf("🔑 Generated admin password (shown once; set KUBENDT_ADMIN_PASSWORD to override):")
	log.Printf("      user: admin   password: %s", pw)
	return nil
}

// InvalidateAllSessions ends every browser session: a session issued before
// now no longer counts. API tokens are unaffected; revoke those individually.
func InvalidateAllSessions() error {
	now := nowUnix()
	mu.Lock()
	sessionsValidAfter = now
	hash := string(adminHash)
	isBound := bound
	mu.Unlock()
	if !isBound {
		return nil
	}
	return writeAdmin(adminRecord{Hash: hash, SessionsValidAfter: now})
}

// Enabled reports whether authentication is active.
func Enabled() bool { return enabled }

// SessionMaxAge is the absolute session lifetime, used as the cookie Max-Age.
func SessionMaxAge() time.Duration { return maxLifetime }

// CheckPassword verifies a candidate password against the admin hash.
func CheckPassword(password string) bool {
	refreshStoreIfStale()
	mu.RLock()
	h := adminHash
	mu.RUnlock()
	if len(h) == 0 {
		return false
	}
	return bcrypt.CompareHashAndPassword(h, []byte(password)) == nil
}

// ── Sessions ────────────────────────────────────────────────────────────────

// sessionClaims is what the cookie carries, signed: who, since when, and
// when it was last seen, which the idle timeout counts from.
type sessionClaims struct {
	Identity string   `json:"i"`
	Roles    []string `json:"r"`
	IssuedAt int64    `json:"iat"`
	SeenAt   int64    `json:"seen"`
}

func signSession(c sessionClaims) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mu.RLock()
	key := sessionKey
	mu.RUnlock()
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	return sessionPrefix + body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func parseSession(raw string) (*sessionClaims, bool) {
	if !strings.HasPrefix(raw, sessionPrefix) {
		return nil, false
	}
	body, sig, ok := strings.Cut(strings.TrimPrefix(raw, sessionPrefix), ".")
	if !ok {
		return nil, false
	}
	want, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, false
	}
	mu.RLock()
	key := sessionKey
	mu.RUnlock()
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	if !hmac.Equal(mac.Sum(nil), want) {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, false
	}
	var c sessionClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, false
	}
	return &c, true
}

// CreateSession issues a browser session and returns the cookie value.
func CreateSession(identity string, roles []string) (string, error) {
	now := nowUnix()
	return signSession(sessionClaims{Identity: identity, Roles: roles, IssuedAt: now, SeenAt: now})
}

// ValidateSession checks a cookie, enforcing the idle and absolute timeouts
// and the moment before which no session counts. A session in use gets a
// fresh cookie every few minutes, returned as refreshed, which slides its
// idle window; "" when the one it has is fine.
func ValidateSession(raw string) (p *Principal, refreshed string, ok bool) {
	refreshStoreIfStale()
	c, ok := parseSession(raw)
	if !ok {
		return nil, "", false
	}
	now := time.Now()
	mu.RLock()
	validAfter := sessionsValidAfter
	mu.RUnlock()
	if c.IssuedAt < validAfter || now.Sub(time.Unix(c.SeenAt, 0)) > idleTimeout || now.Sub(time.Unix(c.IssuedAt, 0)) > maxLifetime {
		return nil, "", false
	}
	if now.Sub(time.Unix(c.SeenAt, 0)) >= sessionRefreshEvery {
		c.SeenAt = now.Unix()
		if fresh, err := signSession(*c); err == nil {
			refreshed = fresh
		}
	}
	return &Principal{Identity: c.Identity, Roles: c.Roles, Method: "session"}, refreshed, true
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

// reloadStore reads the tokens and the admin back from the store, which is
// the truth at runtime: the password another backend set or the sessions it
// ended apply here too.
func reloadStore() error {
	list, err := listTokens()
	if err != nil {
		return err
	}
	admin, err := readAdmin()
	if err != nil {
		return err
	}
	byHash := make(map[string]*tokenEntry, len(list))
	for _, t := range list {
		byHash[t.Hash] = t
	}
	mu.Lock()
	tokens, storeLoadedAt = byHash, time.Now()
	if admin != nil && admin.Hash != "" {
		adminHash, sessionsValidAfter = []byte(admin.Hash), admin.SessionsValidAfter
	}
	mu.Unlock()
	return nil
}

// refreshStoreIfStale re-reads the store once its reading is older than
// storeReloadEvery; nothing without a cluster.
func refreshStoreIfStale() {
	mu.RLock()
	stale := bound && time.Since(storeLoadedAt) > storeReloadEvery
	mu.RUnlock()
	if stale {
		if err := reloadStore(); err != nil {
			log.Printf("⚠️ Could not re-read the auth store: %v", err)
		}
	}
}

// CreateAPIToken mints a token and returns the raw value (shown once).
// expiresAt is a unix timestamp, or 0 for a non-expiring token. Token names
// must be unique.
func CreateAPIToken(name string, expiresAt int64) (string, error) {
	if !IsBound() {
		return "", ErrNoStore
	}
	if err := reloadStore(); err != nil {
		return "", err
	}
	mu.RLock()
	for _, t := range tokens {
		if t.Name == name {
			mu.RUnlock()
			return "", ErrTokenNameExists
		}
	}
	mu.RUnlock()

	tok, err := randomToken(32)
	if err != nil {
		return "", err
	}
	raw := apiTokenPrefix + tok
	entry := &tokenEntry{ID: time.Now().UnixMicro(), Hash: hashToken(raw), Name: name, CreatedAt: nowUnix(), ExpiresAt: expiresAt}
	if err := createToken(entry); err != nil {
		return "", err
	}
	mu.Lock()
	tokens[entry.Hash] = entry
	mu.Unlock()
	return raw, nil
}

// ValidateAPIToken checks a bearer token and records its last use. The
// cache is re-listed from the store every few seconds, so a token another
// backend minted or revoked is seen without a restart.
func ValidateAPIToken(raw string) (*Principal, bool) {
	if !strings.HasPrefix(raw, apiTokenPrefix) || !IsBound() {
		return nil, false
	}
	refreshStoreIfStale()
	h := hashToken(raw)
	mu.RLock()
	t, known := tokens[h]
	mu.RUnlock()
	if !known {
		return nil, false
	}
	now := nowUnix()
	if t.ExpiresAt > 0 && now > t.ExpiresAt {
		return nil, false
	}
	mu.Lock()
	touch := now-t.LastUsedAt >= int64(tokenTouchEvery.Seconds())
	if touch {
		t.LastUsedAt = now
	}
	id := t.ID
	mu.Unlock()
	if touch {
		touchToken(id, now)
	}
	return &Principal{Identity: "admin", Roles: []string{"admin"}, Method: "token"}, true
}

// ListAPITokens returns metadata for all tokens (never the secrets), newest
// first, as the store has them.
func ListAPITokens() ([]TokenMeta, error) {
	if !IsBound() {
		return []TokenMeta{}, nil
	}
	if err := reloadStore(); err != nil {
		return nil, err
	}
	mu.RLock()
	out := make([]TokenMeta, 0, len(tokens))
	for _, t := range tokens {
		m := TokenMeta{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt}
		if t.LastUsedAt > 0 {
			v := t.LastUsedAt
			m.LastUsedAt = &v
		}
		if t.ExpiresAt > 0 {
			v := t.ExpiresAt
			m.ExpiresAt = &v
		}
		out = append(out, m)
	}
	mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// DeleteAPIToken revokes a token by id; reports whether one was removed.
func DeleteAPIToken(id int64) (bool, error) {
	if !IsBound() {
		return false, ErrNoStore
	}
	removed, err := deleteToken(id)
	if err != nil {
		return false, err
	}
	mu.Lock()
	for h, t := range tokens {
		if t.ID == id {
			delete(tokens, h)
		}
	}
	mu.Unlock()
	return removed, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := randRead(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func hoursFromEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if h, err := strconv.ParseFloat(v, 64); err == nil && h > 0 {
			return time.Duration(h * float64(time.Hour))
		}
	}
	return def
}
