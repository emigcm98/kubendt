package kubeclient

import (
	"testing"

	"k8s.io/client-go/rest"
)

func TestApplyRateLimitsDefaults(t *testing.T) {
	t.Setenv("KUBENDT_K8S_QPS", "")
	t.Setenv("KUBENDT_K8S_BURST", "")
	cfg := &rest.Config{}
	applyRateLimits(cfg)
	if cfg.QPS != defaultK8sQPS || cfg.Burst != defaultK8sBurst {
		t.Fatalf("got QPS %v burst %d, want %d/%d", cfg.QPS, cfg.Burst, defaultK8sQPS, defaultK8sBurst)
	}
}

func TestApplyRateLimitsFromEnv(t *testing.T) {
	t.Setenv("KUBENDT_K8S_QPS", "20")
	t.Setenv("KUBENDT_K8S_BURST", " 40 ")
	cfg := &rest.Config{}
	applyRateLimits(cfg)
	if cfg.QPS != 20 || cfg.Burst != 40 {
		t.Fatalf("got QPS %v burst %d, want 20/40", cfg.QPS, cfg.Burst)
	}
}

func TestEnvPositiveIntFallsBackOnBadValues(t *testing.T) {
	for _, v := range []string{"0", "-5", "abc", "1.5"} {
		t.Setenv("KUBENDT_K8S_QPS", v)
		if got := envPositiveInt("KUBENDT_K8S_QPS", 7); got != 7 {
			t.Errorf("%q: got %d, want the default 7", v, got)
		}
	}
}
