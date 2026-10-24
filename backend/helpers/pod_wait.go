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
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

// podReadyTimeout bounds every wait for pods to become Ready.
const podReadyTimeout = 180 * time.Second

// PodNamesForNodes expands node specs into the pod names their StatefulSets produce.
func PodNamesForNodes(nodes []types.NodeSpec) []string {
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		for i := 0; i < node.Replicas; i++ {
			names = append(names, fmt.Sprintf("%s-%d", node.Name, i))
		}
	}
	return names
}

// WaitForPodsReady waits until every replica of every node is Running and Ready.
func WaitForPodsReady(namespace string, nodes []types.NodeSpec) error {
	return WaitForPodsReadyByName(namespace, PodNamesForNodes(nodes))
}

// WaitForPodsReadyByName is WaitForPodsReadyTimeline without the timeline.
func WaitForPodsReadyByName(namespace string, podNames []string) error {
	_, err := WaitForPodsReadyTimeline(namespace, podNames, time.Now())
	return err
}

// podTracker remembers what has already been seen for one pending pod so each
// observed transition is stamped once, when it happens. uid tells a
// replacement pod apart from the one it replaces.
type podTracker struct {
	uid      k8stypes.UID
	seen     map[string]bool
	timeline *types.PodTimeline
}

// WaitForPodsReadyTimeline blocks until every named pod is Running and Ready,
// one of them hits an unrecoverable image error, or the timeout expires, and
// returns a lifecycle timeline per pod. It follows pod events through a watch
// rather than polling, so readiness is noticed as soon as the kubelet reports
// it. A pod that is being deleted counts as not ready, which covers restarts
// without extra bookkeeping: the old pod shows up as terminating, then its
// replacement appears under the same name with a new UID.
//
// Observed stamps are milliseconds since requestStart. Steps that had already
// happened when the initial list ran are marked seen but left unstamped, so
// nothing is attributed to the wrong moment.
func WaitForPodsReadyTimeline(namespace string, podNames []string, requestStart time.Time) (map[string]*types.PodTimeline, error) {
	trackers := make(map[string]*podTracker, len(podNames))
	for _, name := range podNames {
		trackers[name] = &podTracker{seen: map[string]bool{}, timeline: &types.PodTimeline{Pod: name}}
	}
	if len(podNames) == 0 {
		return timelinesOf(trackers), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), podReadyTimeout)
	defer cancel()

	pending := make(map[string]string, len(podNames)) // pod name -> last known reason
	for _, name := range podNames {
		pending[name] = "not-seen"
	}
	log.Printf("⏳ Waiting for %d pod(s) to be Ready in %s: %v", len(pending), namespace, podNames)

	sinceStart := func() int64 { return time.Since(requestStart).Milliseconds() }

	evaluate := func(pod *v1.Pod, fromWatch bool) error {
		if _, tracked := pending[pod.Name]; !tracked {
			return nil
		}
		if pod.DeletionTimestamp != nil {
			pending[pod.Name] = "terminating"
			return nil
		}
		tr := trackers[pod.Name]
		if tr.uid != pod.UID {
			tr.uid = pod.UID
			tr.seen = map[string]bool{}
		}
		obs := &tr.timeline.Observed
		mark := func(step string, happened bool, dst **int64) {
			if !happened || tr.seen[step] {
				return
			}
			tr.seen[step] = true
			if fromWatch {
				ms := sinceStart()
				*dst = &ms
			}
		}
		mark("scheduled", podConditionTrue(pod, v1.PodScheduled), &obs.Scheduled)
		mark("sandbox_ready", podConditionTrue(pod, v1.PodReadyToStartContainers), &obs.SandboxReady)
		mark("container_started", containerStartedAt(pod) != nil, &obs.ContainerStarted)

		// Fail fast on unrecoverable image errors instead of waiting out
		// the whole timeout.
		if reason, detail, bad := fatalPodImageError(pod); bad {
			return fmt.Errorf("pod '%s' cannot start due to an image error (%s)%s", pod.Name, reason, detail)
		}
		if exit, code, exited := mainContainerExited(pod, requestStart); exited && code == 0 {
			return fmt.Errorf("pod %s exited (%s); %s", pod.Name, exit, exitedHint)
		}
		ok, reason := isPodReady(pod)
		if !ok {
			pending[pod.Name] = reason
			return nil
		}
		tr.timeline.Kubernetes = kubernetesStamps(pod)
		ms := sinceStart()
		obs.ReadySeen = &ms
		delete(pending, pod.Name)
		log.Printf("✅ Pod %s is Ready (%d pending)", pod.Name, len(pending))
		return nil
	}

	onDeleted := func(pod *v1.Pod) {
		if _, tracked := pending[pod.Name]; !tracked {
			return
		}
		pending[pod.Name] = "recreating"
		if tr := trackers[pod.Name]; tr.timeline.Observed.OldPodGone == nil {
			ms := sinceStart()
			tr.timeline.Observed.OldPodGone = &ms
		}
		log.Printf("♻️ Pod %s deleted, waiting for its replacement", pod.Name)
	}

	for {
		// List first so nothing that changed before the watch opens is
		// missed, then watch from that exact resource version. A watch the
		// server closes just loops back here.
		list, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			if ctx.Err() != nil {
				return timelinesOf(trackers), pendingTimeoutError(namespace, pending)
			}
			return timelinesOf(trackers), fmt.Errorf("listing pods in namespace '%s': %w", namespace, err)
		}
		for i := range list.Items {
			if err := evaluate(&list.Items[i], false); err != nil {
				return timelinesOf(trackers), err
			}
		}
		if len(pending) == 0 {
			log.Printf("✅ All pods are Running and Ready: %v", podNames)
			return timelinesOf(trackers), nil
		}

		w, err := kubeclient.Clientset.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{ResourceVersion: list.ResourceVersion})
		if err != nil {
			if ctx.Err() != nil {
				return timelinesOf(trackers), pendingTimeoutError(namespace, pending)
			}
			return timelinesOf(trackers), fmt.Errorf("watching pods in namespace '%s': %w", namespace, err)
		}
		done, err := followPodEvents(ctx, w, pending, evaluate, onDeleted)
		w.Stop()
		if err != nil {
			return timelinesOf(trackers), err
		}
		if done {
			log.Printf("✅ All pods are Running and Ready: %v", podNames)
			return timelinesOf(trackers), nil
		}
		if ctx.Err() != nil {
			return timelinesOf(trackers), pendingTimeoutError(namespace, pending)
		}
	}
}

// followPodEvents consumes a pod watch until every pending pod is Ready
// (done=true), the context ends, or the server closes the stream (done=false,
// caller re-lists). Delete events keep the pod pending: it is being recreated.
func followPodEvents(ctx context.Context, w watch.Interface, pending map[string]string, evaluate func(*v1.Pod, bool) error, onDeleted func(*v1.Pod)) (bool, error) {
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case ev, open := <-w.ResultChan():
			if !open {
				return false, nil
			}
			pod, isPod := ev.Object.(*v1.Pod)
			switch ev.Type {
			case watch.Added, watch.Modified:
				if !isPod {
					continue
				}
				if err := evaluate(pod, true); err != nil {
					return false, err
				}
				if len(pending) == 0 {
					return true, nil
				}
			case watch.Deleted:
				if isPod {
					onDeleted(pod)
				}
			case watch.Error:
				// Typically an expired resource version. Re-list and re-watch.
				return false, nil
			}
		}
	}
}

const exitedHint = "a node's process has to keep running, a daemon in the foreground or no 'commands' so the image's shell stays up"

// ExitedPods names, among the pods, those whose main container ended since
// the request began. Finished ones exited with 0: the command is over and
// nothing keeps the container up, so Kubernetes restarts it forever and the
// lab is unusable. Crashed ones exited with an error: an application that
// did not start, which may well do on a later restart once what it waits
// for is up (the Open5GS functions retry until the NRF answers), so they
// are reported, not failed.
func ExitedPods(namespace string, podNames []string, since time.Time) (finished []string, crashed []types.Warning) {
	list, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, nil
	}
	wanted := make(map[string]bool, len(podNames))
	for _, n := range podNames {
		wanted[n] = true
	}
	for i := range list.Items {
		pod := &list.Items[i]
		if !wanted[pod.Name] {
			continue
		}
		exit, code, exited := mainContainerExited(pod, since)
		switch {
		case !exited:
		case code == 0:
			finished = append(finished, fmt.Sprintf("%s (%s)", pod.Name, exit))
		default:
			crashed = append(crashed, types.Warning{Node: pod.Name, Kind: "node_crashed",
				Detail: fmt.Sprintf("exited (%s) and restarted; a node that keeps restarting is an application that does not start, its logs say why", exit)})
		}
	}
	sort.Strings(finished)
	sort.Slice(crashed, func(i, j int) bool { return crashed[i].Node < crashed[j].Node })
	return finished, crashed
}

// ExitedPodsError is the user-facing error for the finished pods.
func ExitedPodsError(finished []string) error {
	noun := "pod"
	if len(finished) > 1 {
		noun = "pods"
	}
	return fmt.Errorf("%s %s exited; %s", noun, strings.Join(finished, ", "), exitedHint)
}

// mainContainerExited reports a container of the pod that ended, with how it
// ended and its exit code: a container dead now, one in restart backoff, or
// one restarted since the request began. A death older than the request on
// a container running now is history, not this request's concern.
func mainContainerExited(pod *v1.Pod, since time.Time) (string, int32, bool) {
	for _, cs := range pod.Status.ContainerStatuses {
		t := cs.State.Terminated
		if t == nil && cs.State.Running == nil {
			t = cs.LastTerminationState.Terminated
		}
		if t == nil && cs.State.Running != nil {
			if last := cs.LastTerminationState.Terminated; last != nil && last.FinishedAt.After(since) {
				t = last
			}
		}
		if t == nil {
			continue
		}
		reason := t.Reason
		if reason == "" {
			reason = "terminated"
		}
		return fmt.Sprintf("%s, exit code %d", reason, t.ExitCode), t.ExitCode, true
	}
	return "", 0, false
}

func timelinesOf(trackers map[string]*podTracker) map[string]*types.PodTimeline {
	out := make(map[string]*types.PodTimeline, len(trackers))
	for name, tr := range trackers {
		out[name] = tr.timeline
	}
	return out
}

func podConditionTrue(pod *v1.Pod, condType v1.PodConditionType) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == condType {
			return c.Status == v1.ConditionTrue
		}
	}
	return false
}

func podConditionTime(pod *v1.Pod, condType v1.PodConditionType) string {
	for _, c := range pod.Status.Conditions {
		if c.Type == condType && c.Status == v1.ConditionTrue {
			return c.LastTransitionTime.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func containerStartedAt(pod *v1.Pod) *metav1.Time {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Running != nil {
			return &cs.State.Running.StartedAt
		}
	}
	return nil
}

// kubernetesStamps reads the lifecycle timestamps Kubernetes wrote on the pod.
func kubernetesStamps(pod *v1.Pod) types.PodKubernetesStamps {
	stamps := types.PodKubernetesStamps{
		Created:      pod.CreationTimestamp.UTC().Format(time.RFC3339),
		Scheduled:    podConditionTime(pod, v1.PodScheduled),
		SandboxReady: podConditionTime(pod, v1.PodReadyToStartContainers),
		Ready:        podConditionTime(pod, v1.PodReady),
	}
	if started := containerStartedAt(pod); started != nil {
		stamps.ContainerStarted = started.UTC().Format(time.RFC3339)
	}
	return stamps
}

// BuildOperationTimeline assembles the response block from the per-pod
// timelines of a wait, the moments each pod's delete was issued (nil when no
// pod was deleted) and the backend phase durations. Pods are sorted by name.
func BuildOperationTimeline(requestStart time.Time, pods map[string]*types.PodTimeline, deleteIssued map[string]int64, phases types.BackendPhasesMs) types.OperationTimeline {
	list := make([]types.PodTimeline, 0, len(pods))
	for name, tl := range pods {
		if ms, ok := deleteIssued[name]; ok {
			v := ms
			tl.Observed.DeleteIssued = &v
		}
		list = append(list, *tl)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Pod < list[j].Pod })
	phases.Total = time.Since(requestStart).Milliseconds()
	return types.OperationTimeline{
		RequestStartedAt: requestStart.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		BackendMs:        phases,
		Pods:             list,
	}
}

// MsPtr returns the duration in milliseconds as a pointer, for optional phases.
func MsPtr(d time.Duration) *int64 {
	ms := d.Milliseconds()
	return &ms
}

// pendingTimeoutError names every pod still not Ready with its reason and,
// for a running pod, what its readiness probe last said, so an image without
// `ip` reads as such instead of as a bare timeout.
func pendingTimeoutError(namespace string, pending map[string]string) error {
	parts := make([]string, 0, len(pending))
	for name, reason := range pending {
		if probe := readinessProbeFailure(namespace, name); probe != "" {
			reason += ", " + probe
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", name, reason))
	}
	sort.Strings(parts)
	return fmt.Errorf("timeout waiting for pods to be Ready: %s", strings.Join(parts, ", "))
}

// readinessProbeFailure returns the last readiness probe failure the kubelet
// recorded for the pod, from its events.
func readinessProbeFailure(namespace, podName string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := kubeclient.Clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.kind=Pod,involvedObject.name=" + podName + ",reason=Unhealthy",
	})
	if err != nil {
		return ""
	}
	return latestReadinessFailure(events.Items)
}

// latestReadinessFailure picks the newest "Readiness probe failed" event and
// keeps the first line of what the probe printed.
func latestReadinessFailure(events []v1.Event) string {
	var best *v1.Event
	for i := range events {
		e := &events[i]
		if !strings.HasPrefix(e.Message, "Readiness probe failed") {
			continue
		}
		if best == nil || eventTime(e).After(eventTime(best)) {
			best = e
		}
	}
	if best == nil {
		return ""
	}
	msg := strings.TrimSpace(strings.TrimPrefix(best.Message, "Readiness probe failed:"))
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	if len(msg) > 160 {
		msg = msg[:160] + "..."
	}
	if msg == "" {
		return "readiness probe failed"
	}
	return "readiness probe failed: " + msg
}

func eventTime(e *v1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	return e.FirstTimestamp.Time
}
