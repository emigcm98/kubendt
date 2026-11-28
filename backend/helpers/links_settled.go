package helpers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"kubendt/kubeclient"
	"kubendt/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// How long the fork's daemon gets to show a change on the Topologies it was
// given, and how often it is asked. Variables so a test can shorten them.
var (
	forkSettleTimeout = 20 * time.Second
	forkSettlePoll    = 500 * time.Millisecond
)

// TopologyLinksUnsettled says why a Topology's status does not yet show what
// its spec declares, or "" when it does: every declared link is in the status
// and none that was removed is still there, none has failed, and the daemon
// reports no plumbing error. The fork's daemon writes that status after each
// reconcile, so it is the proof that a link declared or removed on a running
// pod took effect, with no exec into the pod.
func TopologyLinksUnsettled(topology map[string]interface{}) string {
	declared := map[int64]string{}
	specLinks, _, _ := unstructured.NestedSlice(topology, "spec", "links")
	for _, raw := range specLinks {
		link, _ := raw.(map[string]interface{})
		if uid, ok := linkUID(link["uid"]); ok {
			intf, _ := link["local_intf"].(string)
			declared[uid] = intf
		}
	}
	shown := map[int64]map[string]interface{}{}
	statusLinks, _, _ := unstructured.NestedSlice(topology, "status", "links")
	for _, raw := range statusLinks {
		link, _ := raw.(map[string]interface{})
		if uid, ok := linkUID(link["uid"]); ok {
			shown[uid] = link
		}
	}

	if msg, _, _ := unstructured.NestedString(topology, "status", "plumbing_error"); msg != "" {
		return "plumbing error: " + msg
	}
	uids := make([]int64, 0, len(declared))
	for uid := range declared {
		uids = append(uids, uid)
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	for _, uid := range uids {
		link, ok := shown[uid]
		if !ok {
			return fmt.Sprintf("%s is not wired yet", declared[uid])
		}
		if state, _ := link["state"].(string); state == "failed" {
			msg, _ := link["error"].(string)
			return fmt.Sprintf("%s failed: %s", declared[uid], msg)
		}
	}
	for uid, link := range shown {
		if _, ok := declared[uid]; !ok {
			intf, _ := link["local_intf"].(string)
			return fmt.Sprintf("%s is still wired though removed", intf)
		}
	}
	return ""
}

func linkUID(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	}
	return 0, false
}

// AwaitForkLinks waits for the Topologies of the given pods to settle and
// returns the pods that did not, with why. A pod with no Topology is not
// waited for. One list per round covers every pod.
func AwaitForkLinks(namespace string, pods []string) map[string]string {
	want := make(map[string]struct{}, len(pods))
	for _, p := range pods {
		want[p] = struct{}{}
	}
	unsettled := map[string]string{}
	if len(want) == 0 {
		return unsettled
	}
	deadline := time.Now().Add(forkSettleTimeout)
	for {
		list, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
		unsettled = map[string]string{}
		if err != nil {
			for p := range want {
				unsettled[p] = "the Topologies could not be read: " + err.Error()
			}
		} else {
			for i := range list.Items {
				name := list.Items[i].GetName()
				if _, ok := want[name]; !ok {
					continue
				}
				if why := TopologyLinksUnsettled(list.Items[i].Object); why != "" {
					unsettled[name] = why
				}
			}
		}
		if len(unsettled) == 0 || time.Now().After(deadline) {
			return unsettled
		}
		time.Sleep(forkSettlePoll)
	}
}

// ForkSettleSet lists the pods whose Topologies must show their links settled
// after a modify on the fork's path: the pods it created, the peers whose links
// it touched and the ends of the links it added, except the pods it recreated,
// which the wait and the heal pass cover, and the ones it removed. A name that
// has no Topology any more is skipped later, when they are read.
func ForkSettleSet(addLinks []types.LinkSpec, nodes []types.NodeSpec, include []map[string]struct{}, exclude []map[string]struct{}) []string {
	set := map[string]struct{}{}
	for _, group := range include {
		for pod := range group {
			set[pod] = struct{}{}
		}
	}
	for _, l := range addLinks {
		for _, end := range []string{l.Node, l.PeerNode} {
			if pod := ResolvePodNameFromLink(end, nodes); pod != "" && pod != externalNodeName {
				set[pod] = struct{}{}
			}
		}
	}
	for _, group := range exclude {
		for pod := range group {
			delete(set, pod)
		}
	}
	out := make([]string, 0, len(set))
	for pod := range set {
		out = append(out, pod)
	}
	sort.Strings(out)
	return out
}

// UnsettledDetail words the pods AwaitForkLinks gave up on, for a warning,
// and whether the heal pass took over from it.
func UnsettledDetail(unsettled map[string]string, healed bool) string {
	pods := make([]string, 0, len(unsettled))
	for pod := range unsettled {
		pods = append(pods, pod)
	}
	sort.Strings(pods)
	parts := make([]string, 0, len(pods))
	for _, pod := range pods {
		parts = append(parts, pod+" ("+unsettled[pod]+")")
	}
	detail := fmt.Sprintf("The Meshnet daemon had not settled the links of %s within %s.", strings.Join(parts, ", "), forkSettleTimeout)
	if healed {
		detail += " The heal pass ran in its place."
	}
	return detail
}
