package helpers

import (
	"context"
	"fmt"

	"kubendt/kubeclient"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

const (
	SystemNamespaceEnv     = kubeclient.SystemNamespaceEnv
	DefaultSystemNamespace = kubeclient.DefaultSystemNamespace
)

// systemNamespaces is the client the system namespace goes through, a
// variable so the tests can hand it a fake clientset.
var systemNamespaces = func() corev1.NamespaceInterface {
	return kubeclient.Clientset.CoreV1().Namespaces()
}

// SystemNamespace is where the platform keeps what belongs to no lab: the
// admin and the tokens, the layouts of deleted namespaces.
func SystemNamespace() string { return kubeclient.SystemNamespace() }

// EnsureSystemNamespace creates the system namespace when the cluster lacks
// it. It carries no kubendt/enabled label, so it never lists as a lab.
func EnsureSystemNamespace() error {
	name := SystemNamespace()
	_, err := systemNamespaces().Get(context.TODO(), name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("could not read the system namespace %s: %w", name, err)
	}
	ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   name,
		Labels: map[string]string{"app.kubernetes.io/managed-by": "kubendt-backend", "kubendt/system": "true"},
	}}
	if _, err := systemNamespaces().Create(context.TODO(), ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create the system namespace %s: %w", name, err)
	}
	return nil
}
