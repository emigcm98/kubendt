package helpers

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"kubendt/kubeclient"
	"kubendt/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
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

// allTopologies is the cluster-wide Topology client, a variable so the
// tests can hand it a fake.
var allTopologies = func() dynamic.ResourceInterface {
	return kubeclient.DynamicClient.Resource(TopologyGVR)
}

// GetRegisteredLinkUIDs is every link uid in use on the cluster, read from
// the Topology objects of every namespace: a uid is the VXLAN id of its link
// and must be unique across namespaces, and the objects are the truth.
func GetRegisteredLinkUIDs() (map[int]LinkUIDOwner, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	list, err := allTopologies().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("error listing Topology objects for the link uids: %w", err)
	}
	used := make(map[int]LinkUIDOwner)
	for _, owner := range ownersFromTopologies(list.Items) {
		if _, exists := used[owner.UID]; !exists {
			used[owner.UID] = owner
		}
	}
	return used, nil
}

// ownersFromTopologies is one owner per uid the objects declare, the first
// end seen keeping it.
func ownersFromTopologies(items []unstructured.Unstructured) []LinkUIDOwner {
	ownersByUID := make(map[int]LinkUIDOwner)
	order := []int{}
	for _, item := range items {
		specLinks, found, err := unstructured.NestedSlice(item.Object, "spec", "links")
		if err != nil || !found {
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
				Namespace:     item.GetNamespace(),
				Node:          item.GetName(),
				Interface:     fmt.Sprintf("%v", linkMap["local_intf"]),
				PeerNode:      fromCRDPeerPod(fmt.Sprintf("%v", linkMap["peer_pod"])),
				PeerInterface: fmt.Sprintf("%v", linkMap["peer_intf"]),
				UID:           uid,
			}
			order = append(order, uid)
		}
	}
	owners := make([]LinkUIDOwner, 0, len(order))
	for _, uid := range order {
		owners = append(owners, ownersByUID[uid])
	}
	return owners
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
