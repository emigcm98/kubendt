package helpers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"kubendt/auth"
	"kubendt/kubeclient"
	"kubendt/types"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// What an account may fill the cluster with: namespaces it owns, pods across
// them and pods in any one of them, each zero for no limit. An account's own
// figures win, the environment's defaults apply where the account says
// nothing, and admins are never counted. A lab counts against its owner
// whoever deploys into it. The pods are what the topologies declare, the
// replicas of every node, so the check is made before anything is created.
const (
	EnvQuotaNamespaces       = "KUBENDT_QUOTA_NAMESPACES"
	EnvQuotaPods             = "KUBENDT_QUOTA_PODS"
	EnvQuotaPodsPerNamespace = "KUBENDT_QUOTA_PODS_PER_NAMESPACE"

	EnvPodCPURequest    = "KUBENDT_POD_CPU_REQUEST"
	EnvPodMemoryRequest = "KUBENDT_POD_MEMORY_REQUEST"
	EnvPodCPULimit      = "KUBENDT_POD_CPU_LIMIT"
	EnvPodMemoryLimit   = "KUBENDT_POD_MEMORY_LIMIT"

	LimitRangeName = "kubendt-internal-limits"
)

// ErrQuotaExceeded is what a check answers when the account is at its limit.
var ErrQuotaExceeded = errors.New("quota exceeded")

// Quota is the limits an account is under, zero meaning none.
type Quota struct {
	Namespaces       int `json:"namespaces"`
	Pods             int `json:"pods"`
	PodsPerNamespace int `json:"pods_per_namespace"`
}

// QuotaUsage is what an account has against each limit.
type QuotaUsage struct {
	Namespaces int `json:"namespaces"`
	Pods       int `json:"pods"`
}

func envInt(key string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if n < 0 {
		return 0
	}
	return n
}

func pick(own, def int) int {
	if own > 0 {
		return own
	}
	return def
}

// QuotaFor is the quota an account is under: its own figures, the
// environment's where it has none, the environment's alone for an owner
// that is no account any more. Admins have none at all.
func QuotaFor(name string) Quota {
	u, ok := auth.GetUser(name)
	if ok && (u.Role == auth.RoleAdmin || u.Role == auth.RoleOwner) {
		return Quota{}
	}
	if !ok {
		u = &auth.User{}
	}
	return Quota{
		Namespaces:       pick(u.QuotaNamespaces, envInt(EnvQuotaNamespaces)),
		Pods:             pick(u.QuotaPods, envInt(EnvQuotaPods)),
		PodsPerNamespace: pick(u.QuotaPodsPerNS, envInt(EnvQuotaPodsPerNamespace)),
	}
}

func podsOf(nodes []types.NodeSpec) int {
	n := 0
	for _, node := range nodes {
		n += replicasOf(node.Replicas)
	}
	return n
}

func replicasOf(r int) int {
	if r <= 0 {
		return 1
	}
	return r
}

// namespacesOwnedBy lists the namespaces an account owns.
func namespacesOwnedBy(name string) ([]string, error) {
	list, err := stateNamespaces().List(context.TODO(), metav1.ListOptions{LabelSelector: "kubendt/enabled=true"})
	if err != nil {
		return nil, fmt.Errorf("could not list the namespaces: %w", err)
	}
	var out []string
	for i := range list.Items {
		if list.Items[i].DeletionTimestamp == nil && list.Items[i].Annotations[OwnerAnnotation] == name {
			out = append(out, list.Items[i].Name)
		}
	}
	return out, nil
}

// UsageOf is what an account owns today: namespaces, and the pods their
// topologies declare.
func UsageOf(name string) (QuotaUsage, error) {
	owned, err := namespacesOwnedBy(name)
	if err != nil {
		return QuotaUsage{}, err
	}
	usage := QuotaUsage{Namespaces: len(owned)}
	for _, ns := range owned {
		nodes, err := GetExistingNodes(ns)
		if err != nil {
			return usage, fmt.Errorf("could not count the pods of %s: %w", ns, err)
		}
		usage.Pods += podsOf(nodes)
	}
	return usage, nil
}

// CheckNamespaceQuota says whether an account may own one more namespace.
func CheckNamespaceQuota(name string) error {
	q := QuotaFor(name)
	if q.Namespaces == 0 {
		return nil
	}
	owned, err := namespacesOwnedBy(name)
	if err != nil {
		return err
	}
	if len(owned) >= q.Namespaces {
		return fmt.Errorf("%w: %s owns %d of %d namespaces", ErrQuotaExceeded, name, len(owned), q.Namespaces)
	}
	return nil
}

// CheckPodQuota says whether a namespace may hold planned pods after the
// operation: against its owner's limit per namespace, and against the
// owner's total with the pods of their other namespaces as they are.
func CheckPodQuota(namespace string, planned int) error {
	access, err := ReadNamespaceAccess(namespace)
	if err != nil || access.Owner == "" {
		return nil
	}
	q := QuotaFor(access.Owner)
	if q.PodsPerNamespace > 0 && planned > q.PodsPerNamespace {
		return fmt.Errorf("%w: the topology would hold %d pods and %s's namespaces hold %d at most", ErrQuotaExceeded, planned, access.Owner, q.PodsPerNamespace)
	}
	if q.Pods == 0 {
		return nil
	}
	owned, err := namespacesOwnedBy(access.Owner)
	if err != nil {
		return err
	}
	elsewhere := 0
	for _, ns := range owned {
		if ns == namespace {
			continue
		}
		nodes, err := GetExistingNodes(ns)
		if err != nil {
			return fmt.Errorf("could not count the pods of %s: %w", ns, err)
		}
		elsewhere += podsOf(nodes)
	}
	if elsewhere+planned > q.Pods {
		return fmt.Errorf("%w: %s would have %d pods across their namespaces, %d at most", ErrQuotaExceeded, access.Owner, elsewhere+planned, q.Pods)
	}
	return nil
}

// PodsAfterModify is what a namespace would hold after a modify: what is
// there, less the nodes deleted, scaled as asked, plus the nodes added.
func PodsAfterModify(existing []types.NodeSpec, deleted map[string]struct{}, plans []ScalePlan, added []types.NodeSpec) int {
	n := 0
	scaled := map[string]int{}
	for _, p := range plans {
		scaled[p.Node.Name] = p.NewReplicas
	}
	for _, node := range existing {
		if _, gone := deleted[node.Name]; gone {
			continue
		}
		if to, ok := scaled[node.Name]; ok {
			n += replicasOf(to)
			continue
		}
		n += replicasOf(node.Replicas)
	}
	return n + podsOf(added)
}

// stateLimitRanges is the client the LimitRange goes through, a variable
// so the tests can hand it a fake clientset.
var stateLimitRanges = func(ns string) corev1client.LimitRangeInterface {
	return kubeclient.Clientset.CoreV1().LimitRanges(ns)
}

// LimitRangeFromEnv is the LimitRange the environment asks for every lab
// namespace, nil when it names nothing: default requests and limits for
// every container without its own, so the scheduler counts the pods and
// one lab cannot starve a worker.
func LimitRangeFromEnv() (*corev1.LimitRange, error) {
	item := corev1.LimitRangeItem{Type: corev1.LimitTypeContainer, DefaultRequest: corev1.ResourceList{}, Default: corev1.ResourceList{}}
	set := false
	for _, f := range []struct {
		env  string
		into corev1.ResourceList
		res  corev1.ResourceName
	}{
		{EnvPodCPURequest, item.DefaultRequest, corev1.ResourceCPU},
		{EnvPodMemoryRequest, item.DefaultRequest, corev1.ResourceMemory},
		{EnvPodCPULimit, item.Default, corev1.ResourceCPU},
		{EnvPodMemoryLimit, item.Default, corev1.ResourceMemory},
	} {
		v := strings.TrimSpace(os.Getenv(f.env))
		if v == "" {
			continue
		}
		q, err := resource.ParseQuantity(v)
		if err != nil {
			return nil, fmt.Errorf("%s=%q is not a quantity: %w", f.env, v, err)
		}
		f.into[f.res] = q
		set = true
	}
	if !set {
		return nil, nil
	}
	return &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: LimitRangeName, Labels: internalLabels("limits")},
		Spec:       corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{item}},
	}, nil
}

// EnsureLimitRange writes the environment's LimitRange into a namespace,
// nothing when the environment names none.
func EnsureLimitRange(namespace string) error {
	lr, err := LimitRangeFromEnv()
	if err != nil || lr == nil {
		return err
	}
	lr.Namespace = namespace
	_, err = stateLimitRanges(namespace).Create(context.TODO(), lr, metav1.CreateOptions{})
	if err == nil || apierrors.IsAlreadyExists(err) {
		return nil
	}
	return fmt.Errorf("could not write the limits of namespace %s: %w", namespace, err)
}
