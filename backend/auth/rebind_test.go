package auth

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	k8stesting "k8s.io/client-go/testing"
)

// otherStore is the store of a second cluster, with the accounts given.
func otherStore(t *testing.T, accounts map[string]string) (*fake.Clientset, corev1client.SecretInterface) {
	t.Helper()
	cs := fake.NewClientset()
	secrets := cs.CoreV1().Secrets("kubendt-system")
	prev := homeSecrets
	homeSecrets = secrets
	defer func() { homeSecrets = prev }()
	for name, pw := range accounts {
		h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		role := RoleUser
		if name == OwnerAccount {
			role = RoleOwner
		}
		if err := writeUser(&userRecord{Name: name, Hash: string(h), Role: role, Source: SourceLocal, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	return cs, secrets
}

func ownerHashOn(t *testing.T, secrets corev1client.SecretInterface) string {
	t.Helper()
	s, err := secrets.Get(context.TODO(), userSecretName(OwnerAccount), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return string(s.Data[keyPasswordHash])
}

func startHome(t *testing.T) corev1client.SecretInterface {
	t.Helper()
	home := fakeCluster(t, true)
	t.Setenv("KUBENDT_ADMIN_PASSWORD", "home-pw")
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	return home.CoreV1().Secrets("kubendt-system")
}

func TestRebindToAnotherClustersKubeNDT(t *testing.T) {
	home := startHome(t)
	homeOwner := ownerHashOn(t, home)
	_, there := otherStore(t, map[string]string{OwnerAccount: "there-pw", "bob": "bob-pw"})

	if ok, err := CheckOwnerOn(there, "home-pw"); err != nil || ok {
		t.Fatalf("this owner's password passed for the owner there: %v %v", ok, err)
	}
	if ok, err := CheckOwnerOn(there, "there-pw"); err != nil || !ok {
		t.Fatalf("the owner's password there did not pass: %v %v", ok, err)
	}

	before, err := CreateSession(OwnerAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err := Rebind(there); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := ValidateSession(before); ok {
		t.Error("a session of the old cluster still counts")
	}
	if !CheckPassword("there-pw") || CheckPassword("home-pw") {
		t.Error("the owner is not the one of the cluster switched to")
	}
	if _, ok := Authenticate("bob", "bob-pw"); !ok {
		t.Error("an account of the cluster switched to cannot sign in")
	}
	if ownerHashOn(t, there) == "" || bcrypt.CompareHashAndPassword([]byte(ownerHashOn(t, there)), []byte("there-pw")) != nil {
		t.Error("the environment's password was written over the owner there")
	}
	if ownerHashOn(t, home) != homeOwner {
		t.Error("the old cluster's owner changed")
	}
	after, _ := CreateSession(OwnerAccount)
	if _, _, ok := ValidateSession(after); !ok {
		t.Error("a session issued after the switch does not count")
	}
}

func TestRebindCarriesTheOwnerToAnEmptyCluster(t *testing.T) {
	startHome(t)
	_, empty := otherStore(t, nil)

	if ok, _ := CheckOwnerOn(empty, "nope"); ok {
		t.Error("a wrong password passed for a cluster with no KubeNDT")
	}
	if ok, err := CheckOwnerOn(empty, "home-pw"); err != nil || !ok {
		t.Fatalf("this owner's password did not pass for a cluster with no KubeNDT: %v %v", ok, err)
	}
	if err := Rebind(empty); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(ownerHashOn(t, empty)), []byte("home-pw")) != nil {
		t.Error("the owner was not carried to the empty cluster")
	}
	if _, err := empty.Get(context.TODO(), sessionKeySecretName, metav1.GetOptions{}); err != nil {
		t.Errorf("the empty cluster got no session key: %v", err)
	}
	if !CheckPassword("home-pw") {
		t.Error("the owner cannot sign in after the switch")
	}
}

func TestRebindOnTheSameClusterKeepsTheSessions(t *testing.T) {
	home := startHome(t)
	raw, _ := CreateSession(OwnerAccount)
	if err := Rebind(home); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := ValidateSession(raw); !ok {
		t.Error("a switch on the same cluster ended a session")
	}
}

func TestRebindFailureKeepsTheOldStore(t *testing.T) {
	startHome(t)
	raw, _ := CreateSession(OwnerAccount)
	broken, _ := otherStore(t, nil)
	broken.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("the cluster went away")
	})
	if err := Rebind(broken.CoreV1().Secrets("kubendt-system")); err == nil {
		t.Fatal("a switch to a cluster that cannot be read went through")
	}
	if _, _, ok := ValidateSession(raw); !ok {
		t.Error("a failed switch ended the sessions")
	}
	if !CheckPassword("home-pw") {
		t.Error("a failed switch changed the owner")
	}
}
