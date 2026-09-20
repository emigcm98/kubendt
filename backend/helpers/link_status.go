package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"

	"kubendt/kubeclient"
	"kubendt/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Link realizations as Meshnet wires them. The CNI compares the worker IPs
// recorded on both Topology CRDs: same worker gives a veth pair, different
// workers a VXLAN tunnel.
const (
	LinkRealizationVeth     = "veth"
	LinkRealizationVxlan    = "vxlan"
	LinkRealizationExternal = "external"
	LinkRealizationPending  = "pending"
)

// BuildLinkStatus returns every link in the namespace with the worker each
// endpoint runs on and how Meshnet realized it. Links come from the Topology
// CRDs, workers from the pods, names from the kubendt/linknames annotation.
func BuildLinkStatus(namespace string) ([]types.LinkStatus, error) {
	links, err := BuildLinksFromTopologyCRDs(namespace)
	if err != nil {
		return nil, err
	}

	podList, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("error listing pods: %w", err)
	}
	workerOf := make(map[string]string, len(podList.Items))
	for _, pod := range podList.Items {
		workerOf[pod.Name] = pod.Spec.NodeName
	}

	names := linkNamesFromTopologyCRDs(namespace)

	out := make([]types.LinkStatus, 0, len(links))
	for _, l := range links {
		external := l.Node == externalNodeName || l.PeerNode == externalNodeName
		st := types.LinkStatus{
			UID:        l.UID,
			Node:       l.Node,
			LocalIntf:  l.LocalIntf,
			LocalIP:    l.LocalIP,
			PeerNode:   l.PeerNode,
			PeerIntf:   l.PeerIntf,
			PeerIP:     l.PeerIP,
			NodeWorker: workerOf[l.Node],
			PeerWorker: workerOf[l.PeerNode],
		}
		if l.UID != nil {
			st.Name = names[*l.UID]
		}
		st.Realization = linkRealization(st.NodeWorker, st.PeerWorker, external)
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].LocalIntf < out[j].LocalIntf
	})
	return out, nil
}

// linkRealization derives how Meshnet wired a link from where its ends run.
func linkRealization(nodeWorker, peerWorker string, external bool) string {
	switch {
	case external:
		return LinkRealizationExternal
	case nodeWorker == "" || peerWorker == "":
		return LinkRealizationPending
	case nodeWorker == peerWorker:
		return LinkRealizationVeth
	default:
		return LinkRealizationVxlan
	}
}

// linkNamesFromTopologyCRDs collects the optional per-link names, keyed by
// link UID, from the kubendt/linknames annotation of every Topology CRD.
func linkNamesFromTopologyCRDs(namespace string) map[int]string {
	names := map[int]string{}
	list, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Printf("⚠️ link status: could not list Topology CRDs for link names: %v", err)
		return names
	}
	for _, item := range list.Items {
		raw := item.GetAnnotations()["kubendt/linknames"]
		if raw == "" {
			continue
		}
		var byUID map[string]string
		if err := json.Unmarshal([]byte(raw), &byUID); err != nil {
			log.Printf("⚠️ link status: could not parse kubendt/linknames on %s: %v", item.GetName(), err)
			continue
		}
		for k, v := range byUID {
			if uid, err := strconv.Atoi(k); err == nil && v != "" {
				names[uid] = v
			}
		}
	}
	return names
}
