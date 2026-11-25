package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kubendt/auth"
	"kubendt/kubeclient"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

// A kubeconfig with two contexts on clusters that never answer, so the tests
// reach the gates without a cluster.
const twoContexts = `apiVersion: v1
kind: Config
clusters:
- name: here
  cluster: {server: "https://127.0.0.1:1"}
- name: there
  cluster: {server: "https://127.0.0.1:2"}
users:
- name: u
  user: {token: t}
contexts:
- name: here
  context: {cluster: here, user: u}
- name: there
  context: {cluster: there, user: u}
current-context: here
`

func switchRouter(p *auth.Principal) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("principal", p)
		c.Next()
	})
	r.POST("/kube/context", SetKubeContext)
	return r
}

func postContext(r *gin.Engine, body string) (int, map[string]any) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/kube/context", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// storeWithOwner is another cluster's system namespace with its owner.
func storeWithOwner(t *testing.T, password string) corev1client.SecretInterface {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	secrets := fake.NewClientset().CoreV1().Secrets("kubendt-system")
	_, err = secrets.Create(context.TODO(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kubendt-user-" + auth.OwnerAccount, Labels: map[string]string{"kubendt/kind": "user"}},
		Data:       map[string][]byte{"password_hash": h, "role": []byte(auth.RoleOwner), "source": []byte("local")},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return secrets
}

// The switch is the owner's, only on a kubeconfig, decided on a cluster ID
// read fresh, and a context on another cluster wants that cluster's owner
// password and no open tool socket before anything is touched. A switch that
// fails half way goes back to where it started.
func TestContextSwitchGates(t *testing.T) {
	t.Setenv("KUBENDT_AUTH_DISABLED", "")
	if !auth.Enabled() {
		t.Setenv("KUBENDT_ADMIN_PASSWORD", "pw")
		_ = auth.Init()
	}
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(twoContexts), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	t.Setenv("KUBE_CONTEXT", "")
	realTarget := resolveTarget
	t.Cleanup(func() {
		resolveTarget = realTarget
		openStreams.Store(0)
		kubeclient.SetModeForTest("")
		kubeclient.SetClusterIDForTest("")
		kubeclient.Config, kubeclient.Clientset, kubeclient.DynamicClient = nil, nil, nil
	})
	admin := &auth.Principal{Identity: "adm", Role: auth.RoleAdmin, Roles: []string{auth.RoleAdmin}}
	owner := &auth.Principal{Identity: auth.OwnerAccount, Role: auth.RoleOwner, Roles: []string{auth.RoleOwner}}

	kubeclient.SetModeForTest(kubeclient.ModeInCluster)
	if code, out := postContext(switchRouter(owner), `{"context":"there"}`); code != http.StatusConflict || out["code"] != "IN_CLUSTER" {
		t.Fatalf("a switch in the cluster: %d %v", code, out)
	}

	kubeclient.SetModeForTest(kubeclient.ModeKubeconfig)
	if code, out := postContext(switchRouter(admin), `{"context":"there"}`); code != http.StatusForbidden || out["code"] != "OWNER_ONLY" {
		t.Fatalf("an admin switching: %d %v", code, out)
	}
	if code, out := postContext(switchRouter(owner), `{"context":"nowhere"}`); code != http.StatusBadRequest || out["code"] != "CONTEXT_NOT_FOUND" {
		t.Fatalf("a context the kubeconfig lacks: %d %v", code, out)
	}
	if code, out := postContext(switchRouter(owner), `{"context":"there"}`); code != http.StatusBadGateway || out["code"] != "CONTEXT_UNREACHABLE" {
		t.Fatalf("a context whose cluster does not answer: %d %v", code, out)
	}

	kubeclient.SetClusterIDForTest("cluster-here")
	there := storeWithOwner(t, "there-pw")
	resolveTarget = func(string) (corev1client.SecretInterface, string, error) { return there, "cluster-there", nil }
	if code, out := postContext(switchRouter(owner), `{"context":"there"}`); code != http.StatusBadRequest || out["code"] != "PASSWORD_REQUIRED" {
		t.Fatalf("another cluster without its owner's password: %d %v", code, out)
	}
	if code, out := postContext(switchRouter(owner), `{"context":"there","password":"pw"}`); code != http.StatusUnauthorized || out["code"] != "INVALID_CREDENTIALS" {
		t.Fatalf("this owner's password for the owner there: %d %v", code, out)
	}
	openStreams.Store(1)
	if code, out := postContext(switchRouter(owner), `{"context":"there","password":"there-pw"}`); code != http.StatusConflict || out["code"] != "STREAMS_OPEN" {
		t.Fatalf("a switch with a shell open: %d %v", code, out)
	}
	openStreams.Store(0)

	// The same cluster by its ID, whatever the context's name: no password.
	// Its system namespace cannot be read, so the switch is undone.
	resolveTarget = func(string) (corev1client.SecretInterface, string, error) { return nil, "cluster-here", nil }
	code, out := postContext(switchRouter(owner), `{"context":"there"}`)
	if code != http.StatusInternalServerError || !strings.Contains(out["error"].(string), "undone") {
		t.Fatalf("a switch that cannot finish: %d %v", code, out)
	}
	if kubeclient.ContextOverride() != "" || kubeclient.ActiveContextName() != "here" {
		t.Errorf("a failed switch stayed on %q (override %q)", kubeclient.ActiveContextName(), kubeclient.ContextOverride())
	}
}

// In the cluster there is no kubeconfig file, only the ServiceAccount: the
// page must still read as configured, with its mode and cluster.
func TestKubeConfigInfoInCluster(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("KUBE_CONTEXT", "")
	kubeclient.SetModeForTest(kubeclient.ModeInCluster)
	kubeclient.SetClusterIDForTest("cluster-here")
	kubeclient.Config = &rest.Config{Host: "https://10.96.0.1:443"}
	t.Cleanup(func() {
		kubeclient.SetModeForTest("")
		kubeclient.SetClusterIDForTest("")
		kubeclient.Config = nil
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/kube/config", GetKubeConfigInfo)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/kube/config", nil))
	var out kubeConfigInfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Configured || out.Mode != kubeclient.ModeInCluster || out.ClusterID != "cluster-here" {
		t.Fatalf("in the cluster: %+v", out)
	}
}
