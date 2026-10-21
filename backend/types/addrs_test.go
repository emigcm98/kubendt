package types

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAddrsJSON(t *testing.T) {
	cases := []struct {
		in   string
		want Addrs
		out  string
	}{
		{`""`, nil, `""`},
		{`null`, nil, `""`},
		{`"10.0.0.1/24"`, Addrs{"10.0.0.1/24"}, `"10.0.0.1/24"`},
		{`["10.0.0.1/24"]`, Addrs{"10.0.0.1/24"}, `"10.0.0.1/24"`},
		{`["10.0.0.1/24", "fd00::1/64"]`, Addrs{"10.0.0.1/24", "fd00::1/64"}, `["10.0.0.1/24","fd00::1/64"]`},
		{`[" 10.0.0.1/24 ", ""]`, Addrs{"10.0.0.1/24"}, `"10.0.0.1/24"`},
	}
	for _, c := range cases {
		var got Addrs
		if err := json.Unmarshal([]byte(c.in), &got); err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %#v, want %#v", c.in, got, c.want)
		}
		out, _ := json.Marshal(got)
		if string(out) != c.out {
			t.Errorf("%s: marshals to %s, want %s", c.in, out, c.out)
		}
	}
	var bad Addrs
	if err := json.Unmarshal([]byte(`{"a":1}`), &bad); err == nil {
		t.Error("an object should not parse as addresses")
	}
	if err := json.Unmarshal([]byte(`[1, 2]`), &bad); err == nil {
		t.Error("numbers should not parse as addresses")
	}
}

func TestAddrsParts(t *testing.T) {
	a := Addrs{"10.0.0.1/24", "10.0.0.5/24", "fd00::1/64"}
	if a.First() != "10.0.0.1/24" || !reflect.DeepEqual(a.Extra(), []string{"10.0.0.5/24", "fd00::1/64"}) || a.Empty() {
		t.Errorf("parts of %v wrong", a)
	}
	var none Addrs
	if none.First() != "" || none.Extra() != nil || !none.Empty() {
		t.Error("empty addrs have no parts")
	}
	if OneAddr("  ") != nil || OneAddr("x").First() != "x" {
		t.Error("OneAddr")
	}
}
