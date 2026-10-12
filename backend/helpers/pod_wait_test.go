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
