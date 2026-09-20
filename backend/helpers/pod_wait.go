package helpers

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"kubendt/kubeclient"
	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// podReadyTimeout bounds every wait for pods to become Ready.
const podReadyTimeout = 180 * time.Second

// WaitForPodsReady waits until every replica of every node is Running and Ready.
func WaitForPodsReady(namespace string, nodes []types.NodeSpec) error {
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		for i := 0; i < node.Replicas; i++ {
			names = append(names, fmt.Sprintf("%s-%d", node.Name, i))
		}
	}
	return WaitForPodsReadyByName(namespace, names)
}

// WaitForPodsReadyByName blocks until every named pod is Running and Ready,
// one of them hits an unrecoverable image error, or the timeout expires. It
// follows pod events through a watch rather than polling, so readiness is
// noticed as soon as the kubelet reports it. A pod that is being deleted
// counts as not ready, which covers restarts without tracking UIDs: the old
// pod shows up as terminating, then its replacement appears under the same
// name.
func WaitForPodsReadyByName(namespace string, podNames []string) error {
	if len(podNames) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), podReadyTimeout)
	defer cancel()

	pending := make(map[string]string, len(podNames)) // pod name -> last known reason
	for _, name := range podNames {
		pending[name] = "not-seen"
	}
	log.Printf("⏳ Waiting for %d pod(s) to be Ready in %s: %v", len(pending), namespace, podNames)

	evaluate := func(pod *v1.Pod) error {
		if _, tracked := pending[pod.Name]; !tracked {
			return nil
		}
		// Fail fast on unrecoverable image errors instead of waiting out
		// the whole timeout.
		if reason, detail, bad := fatalPodImageError(pod); bad {
			return fmt.Errorf("pod '%s' cannot start due to an image error (%s)%s", pod.Name, reason, detail)
		}
		ok, reason := isPodReady(pod)
		if !ok {
			pending[pod.Name] = reason
			return nil
		}
		delete(pending, pod.Name)
		log.Printf("✅ Pod %s is Ready (%d pending)", pod.Name, len(pending))
		return nil
	}

	for {
		// List first so nothing that changed before the watch opens is
		// missed, then watch from that exact resource version. A watch the
		// server closes just loops back here.
		list, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			if ctx.Err() != nil {
				return pendingTimeoutError(pending)
			}
			return fmt.Errorf("listing pods in namespace '%s': %w", namespace, err)
		}
		for i := range list.Items {
			if err := evaluate(&list.Items[i]); err != nil {
				return err
			}
		}
		if len(pending) == 0 {
			log.Printf("✅ All pods are Running and Ready: %v", podNames)
			return nil
		}

		w, err := kubeclient.Clientset.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{ResourceVersion: list.ResourceVersion})
		if err != nil {
			if ctx.Err() != nil {
				return pendingTimeoutError(pending)
			}
			return fmt.Errorf("watching pods in namespace '%s': %w", namespace, err)
		}
		done, err := followPodEvents(ctx, w, pending, evaluate)
		w.Stop()
		if err != nil {
			return err
		}
		if done {
			log.Printf("✅ All pods are Running and Ready: %v", podNames)
			return nil
		}
		if ctx.Err() != nil {
			return pendingTimeoutError(pending)
		}
	}
}

// followPodEvents consumes a pod watch until every pending pod is Ready
// (done=true), the context ends, or the server closes the stream (done=false,
// caller re-lists). Delete events keep the pod pending: it is being recreated.
func followPodEvents(ctx context.Context, w watch.Interface, pending map[string]string, evaluate func(*v1.Pod) error) (bool, error) {
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case ev, open := <-w.ResultChan():
			if !open {
				return false, nil
			}
			switch ev.Type {
			case watch.Added, watch.Modified:
				pod, ok := ev.Object.(*v1.Pod)
				if !ok {
					continue
				}
				if err := evaluate(pod); err != nil {
					return false, err
				}
				if len(pending) == 0 {
					return true, nil
				}
			case watch.Deleted:
				if pod, ok := ev.Object.(*v1.Pod); ok {
					if _, tracked := pending[pod.Name]; tracked {
						pending[pod.Name] = "recreating"
						log.Printf("♻️ Pod %s deleted, waiting for its replacement", pod.Name)
					}
				}
			case watch.Error:
				// Typically an expired resource version. Re-list and re-watch.
				return false, nil
			}
		}
	}
}

func pendingTimeoutError(pending map[string]string) error {
	parts := make([]string, 0, len(pending))
	for name, reason := range pending {
		parts = append(parts, fmt.Sprintf("%s (%s)", name, reason))
	}
	sort.Strings(parts)
	return fmt.Errorf("timeout waiting for pods to be Ready: %s", strings.Join(parts, ", "))
}
