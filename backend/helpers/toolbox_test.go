package helpers

import (
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
)

func TestValidToolboxName(t *testing.T) {
	for name, ok := range map[string]bool{
		"toolbox-0123abcdef": true,
		"capture-0123abcdef": true, // injected before the rename
		"toolbox-0123":       false,
		"debug-0123abcdef":   false,
		"toolbox-0123ABCDEF": false,
		"":                   false,
	} {
		if got := ValidToolboxName(name); got != ok {
			t.Errorf("ValidToolboxName(%q) = %v, want %v", name, got, ok)
		}
	}
	if n := newToolboxName(); !ValidToolboxName(n) {
		t.Errorf("minted name %q is not valid", n)
	}
}

func TestToolboxImage(t *testing.T) {
	t.Setenv("KUBENDT_TOOLBOX_IMAGE", "")
	t.Setenv("KUBENDT_CAPTURE_IMAGE", "")
	if got := ToolboxImage(); got != DefaultToolboxImage() {
		t.Errorf("default image %q, want %q", got, DefaultToolboxImage())
	}
	t.Setenv("KUBENDT_CAPTURE_IMAGE", "mirror/capture:1")
	if got := ToolboxImage(); got != "mirror/capture:1" {
		t.Errorf("old variable ignored, got %q", got)
	}
	t.Setenv("KUBENDT_TOOLBOX_IMAGE", "mirror/toolbox:1")
	if got := ToolboxImage(); got != "mirror/toolbox:1" {
		t.Errorf("new variable does not win, got %q", got)
	}
}

func TestToolboxPullPolicy(t *testing.T) {
	for image, want := range map[string]v1.PullPolicy{
		"ghcr.io/emigcm98/kubendt-toolbox:latest":        v1.PullAlways,
		"nicolaka/netshoot":                              v1.PullAlways,
		"ghcr.io/emigcm98/kubendt-toolbox:1.8.0":         v1.PullIfNotPresent,
		"localhost:5000/kubendt-toolbox:dev":             v1.PullIfNotPresent,
		"ghcr.io/emigcm98/kubendt-toolbox@sha256:abcdef": v1.PullIfNotPresent,
	} {
		if got := toolboxPullPolicy(image); got != want {
			t.Errorf("%s: %s, want %s", image, got, want)
		}
	}
}

func TestToolboxCommandFallsBackWithoutTini(t *testing.T) {
	cmd := toolboxCommand()
	if len(cmd) != 3 || cmd[0] != "sh" || cmd[1] != "-c" {
		t.Fatalf("shape: %v", cmd)
	}
	if !strings.Contains(cmd[2], "exec tini -- sleep "+toolboxSleeperSeconds) || !strings.HasSuffix(cmd[2], "; exec sleep "+toolboxSleeperSeconds) {
		t.Errorf("command: %s", cmd[2])
	}
}
