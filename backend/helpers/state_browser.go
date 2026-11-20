package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"kubendt/kubeclient"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	coordinationclient "k8s.io/client-go/kubernetes/typed/coordination/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/yaml"
)

// The state browser: every Kubernetes object KubeNDT creates or uses, per
// namespace and across the cluster, each told for what it is to the
// platform. Read-only, it is `kubectl get` from the UI and documents by
// itself what KubeNDT puts in a cluster.

// StateObject is one row of the browser.
type StateObject struct {
	Kind            string            `json:"kind"`
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace,omitempty"`
	Role            string            `json:"role"`
	Node            string            `json:"node,omitempty"`
	Detail          string            `json:"detail,omitempty"`
	Size            int               `json:"size"`
	CreatedAt       string            `json:"created_at,omitempty"`
	ResourceVersion string            `json:"resource_version"`
	Labels          map[string]string `json:"labels,omitempty"`
}

// NamespaceState is a namespace with its objects, a lab or the system one.
type NamespaceState struct {
	Name    string        `json:"name"`
	Role    string        `json:"role"`
	Objects []StateObject `json:"objects"`
}

// The clients the browser reads through, variables so the tests can hand
// them a fake clientset.
var (
	stateNamespaces = func() corev1client.NamespaceInterface {
		return kubeclient.Clientset.CoreV1().Namespaces()
	}
	stateStatefulSets = func(ns string) appsv1client.StatefulSetInterface {
		return kubeclient.Clientset.AppsV1().StatefulSets(ns)
	}
	statePods = func(ns string) corev1client.PodInterface {
		return kubeclient.Clientset.CoreV1().Pods(ns)
	}
	stateConfigMaps = func(ns string) corev1client.ConfigMapInterface {
		return kubeclient.Clientset.CoreV1().ConfigMaps(ns)
	}
	stateSecrets = func(ns string) corev1client.SecretInterface {
		return kubeclient.Clientset.CoreV1().Secrets(ns)
	}
	stateLeases = func(ns string) coordinationclient.LeaseInterface {
		return kubeclient.Clientset.CoordinationV1().Leases(ns)
	}
	stateTopologies = func(ns string) dynamic.ResourceInterface {
		return kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(ns)
	}
)

// roleRank orders the rows the way a namespace is read: what the lab is,
// then what the platform keeps about it.
var roleRank = map[string]int{
	"namespace": 0, "node": 1, "pod": 2, "topology": 3, "env": 4,
	"file": 5, "folder": 6, "mount": 7, "history": 8, "layout": 9, "layout-attic": 10,
	"lock": 11, "iface-counts": 12, "limits": 13, "user": 14, "token": 15, "session-key": 16,
}

func rankOf(role string) int {
	if r, ok := roleRank[role]; ok {
		return r
	}
	return len(roleRank)
}

func jsonSize(obj any) int {
	raw, err := json.Marshal(obj)
	if err != nil {
		return 0
	}
	return len(raw)
}

func stamp(t metav1.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// nodeOfPod is the StatefulSet a pod belongs to, its name without the
// ordinal.
func nodeOfPod(pod string) string {
	if i := strings.LastIndex(pod, "-"); i > 0 {
		return pod[:i]
	}
	return pod
}

func plural(n int, one string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %ss", n, one)
}

// ListStateNamespaces is every lab namespace and the system one, each with
// its objects, read side by side.
func ListStateNamespaces() ([]NamespaceState, error) {
	list, err := stateNamespaces().List(context.TODO(), metav1.ListOptions{LabelSelector: "kubendt/enabled=true"})
	if err != nil {
		return nil, fmt.Errorf("could not list the namespaces: %w", err)
	}
	var out []NamespaceState
	for _, ns := range list.Items {
		out = append(out, NamespaceState{Name: ns.Name, Role: "lab"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if _, err := stateNamespaces().Get(context.TODO(), SystemNamespace(), metav1.GetOptions{}); err == nil {
		out = append(out, NamespaceState{Name: SystemNamespace(), Role: "system"})
	}

	var wg sync.WaitGroup
	errs := make([]error, len(out))
	sem := make(chan struct{}, 4)
	for i := range out {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i].Objects, errs[i] = ListStateObjects(out[i].Name)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("%s: %w", out[i].Name, err)
		}
	}
	if out == nil {
		out = []NamespaceState{}
	}
	return out, nil
}

// ListStateObjects is what a namespace holds, the Namespace object first.
func ListStateObjects(namespace string) ([]StateObject, error) {
	ctx := context.TODO()
	var rows []StateObject

	ns, err := stateNamespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not read namespace %s: %w", namespace, err)
	}
	rows = append(rows, namespaceRow(ns))

	stsList, err := stateStatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not list the StatefulSets of %s: %w", namespace, err)
	}
	nodes := map[string]bool{}
	for i := range stsList.Items {
		sts := &stsList.Items[i]
		nodes[sts.Name] = true
		replicas := int32(1)
		if sts.Spec.Replicas != nil {
			replicas = *sts.Spec.Replicas
		}
		labels := sts.Spec.Template.Labels
		rows = append(rows, StateObject{
			Kind: "StatefulSet", Name: sts.Name, Namespace: namespace, Role: "node", Node: sts.Name,
			Detail:    strings.TrimSpace(fmt.Sprintf("%s %s, %s", labels["kubendt/type"], labels["kubendt/driver"], plural(int(replicas), "replica"))),
			Size:      jsonSize(sts),
			CreatedAt: stamp(sts.CreationTimestamp), ResourceVersion: sts.ResourceVersion, Labels: labels,
		})
	}

	pods, err := statePods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not list the pods of %s: %w", namespace, err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		node := pod.Labels["app"]
		if node == "" {
			node = nodeOfPod(pod.Name)
		}
		detail := string(pod.Status.Phase)
		if pod.Spec.NodeName != "" {
			detail += " on " + pod.Spec.NodeName
		}
		rows = append(rows, StateObject{
			Kind: "Pod", Name: pod.Name, Namespace: namespace, Role: "pod", Node: node, Detail: detail,
			Size: jsonSize(pod), CreatedAt: stamp(pod.CreationTimestamp), ResourceVersion: pod.ResourceVersion, Labels: pod.Labels,
		})
	}

	topos, err := stateTopologies(namespace).List(ctx, metav1.ListOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("could not list the Topologies of %s: %w", namespace, err)
	}
	if topos != nil {
		for i := range topos.Items {
			t := &topos.Items[i]
			links, _, _ := unstructured.NestedSlice(t.Object, "spec", "links")
			rows = append(rows, StateObject{
				Kind: "Topology", Name: t.GetName(), Namespace: namespace, Role: "topology", Node: nodeOfPod(t.GetName()),
				Detail: plural(len(links), "link"), Size: jsonSize(t.Object),
				CreatedAt: stamp(t.GetCreationTimestamp()), ResourceVersion: t.GetResourceVersion(), Labels: t.GetLabels(),
			})
		}
	}

	cms, err := stateConfigMaps(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not list the ConfigMaps of %s: %w", namespace, err)
	}
	for i := range cms.Items {
		if row, ok := configMapRow(&cms.Items[i], nodes); ok {
			rows = append(rows, row)
		}
	}

	secrets, err := stateSecrets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not list the Secrets of %s: %w", namespace, err)
	}
	for i := range secrets.Items {
		if row, ok := secretRow(&secrets.Items[i]); ok {
			rows = append(rows, row)
		}
	}

	leases, err := stateLeases(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not list the Leases of %s: %w", namespace, err)
	}
	for i := range leases.Items {
		rows = append(rows, leaseRow(&leases.Items[i]))
	}

	limits, err := stateLimitRanges(namespace).List(ctx, metav1.ListOptions{})
	if err != nil && !apierrors.IsForbidden(err) {
		return nil, fmt.Errorf("could not list the LimitRanges of %s: %w", namespace, err)
	}
	if limits != nil {
		for i := range limits.Items {
			lr := &limits.Items[i]
			rows = append(rows, StateObject{
				Kind: "LimitRange", Name: lr.Name, Namespace: namespace, Role: "limits", Detail: limitRangeDetail(lr),
				Size: jsonSize(lr), CreatedAt: stamp(lr.CreationTimestamp), ResourceVersion: lr.ResourceVersion, Labels: lr.Labels,
			})
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if ri, rj := rankOf(rows[i].Role), rankOf(rows[j].Role); ri != rj {
			return ri < rj
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

func limitRangeDetail(lr *corev1.LimitRange) string {
	var parts []string
	for _, item := range lr.Spec.Limits {
		for _, res := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			req, hasReq := item.DefaultRequest[res]
			lim, hasLim := item.Default[res]
			switch {
			case hasReq && hasLim:
				parts = append(parts, fmt.Sprintf("%s %s to %s", res, req.String(), lim.String()))
			case hasReq:
				parts = append(parts, fmt.Sprintf("%s %s", res, req.String()))
			case hasLim:
				parts = append(parts, fmt.Sprintf("%s up to %s", res, lim.String()))
			}
		}
	}
	if len(parts) == 0 {
		return "defaults for every container"
	}
	return "every container " + strings.Join(parts, ", ")
}

func namespaceRow(ns *corev1.Namespace) StateObject {
	detail := "lab"
	if ns.Labels["kubendt/system"] == "true" {
		detail = "the platform's own"
	}
	if access := ParseNamespaceAccess(ns); access.Owner != "" {
		detail += " of " + access.Owner
		if n := len(access.Guests); n > 0 {
			detail += ", " + plural(n, "guest")
		}
	} else if ns.Labels["kubendt/system"] != "true" {
		detail += ", no owner"
	}
	if ns.DeletionTimestamp != nil {
		detail += ", terminating"
	}
	return StateObject{
		Kind: "Namespace", Name: ns.Name, Role: "namespace", Detail: detail, Size: jsonSize(ns),
		CreatedAt: stamp(ns.CreationTimestamp), ResourceVersion: ns.ResourceVersion, Labels: ns.Labels,
	}
}

func dataBytes(data map[string]string) int {
	n := 0
	for _, v := range data {
		n += len(v)
	}
	return n
}

func configMapRow(cm *corev1.ConfigMap, nodes map[string]bool) (StateObject, bool) {
	if cm.Name == "kube-root-ca.crt" {
		return StateObject{}, false
	}
	row := StateObject{
		Kind: "ConfigMap", Name: cm.Name, Namespace: cm.Namespace, Size: jsonSize(cm),
		CreatedAt: stamp(cm.CreationTimestamp), ResourceVersion: cm.ResourceVersion, Labels: cm.Labels,
	}
	path := cm.Annotations[MountFilePathAnnotation]
	switch kind := cm.Labels["kubendt/kind"]; {
	case kind == "file":
		row.Role, row.Detail = "file", fmt.Sprintf("%s, %d bytes", path, dataBytes(cm.Data))
	case kind == "folder":
		row.Role, row.Detail = "folder", path+", empty"
	case kind == "mount":
		row.Role, row.Detail = "mount", path+", deleted, kept for the pods that mount it"
	case kind == "history":
		pod := cm.Labels["kubendt/pod"]
		row.Role, row.Node = "history", nodeOfPod(pod)
		if ops, err := decodeHistory(cm.Namespace, pod, cm); err == nil {
			row.Detail = fmt.Sprintf("%s, %s", pod, plural(len(ops), "operation"))
		} else {
			row.Detail = pod + ", unreadable"
		}
	case kind == "layout":
		row.Role, row.Detail = "layout", layoutDetail(cm.Data[layoutKey])
	case kind == "layout-attic":
		row.Role, row.Detail = "layout-attic", "layout of the deleted namespace "+cm.Labels["kubendt/namespace"]
	case kind != "":
		row.Role = kind
	case cm.Name == IfaceCountsConfigMapName:
		row.Role, row.Detail = "iface-counts", plural(len(cm.Data), "node")
	case strings.HasSuffix(cm.Name, "-env") && nodes[strings.TrimSuffix(cm.Name, "-env")]:
		row.Role, row.Node = "env", strings.TrimSuffix(cm.Name, "-env")
		row.Detail = plural(len(cm.Data), "variable")
	default:
		row.Role = "other"
	}
	return row, true
}

func layoutDetail(raw string) string {
	var l struct {
		Positions   map[string]any `json:"positions"`
		Annotations []any          `json:"annotations"`
	}
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return "unreadable"
	}
	return fmt.Sprintf("%s, %s", plural(len(l.Positions), "position"), plural(len(l.Annotations), "annotation"))
}

func secretRow(s *corev1.Secret) (StateObject, bool) {
	if s.Type == corev1.SecretTypeServiceAccountToken {
		return StateObject{}, false
	}
	row := StateObject{
		Kind: "Secret", Name: s.Name, Namespace: s.Namespace, Size: jsonSize(s),
		CreatedAt: stamp(s.CreationTimestamp), ResourceVersion: s.ResourceVersion, Labels: s.Labels,
	}
	bytes := 0
	for _, v := range s.Data {
		bytes += len(v)
	}
	path := s.Annotations[MountFilePathAnnotation]
	switch kind := s.Labels["kubendt/kind"]; kind {
	case "file":
		row.Role, row.Detail = "file", fmt.Sprintf("%s, %d bytes, sensitive", path, bytes)
	case "mount":
		row.Role, row.Detail = "mount", path+", deleted, kept for the pods that mount it"
	case "user":
		row.Role, row.Detail = "user", s.Labels["kubendt/user"]+", password hash"
	case "token":
		row.Role, row.Detail = "token", string(s.Data["name"])
	case "session-key":
		row.Role, row.Detail = "session-key", "what the sessions are signed with"
	case "":
		row.Role = "other"
	default:
		row.Role = kind
	}
	return row, true
}

func leaseRow(l *coordinationv1.Lease) StateObject {
	row := StateObject{
		Kind: "Lease", Name: l.Name, Namespace: l.Namespace, Size: jsonSize(l),
		CreatedAt: stamp(l.CreationTimestamp), ResourceVersion: l.ResourceVersion, Labels: l.Labels,
	}
	if l.Labels["kubendt/kind"] != "lock" {
		row.Role = "other"
		return row
	}
	row.Role = "lock"
	holder := ""
	if l.Spec.HolderIdentity != nil {
		holder = *l.Spec.HolderIdentity
	}
	expired := false
	if l.Spec.RenewTime != nil && l.Spec.LeaseDurationSeconds != nil {
		expired = time.Since(l.Spec.RenewTime.Time) > time.Duration(*l.Spec.LeaseDurationSeconds)*time.Second
	}
	switch {
	case holder == "":
		row.Detail = "free"
	case expired:
		row.Detail = "expired, was held by " + holder
	default:
		row.Detail = "held by " + holder
	}
	if op := l.Annotations["kubendt/operation"]; op != "" && holder != "" {
		row.Detail += " for " + op
	}
	return row
}

// GetStateObject is one object as the API server has it, without the
// managed fields, a Secret's values replaced by their sizes, as a document
// and as YAML.
func GetStateObject(namespace, kind, name string) (map[string]any, string, error) {
	ctx := context.TODO()
	var obj runtime.Object
	var err error
	apiVersion, typed := "v1", ""
	switch strings.ToLower(kind) {
	case "namespace":
		obj, err = stateNamespaces().Get(ctx, name, metav1.GetOptions{})
		typed = "Namespace"
	case "statefulset":
		obj, err = stateStatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		apiVersion, typed = "apps/v1", "StatefulSet"
	case "pod":
		obj, err = statePods(namespace).Get(ctx, name, metav1.GetOptions{})
		typed = "Pod"
	case "configmap":
		obj, err = stateConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		typed = "ConfigMap"
	case "secret":
		obj, err = stateSecrets(namespace).Get(ctx, name, metav1.GetOptions{})
		typed = "Secret"
	case "lease":
		obj, err = stateLeases(namespace).Get(ctx, name, metav1.GetOptions{})
		apiVersion, typed = "coordination.k8s.io/v1", "Lease"
	case "limitrange":
		obj, err = stateLimitRanges(namespace).Get(ctx, name, metav1.GetOptions{})
		typed = "LimitRange"
	case "topology":
		var u *unstructured.Unstructured
		u, err = stateTopologies(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			obj = u
		}
	default:
		return nil, "", fmt.Errorf("unknown kind %q", kind)
	}
	if err != nil {
		return nil, "", err
	}
	doc, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, "", fmt.Errorf("could not read %s %s: %w", kind, name, err)
	}
	if typed != "" {
		doc["apiVersion"], doc["kind"] = apiVersion, typed
	}
	if meta, ok := doc["metadata"].(map[string]any); ok {
		delete(meta, "managedFields")
	}
	if typed == "Secret" {
		if data, ok := doc["data"].(map[string]any); ok {
			for k, v := range data {
				if s, ok := v.(string); ok {
					// Base64 in the document, the size is of the value itself.
					data[k] = fmt.Sprintf("<%d bytes>", len(s)*3/4)
				}
			}
		}
		delete(doc, "stringData")
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return nil, "", err
	}
	return doc, string(raw), nil
}
