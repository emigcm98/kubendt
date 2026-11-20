package kubeclient

import (
	"os"
	"strings"
)

const (
	SystemNamespaceEnv     = "KUBENDT_SYSTEM_NAMESPACE"
	DefaultSystemNamespace = "kubendt-system"
)

// SystemNamespace is where the platform keeps what belongs to no lab: the
// admin, the API tokens, the session key, the layouts of deleted namespaces.
func SystemNamespace() string {
	if ns := strings.TrimSpace(os.Getenv(SystemNamespaceEnv)); ns != "" {
		return ns
	}
	return DefaultSystemNamespace
}
