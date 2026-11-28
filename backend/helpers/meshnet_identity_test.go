package helpers

import (
	"context"
	"os"
	"reflect"
	"testing"

	"kubendt/kubeclient"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

func TestSplitImage(t *testing.T) {
	for _, tc := range []struct{ image, path, tag, digest string }{
		{"networkop/meshnet:latest", "networkop/meshnet", "latest", ""},
		{"docker.io/networkop/meshnet@sha256:3826d4", "networkop/meshnet", "", "sha256:3826d4"},
		{"ghcr.io/emigcm98/meshnet:sha-30ab7f0", "emigcm98/meshnet", "sha-30ab7f0", ""},
		{"ghcr.io/emigcm98/meshnet:v1.0.0@sha256:aee1ba", "emigcm98/meshnet", "v1.0.0", "sha256:aee1ba"},
		{"registry.lab:5000/mirror/emigcm98/meshnet:v1.2.3", "mirror/emigcm98/meshnet", "v1.2.3", ""},
		{"localhost/meshnet:dev", "meshnet", "dev", ""},
		{"registry.lab:5000/meshnet", "meshnet", "", ""},
	} {
		path, tag, digest := SplitImage(tc.image)
		if path != tc.path || tag != tc.tag || digest != tc.digest {
			t.Errorf("%s: got %q %q %q, want %q %q %q", tc.image, path, tag, digest, tc.path, tc.tag, tc.digest)
		}
	}
}

func TestIdentifyMeshnet(t *testing.T) {
	fork := "https://github.com/emigcm98/meshnet-cni"
	for _, tc := range []struct {
		image string
		want  MeshnetIdentity
	}{
		{"networkop/meshnet:latest", MeshnetIdentity{Flavour: MeshnetStock, Support: MeshnetSupported, URL: "https://github.com/networkop/meshnet-cni"}},
		// The stock one pinned by digest, as a rollback leaves it.
		{"docker.io/networkop/meshnet@sha256:3826d462f75c", MeshnetIdentity{Flavour: MeshnetStock, Support: MeshnetSupported, URL: "https://github.com/networkop/meshnet-cni"}},
		{"ghcr.io/emigcm98/meshnet:v1.0.0", MeshnetIdentity{Flavour: MeshnetFork, Version: "1.0.0", Support: MeshnetSupported, URL: fork + "/tree/v1.0.0"}},
		{"ghcr.io/emigcm98/meshnet:v1.4.2@sha256:aee1ba", MeshnetIdentity{Flavour: MeshnetFork, Version: "1.4.2", Support: MeshnetSupported, URL: fork + "/tree/v1.4.2"}},
		{"ghcr.io/emigcm98/meshnet:v0.9.0", MeshnetIdentity{Flavour: MeshnetFork, Version: "0.9.0", Support: MeshnetTooOld, URL: fork + "/tree/v0.9.0"}},
		{"ghcr.io/emigcm98/meshnet:v2.0.0", MeshnetIdentity{Flavour: MeshnetFork, Version: "2.0.0", Support: MeshnetUntested, URL: fork + "/tree/v2.0.0"}},
		// A pre-release is a build on the way to a release, no claim on it.
		{"ghcr.io/emigcm98/meshnet:v1.0.0-rc.1", MeshnetIdentity{Flavour: MeshnetFork, Version: "1.0.0-rc.1", Support: MeshnetDevelopment, URL: fork + "/tree/v1.0.0-rc.1"}},
		{"ghcr.io/emigcm98/meshnet:sha-30ab7f0", MeshnetIdentity{Flavour: MeshnetFork, Commit: "30ab7f0", Support: MeshnetDevelopment, URL: fork + "/commit/30ab7f0"}},
		// The base manifest's default and a hand-loaded tag: the fork, nothing more known.
		{"ghcr.io/emigcm98/meshnet:latest", MeshnetIdentity{Flavour: MeshnetFork, Support: MeshnetDevelopment, URL: fork}},
		{"ghcr.io/emigcm98/meshnet:hostends-1", MeshnetIdentity{Flavour: MeshnetFork, Support: MeshnetDevelopment, URL: fork}},
		{"registry.lab:5000/mirror/emigcm98/meshnet:v1.0.0", MeshnetIdentity{Flavour: MeshnetFork, Version: "1.0.0", Support: MeshnetSupported, URL: fork + "/tree/v1.0.0"}},
		// Nothing of the image string reaches the link.
		{"ghcr.io/emigcm98/meshnet:sha-zzzzzzz", MeshnetIdentity{Flavour: MeshnetFork, Support: MeshnetDevelopment, URL: fork}},
		{"example.org/kne/meshnet:ga", MeshnetIdentity{Flavour: MeshnetNotRecognised, Support: MeshnetUnsupported}},
		{"example.org/notemigcm98/meshnet:v1.0.0", MeshnetIdentity{Flavour: MeshnetNotRecognised, Support: MeshnetUnsupported}},
	} {
		tc.want.Image = tc.image
		if got := IdentifyMeshnet(tc.image); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.image, got, tc.want)
		}
	}
}

func crdFixture(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile("testdata/meshnet/" + name)
	if err != nil {
		t.Fatal(err)
	}
	obj := map[string]interface{}{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: obj}
}

// The CRDs as their repositories ship them: the stock one at the commit the
// fork started from, the fork's at the commit the name says.
func TestMeshnetCRDSide(t *testing.T) {
	stock, fork := crdFixture(t, "crd_stock_d7c306c.yaml"), crdFixture(t, "crd_fork_ed50f63.yaml")
	if got := meshnetCRDSide(stock.Object); got != MeshnetCRDStock {
		t.Errorf("stock CRD read as %q", got)
	}
	if got := meshnetCRDSide(fork.Object); got != MeshnetCRDFork {
		t.Errorf("fork CRD read as %q", got)
	}
	if got := meshnetCRDSide(map[string]interface{}{"spec": map[string]interface{}{}}); got != MeshnetCRDUnreadable {
		t.Errorf("a CRD without the version read as %q", got)
	}
	// What lets a cluster swap one for the other with its labs up: the fork
	// keeps every field of the stock link.
	path := []string{"schema", "openAPIV3Schema", "properties", "spec", "properties", "links", "items", "properties"}
	version := func(u *unstructured.Unstructured) map[string]interface{} {
		versions, _, _ := unstructured.NestedSlice(u.Object, "spec", "versions")
		return versions[0].(map[string]interface{})
	}
	stockLink, forkLink := dig(version(stock), path...), dig(version(fork), path...)
	if len(stockLink) == 0 {
		t.Fatal("no link fields in the stock fixture")
	}
	for field, def := range stockLink {
		theirs, ok := forkLink[field].(map[string]interface{})
		if !ok || theirs["type"] != def.(map[string]interface{})["type"] {
			t.Errorf("the fork's link lacks the stock field %q or changes its type", field)
		}
	}
}

func TestMeshnetCRDRead(t *testing.T) {
	old := kubeclient.DynamicClient
	t.Cleanup(func() { kubeclient.DynamicClient = old })
	kinds := map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"}

	fork := crdFixture(t, "crd_fork_ed50f63.yaml")
	kubeclient.DynamicClient = dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, fork)
	if got := MeshnetCRD(context.Background()); got != MeshnetCRDFork {
		t.Errorf("the fork's CRD in the cluster read as %q", got)
	}

	empty := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds)
	kubeclient.DynamicClient = empty
	if got := MeshnetCRD(context.Background()); got != MeshnetCRDMissing {
		t.Errorf("no CRD read as %q", got)
	}

	// A ClusterRole from before this read, or a kubeconfig without it.
	denied := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, fork)
	denied.PrependReactor("get", "customresourcedefinitions", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(crdGVR.GroupResource(), "topologies.networkop.co.uk", nil)
	})
	kubeclient.DynamicClient = denied
	if got := MeshnetCRD(context.Background()); got != MeshnetCRDUnreadable {
		t.Errorf("a forbidden read gave %q, it must not look like stock", got)
	}
}

func TestMeshnetPath(t *testing.T) {
	for name, tc := range map[string]struct {
		flavour, support, crd string
		images                int
		ready                 bool
		want                  string
	}{
		"the fork on its CRD":            {MeshnetFork, MeshnetSupported, MeshnetCRDFork, 1, true, MeshnetPathFork},
		"a development build":            {MeshnetFork, MeshnetDevelopment, MeshnetCRDFork, 1, true, MeshnetPathFork},
		"the stock one":                  {MeshnetStock, MeshnetSupported, MeshnetCRDStock, 1, true, MeshnetPathStock},
		"the fork on the stock CRD":      {MeshnetFork, MeshnetSupported, MeshnetCRDStock, 1, true, MeshnetPathStock},
		"stock on the fork's CRD":        {MeshnetStock, MeshnetSupported, MeshnetCRDFork, 1, true, MeshnetPathStock},
		"an upgrade halfway":             {MeshnetFork, MeshnetSupported, MeshnetCRDFork, 2, true, MeshnetPathStock},
		"a node without its pod":         {MeshnetFork, MeshnetSupported, MeshnetCRDFork, 1, false, MeshnetPathStock},
		"no pod seen at all":             {MeshnetFork, MeshnetSupported, MeshnetCRDFork, 0, true, MeshnetPathStock},
		"a CRD that could not be read":   {MeshnetFork, MeshnetSupported, MeshnetCRDUnreadable, 1, true, MeshnetPathStock},
		"a version older than supported": {MeshnetFork, MeshnetTooOld, MeshnetCRDFork, 1, true, MeshnetPathStock},
		"a newer major":                  {MeshnetFork, MeshnetUntested, MeshnetCRDFork, 1, true, MeshnetPathStock},
		"an unknown daemon":              {MeshnetNotRecognised, MeshnetUnsupported, MeshnetCRDFork, 1, true, MeshnetPathStock},
	} {
		if got := MeshnetPath(tc.flavour, tc.support, tc.crd, tc.images, tc.ready); got != tc.want {
			t.Errorf("%s: path %q, want %q", name, got, tc.want)
		}
	}
}
