package helpers

import "testing"

func TestRuntimeOf(t *testing.T) {
	for name, c := range map[string]struct {
		labels map[string]string
		want   string
	}{
		"runtime label":       {map[string]string{"kubendt/runtime": "qemu"}, RuntimeLabelQEMU},
		"container label":     {map[string]string{"kubendt/runtime": "k8s-linux"}, RuntimeLabelContainer},
		"legacy qemu label":   {map[string]string{"kubendt/qemu": "true"}, RuntimeLabelQEMU},
		"legacy false":        {map[string]string{"kubendt/qemu": "false"}, RuntimeLabelContainer},
		"no labels":           {map[string]string{}, RuntimeLabelContainer},
		"runtime wins legacy": {map[string]string{"kubendt/runtime": "k8s-linux", "kubendt/qemu": "true"}, RuntimeLabelContainer},
	} {
		if got := RuntimeOf(c.labels); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
	if !IsGuestVM(map[string]string{"kubendt/runtime": "qemu"}) || IsGuestVM(nil) {
		t.Errorf("IsGuestVM follows RuntimeOf")
	}
	if RuntimeLabelValue(true) != "qemu" || RuntimeLabelValue(false) != "k8s-linux" {
		t.Errorf("label values")
	}
}
