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
// home cluster, the first one the backend binds to: kubendt-user-admin (the
// bcrypt hash and the moment before which no session counts), one
// kubendt-token-<id> per API token, and kubendt-internal-session-key, the
// HMAC key every backend on the cluster signs sessions with.
const (
	adminSecretName      = "kubendt-user-admin"
	sessionKeySecretName = "kubendt-internal-session-key"
	tokenSecretPrefix    = "kubendt-token-"
	tokenSelector        = "kubendt/kind=token"

	keyPasswordHash      = "password_hash"
	keySessionsValidFrom = "sessions_valid_after"
	keyHash              = "hash"
	keyName              = "name"
	keyCreatedAt         = "created_at"
	keyExpiresAt         = "expires_at"
	keyLastUsedAt        = "last_used_at"
	keySessionKey        = "key"
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

type adminRecord struct {
	Hash               string
	SessionsValidAfter int64
}

func readAdmin() (*adminRecord, error) {
	s, err := homeSecrets.Get(context.TODO(), adminSecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the admin secret: %w", err)
	}
	validAfter, _ := strconv.ParseInt(string(s.Data[keySessionsValidFrom]), 10, 64)
	return &adminRecord{Hash: string(s.Data[keyPasswordHash]), SessionsValidAfter: validAfter}, nil
}

func writeAdmin(r adminRecord) error {
	data := map[string][]byte{
		keyPasswordHash:      []byte(r.Hash),
		keySessionsValidFrom: []byte(strconv.FormatInt(r.SessionsValidAfter, 10)),
	}
	labels := internalLabels("user")
	labels["kubendt/user"] = "admin"
	return upsertSecret(adminSecretName, labels, data)
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

type tokenEntry struct {
	ID         int64
	Hash       string
	Name       string
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
	t := &tokenEntry{ID: id, Hash: string(s.Data[keyHash]), Name: string(s.Data[keyName])}
	t.CreatedAt, _ = strconv.ParseInt(string(s.Data[keyCreatedAt]), 10, 64)
	t.ExpiresAt, _ = strconv.ParseInt(string(s.Data[keyExpiresAt]), 10, 64)
	t.LastUsedAt, _ = strconv.ParseInt(string(s.Data[keyLastUsedAt]), 10, 64)
	return t, t.Hash != ""
}

func tokenData(t *tokenEntry) map[string][]byte {
	return map[string][]byte{
		"id":          []byte(strconv.FormatInt(t.ID, 10)),
		keyHash:       []byte(t.Hash),
		keyName:       []byte(t.Name),
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
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: tokenSecretName(t.ID), Labels: internalLabels("token")}, Data: tokenData(t)}
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
// backend kept in SQLite into the store, once, when the store has no admin
// yet, and drops the rows. The sessions table goes with them: a session is a
// signed cookie now.
func migrateFromDatabase() error {
	db := database.DB
	if db == nil {
		return nil
	}
	current, err := readAdmin()
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
			if err := writeAdmin(adminRecord{Hash: hash}); err != nil {
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
			var t tokenEntry
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
