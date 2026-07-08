package helpers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"kubendt/kubeclient"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NamespaceOperationLock struct {
	Namespace     string `json:"namespace"`
	OperationType string `json:"operationType"`
	StartedAt     string `json:"startedAt"`
}

func NamespaceHasTopology(namespace string) (bool, error) {
	if DB == nil {
		return false, fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return false, err
	}

	if err := ensureNamespaceStateRow(namespace); err != nil {
		return false, err
	}

	var hasTopology int
	if err := DB.QueryRow(`SELECT has_topology FROM namespace_state WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace).Scan(&hasTopology); err != nil {
		return false, fmt.Errorf("error reading namespace topology state: %w", err)
	}

	return hasTopology == 1, nil
}

func SetNamespaceHasTopology(namespace string, hasTopology bool) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}

	if err := ensureNamespaceStateRow(namespace); err != nil {
		return err
	}

	value := 0
	if hasTopology {
		value = 1
	}

	_, err = DB.Exec(`UPDATE namespace_state SET has_topology = ?, updated_at = CURRENT_TIMESTAMP WHERE cluster_id = ? AND namespace = ?`, value, clusterID, namespace)
	if err != nil {
		return fmt.Errorf("error updating namespace topology state: %w", err)
	}

	return nil
}

func SyncNamespaceTopologyState(namespace string) (bool, error) {
	existingNodes, err := GetExistingNodes(namespace)
	if err != nil {
		return false, fmt.Errorf("error checking existing nodes: %w", err)
	}
	if len(existingNodes) > 0 {
		if err := SetNamespaceHasTopology(namespace, true); err != nil {
			return false, err
		}
		return true, nil
	}

	topologyList, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("error checking existing topologies: %w", err)
	}

	hasTopology := len(topologyList.Items) > 0
	if err := SetNamespaceHasTopology(namespace, hasTopology); err != nil {
		return false, err
	}

	return hasTopology, nil
}

func AcquireNamespaceOperationLock(namespace, operationType string) (*NamespaceOperationLock, bool, error) {
	if DB == nil {
		return nil, false, fmt.Errorf("database not initialized")
	}

	if operationType == "" {
		operationType = "unknown"
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return nil, false, err
	}

	_, err = DB.Exec(`INSERT INTO namespace_operations (cluster_id, namespace, operation_type, started_at) VALUES (?, ?, ?, ?)`, clusterID, namespace, operationType, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
			current, readErr := GetNamespaceOperationLock(namespace)
			if readErr != nil {
				return nil, false, fmt.Errorf("namespace has an operation in progress and lock details could not be read: %w", readErr)
			}
			if current == nil {
				return nil, false, fmt.Errorf("namespace has an operation in progress")
			}
			return current, false, nil
		}
		return nil, false, fmt.Errorf("error acquiring namespace operation lock: %w", err)
	}

	return &NamespaceOperationLock{Namespace: namespace, OperationType: operationType, StartedAt: time.Now().UTC().Format(time.RFC3339)}, true, nil
}

func ReleaseNamespaceOperationLock(namespace string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}

	_, err = DB.Exec(`DELETE FROM namespace_operations WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace)
	if err != nil {
		return fmt.Errorf("error releasing namespace operation lock: %w", err)
	}

	return nil
}

// ClearAllNamespaceOperationLocks wipes every row in namespace_operations and
// returns how many it deleted. Meant to be called once at process startup:
// any lock that survived a previous run is necessarily stale (no operation is
// in flight on a freshly-booted process), so keeping them would leave
// namespaces wedged until the user manually intervened. Crashes, Ctrl-C,
// OOM-kill, anything that didn't go through the per-handler `defer release`
// gets cleaned up here.
func ClearAllNamespaceOperationLocks() (int64, error) {
	if DB == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	res, err := DB.Exec(`DELETE FROM namespace_operations`)
	if err != nil {
		return 0, fmt.Errorf("error clearing namespace operation locks: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

func GetNamespaceOperationLock(namespace string) (*NamespaceOperationLock, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return nil, err
	}

	var lock NamespaceOperationLock
	err = DB.QueryRow(`SELECT namespace, operation_type, started_at FROM namespace_operations WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace).Scan(&lock.Namespace, &lock.OperationType, &lock.StartedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("error reading namespace operation lock: %w", err)
	}

	return &lock, nil
}

func DeleteNamespaceState(namespace string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}

	if _, err := DB.Exec(`DELETE FROM namespace_state WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace); err != nil {
		return fmt.Errorf("error deleting namespace state: %w", err)
	}
	if _, err := DB.Exec(`DELETE FROM namespace_operations WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace); err != nil {
		return fmt.Errorf("error deleting namespace operation lock: %w", err)
	}
	if err := DeleteNamespaceLinkUIDRegistry(namespace); err != nil {
		return err
	}
	if err := DeleteNamespaceDriverOperationHistory(namespace); err != nil {
		return err
	}
	return nil
}

func ensureNamespaceStateRow(namespace string) error {
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	_, err = DB.Exec(`INSERT OR IGNORE INTO namespace_state (cluster_id, namespace, has_topology) VALUES (?, ?, 0)`, clusterID, namespace)
	if err != nil {
		return fmt.Errorf("error ensuring namespace state row: %w", err)
	}
	return nil
}
