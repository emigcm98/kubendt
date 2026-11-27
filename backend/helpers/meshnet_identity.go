package helpers

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"kubendt/kubeclient"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// The flavours of Meshnet a cluster may run.
const (
	MeshnetStock         = "stock"
	MeshnetFork          = "fork"
	MeshnetNotRecognised = "not recognised"
)

// MeshnetForkName is what the fork is called in what a user reads.
const MeshnetForkName = "KubeNDT Meshnet"

// How a detected Meshnet stands against what this KubeNDT works with.
const (
	MeshnetSupported   = "supported"
	MeshnetDevelopment = "development" // a build of the fork that is not a release
	MeshnetTooOld      = "too-old"
	MeshnetUntested    = "untested" // a newer major of the fork
	MeshnetUnsupported = "unsupported"
)

// The Topology CRD installed, by whose fields it has.
const (
	MeshnetCRDFork       = "fork"
	MeshnetCRDStock      = "stock"
	MeshnetCRDMissing    = "missing"
	MeshnetCRDUnreadable = "unreadable"
)

// The fork's releases this KubeNDT works with: from this one on, within its
// major. A later minor only adds optional fields, a major redoes the CRD.
var meshnetForkMin = [3]int{1, 0, 0}

const (
	meshnetStockRepo = "networkop/meshnet"
	meshnetForkRepo  = "emigcm98/meshnet"
	meshnetStockURL  = "https://github.com/networkop/meshnet-cni"
	meshnetForkURL   = "https://github.com/emigcm98/meshnet-cni"
)

var (
	meshnetReleaseTag = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?$`)
	meshnetCommitTag  = regexp.MustCompile(`^sha-([0-9a-f]{7,40})$`)
)

// MeshnetIdentity is what a Meshnet image says of itself.
type MeshnetIdentity struct {
	Flavour string
	Image   string
	// Version is the release of the fork (1.0.0), Commit the short commit of
	// one of its development builds. Empty otherwise.
	Version string
	Commit  string
	Support string
	// URL is where to read about it: built from fixed addresses and the
	// validated version or commit, never from the image string.
	URL string
}

// SplitImage cuts an image reference into its repository path without the
// registry host, its tag and its digest.
func SplitImage(image string) (path, tag, digest string) {
	name := strings.TrimSpace(image)
	if i := strings.Index(name, "@"); i >= 0 {
		name, digest = name[:i], name[i+1:]
	}
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name, tag = name[:i], name[i+1:]
	}
	if i := strings.Index(name, "/"); i >= 0 {
		host := name[:i]
		if strings.ContainsAny(host, ".:") || host == "localhost" {
			name = name[i+1:]
		}
	}
	return name, tag, digest
}

func imageIsOf(path, repo string) bool {
	return path == repo || strings.HasSuffix(path, "/"+repo)
}

// IdentifyMeshnet reads the flavour, the version and the support state from the
// image a Meshnet DaemonSet runs. A mirror keeps the flavour as long as it
// keeps the repository's path.
func IdentifyMeshnet(image string) MeshnetIdentity {
	path, tag, _ := SplitImage(image)
	id := MeshnetIdentity{Image: image}
	switch {
	case imageIsOf(path, meshnetStockRepo):
		id.Flavour, id.Support, id.URL = MeshnetStock, MeshnetSupported, meshnetStockURL
	case imageIsOf(path, meshnetForkRepo):
		id.Flavour, id.Support, id.URL = MeshnetFork, MeshnetDevelopment, meshnetForkURL
		if m := meshnetReleaseTag.FindStringSubmatch(tag); m != nil {
			id.Version = strings.TrimPrefix(tag, "v")
			id.URL = meshnetForkURL + "/tree/" + tag
			if m[4] == "" {
				id.Support = forkSupport(m[1], m[2], m[3])
			}
		} else if m := meshnetCommitTag.FindStringSubmatch(tag); m != nil {
			id.Commit = m[1]
			if len(id.Commit) > 7 {
				id.Commit = id.Commit[:7]
			}
			id.URL = meshnetForkURL + "/commit/" + m[1]
		}
	default:
		id.Flavour, id.Support = MeshnetNotRecognised, MeshnetUnsupported
	}
	return id
}

func forkSupport(major, minor, patch string) string {
	var v [3]int
	for i, s := range []string{major, minor, patch} {
		v[i], _ = strconv.Atoi(s)
	}
	if v[0] > meshnetForkMin[0] {
		return MeshnetUntested
	}
	for i := range v {
		if v[i] != meshnetForkMin[i] {
			if v[i] < meshnetForkMin[i] {
				return MeshnetTooOld
			}
			break
		}
	}
	return MeshnetSupported
}

// MeshnetSupportRange is the rule above in words, for a message.
func MeshnetSupportRange() string {
	lo := strconv.Itoa(meshnetForkMin[0]) + "." + strconv.Itoa(meshnetForkMin[1]) + "." + strconv.Itoa(meshnetForkMin[2])
	return "the stock Meshnet, or " + MeshnetForkName + " from " + lo + " and below " + strconv.Itoa(meshnetForkMin[0]+1) + ".0.0"
}

var crdGVR = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

// MeshnetCRD says whose Topology CRD the cluster has. A read that fails for
// anything but a missing CRD is "unreadable", which is not "stock": a
// kubeconfig or a ClusterRole without that right must not look like a
// mismatch.
func MeshnetCRD(ctx context.Context) string {
	if kubeclient.DynamicClient == nil {
		return MeshnetCRDUnreadable
	}
	crd, err := kubeclient.DynamicClient.Resource(crdGVR).Get(ctx, TopologyGVR.Resource+"."+TopologyGVR.Group, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return MeshnetCRDMissing
	}
	if err != nil {
		return MeshnetCRDUnreadable
	}
	return meshnetCRDSide(crd.Object)
}

// meshnetCRDSide looks at the served version KubeNDT writes: the fork's has
// shaping on a link and the links in the status, the stock one neither.
func meshnetCRDSide(crd map[string]interface{}) string {
	spec, _ := crd["spec"].(map[string]interface{})
	versions, _ := spec["versions"].([]interface{})
	for _, v := range versions {
		version, _ := v.(map[string]interface{})
		if version["name"] != TopologyGVR.Version {
			continue
		}
		link := dig(version, "schema", "openAPIV3Schema", "properties", "spec", "properties", "links", "items", "properties")
		status := dig(version, "schema", "openAPIV3Schema", "properties", "status", "properties")
		_, shaping := link["shaping"]
		_, links := status["links"]
		if shaping && links {
			return MeshnetCRDFork
		}
		return MeshnetCRDStock
	}
	return MeshnetCRDUnreadable
}

func dig(m map[string]interface{}, keys ...string) map[string]interface{} {
	for _, k := range keys {
		m, _ = m[k].(map[string]interface{})
		if m == nil {
			return map[string]interface{}{}
		}
	}
	return m
}
