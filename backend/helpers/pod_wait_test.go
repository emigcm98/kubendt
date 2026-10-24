package helpers

import (
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLatestReadinessFailure(t *testing.T) {
	at := func(s int) metav1.Time { return metav1.NewTime(time.Date(2026, 10, 12, 9, 0, s, 0, time.UTC)) }
	events := []v1.Event{
		{Message: "Readiness probe failed: sh: tc: not found", LastTimestamp: at(1)},
		{Message: "Liveness probe failed: whatever", LastTimestamp: at(9)},
		{Message: "Readiness probe failed: ip not found in PATH, the image needs iproute2\nsecond line", LastTimestamp: at(5)},
	}
	if got := latestReadinessFailure(events); got != "readiness probe failed: ip not found in PATH, the image needs iproute2" {
		t.Errorf("got %q", got)
	}
	if got := latestReadinessFailure([]v1.Event{{Message: "Readiness probe failed: ", LastTimestamp: at(1)}}); got != "readiness probe failed" {
		t.Errorf("empty output: got %q", got)
	}
	if got := latestReadinessFailure(nil); got != "" {
		t.Errorf("no events: got %q", got)
	}
}

func TestMainContainerExited(t *testing.T) {
	since := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	term := func(reason string, code int32, finished time.Time) v1.ContainerState {
		return v1.ContainerState{Terminated: &v1.ContainerStateTerminated{Reason: reason, ExitCode: code, FinishedAt: metav1.NewTime(finished)}}
	}
	running := v1.ContainerState{Running: &v1.ContainerStateRunning{}}
	waiting := v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
	pod := func(state, last v1.ContainerState) *v1.Pod {
		return &v1.Pod{Status: v1.PodStatus{ContainerStatuses: []v1.ContainerStatus{{State: state, LastTerminationState: last}}}}
	}
	cases := []struct {
		name string
		pod  *v1.Pod
		want string
	}{
		{"running, never died", pod(running, v1.ContainerState{}), ""},
		{"dead now", pod(term("Completed", 0, since.Add(time.Second)), v1.ContainerState{}), "Completed, exit code 0"},
		{"in backoff after dying", pod(waiting, term("Error", 1, since.Add(time.Second))), "Error, exit code 1"},
		{"restarted during this wait", pod(running, term("Completed", 0, since.Add(2*time.Second))), "Completed, exit code 0"},
		{"running after an old death", pod(running, term("Error", 137, since.Add(-time.Hour))), ""},
		{"no reason", pod(term("", 2, since.Add(time.Second)), v1.ContainerState{}), "terminated, exit code 2"},
	}
	for _, c := range cases {
		got, exited := mainContainerExited(c.pod, since)
		if exited != (c.want != "") || got != c.want {
			t.Errorf("%s: got %q, %v", c.name, got, exited)
		}
	}
}
