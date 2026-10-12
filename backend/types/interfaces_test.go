package types

import "testing"

func TestValidateLinuxInterfaceName(t *testing.T) {
	valid := []string{"eth0", "lo", "veth123", "a"}
	for _, n := range valid {
		if err := ValidateLinuxInterfaceName(n); err != nil {
			t.Errorf("expected %q valid, got error: %v", n, err)
		}
	}
	invalid := []string{"", "this-name-is-too-long", "eth/0", "eth 0", "eth:0"}
	for _, n := range invalid {
		if err := ValidateLinuxInterfaceName(n); err == nil {
			t.Errorf("expected %q invalid, got no error", n)
		}
	}
}
