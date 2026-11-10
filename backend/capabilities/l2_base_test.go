package capabilities

import (
	"reflect"
	"testing"
)

func TestL2BaseSetMAC(t *testing.T) {
	got := L2Base{}.SetMAC("eth1", "02:00:00:00:01:01")
	want := [][]string{{"ip", "link", "set", "eth1", "address", "02:00:00:00:01:01"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
