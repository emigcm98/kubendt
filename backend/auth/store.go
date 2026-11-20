package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"kubendt/database"
	"kubendt/kubeclient"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// The platform's own state lives as Secrets in the system namespace of the
// home cluster, the first one the backend binds to: one kubendt-user-<name>
// per user (the bcrypt hash, the role, the flags, the moment before which
// no session of theirs counts), one kubendt-token-<id> per API token with
// the user it acts as, and kubendt-internal-session-key, the HMAC key every
// backend on the cluster signs sessions with.
const (
	userSecretPrefix     = "kubendt-user-"
	sessionKeySecretName = "kubendt-internal-session-key"
	tokenSecretPrefix    = "kubendt-token-"
	tokenSelector        = "kubendt/kind=token"
	userSelector         = "kubendt/kind=user"

	keyPasswordHash        = "password_hash"
	keyRole                = "role"
	keyDisabled            = "disabled"
	keyMustChangePassword  = "must_change_password"
	keyCanCreateNamespaces = "can_create_namespaces"
	keyQuotaNamespaces     = "quota_namespaces"
	keyQuotaPods           = "quota_pods"
	keyQuotaPodsPerNS      = "quota_pods_per_namespace"
	keySource              = "source"
	keySessionsValidFrom   = "sessions_valid_after"
	keyHash                = "hash"
	keyName                = "name"
	keyUser                = "user"
	keyCreatedAt           = "created_at"
	keyExpiresAt           = "expires_at"
	keyLastUsedAt          = "last_used_at"
	keySessionKey          = "key"
)

// ErrNoStore says the backend has no cluster to keep what was asked.
var ErrNoStore = errors.New("no cluster is bound, the platform state lives there")

// homeSecrets is the Secrets client of the system namespace, taken when the
// backend binds so a context switch later does not move the store. A
// variable so the tests can hand it a fake clientset.
var homeSecrets corev1client.SecretInterface

var secretsOfCurrentCluster = func() corev1client.SecretInterface {
	if kubeclient.Clientset == nil {
		return nil
	}
	return kubeclient.Clientset.CoreV1().Secrets(kubeclient.SystemNamespace())
}

func internalLabels(kind string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "kubendt-backend",
		"kubendt/internal":             "true",
		"kubendt/kind":                 kind,
	}
}

// userRecord is a user as the store holds it.
type userRecord struct {
	Name                  string
	Hash                  string
	Role                  string
	Disabled              bool
	MustChangePassword    bool
	CanCreateNamespaces   bool
	QuotaNamespaces       int
	QuotaPods             int
	QuotaPodsPerNamespace int
	Source                string
	CreatedAt             int64
	SessionsValidAfter    int64
}

func userSecretName(name string) string { return userSecretPrefix + name }

func boolOf(b []byte) bool { return string(b) == "true" }

func intOf(b []byte) int {
	n, _ := strconv.Atoi(string(b))
	return n
}

func int64Of(b []byte) int64 {
	n, _ := strconv.ParseInt(string(b), 10, 64)
	return n
}

func userFromSecret(s *corev1.Secret) *userRecord {
	name := s.Labels["kubendt/user"]
	if name == "" {
		name = s.Name[len(userSecretPrefix):]
	}
	return &userRecord{
		Name:                  name,
		Hash:                  string(s.Data[keyPasswordHash]),
		Role:                  string(s.Data[keyRole]),
		Disabled:              boolOf(s.Data[keyDisabled]),
		MustChangePassword:    boolOf(s.Data[keyMustChangePassword]),
		CanCreateNamespaces:   boolOf(s.Data[keyCanCreateNamespaces]),
		QuotaNamespaces:       intOf(s.Data[keyQuotaNamespaces]),
		QuotaPods:             intOf(s.Data[keyQuotaPods]),
		QuotaPodsPerNamespace: intOf(s.Data[keyQuotaPodsPerNS]),
		Source:                string(s.Data[keySource]),
		CreatedAt:             int64Of(s.Data[keyCreatedAt]),
		SessionsValidAfter:    int64Of(s.Data[keySessionsValidFrom]),
	}
}

func userData(u *userRecord) map[string][]byte {
	return map[string][]byte{
		keyPasswordHash:        []byte(u.Hash),
		keyRole:                []byte(u.Role),
		keyDisabled:            []byte(strconv.FormatBool(u.Disabled)),
		keyMustChangePassword:  []byte(strconv.FormatBool(u.MustChangePassword)),
		keyCanCreateNamespaces: []byte(strconv.FormatBool(u.CanCreateNamespaces)),
		keyQuotaNamespaces:     []byte(strconv.Itoa(u.QuotaNamespaces)),
		keyQuotaPods:           []byte(strconv.Itoa(u.QuotaPods)),
		keyQuotaPodsPerNS:      []byte(strconv.Itoa(u.QuotaPodsPerNamespace)),
		keySource:              []byte(u.Source),
		keyCreatedAt:           []byte(strconv.FormatInt(u.CreatedAt, 10)),
		keySessionsValidFrom:   []byte(strconv.FormatInt(u.SessionsValidAfter, 10)),
	}
}

func readUser(name string) (*userRecord, error) {
	s, err := homeSecrets.Get(context.TODO(), userSecretName(name), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the user %s: %w", name, err)
	}
	return userFromSecret(s), nil
}

func listUserRecords() ([]*userRecord, error) {
	list, err := homeSecrets.List(context.TODO(), metav1.ListOptions{LabelSelector: userSelector})
	if err != nil {
		return nil, fmt.Errorf("could not list the users: %w", err)
	}
	out := make([]*userRecord, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, userFromSecret(&list.Items[i]))
	}
	return out, nil
}

func writeUser(u *userRecord) error {
	labels := internalLabels("user")
	labels["kubendt/user"] = u.Name
	return upsertSecret(userSecretName(u.Name), labels, userData(u))
}

func deleteUserSecret(name string) error {
	err := homeSecrets.Delete(context.TODO(), userSecretName(name), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("could not delete the user %s: %w", name, err)
	}
	return nil
}

func upsertSecret(name string, labels map[string]string, data map[string][]byte) error {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}, Data: data}
	_, err := homeSecrets.Create(context.TODO(), s, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not write the secret %s: %w", name, err)
	}
	current, err := homeSecrets.Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not read the secret %s: %w", name, err)
	}
	current.Data = data
	if current.Labels == nil {
		current.Labels = map[string]string{}
	}
	for k, v := range labels {
		current.Labels[k] = v
	}
	if _, err := homeSecrets.Update(context.TODO(), current, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("could not write the secret %s: %w", name, err)
	}
	return nil
}

// loadOrCreateSessionKey is the key the sessions are signed with, made once
// per cluster so every backend on it accepts the others' cookies.
func loadOrCreateSessionKey() ([]byte, error) {
	s, err := homeSecrets.Get(context.TODO(), sessionKeySecretName, metav1.GetOptions{})
	if err == nil && len(s.Data[keySessionKey]) >= 32 {
		return s.Data[keySessionKey], nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("could not read the session key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := randRead(key); err != nil {
		return nil, err
	}
	created := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: sessionKeySecretName, Labels: internalLabels("session-key")}, Data: map[string][]byte{keySessionKey: key}}
	if _, err := homeSecrets.Create(context.TODO(), created, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("could not write the session key: %w", err)
		}
		// Another backend made it first, take that one.
		s, err := homeSecrets.Get(context.TODO(), sessionKeySecretName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("could not read the session key: %w", err)
		}
		return s.Data[keySessionKey], nil
	}
	return key, nil
}

// tokenEntry is an API token as the store holds it; User is who it acts as,
// the owner account for the tokens minted before users existed.
type tokenEntry struct {
	ID         int64
	Hash       string
	Name       string
	User       string
	CreatedAt  int64
	ExpiresAt  int64
	LastUsedAt int64
}

func tokenSecretName(id int64) string { return tokenSecretPrefix + strconv.FormatInt(id, 10) }

func tokenFromSecret(s *corev1.Secret) (*tokenEntry, bool) {
	id, err := strconv.ParseInt(string(s.Data["id"]), 10, 64)
	if err != nil || id <= 0 {
		return nil, false
	}
	t := &tokenEntry{ID: id, Hash: string(s.Data[keyHash]), Name: string(s.Data[keyName]), User: string(s.Data[keyUser])}
	if t.User == "" {
		t.User = OwnerAccount
	}
	t.CreatedAt = int64Of(s.Data[keyCreatedAt])
	t.ExpiresAt = int64Of(s.Data[keyExpiresAt])
	t.LastUsedAt = int64Of(s.Data[keyLastUsedAt])
	return t, t.Hash != ""
}

func tokenData(t *tokenEntry) map[string][]byte {
	return map[string][]byte{
		"id":          []byte(strconv.FormatInt(t.ID, 10)),
		keyHash:       []byte(t.Hash),
		keyName:       []byte(t.Name),
		keyUser:       []byte(t.User),
		keyCreatedAt:  []byte(strconv.FormatInt(t.CreatedAt, 10)),
		keyExpiresAt:  []byte(strconv.FormatInt(t.ExpiresAt, 10)),
		keyLastUsedAt: []byte(strconv.FormatInt(t.LastUsedAt, 10)),
	}
}

func listTokens() ([]*tokenEntry, error) {
	list, err := homeSecrets.List(context.TODO(), metav1.ListOptions{LabelSelector: tokenSelector})
	if err != nil {
		return nil, fmt.Errorf("could not list the API tokens: %w", err)
	}
	out := make([]*tokenEntry, 0, len(list.Items))
	for i := range list.Items {
		if t, ok := tokenFromSecret(&list.Items[i]); ok {
			out = append(out, t)
		}
	}
	return out, nil
}

func createToken(t *tokenEntry) error {
	labels := internalLabels("token")
	labels["kubendt/user"] = t.User
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: tokenSecretName(t.ID), Labels: labels}, Data: tokenData(t)}
	if _, err := homeSecrets.Create(context.TODO(), s, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("could not write the token: %w", err)
	}
	return nil
}

func deleteToken(id int64) (bool, error) {
	err := homeSecrets.Delete(context.TODO(), tokenSecretName(id), metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("could not delete the token: %w", err)
	}
	return true, nil
}

// touchToken stamps the last use on the object, best effort, at most every
// few minutes per token so a busy script does not write on every call.
func touchToken(id int64, at int64) {
	client := homeSecrets
	if client == nil {
		return
	}
	s, err := client.Get(context.TODO(), tokenSecretName(id), metav1.GetOptions{})
	if err != nil {
		return
	}
	if s.Data == nil {
		s.Data = map[string][]byte{}
	}
	s.Data[keyLastUsedAt] = []byte(strconv.FormatInt(at, 10))
	_, _ = client.Update(context.TODO(), s, metav1.UpdateOptions{})
}

// migrateFromDatabase moves the admin hash and the API tokens an older
// backend kept in SQLite into the store, once, when the store has no owner
// yet, and drops the rows. The sessions table goes with them: a session is a
// signed cookie now.
func migrateFromDatabase() error {
	db := database.DB
	if db == nil {
		return nil
	}
	current, err := readUser(OwnerAccount)
	if err != nil {
		return err
	}
	moved := 0
	if current == nil {
		var hash string
		err := db.QueryRow(`SELECT password_hash FROM auth_config WHERE id = 1`).Scan(&hash)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if hash != "" {
			if err := writeUser(&userRecord{Name: OwnerAccount, Hash: hash, Role: RoleOwner, Source: SourceLocal, CreatedAt: nowUnix()}); err != nil {
				return err
			}
			moved++
		}
		rows, err := db.Query(`SELECT id, token_hash, name, created_at, last_used_at, expires_at FROM api_tokens`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t := tokenEntry{User: OwnerAccount}
			var lastUsed, expires sql.NullInt64
			if err := rows.Scan(&t.ID, &t.Hash, &t.Name, &t.CreatedAt, &lastUsed, &expires); err != nil {
				return err
			}
			t.LastUsedAt, t.ExpiresAt = lastUsed.Int64, expires.Int64
			if err := createToken(&t); err != nil && !apierrors.IsAlreadyExists(errors.Unwrap(err)) {
				return err
			}
			moved++
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	for _, table := range []string{"auth_config", "api_tokens", "sessions"} {
		if _, err := db.Exec(`DELETE FROM ` + table); err != nil {
			return err
		}
	}
	if moved > 0 {
		log.Printf("ℹ️ Admin password and API tokens moved from the database to the cluster (%d objects)", moved)
	}
	return nil
}

func nowUnix() int64 { return time.Now().Unix() }
