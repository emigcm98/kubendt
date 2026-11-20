package auth

import (
	"context"
	"errors"
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

func owner() *Principal {
	return &Principal{Identity: OwnerAccount, Role: RoleOwner, Roles: []string{RoleOwner}}
}

func TestOwnerPasswordAndSessions(t *testing.T) {
	fakeCluster(t, false)
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	idleTimeout, maxLifetime = time.Hour, 2*time.Hour
	if !CheckPassword("pw") || CheckPassword("wrong") {
		t.Fatal("the owner's password")
	}
	if p, ok := Authenticate("", "pw"); !ok || p.Identity != OwnerAccount || p.Role != RoleOwner {
		t.Fatalf("a login without a username is the owner: %v %+v", ok, p)
	}
	if _, ok := Authenticate("nobody", "pw"); ok {
		t.Error("an unknown account authenticated")
	}

	raw, err := CreateSession(OwnerAccount)
	if err != nil {
		t.Fatal(err)
	}
	p, refreshed, ok := ValidateSession(raw)
	if !ok || p.Identity != OwnerAccount || p.Role != RoleOwner || p.Method != "session" || refreshed != "" {
		t.Fatalf("session: ok=%v p=%+v refreshed=%q", ok, p, refreshed)
	}
	if _, _, ok := ValidateSession(raw[:len(raw)-2] + "xx"); ok {
		t.Error("a tampered signature validated")
	}
	now := time.Now().Unix()
	seenAWhileAgo, _ := signSession(sessionClaims{Identity: OwnerAccount, IssuedAt: now - 1800, SeenAt: now - 600})
	if _, refreshed, ok := ValidateSession(seenAWhileAgo); !ok || refreshed == "" {
		t.Error("a session in use for ten minutes gets a fresh cookie")
	}
	idle, _ := signSession(sessionClaims{Identity: OwnerAccount, IssuedAt: now - 7200, SeenAt: now - 3601})
	if _, _, ok := ValidateSession(idle); ok {
		t.Error("an idle session validated")
	}
	old, _ := signSession(sessionClaims{Identity: OwnerAccount, IssuedAt: now - 7201, SeenAt: now})
	if _, _, ok := ValidateSession(old); ok {
		t.Error("a session past its absolute lifetime validated")
	}
	unknown, _ := signSession(sessionClaims{Identity: "ghost", IssuedAt: now, SeenAt: now})
	if _, _, ok := ValidateSession(unknown); ok {
		t.Error("a session of an account that does not exist validated")
	}

	time.Sleep(1100 * time.Millisecond)
	if err := EndSessions(OwnerAccount); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := ValidateSession(raw); ok {
		t.Error("a session issued before the invalidation validated")
	}
	later, _ := CreateSession(OwnerAccount)
	if _, _, ok := ValidateSession(later); !ok {
		t.Error("a session issued after the invalidation must validate")
	}
}

func TestBindKeepsTheOwnerAndTheKeyInTheCluster(t *testing.T) {
	cs := fakeCluster(t, true)
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw1")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if !IsBound() || !CheckPassword("pw1") {
		t.Fatal("not bound or password rejected")
	}
	admin, err := cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), "kubendt-user-admin", metav1.GetOptions{})
	if err != nil || bcrypt.CompareHashAndPassword(admin.Data["password_hash"], []byte("pw1")) != nil || admin.Labels["kubendt/kind"] != "user" || string(admin.Data["role"]) != RoleOwner {
		t.Fatalf("owner secret: %v %v", err, admin)
	}
	keySecret, err := cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), "kubendt-internal-session-key", metav1.GetOptions{})
	if err != nil || len(keySecret.Data["key"]) != 32 {
		t.Fatalf("session key secret: %v", err)
	}
	cookie, _ := CreateSession(OwnerAccount)

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

	// The password changes in the environment: the store follows and every
	// session of the owner issued before is out.
	time.Sleep(1100 * time.Millisecond)
	bound = false
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw2")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("pw2") || CheckPassword("pw1") {
		t.Error("the new password did not take")
	}
	if _, _, ok := ValidateSession(cookie); ok {
		t.Error("a session from before the password change validated")
	}

	// A backend that did not restart reads the change from the store.
	users[OwnerAccount] = &userRecord{Name: OwnerAccount, Role: RoleOwner, Hash: "stale"}
	storeLoadedAt = time.Now().Add(-time.Minute)
	if !CheckPassword("pw2") {
		t.Error("the running backend did not pick the new password up")
	}

	// With the variable unset the stored hash is the one. An owner stored by
	// a backend older than the roles gets its role on the way.
	admin, _ = cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), "kubendt-user-admin", metav1.GetOptions{})
	delete(admin.Data, "role")
	delete(admin.Data, "source")
	if _, err := cs.CoreV1().Secrets("kubendt-system").Update(context.TODO(), admin, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	bound = false
	users = map[string]*userRecord{}
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("pw2") {
		t.Error("the stored password was not read")
	}
	admin, _ = cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), "kubendt-user-admin", metav1.GetOptions{})
	if string(admin.Data["role"]) != RoleOwner || string(admin.Data["source"]) != SourceLocal {
		t.Errorf("the owner's role was not written back: %v", admin.Data)
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
	generated := users[OwnerAccount].Hash
	if generated == "" {
		t.Fatal("no generated password")
	}
	if _, err := CreateAPIToken(OwnerAccount, "ci", 0); !errors.Is(err, ErrNoStore) {
		t.Errorf("tokens need a cluster, got %v", err)
	}
	if _, err := CreateUser(owner(), "alice", "x", RoleUser, false, [3]int{0, 0, 0}); !errors.Is(err, ErrNoStore) {
		t.Errorf("users need a cluster, got %v", err)
	}
	secretsOfCurrentCluster = func() corev1client.SecretInterface { return cs.CoreV1().Secrets("kubendt-system") }
	if err := Bind(); err != nil {
		t.Fatal(err)
	}
	admin, err := cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), "kubendt-user-admin", metav1.GetOptions{})
	if err != nil || string(admin.Data["password_hash"]) != generated {
		t.Fatalf("the generated password was not stored: %v", err)
	}
	if _, err := CreateAPIToken(OwnerAccount, "ci", 0); err != nil {
		t.Errorf("tokens work once bound: %v", err)
	}
}

func TestAccountsAndWhoMayManageWhom(t *testing.T) {
	cs := fakeCluster(t, true)
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateUser(owner(), "Alice", "x", RoleUser, false, [3]int{0, 0, 0}); !errors.Is(err, ErrBadUsername) {
		t.Errorf("a username is lowercase: %v", err)
	}
	if _, err := CreateUser(owner(), "alice", "x", RoleOwner, false, [3]int{0, 0, 0}); !errors.Is(err, ErrBadRole) {
		t.Errorf("there is one owner: %v", err)
	}
	generated, err := CreateUser(owner(), "alice", "", RoleUser, true, [3]int{2, 10, 0})
	if err != nil || generated == "" {
		t.Fatalf("create alice: %v %q", err, generated)
	}
	if _, err := CreateUser(owner(), "alice", "x", RoleUser, false, [3]int{0, 0, 0}); !errors.Is(err, ErrUserExists) {
		t.Errorf("twice: %v", err)
	}
	if _, err := CreateUser(owner(), "bob", "bob-pw", RoleAdmin, false, [3]int{0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	s, err := cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), "kubendt-user-alice", metav1.GetOptions{})
	if err != nil || s.Labels["kubendt/user"] != "alice" || string(s.Data["role"]) != RoleUser || string(s.Data["must_change_password"]) != "true" || string(s.Data["quota_pods"]) != "10" {
		t.Fatalf("alice's secret: %v %v", err, s)
	}

	// Alice must change her password before anything else, then she is in.
	p, ok := Authenticate("alice", generated)
	if !ok || !p.MustChangePassword || p.Role != RoleUser {
		t.Fatalf("alice: %v %+v", ok, p)
	}
	if err := ChangePassword("alice", "wrong", "new-pw"); !errors.Is(err, ErrBadPassword) {
		t.Errorf("the current password must match: %v", err)
	}
	if err := ChangePassword("alice", generated, "new-pw"); err != nil {
		t.Fatal(err)
	}
	if p, ok := Authenticate("alice", "new-pw"); !ok || p.MustChangePassword {
		t.Errorf("after the change: %v %+v", ok, p)
	}

	bob := &Principal{Identity: "bob", Role: RoleAdmin, Roles: []string{RoleAdmin}}
	alice := &Principal{Identity: "alice", Role: RoleUser, Roles: []string{RoleUser}}
	if _, err := CreateUser(bob, "carol", "x", RoleAdmin, false, [3]int{0, 0, 0}); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("an admin does not create admins: %v", err)
	}
	if _, err := CreateUser(bob, "carol", "x", RoleUser, false, [3]int{0, 0, 0}); err != nil {
		t.Errorf("an admin creates users: %v", err)
	}
	if _, err := CreateUser(alice, "dave", "x", RoleUser, false, [3]int{0, 0, 0}); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("a user creates nobody: %v", err)
	}
	role := RoleAdmin
	if err := UpdateUser(bob, "carol", UserPatch{Role: &role}); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("an admin does not promote: %v", err)
	}
	if err := UpdateUser(owner(), "carol", UserPatch{Role: &role}); err != nil {
		t.Errorf("the owner promotes: %v", err)
	}
	disabled := true
	if err := UpdateUser(bob, "carol", UserPatch{Disabled: &disabled}); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("an admin does not touch another admin: %v", err)
	}
	if err := UpdateUser(bob, OwnerAccount, UserPatch{Disabled: &disabled}); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("an admin does not touch the owner: %v", err)
	}
	if err := UpdateUser(owner(), OwnerAccount, UserPatch{Disabled: &disabled}); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("the owner is not disabled: %v", err)
	}
	if err := DeleteUser(owner(), OwnerAccount); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("the owner is not deleted: %v", err)
	}

	// Disabling alice ends her sessions and her tokens.
	cookie, _ := CreateSession("alice")
	tok, err := CreateAPIToken("alice", "script", 0)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := ValidateAPIToken(tok); !ok || p.Identity != "alice" || p.Role != RoleUser {
		t.Fatalf("alice's token acts as alice: %v %+v", ok, p)
	}
	if err := UpdateUser(bob, "alice", UserPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := ValidateSession(cookie); ok {
		t.Error("a disabled account's session validated")
	}
	if _, ok := ValidateAPIToken(tok); ok {
		t.Error("a disabled account's token validated")
	}
	enabled := false
	if err := UpdateUser(bob, "alice", UserPatch{Disabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ValidateAPIToken(tok); !ok {
		t.Error("the token works again once enabled")
	}

	// A reset forces a change again; a delete takes the tokens along.
	if _, err := ResetPassword(bob, "alice", "reset-pw"); err != nil {
		t.Fatal(err)
	}
	if p, ok := Authenticate("alice", "reset-pw"); !ok || !p.MustChangePassword {
		t.Errorf("after the reset: %v %+v", ok, p)
	}
	list, _ := ListUsers()
	if len(list) != 4 || list[0].Name != OwnerAccount || list[1].Name != "alice" {
		t.Errorf("the list, owner first then by name: %+v", list)
	}
	if err := DeleteUser(bob, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ValidateAPIToken(tok); ok {
		t.Error("a deleted account's token validated")
	}
	if _, err := cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), "kubendt-user-alice", metav1.GetOptions{}); err == nil {
		t.Error("alice's secret is still there")
	}
}

func TestAPITokensBelongToAccounts(t *testing.T) {
	cs := fakeCluster(t, true)
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateUser(owner(), "alice", "x", RoleUser, false, [3]int{0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	raw, err := CreateAPIToken(OwnerAccount, "ci", 0)
	if err != nil || len(raw) < 10 || raw[:4] != "kdt_" {
		t.Fatalf("token: %q %v", raw, err)
	}
	if _, err := CreateAPIToken(OwnerAccount, "ci", 0); !errors.Is(err, ErrTokenNameExists) {
		t.Errorf("duplicate name for one account: %v", err)
	}
	if _, err := CreateAPIToken("alice", "ci", 0); err != nil {
		t.Errorf("the same name for another account is fine: %v", err)
	}
	if _, err := CreateAPIToken("ghost", "ci", 0); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("no token for an account that is not there: %v", err)
	}
	p, ok := ValidateAPIToken(raw)
	if !ok || p.Method != "token" || p.Identity != OwnerAccount || p.Role != RoleOwner {
		t.Fatalf("token did not validate: %v %+v", ok, p)
	}
	own, _ := ListAPITokens(OwnerAccount)
	all, _ := ListAPITokens("")
	if len(own) != 1 || own[0].Name != "ci" || own[0].User != OwnerAccount || len(all) != 2 {
		t.Fatalf("lists: own %+v all %+v", own, all)
	}
	s, err := cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), tokenSecretName(own[0].ID), metav1.GetOptions{})
	if err != nil || s.Labels["kubendt/kind"] != "token" || string(s.Data["user"]) != OwnerAccount {
		t.Fatalf("token secret: %v %v", err, s)
	}
	if TokenOwner(own[0].ID) != OwnerAccount || TokenOwner(1) != "" {
		t.Error("the owner of a token")
	}

	// A token minted before accounts existed, with no user, is the owner's.
	legacy := &tokenEntry{ID: 7, Hash: hashToken("kdt_legacy"), Name: "legacy", CreatedAt: nowUnix()}
	if err := createToken(legacy); err != nil {
		t.Fatal(err)
	}
	s, _ = cs.CoreV1().Secrets("kubendt-system").Get(context.TODO(), tokenSecretName(7), metav1.GetOptions{})
	delete(s.Data, "user")
	_, _ = cs.CoreV1().Secrets("kubendt-system").Update(context.TODO(), s, metav1.UpdateOptions{})
	storeLoadedAt = time.Now().Add(-time.Minute)
	if p, ok := ValidateAPIToken("kdt_legacy"); !ok || p.Identity != OwnerAccount {
		t.Errorf("a legacy token is the owner's: %v %+v", ok, p)
	}

	expired, _ := CreateAPIToken(OwnerAccount, "expired", time.Now().Add(-time.Hour).Unix())
	if _, ok := ValidateAPIToken(expired); ok {
		t.Error("expired token accepted")
	}
	removed, err := DeleteAPIToken(own[0].ID)
	if err != nil || !removed {
		t.Fatalf("delete: %v %v", removed, err)
	}
	if _, ok := ValidateAPIToken(raw); ok {
		t.Error("a revoked token validated")
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
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("old-pw") {
		t.Error("the stored password did not move")
	}
	if p, ok := ValidateAPIToken("kdt_old"); !ok || p.Identity != OwnerAccount {
		t.Errorf("the old token did not move as the owner's: %v %+v", ok, p)
	}
	if list, _ := cs.CoreV1().Secrets("kubendt-system").List(context.TODO(), metav1.ListOptions{LabelSelector: "kubendt/kind=user"}); len(list.Items) != 1 || string(list.Items[0].Data["role"]) != RoleOwner {
		t.Errorf("user secrets: %+v", list.Items)
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
