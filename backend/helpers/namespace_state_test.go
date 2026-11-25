package helpers

import (
	"context"
	"testing"

	"kubendt/types"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
)

// A Topology object as Meshnet's CRD holds it, for the fake dynamic client.
func topologyCR(namespace, pod string, links ...map[string]interface{}) *unstructured.Unstructured {
	spec := make([]interface{}, 0, len(links))
	for _, l := range links {
		spec = append(spec, l)
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "networkop.co.uk/v1beta1",
		"kind":       "Topology",
		"metadata":   map[string]interface{}{"name": pod, "namespace": namespace},
		"spec":       map[string]interface{}{"links": spec},
	}}
}

func fakeTopologies(t *testing.T, objs ...runtime.Object) dynamic.Interface {
	t.Helper()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{TopologyGVR: "TopologyList"}, objs...)
	prevNS, prevAll := topologyObjects, allTopologies
	topologyObjects = func(ns string) dynamic.ResourceInterface { return dyn.Resource(TopologyGVR).Namespace(ns) }
	allTopologies = func() dynamic.ResourceInterface { return dyn.Resource(TopologyGVR) }
	t.Cleanup(func() { topologyObjects, allTopologies = prevNS, prevAll })
	return dyn
}

func TestNamespaceHasTopologyReadsTheCluster(t *testing.T) {
	cs := fake.NewClientset(&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "r1", Namespace: "with-sts"}})
	prev := statefulSets
	statefulSets = func(ns string) appsv1client.StatefulSetInterface { return cs.AppsV1().StatefulSets(ns) }
	t.Cleanup(func() { statefulSets = prev })
	fakeTopologies(t, topologyCR("with-crd", "h1-0", map[string]interface{}{"uid": int64(42), "local_intf": "eth1", "peer_pod": "h2-0", "peer_intf": "eth1"}))

	cases := map[string]bool{"with-sts": true, "with-crd": true, "empty": false}
	for ns, want := range cases {
		got, err := NamespaceHasTopology(ns)
		if err != nil || got != want {
			t.Errorf("%s: got %v, %v, want %v", ns, got, err, want)
		}
	}
}

func TestRegisteredLinkUIDsComeFromEveryNamespace(t *testing.T) {
	fakeTopologies(t,
		topologyCR("a", "r1-0", map[string]interface{}{"uid": int64(1000001), "local_intf": "eth1", "peer_pod": "r2-0", "peer_intf": "eth1"}),
		topologyCR("a", "r2-0", map[string]interface{}{"uid": int64(1000001), "local_intf": "eth1", "peer_pod": "r1-0", "peer_intf": "eth1"}),
		topologyCR("b", "h1-0", map[string]interface{}{"uid": int64(1000002), "local_intf": "eth1", "peer_pod": "external", "peer_intf": "eth0"}),
	)
	used, err := GetRegisteredLinkUIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(used) != 2 || used[1000001].Namespace != "a" || used[1000002].Namespace != "b" {
		t.Fatalf("every uid on the cluster, once, with its namespace: %+v", used)
	}
	uid := 1000002
	_, err = PrepareUniqueLinkUIDs("c", []types.LinkSpec{{Node: "x", LocalIntf: "eth1", PeerNode: "y", PeerIntf: "eth1", UID: &uid}})
	if err == nil {
		t.Fatal("a uid another namespace holds must be refused")
	}
}

func TestLabSizesCountNodesAndLinksOnce(t *testing.T) {
	two, three := int32(2), int32(3)
	cs := fake.NewClientset(
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "r1", Namespace: "a"}, Spec: appsv1.StatefulSetSpec{Replicas: &two}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "a"}, Spec: appsv1.StatefulSetSpec{Replicas: &three}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "b"}},
	)
	prev := allStatefulSets
	allStatefulSets = func() appsv1client.StatefulSetInterface { return cs.AppsV1().StatefulSets("") }
	t.Cleanup(func() { allStatefulSets = prev })
	fakeTopologies(t,
		// one link listed by both ends, one to the outside
		topologyCR("a", "r1-0", map[string]interface{}{"local_intf": "eth1", "peer_pod": "h-0", "peer_intf": "eth1"},
			map[string]interface{}{"local_intf": "eth2", "peer_pod": "localhost", "peer_intf": "ext0"}),
		topologyCR("a", "h-0", map[string]interface{}{"local_intf": "eth1", "peer_pod": "r1-0", "peer_intf": "eth1"}),
	)

	sizes, err := LabSizes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a := sizes["a"]; a == nil || !a.HasTopology || a.Pods != 5 || a.Links != 2 {
		t.Errorf("a: %+v, want a topology of 5 pods and 2 links", a)
	}
	if b := sizes["b"]; b == nil || !b.HasTopology || b.Pods != 1 || b.Links != 0 {
		t.Errorf("b: %+v, want 1 pod and no links", b)
	}
	if sizes["empty"] != nil {
		t.Errorf("a namespace with nothing has no entry: %+v", sizes["empty"])
	}
}
