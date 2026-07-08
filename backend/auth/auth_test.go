package auth

import (
	"path/filepath"
	"testing"
	"time"

	"kubendt/database"

	"golang.org/x/crypto/bcrypt"
)

// setupDB points the global database at a fresh temp SQLite file and creates
// the tables. Auth stores sessions/tokens there.
func setupDB(t *testing.T) {
	t.Helper()
	t.Setenv("KUBENDT_DB_PATH", filepath.Join(t.TempDir(), "test.db"))
	database.InitDB()
}

func TestCheckPassword(t *testing.T) {
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

func TestSessionLifecycle(t *testing.T) {
	setupDB(t)
	idleTimeout = time.Hour
	maxLifetime = time.Hour

	raw, err := CreateSession("admin", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}

	p, ok := ValidateSession(raw)
	if !ok || p.Identity != "admin" || p.Method != "session" {
		t.Fatalf("session did not validate: ok=%v p=%+v", ok, p)
	}
	if len(p.Roles) != 1 || p.Roles[0] != "admin" {
		t.Errorf("unexpected roles: %v", p.Roles)
	}

	if _, ok := ValidateSession("does-not-exist"); ok {
		t.Error("unknown token validated")
	}

	if err := DeleteSession(raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := ValidateSession(raw); ok {
		t.Error("session still valid after delete")
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	setupDB(t)
	idleTimeout = 10 * time.Millisecond
	maxLifetime = time.Hour

	raw, err := CreateSession("admin", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, ok := ValidateSession(raw); ok {
		t.Error("session should have expired by idle timeout")
	}
}

func TestInvalidateAllSessions(t *testing.T) {
	setupDB(t)
	idleTimeout = time.Hour
	maxLifetime = time.Hour

	r1, _ := CreateSession("admin", []string{"admin"})
	_, _ = CreateSession("admin", []string{"admin"})

	n, err := InvalidateAllSessions()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("expected 2 sessions cleared, got %d", n)
	}
	if _, ok := ValidateSession(r1); ok {
		t.Error("session survived InvalidateAllSessions")
	}
}

func TestAPITokenLifecycle(t *testing.T) {
	setupDB(t)

	raw, err := CreateAPIToken("ci", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < len(apiTokenPrefix) || raw[:len(apiTokenPrefix)] != apiTokenPrefix {
		t.Errorf("token missing %q prefix: %s", apiTokenPrefix, raw)
	}

	p, ok := ValidateAPIToken(raw)
	if !ok || p.Method != "token" {
		t.Fatalf("token did not validate: ok=%v p=%+v", ok, p)
	}
	if _, ok := ValidateAPIToken("not-a-token"); ok {
		t.Error("non-prefixed token validated")
	}

	// Duplicate name is rejected.
	if _, err := CreateAPIToken("ci", 0); err != ErrTokenNameExists {
		t.Errorf("expected ErrTokenNameExists, got %v", err)
	}

	tokens, err := ListAPITokens()
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].Name != "ci" {
		t.Fatalf("unexpected token list: %+v", tokens)
	}

	removed, err := DeleteAPIToken(tokens[0].ID)
	if err != nil || !removed {
		t.Fatalf("delete failed: removed=%v err=%v", removed, err)
	}
	if removed, _ := DeleteAPIToken(tokens[0].ID); removed {
		t.Error("second delete reported a removal")
	}
}

func TestAPITokenExpiry(t *testing.T) {
	setupDB(t)

	// Already-expired token must not validate.
	past := time.Now().Add(-time.Hour).Unix()
	raw, err := CreateAPIToken("expired", past)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ValidateAPIToken(raw); ok {
		t.Error("expired token validated")
	}

	// Future expiry is fine.
	future := time.Now().Add(time.Hour).Unix()
	raw2, _ := CreateAPIToken("valid", future)
	if _, ok := ValidateAPIToken(raw2); !ok {
		t.Error("token with future expiry rejected")
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
