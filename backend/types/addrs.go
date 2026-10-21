package types

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Addrs is the addresses of one end of a link. In JSON it is one string
// ("10.0.0.1/24") or an array of them; the first is what Meshnet wires with
// the link, the rest the platform adds after wiring.
type Addrs []string

func (a *Addrs) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*a = OneAddr(one)
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("an address or an array of addresses, got %s", string(data))
	}
	out := make(Addrs, 0, len(many))
	for _, s := range many {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	*a = out
	return nil
}

// A single address stays a string, so a topology written before lists
// reads back unchanged.
func (a Addrs) MarshalJSON() ([]byte, error) {
	switch len(a) {
	case 0:
		return json.Marshal("")
	case 1:
		return json.Marshal(a[0])
	}
	return json.Marshal([]string(a))
}

// OneAddr is the Addrs of a single value, empty for "".
func OneAddr(s string) Addrs {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return Addrs{s}
}

// First is the address Meshnet wires, "" when the end has none.
func (a Addrs) First() string {
	if len(a) == 0 {
		return ""
	}
	return a[0]
}

// Extra is every address after the first, the platform's to set.
func (a Addrs) Extra() []string {
	if len(a) < 2 {
		return nil
	}
	return a[1:]
}

func (a Addrs) Empty() bool { return len(a) == 0 }

func (a Addrs) String() string { return strings.Join(a, ", ") }
