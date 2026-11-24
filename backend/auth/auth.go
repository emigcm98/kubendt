// Package auth handles password login, browser sessions (a signed cookie, no
// server-side record) and API tokens for scripts. The users, the tokens and
// the key the sessions are signed with are Secrets in the system namespace
// of the home cluster, so any backend on it serves the same logins. The
// credentials of a user go through the Authenticator interface so another
// source of identity (OIDC, LDAP) can be added later.
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

// Principal is the authenticated identity attached to a request: the
// account, its platform role, and how it came in.
type Principal struct {
	Identity           string
	Role               string
	Roles              []string
	Method             string // "session" | "token" | "password"
	MustChangePassword bool
}

// HasRole reports whether the principal carries the given role.
func (p *Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func principalOf(u *userRecord, method string) *Principal {
	return &Principal{Identity: u.Name, Role: u.Role, Roles: []string{u.Role}, Method: method, MustChangePassword: u.MustChangePassword}
}

var (
	enabled     bool
	idleTimeout time.Duration
	maxLifetime time.Duration

	mu            sync.RWMutex
	users         map[string]*userRecord // by name
	sessionKey    []byte
	bound         bool
	tokens        map[string]*tokenEntry // by hash
	storeLoadedAt time.Time
)

var randRead = rand.Read

// Authenticator verifies a username and password. LocalAuthenticator is the
// only implementation today; an OIDC or LDAP one would satisfy the same
// interface and map what it learns onto the same accounts.
type Authenticator interface {
	Authenticate(username, password string) (*Principal, error)
}

// LocalAuthenticator checks against the accounts of the store.
type LocalAuthenticator struct{}

func (LocalAuthenticator) Authenticate(username, password string) (*Principal, error) {
	p, ok := Authenticate(username, password)
	if !ok {
		return nil, errors.New("invalid credentials")
	}
	return p, nil
}

// Init loads the auth configuration and, when a cluster is configured, binds
// to it. Without one the owner's password comes from KUBENDT_ADMIN_PASSWORD
// or is generated, the sessions are signed with a key of this process, and
// the users and tokens wait for a cluster; Bind picks the store up once one
// is loaded.
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
	bound, tokens, users = false, map[string]*tokenEntry{}, map[string]*userRecord{}
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
		if err := resolveOwnerPassword(nil); err != nil {
			return err
		}
		log.Printf("ℹ️ No cluster yet: the admin password is this process's, sessions end with it, users and API tokens wait for a kubeconfig")
	}
	return nil
}

// Bind takes the store of the configured cluster, once: the first cluster is
// the home cluster, and a context switch later does not move the users or
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
	stored, err := readUser(OwnerAccount)
	if err != nil {
		return err
	}
	if err := resolveOwnerPassword(stored); err != nil {
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

// resolveOwnerPassword sets the owner account from KUBENDT_ADMIN_PASSWORD
// (the source of truth when set, written to the store when it changed, which
// also ends the owner's sessions), from the stored record, or from a
// password generated and printed once. stored is nil when no cluster holds
// one. An owner stored by a backend older than the roles gets its role.
func resolveOwnerPassword(stored *userRecord) error {
	owner := &userRecord{Name: OwnerAccount, Role: RoleOwner, Source: SourceLocal, CreatedAt: nowUnix()}
	if stored != nil {
		*owner = *stored
		owner.Role, owner.Name = RoleOwner, OwnerAccount
		if owner.Source == "" {
			owner.Source = SourceLocal
		}
	}
	needsRole := stored != nil && (stored.Role != RoleOwner || stored.Source == "")
	persist := func() error {
		if homeSecrets == nil {
			return nil
		}
		return writeUser(owner)
	}
	keep := func() {
		mu.Lock()
		users[OwnerAccount] = owner
		mu.Unlock()
	}

	if pw := os.Getenv("KUBENDT_ADMIN_PASSWORD"); pw != "" {
		if owner.Hash != "" && bcrypt.CompareHashAndPassword([]byte(owner.Hash), []byte(pw)) == nil {
			if needsRole {
				if err := persist(); err != nil {
					return err
				}
			}
			keep()
			return nil
		}
		h, err := hashPassword(pw)
		if err != nil {
			return err
		}
		if checkPassword(OwnerAccount, pw) != nil {
			log.Printf("⚠️ KUBENDT_ADMIN_PASSWORD does not meet the password policy")
		}
		if owner.Hash != "" {
			owner.SessionsValidAfter = nowUnix()
			log.Printf("🔒 Admin password changed, every session of the owner must sign in again")
		}
		owner.Hash = h
		if err := persist(); err != nil {
			return err
		}
		keep()
		return nil
	}

	if owner.Hash != "" {
		if needsRole {
			if err := persist(); err != nil {
				return err
			}
		}
		keep()
		return nil
	}

	// A password of this process when nothing holds one: in memory until a
	// cluster is bound, written to it when there is one.
	mu.RLock()
	current := users[OwnerAccount]
	mu.RUnlock()
	if current != nil && current.Hash != "" {
		owner.Hash = current.Hash
		if err := persist(); err != nil {
			return err
		}
		keep()
		return nil
	}
	pw, err := randomToken(18)
	if err != nil {
		return err
	}
	if owner.Hash, err = hashPassword(pw); err != nil {
		return err
	}
	if err := persist(); err != nil {
		return err
	}
	keep()
	log.Printf("🔑 Generated admin password (shown once; set KUBENDT_ADMIN_PASSWORD to override):")
	log.Printf("      user: admin   password: %s", pw)
	return nil
}

// EndSessions ends every browser session of an account: a session issued
// before now no longer counts. Its API tokens are unaffected.
func EndSessions(name string) error {
	mu.Lock()
	u, ok := users[name]
	if !ok {
		mu.Unlock()
		return ErrUserNotFound
	}
	changed := *u
	changed.SessionsValidAfter = nowUnix()
	users[name] = &changed
	isBound := bound
	mu.Unlock()
	if !isBound {
		return nil
	}
	return writeUser(&changed)
}

// Enabled reports whether authentication is active.
func Enabled() bool { return enabled }

// SessionMaxAge is the absolute session lifetime, used as the cookie Max-Age.
func SessionMaxAge() time.Duration { return maxLifetime }

// Authenticate checks a username and password against the accounts; an
// empty username is the owner account, so a login that names no user keeps
// working. A disabled account never authenticates.
func Authenticate(username, password string) (*Principal, bool) {
	if username == "" {
		username = OwnerAccount
	}
	refreshStoreIfStale()
	mu.RLock()
	u, ok := users[username]
	mu.RUnlock()
	if !ok || u.Disabled || u.Hash == "" {
		return nil, false
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(password)) != nil {
		return nil, false
	}
	return principalOf(u, "password"), true
}

// CheckPassword verifies a candidate password against the owner account.
func CheckPassword(password string) bool {
	_, ok := Authenticate(OwnerAccount, password)
	return ok
}

// ── Sessions ────────────────────────────────────────────────────────────────

// sessionClaims is what the cookie carries, signed: who, since when, and
// when it was last seen, which the idle timeout counts from. The role is
// read from the account at each request, not from the cookie, so a change
// of role or standing applies within the store's reload.
type sessionClaims struct {
	Identity string   `json:"i"`
	Roles    []string `json:"r,omitempty"`
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

// CreateSession issues a browser session for an account and returns the
// cookie value.
func CreateSession(identity string) (string, error) {
	now := nowUnix()
	return signSession(sessionClaims{Identity: identity, IssuedAt: now, SeenAt: now})
}

// ValidateSession checks a cookie, enforcing the idle and absolute timeouts
// and the moment before which no session of the account counts. A session
// in use gets a fresh cookie every few minutes, returned as refreshed, which
// slides its idle window; "" when the one it has is fine.
func ValidateSession(raw string) (p *Principal, refreshed string, ok bool) {
	refreshStoreIfStale()
	c, ok := parseSession(raw)
	if !ok {
		return nil, "", false
	}
	now := time.Now()
	mu.RLock()
	u, known := users[c.Identity]
	mu.RUnlock()
	if !known || u.Disabled {
		return nil, "", false
	}
	if c.IssuedAt < u.SessionsValidAfter || now.Sub(time.Unix(c.SeenAt, 0)) > idleTimeout || now.Sub(time.Unix(c.IssuedAt, 0)) > maxLifetime {
		return nil, "", false
	}
	if now.Sub(time.Unix(c.SeenAt, 0)) >= sessionRefreshEvery {
		c.SeenAt = now.Unix()
		if fresh, err := signSession(*c); err == nil {
			refreshed = fresh
		}
	}
	return principalOf(u, "session"), refreshed, true
}

// ── API tokens ──────────────────────────────────────────────────────────────

// TokenMeta describes an API token without exposing its secret.
type TokenMeta struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	User       string `json:"user"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at,omitempty"`
	ExpiresAt  *int64 `json:"expires_at,omitempty"`
}

// ErrTokenNameExists is returned when creating a token whose name is taken.
var ErrTokenNameExists = errors.New("a token with that name already exists")

// reloadStore reads the users and the tokens back from the store, which is
// the truth at runtime: the password another backend set, the sessions it
// ended, the user it disabled, apply here too.
func reloadStore() error {
	list, err := listTokens()
	if err != nil {
		return err
	}
	accounts, err := listUserRecords()
	if err != nil {
		return err
	}
	byHash := make(map[string]*tokenEntry, len(list))
	for _, t := range list {
		byHash[t.Hash] = t
	}
	byName := make(map[string]*userRecord, len(accounts))
	for _, u := range accounts {
		byName[u.Name] = u
	}
	mu.Lock()
	if owner, ok := byName[OwnerAccount]; !ok || owner.Hash == "" {
		// The store has no owner yet (a cluster just bound), keep the one
		// this process resolved.
		if current := users[OwnerAccount]; current != nil {
			byName[OwnerAccount] = current
		}
	}
	tokens, users, storeLoadedAt = byHash, byName, time.Now()
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

// CreateAPIToken mints a token for an account and returns the raw value
// (shown once). expiresAt is a unix timestamp, or 0 for a non-expiring
// token. Token names are unique per account.
func CreateAPIToken(user, name string, expiresAt int64) (string, error) {
	if !IsBound() {
		return "", ErrNoStore
	}
	if err := reloadStore(); err != nil {
		return "", err
	}
	mu.RLock()
	_, known := users[user]
	for _, t := range tokens {
		if t.User == user && t.Name == name {
			mu.RUnlock()
			return "", ErrTokenNameExists
		}
	}
	mu.RUnlock()
	if !known {
		return "", ErrUserNotFound
	}

	tok, err := randomToken(32)
	if err != nil {
		return "", err
	}
	raw := apiTokenPrefix + tok
	entry := &tokenEntry{ID: time.Now().UnixMicro(), Hash: hashToken(raw), Name: name, User: user, CreatedAt: nowUnix(), ExpiresAt: expiresAt}
	if err := createToken(entry); err != nil {
		return "", err
	}
	mu.Lock()
	tokens[entry.Hash] = entry
	mu.Unlock()
	return raw, nil
}

// ValidateAPIToken checks a bearer token and records its last use. The
// token acts as its account and never above it: a disabled account's tokens
// stop with it. The cache is re-listed from the store every few seconds, so
// a token another backend minted or revoked is seen without a restart.
func ValidateAPIToken(raw string) (*Principal, bool) {
	if !strings.HasPrefix(raw, apiTokenPrefix) || !IsBound() {
		return nil, false
	}
	refreshStoreIfStale()
	h := hashToken(raw)
	mu.RLock()
	t, known := tokens[h]
	var u *userRecord
	if known {
		u = users[t.User]
	}
	mu.RUnlock()
	if !known || u == nil || u.Disabled {
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
	p := principalOf(u, "token")
	p.MustChangePassword = false
	return p, true
}

// ListAPITokens returns the metadata of an account's tokens (never the
// secrets), newest first; "" lists everyone's.
func ListAPITokens(user string) ([]TokenMeta, error) {
	if !IsBound() {
		return []TokenMeta{}, nil
	}
	if err := reloadStore(); err != nil {
		return nil, err
	}
	mu.RLock()
	out := make([]TokenMeta, 0, len(tokens))
	for _, t := range tokens {
		if user != "" && t.User != user {
			continue
		}
		m := TokenMeta{ID: t.ID, Name: t.Name, User: t.User, CreatedAt: t.CreatedAt}
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

// TokenOwner is the account a token acts as, "" when there is no such token.
func TokenOwner(id int64) string {
	refreshStoreIfStale()
	mu.RLock()
	defer mu.RUnlock()
	for _, t := range tokens {
		if t.ID == id {
			return t.User
		}
	}
	return ""
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
