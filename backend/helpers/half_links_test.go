package helpers

import (
	"reflect"
	"strings"
	"testing"

	"kubendt/kubeclient"
	"kubendt/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func declares(pod, localIntf, peerPod, peerIntf string) map[string]interface{} {
	return map[string]interface{}{"local_intf": localIntf, "peer_pod": peerPod, "peer_intf": peerIntf, "uid": int64(7)}
}

func topologyWith(name string, links ...map[string]interface{}) *unstructured.Unstructured {
	items := make([]interface{}, 0, len(links))
	for _, l := range links {
		items = append(items, l)
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "networkop.co.uk/v1beta1",
		"kind":       "Topology",
		"metadata":   map[string]interface{}{"name": name, "namespace": "lab"},
		"spec":       map[string]interface{}{"links": items},
	}}
}

func withTopologies(t *testing.T, objs ...runtime.Object) {
	t.Helper()
	old := kubeclient.DynamicClient
	t.Cleanup(func() { kubeclient.DynamicClient = old })
	kinds := map[schema.GroupVersionResource]string{TopologyGVR: "TopologyList"}
	kubeclient.DynamicClient = dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, objs...)
}

var halfNodes = []types.NodeSpec{{Name: "a", Replicas: 1}, {Name: "b", Replicas: 1}}

func halfLink() types.LinkSpec {
	return types.LinkSpec{Node: "a-0", LocalIntf: "eth1", PeerNode: "b-0", PeerIntf: "eth2"}
}

func TestValidateDeleteLinksExistWithAHalfLink(t *testing.T) {
	both := []runtime.Object{topologyWith("a-0", declares("a-0", "eth1", "b-0", "eth2")), topologyWith("b-0", declares("b-0", "eth2", "a-0", "eth1"))}
	withTopologies(t, both...)
	if err := ValidateDeleteLinksExist("lab", []types.LinkSpec{halfLink()}, halfNodes); err != nil {
		t.Errorf("a link at both ends: %v", err)
	}
	// What a delete that stopped halfway leaves: the end of b-0 is gone.
	withTopologies(t, topologyWith("a-0", declares("a-0", "eth1", "b-0", "eth2")), topologyWith("b-0"))
	if err := ValidateDeleteLinksExist("lab", []types.LinkSpec{halfLink()}, halfNodes); err != nil {
		t.Errorf("a link at one end must still be deletable: %v", err)
	}
	withTopologies(t, topologyWith("a-0"), topologyWith("b-0"))
	if err := ValidateDeleteLinksExist("lab", []types.LinkSpec{halfLink()}, halfNodes); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("a link at neither end: got %v", err)
	}
	// A pod with no Topology at all is no end.
	withTopologies(t, topologyWith("a-0", declares("a-0", "eth1", "b-0", "eth2")))
	if err := ValidateDeleteLinksExist("lab", []types.LinkSpec{halfLink()}, halfNodes); err != nil {
		t.Errorf("one end with its peer's Topology gone: %v", err)
	}
}

func TestHalfDeclaredEnds(t *testing.T) {
	withTopologies(t, topologyWith("a-0", declares("a-0", "eth1", "b-0", "eth2")), topologyWith("b-0"))
	got, err := halfDeclaredEnds("lab", []types.LinkSpec{halfLink()}, halfNodes)
	want := map[string][]string{"a-0": {"eth1|b-0|eth2"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v %v, want %v", got, err, want)
	}
	if got := staleInterfaces(got); !got["a-0"]["eth1"] {
		t.Errorf("staleInterfaces gave %v", got)
	}

	// Both ends there is a real conflict, not a half link.
	withTopologies(t, topologyWith("a-0", declares("a-0", "eth1", "b-0", "eth2")), topologyWith("b-0", declares("b-0", "eth2", "a-0", "eth1")))
	if got, _ := halfDeclaredEnds("lab", []types.LinkSpec{halfLink()}, halfNodes); len(got) != 0 {
		t.Errorf("a whole link is not half: %v", got)
	}
	// An uplink has one entry by design.
	withTopologies(t, topologyWith("a-0", declares("a-0", "eth1", "localhost", "ens18")))
	uplink := types.LinkSpec{Node: "a-0", LocalIntf: "eth1", PeerNode: "external", PeerIntf: "ens18"}
	if got, _ := halfDeclaredEnds("lab", []types.LinkSpec{uplink}, halfNodes); len(got) != 0 {
		t.Errorf("an uplink is not half: %v", got)
	}
	// Another link on the same interface is no half of this one.
	withTopologies(t, topologyWith("a-0", declares("a-0", "eth1", "c-0", "eth9")), topologyWith("b-0"))
	if got, _ := halfDeclaredEnds("lab", []types.LinkSpec{halfLink()}, halfNodes); len(got) != 0 {
		t.Errorf("a different link on eth1 is a conflict, not a half: %v", got)
	}
}

func TestRedoingAHalfLink(t *testing.T) {
	withTopologies(t, topologyWith("a-0", declares("a-0", "eth1", "b-0", "eth2")), topologyWith("b-0"))
	links := []types.LinkSpec{halfLink()}
	if err := validateInterfaceConflicts("lab", links, halfNodes, nil); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("without the stale end ignored this is the conflict the user hit, got %v", err)
	}
	stale, _ := halfDeclaredEnds("lab", links, halfNodes)
	if err := validateInterfaceConflicts("lab", links, halfNodes, staleInterfaces(stale)); err != nil {
		t.Fatalf("with it ignored: %v", err)
	}
	if err := dropLinkEnds("lab", stale); err != nil {
		t.Fatal(err)
	}
	got, _ := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace("lab").Get(t.Context(), "a-0", metav1.GetOptions{})
	if specLinks, _, _ := unstructured.NestedSlice(got.Object, "spec", "links"); len(specLinks) != 0 {
		t.Errorf("the stale end is still there: %v", specLinks)
	}
	// Now the interface is free for the add.
	if err := validateInterfaceConflicts("lab", links, halfNodes, nil); err != nil {
		t.Errorf("after the drop: %v", err)
	}
}
