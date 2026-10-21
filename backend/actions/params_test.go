package actions

import (
	"reflect"
	"testing"
)

func TestCheckKindFamily(t *testing.T) {
	for _, ok := range []string{"4", "6"} {
		if err := checkKind(KindFamily, reflect.ValueOf(ok)); err != nil {
			t.Errorf("family %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"5", "ipv6", "v4"} {
		if err := checkKind(KindFamily, reflect.ValueOf(bad)); err == nil {
			t.Errorf("family %q should be rejected", bad)
		}
	}
}
