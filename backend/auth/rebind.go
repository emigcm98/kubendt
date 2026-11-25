package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/crypto/bcrypt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// A switch to a context on another cluster moves the store with it: that
// cluster is another KubeNDT, with its own accounts, tokens and session key.

var rebindMu sync.Mutex

// ownerOn reads the owner of another cluster's store, nil when that cluster
// has no KubeNDT yet.
func ownerOn(secrets corev1client.SecretInterface) (*userRecord, error) {
	s, err := secrets.Get(context.TODO(), userSecretName(OwnerAccount), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the owner of that cluster: %w", err)
	}
	if u := userFromSecret(s); u.Hash != "" {
		return u, nil
	}
	return nil, nil
}

// CheckOwnerOn tells whether a password is the owner's of another cluster's
// store. With no KubeNDT there yet it is checked against this owner, whose
// password that cluster will take.
func CheckOwnerOn(secrets corev1client.SecretInterface, password string) (bool, error) {
	owner, err := ownerOn(secrets)
	if err != nil {
		return false, err
	}
	if owner == nil {
		return CheckPassword(password), nil
	}
	return bcrypt.CompareHashAndPassword([]byte(owner.Hash), []byte(password)) == nil, nil
}

// Rebind moves the store to the cluster the client was just switched to. Its
// accounts, tokens and session key take over, so the sessions signed with
// the old key end by themselves; on the same cluster nothing changes but the
// credentials. A cluster with no owner yet takes this one's, whose password
// the switch has checked. Neither the environment's password nor an old
// database applies here: they belong to the cluster the backend starts on.
// On a failure the old store stays.
func Rebind(secrets corev1client.SecretInterface) error {
	if !enabled {
		return nil
	}
	rebindMu.Lock()
	defer rebindMu.Unlock()

	mu.RLock()
	current := users[OwnerAccount]
	prevSecrets, prevKey, prevBound := homeSecrets, sessionKey, bound
	mu.RUnlock()
	fail := func(err error) error {
		homeSecrets = prevSecrets
		mu.Lock()
		sessionKey, bound = prevKey, prevBound
		mu.Unlock()
		if prevBound {
			if reloadErr := reloadStore(); reloadErr != nil {
				return fmt.Errorf("%w (and the old store could not be read again: %v)", err, reloadErr)
			}
		}
		return err
	}

	homeSecrets = secrets
	owner, err := readUser(OwnerAccount)
	if err != nil {
		return fail(err)
	}
	if owner == nil || owner.Hash == "" {
		if current == nil || current.Hash == "" {
			return fail(errors.New("there is no owner to carry to that cluster"))
		}
		carried := *current
		if err := writeUser(&carried); err != nil {
			return fail(err)
		}
	}
	key, err := loadOrCreateSessionKey()
	if err != nil {
		return fail(err)
	}
	mu.Lock()
	sessionKey, bound = key, true
	mu.Unlock()
	if err := reloadStore(); err != nil {
		return fail(err)
	}
	return nil
}
