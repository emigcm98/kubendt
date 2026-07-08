package capabilities

import (
	"sort"
	"sync"
)

type MatcherFunc func(driver any) bool

type CapabilityDescriptor interface {
	ID() string
	Label() string
	Description() string
	Methods() map[string]string
	Match(driver any) bool
}

type capDesc struct {
	id, label, desc string
	methods         map[string]string
	match           MatcherFunc
}

func (c capDesc) ID() string                 { return c.id }
func (c capDesc) Label() string              { return c.label }
func (c capDesc) Description() string        { return c.desc }
func (c capDesc) Methods() map[string]string { return c.methods }
func (c capDesc) Match(d any) bool           { return c.match(d) }

var (
	mu   sync.RWMutex
	all  []CapabilityDescriptor
	byID = map[string]CapabilityDescriptor{}
)

// Register with a manual matcher (when a capability is not just “implements interface X”)
func RegisterCapabilityWithMatcher(id, label, description string, methods map[string]string, matcher MatcherFunc) {
	mu.Lock()
	defer mu.Unlock()
	cd := capDesc{id: id, label: label, desc: description, methods: methods, match: matcher}
	all = append(all, cd)
	byID[id] = cd
}

// Generic registration by interface C (like `Register[T Driver]`)
func RegisterCapability[C any](id, label, description string, methods map[string]string) {
	RegisterCapabilityWithMatcher(id, label, description, methods, func(d any) bool {
		_, ok := d.(C)
		return ok
	})
}

func ListAll() []CapabilityDescriptor {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]CapabilityDescriptor, len(all))
	copy(out, all)
	return out
}

func ForDriver(driver any) []CapabilityDescriptor {
	mu.RLock()
	defer mu.RUnlock()
	var out []CapabilityDescriptor
	for _, cd := range all {
		if cd.Match(driver) {
			out = append(out, cd)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}
