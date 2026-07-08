package helpers

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"time"

	"kubendt/kubeclient"
	"kubendt/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	minLinkUID            = 1_000_000
	maxLinkUID            = 9_999_999
	maxLinkUIDRandomTries = 256
)

type LinkUIDOwner struct {
	Namespace     string
	Node          string
	Interface     string
	PeerNode      string
	PeerInterface string
	UID           int
}

func PrepareUniqueLinkUIDs(namespace string, links []types.LinkSpec) ([]types.LinkSpec, error) {
	usedUIDs, err := GetRegisteredLinkUIDs()
	if err != nil {
		return nil, err
	}

	prepared := make([]types.LinkSpec, len(links))
	copy(prepared, links)

	reservedInRequest := make(map[int]LinkUIDOwner, len(prepared))
	for i := range prepared {
		owner := linkUIDOwnerFromSpec(namespace, prepared[i])
		if prepared[i].UID == nil {
			continue
		}

		uid := *prepared[i].UID
		if uid < minLinkUID || uid > maxLinkUID {
			return nil, fmt.Errorf("VALIDATION:link[%d].uid %d is out of allowed range [%d, %d]", i, uid, minLinkUID, maxLinkUID)
		}
		if existing, exists := reservedInRequest[uid]; exists {
			return nil, fmt.Errorf("VALIDATION:%s is already in use by %s", describeLinkUIDOwner(owner), describeLinkUIDOwner(existing))
		}
		if existing, exists := usedUIDs[uid]; exists {
			return nil, fmt.Errorf("VALIDATION:%s is already in use by %s", describeLinkUIDOwner(owner), describeLinkUIDOwner(existing))
		}
		reservedInRequest[uid] = owner
	}

	for i := range prepared {
		if prepared[i].UID != nil {
			continue
		}

		uid, err := generateAvailableLinkUID(usedUIDs, reservedInRequest)
		if err != nil {
			return nil, err
		}
		prepared[i].UID = &uid
		owner := linkUIDOwnerFromSpec(namespace, prepared[i])
		owner.UID = uid
		reservedInRequest[uid] = owner
	}

	return prepared, nil
}

func GetRegisteredLinkUIDs() (map[int]LinkUIDOwner, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return nil, err
	}

	rows, err := DB.Query(`
		SELECT uid, namespace, node_name, interface_name, peer_node_name, peer_interface_name
		FROM link_uid_registry
		WHERE cluster_id = ?
		ORDER BY uid, namespace, node_name, interface_name`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("error reading link UID registry: %w", err)
	}
	defer rows.Close()

	used := make(map[int]LinkUIDOwner)
	for rows.Next() {
		var owner LinkUIDOwner
		var peerNode sql.NullString
		var peerInterface sql.NullString
		if err := rows.Scan(&owner.UID, &owner.Namespace, &owner.Node, &owner.Interface, &peerNode, &peerInterface); err != nil {
			return nil, fmt.Errorf("error scanning link UID registry: %w", err)
		}
		owner.PeerNode = peerNode.String
		owner.PeerInterface = peerInterface.String
		if _, exists := used[owner.UID]; !exists {
			used[owner.UID] = owner
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating link UID registry: %w", err)
	}

	return used, nil
}

func SyncLinkUIDRegistryFromCluster() error {
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	namespaceList, err := kubeclient.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing namespaces to seed link UID registry: %w", err)
	}

	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	tx, err := DB.Begin()
	if err != nil {
		return fmt.Errorf("error starting link UID registry sync transaction: %w", err)
	}
	defer tx.Rollback()

	// Only this cluster's registry is rebuilt; other clusters' rows are left
	// untouched.
	if _, err := tx.Exec(`DELETE FROM link_uid_registry WHERE cluster_id = ?`, clusterID); err != nil {
		return fmt.Errorf("error clearing link UID registry before sync: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO link_uid_registry (cluster_id, uid, namespace, node_name, interface_name, peer_node_name, peer_interface_name, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`)
	if err != nil {
		return fmt.Errorf("error preparing link UID registry insert: %w", err)
	}
	defer stmt.Close()

	inserted := make(map[string]struct{})
	for _, ns := range namespaceList.Items {
		owners, err := collectNamespaceLinkUIDOwners(ns.Name)
		if err != nil {
			return err
		}
		if err := insertLinkUIDOwners(stmt, clusterID, owners, inserted); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing link UID registry sync: %w", err)
	}

	return nil
}

func SyncNamespaceLinkUIDRegistry(namespace string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}

	owners, err := collectNamespaceLinkUIDOwners(namespace)
	if err != nil {
		return err
	}

	tx, err := DB.Begin()
	if err != nil {
		return fmt.Errorf("error starting namespace link UID registry sync: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM link_uid_registry WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace); err != nil {
		return fmt.Errorf("error clearing namespace link UID registry for %s: %w", namespace, err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO link_uid_registry (cluster_id, uid, namespace, node_name, interface_name, peer_node_name, peer_interface_name, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`)
	if err != nil {
		return fmt.Errorf("error preparing namespace link UID registry insert: %w", err)
	}
	defer stmt.Close()

	inserted := make(map[string]struct{}, len(owners))
	if err := insertLinkUIDOwners(stmt, clusterID, owners, inserted); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing namespace link UID registry sync: %w", err)
	}

	return nil
}

func DeleteNamespaceLinkUIDRegistry(namespace string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}

	_, err = DB.Exec(`DELETE FROM link_uid_registry WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace)
	if err != nil {
		return fmt.Errorf("error deleting link UID registry for namespace %s: %w", namespace, err)
	}

	return nil
}

func collectNamespaceLinkUIDOwners(namespace string) ([]LinkUIDOwner, error) {
	topologyList, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("error listing topology resources in namespace %s for link UID registry sync: %w", namespace, err)
	}

	ownersByUID := make(map[int]LinkUIDOwner)
	for _, item := range topologyList.Items {
		specLinks, found, err := unstructured.NestedSlice(item.Object, "spec", "links")
		if err != nil {
			return nil, fmt.Errorf("error reading links from Topology %s/%s: %w", namespace, item.GetName(), err)
		}
		if !found {
			continue
		}

		for _, linkData := range specLinks {
			linkMap, ok := linkData.(map[string]interface{})
			if !ok {
				continue
			}

			uid, ok := readLinkUIDValue(linkMap["uid"])
			if !ok {
				continue
			}

			if _, exists := ownersByUID[uid]; exists {
				continue
			}

			ownersByUID[uid] = LinkUIDOwner{
				Namespace:     namespace,
				Node:          item.GetName(),
				Interface:     fmt.Sprintf("%v", linkMap["local_intf"]),
				PeerNode:      fromCRDPeerPod(fmt.Sprintf("%v", linkMap["peer_pod"])),
				PeerInterface: fmt.Sprintf("%v", linkMap["peer_intf"]),
				UID:           uid,
			}
		}
	}

	owners := make([]LinkUIDOwner, 0, len(ownersByUID))
	for _, owner := range ownersByUID {
		owners = append(owners, owner)
	}

	return owners, nil
}

func insertLinkUIDOwners(stmt *sql.Stmt, clusterID string, owners []LinkUIDOwner, inserted map[string]struct{}) error {
	for _, owner := range owners {
		key := fmt.Sprintf("%d|%s|%s|%s", owner.UID, owner.Namespace, owner.Node, owner.Interface)
		if _, exists := inserted[key]; exists {
			continue
		}
		if _, err := stmt.Exec(clusterID, owner.UID, owner.Namespace, owner.Node, owner.Interface, nullableString(owner.PeerNode), nullableString(owner.PeerInterface)); err != nil {
			return fmt.Errorf("error inserting link UID %d for %s/%s: %w", owner.UID, owner.Namespace, owner.Node, err)
		}
		inserted[key] = struct{}{}
	}
	return nil
}

func generateAvailableLinkUID(usedUIDs map[int]LinkUIDOwner, reserved map[int]LinkUIDOwner) (int, error) {
	for i := 0; i < maxLinkUIDRandomTries; i++ {
		candidate := rand.Intn(maxLinkUID-minLinkUID+1) + minLinkUID
		if _, exists := usedUIDs[candidate]; exists {
			continue
		}
		if _, exists := reserved[candidate]; exists {
			continue
		}
		return candidate, nil
	}

	for candidate := minLinkUID; candidate <= maxLinkUID; candidate++ {
		if _, exists := usedUIDs[candidate]; exists {
			continue
		}
		if _, exists := reserved[candidate]; exists {
			continue
		}
		return candidate, nil
	}

	return 0, fmt.Errorf("VALIDATION:no free link UID is available in range [%d, %d]", minLinkUID, maxLinkUID)
}

func readLinkUIDValue(raw interface{}) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}

func linkUIDOwnerFromSpec(namespace string, link types.LinkSpec) LinkUIDOwner {
	uid := 0
	if link.UID != nil {
		uid = *link.UID
	}

	return LinkUIDOwner{
		Namespace:     namespace,
		Node:          link.Node,
		Interface:     link.LocalIntf,
		PeerNode:      link.PeerNode,
		PeerInterface: link.PeerIntf,
		UID:           uid,
	}
}

func describeLinkUIDOwner(owner LinkUIDOwner) string {
	return fmt.Sprintf("%s with UID %d of %s (%s)", owner.Interface, owner.UID, owner.Node, owner.Namespace)
}

func nullableString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
