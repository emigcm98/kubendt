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

func TestCheckKindMAC(t *testing.T) {
	for _, ok := range []string{"02:00:00:00:01:01", "08-00-00-00-00-fe", "0800.0000.00fe"} {
		if err := checkKind(KindMAC, reflect.ValueOf(ok)); err != nil {
			t.Errorf("mac %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"02:00:00:00:01", "02:00:00:00:01:01:00:00", "zz:00:00:00:01:01", "10.0.0.1"} {
		if err := checkKind(KindMAC, reflect.ValueOf(bad)); err == nil {
			t.Errorf("mac %q should be rejected", bad)
		}
	}
}
