package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kubendt/database"

	"golang.org/x/crypto/bcrypt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// fakeCluster hands the store a fake clientset, or none, and resets the
// package state as a fresh process would have it.
func fakeCluster(t *testing.T, withCluster bool) *fake.Clientset {
	t.Helper()
	cs := fake.NewClientset()
	prev := secretsOfCurrentCluster
	secretsOfCurrentCluster = func() corev1client.SecretInterface {
		if !withCluster {
			return nil
		}
		return cs.CoreV1().Secrets("kubendt-system")
	}
	prevDB := database.DB
	database.DB = nil
	homeSecrets = nil
	t.Setenv("KUBENDT_AUTH_DISABLED", "")
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "")
	t.Cleanup(func() {
		secretsOfCurrentCluster = prev
		database.DB = prevDB
		homeSecrets = nil
		bound = false
	})
	return cs
}

func TestCheckPassword(t *testing.T) {
	fakeCluster(t, false)
	h, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	adminHash = h
	if !CheckPassword("s3cret") {
		t.Error("correct password rejected")
	}
	if CheckPassword("wrong") {
		t.Error("wrong password accepted")
	}
	adminHash = nil
	if CheckPassword("s3cret") {
		t.Error("password accepted with no hash configured")
	}
}

func TestSessionIsASignedCookieThatSlides(t *testing.T) {
	fakeCluster(t, false)
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	idleTimeout, maxLifetime = time.Hour, 2*time.Hour

	raw, err := CreateSession("admin", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	p, refreshed, ok := ValidateSession(raw)
	if !ok || p.Identity != "admin" || p.Method != "session" || len(p.Roles) != 1 || p.Roles[0] != "admin" {
		t.Fatalf("session did not validate: ok=%v p=%+v", ok, p)
	}
	if refreshed != "" {
		t.Error("a session just issued needs no refresh")
	}
	if _, _, ok := ValidateSession(raw[:len(raw)-2] + "xx"); ok {
		t.Error("a tampered signature validated")
	}
	if _, _, ok := ValidateSession("does-not-exist"); ok {
		t.Error("garbage validated")
	}

	now := time.Now().Unix()
	seenAWhileAgo, _ := signSession(sessionClaims{Identity: "admin", Roles: []string{"admin"}, IssuedAt: now - 1800, SeenAt: now - 600})
	if _, refreshed, ok := ValidateSession(seenAWhileAgo); !ok || refreshed == "" {
		t.Error("a session in use for ten minutes gets a fresh cookie")
	} else if c, _ := parseSession(refreshed); c.SeenAt < now || c.IssuedAt != now-1800 {
		t.Errorf("the refreshed cookie keeps its issue time and moves seen: %+v", c)
	}
	idle, _ := signSession(sessionClaims{Identity: "admin", IssuedAt: now - 7200, SeenAt: now - 3601})
	if _, _, ok := ValidateSession(idle); ok {
		t.Error("an idle session validated")
	}
	old, _ := signSession(sessionClaims{Identity: "admin", IssuedAt: now - 7201, SeenAt: now})
	if _, _, ok := ValidateSession(old); ok {
		t.Error("a session past its absolute lifetime validated")
	}

	time.Sleep(1100 * time.Millisecond)
	if err := InvalidateAllSessions(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := ValidateSession(raw); ok {
		t.Error("a session issued before the invalidation validated")
	}
	later, _ := CreateSession("admin", []string{"admin"})
	if _, _, ok := ValidateSession(later); !ok {
		t.Error("a session issued after the invalidation must validate")
	}
}

func TestBindKeepsTheAdminAndTheKeyInTheCluster(t *testing.T) {
	cs := fakeCluster(t, true)
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw1")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if !IsBound() || !CheckPassword("pw1") {
		t.Fatal("not bound or password rejected")
	}
	admin, err := cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), "kubendt-user-admin", metav1.GetOptions{})
	if err != nil || bcrypt.CompareHashAndPassword(admin.Data["password_hash"], []byte("pw1")) != nil || admin.Labels["kubendt/kind"] != "user" {
		t.Fatalf("admin secret: %v %v", err, admin)
	}
	keySecret, err := cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), "kubendt-internal-session-key", metav1.GetOptions{})
	if err != nil || len(keySecret.Data["key"]) != 32 {
		t.Fatalf("session key secret: %v", err)
	}
	cookie, _ := CreateSession("admin", []string{"admin"})

	// A second backend on the same cluster takes the same key and the same
	// password, so the cookie of the first one is good for it.
	bound = false
	sessionKey = nil
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := ValidateSession(cookie); !ok {
		t.Error("the other backend's cookie was rejected")
	}
	if string(sessionKey) != string(keySecret.Data["key"]) {
		t.Error("the second backend did not take the stored key")
	}

	// The password changes in the environment: the store follows and every
	// session issued before is out.
	time.Sleep(1100 * time.Millisecond)
	bound = false
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw2")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("pw2") || CheckPassword("pw1") {
		t.Error("the new password did not take")
	}
	admin, _ = cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), "kubendt-user-admin", metav1.GetOptions{})
	if bcrypt.CompareHashAndPassword(admin.Data["password_hash"], []byte("pw2")) != nil || string(admin.Data["sessions_valid_after"]) == "0" {
		t.Errorf("admin secret after the change: %v", admin.Data)
	}
	if _, _, ok := ValidateSession(cookie); ok {
		t.Error("a session from before the password change validated")
	}

	// A backend that did not restart reads the change from the store.
	adminHash, sessionsValidAfter = []byte("stale"), 0
	storeLoadedAt = time.Now().Add(-time.Minute)
	if !CheckPassword("pw2") {
		t.Error("the running backend did not pick the new password up")
	}
	if _, _, ok := ValidateSession(cookie); ok {
		t.Error("the running backend still honours the ended sessions")
	}

	// With the variable unset the stored hash is the one.
	bound = false
	adminHash = nil
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("pw2") {
		t.Error("the stored password was not read")
	}
}

func TestWithoutAClusterAuthWaitsForOne(t *testing.T) {
	cs := fakeCluster(t, false)
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if IsBound() {
		t.Fatal("bound with no cluster")
	}
	if len(adminHash) == 0 {
		t.Fatal("no generated password")
	}
	generated := string(adminHash)
	if _, err := CreateAPIToken("ci", 0); err != ErrNoStore {
		t.Errorf("tokens need a cluster, got %v", err)
	}
	if list, err := ListAPITokens(); err != nil || len(list) != 0 {
		t.Errorf("no tokens without a cluster: %v %v", list, err)
	}

	// A kubeconfig is loaded: the password of this process goes to the store.
	secretsOfCurrentCluster = func() corev1client.SecretInterface { return cs.CoreV1().Secrets("kubendt-system") }
	if err := Bind(); err != nil {
		t.Fatal(err)
	}
	admin, err := cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), "kubendt-user-admin", metav1.GetOptions{})
	if err != nil || string(admin.Data["password_hash"]) != generated {
		t.Fatalf("the generated password was not stored: %v", err)
	}
	if _, err := CreateAPIToken("ci", 0); err != nil {
		t.Errorf("tokens work once bound: %v", err)
	}
}

func TestAPITokenLifecycle(t *testing.T) {
	cs := fakeCluster(t, true)
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	raw, err := CreateAPIToken("ci", 0)
	if err != nil || len(raw) < 10 || raw[:4] != "kdt_" {
		t.Fatalf("token: %q %v", raw, err)
	}
	if _, err := CreateAPIToken("ci", 0); err != ErrTokenNameExists {
		t.Errorf("duplicate name: %v", err)
	}
	p, ok := ValidateAPIToken(raw)
	if !ok || p.Method != "token" {
		t.Fatalf("token did not validate: %v %+v", ok, p)
	}
	if _, ok := ValidateAPIToken("kdt_nope"); ok {
		t.Error("unknown token validated")
	}
	list, err := ListAPITokens()
	if err != nil || len(list) != 1 || list[0].Name != "ci" || list[0].LastUsedAt == nil {
		t.Fatalf("list: %+v %v", list, err)
	}
	s, err := cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), tokenSecretName(list[0].ID), metav1.GetOptions{})
	if err != nil || s.Labels["kubendt/kind"] != "token" || string(s.Data["name"]) != "ci" {
		t.Fatalf("token secret: %v %v", err, s)
	}

	// A token another backend minted is found on the next look at the store.
	other := &tokenEntry{ID: 7, Hash: hashToken("kdt_other"), Name: "other", CreatedAt: nowUnix()}
	if err := createToken(other); err != nil {
		t.Fatal(err)
	}
	storeLoadedAt = time.Now().Add(-time.Minute)
	if _, ok := ValidateAPIToken("kdt_other"); !ok {
		t.Error("the other backend's token was not picked up")
	}

	removed, err := DeleteAPIToken(list[0].ID)
	if err != nil || !removed {
		t.Fatalf("delete: %v %v", removed, err)
	}
	if removed, _ := DeleteAPIToken(list[0].ID); removed {
		t.Error("deleting twice reported a removal")
	}
	if _, ok := ValidateAPIToken(raw); ok {
		t.Error("a revoked token validated")
	}
}

func TestAPITokenExpiry(t *testing.T) {
	fakeCluster(t, true)
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	expired, _ := CreateAPIToken("expired", time.Now().Add(-time.Hour).Unix())
	if _, ok := ValidateAPIToken(expired); ok {
		t.Error("expired token accepted")
	}
	valid, _ := CreateAPIToken("valid", time.Now().Add(time.Hour).Unix())
	if _, ok := ValidateAPIToken(valid); !ok {
		t.Error("token with future expiry rejected")
	}
}

func TestBindMovesTheRowsOfAnOlderBackend(t *testing.T) {
	cs := fakeCluster(t, true)
	t.Setenv("KUBENDT_DB_PATH", filepath.Join(t.TempDir(), "old.db"))
	database.InitDB()
	defer func() { _ = database.DB.Close() }()
	h, _ := bcrypt.GenerateFromPassword([]byte("old-pw"), bcrypt.DefaultCost)
	if _, err := database.DB.Exec(`INSERT INTO auth_config (id, password_hash) VALUES (1, ?)`, string(h)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.Exec(`INSERT INTO api_tokens (token_hash, name, created_at, expires_at) VALUES (?, 'ci', 1700000000, NULL)`, hashToken("kdt_old")); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.Exec(`INSERT INTO sessions (token_hash, identity, roles, created_at, last_seen_at) VALUES ('x', 'admin', 'admin', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("old-pw") {
		t.Error("the stored password did not move")
	}
	if _, ok := ValidateAPIToken("kdt_old"); !ok {
		t.Error("the old token did not move")
	}
	if list, _ := cs.CoreV1().Secrets("kubendt-system").List(context.TODO(), metav1.ListOptions{LabelSelector: "kubendt/kind=token"}); len(list.Items) != 1 || string(list.Items[0].Data["name"]) != "ci" {
		t.Errorf("token secrets: %+v", list.Items)
	}
	for _, table := range []string{"auth_config", "api_tokens", "sessions"} {
		var n int
		_ = database.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
		if n != 0 {
			t.Errorf("%s still has %d rows", table, n)
		}
	}
}

func TestLoginLimiter(t *testing.T) {
	loginMu.Lock()
	loginAttempts = map[string]*loginAttempt{}
	loginMu.Unlock()

	const ip = "10.0.0.1"
	for i := 0; i < loginMaxAttempts; i++ {
		if ok, _ := LoginAllowed(ip); !ok {
			t.Fatalf("blocked too early at attempt %d", i)
		}
		RegisterLoginFailure(ip)
	}
	if ok, _ := LoginAllowed(ip); ok {
		t.Error("should be blocked after max failed attempts")
	}

	// A different IP is unaffected.
	if ok, _ := LoginAllowed("10.0.0.2"); !ok {
		t.Error("unrelated IP should not be blocked")
	}

	// Success clears the counter.
	RegisterLoginSuccess(ip)
	if ok, _ := LoginAllowed(ip); !ok {
		t.Error("counter not cleared after success")
	}
}
