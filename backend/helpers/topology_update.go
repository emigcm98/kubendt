package helpers

import (
	"context"
	"time"

	"kubendt/kubeclient"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

// topologyBackoff is how long a write to a Topology keeps reading it again
// when something else wrote in between: about a second in all.
var topologyBackoff = wait.Backoff{Steps: 6, Duration: 20 * time.Millisecond, Factor: 2, Jitter: 0.2}

// updateTopology reads a Topology, lets mutate change it and writes it back,
// reading it again and starting over when the write finds another one in
// between. Meshnet's daemon writes the status of the far end of a link within
// milliseconds of a change to the near one, which is the Topology a modify
// updates next, so a plain read and write loses that race now and then. mutate
// returns whether it changed anything, and it runs on each fresh read. The
// error of a failed read, such as a Topology that is not there, is returned
// as it came.
func updateTopology(namespace, name string, mutate func(obj *unstructured.Unstructured) (bool, error)) (bool, error) {
	changed := false
	err := retry.RetryOnConflict(topologyBackoff, func() error {
		obj, err := kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		changed, err = mutate(obj)
		if err != nil || !changed {
			return err
		}
		_, err = kubeclient.DynamicClient.Resource(TopologyGVR).Namespace(namespace).Update(context.TODO(), obj, metav1.UpdateOptions{})
		return err
	})
	return changed && err == nil, err
}
