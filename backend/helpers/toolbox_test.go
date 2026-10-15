package helpers

import "testing"

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
	if got := ToolboxImage(); got != DefaultToolboxImage {
		t.Errorf("default image %q, want %q", got, DefaultToolboxImage)
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
