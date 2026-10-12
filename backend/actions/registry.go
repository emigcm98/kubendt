// Package actions is the one declaration of every configuration action: its
// type, label, parameters and the capability method that builds its commands.
// Dispatch, validation and GET /drivers derive from it, so an action is added
// in one place and a driver supports it by implementing the interface.
package actions

import (
	"fmt"
	"sort"
	"sync"

	"kubendt/types"
)

// Capability groups the actions behind one interface. The ID is what
// GET /drivers shows and what an error names when a driver lacks it.
type Capability struct {
	ID          string
	Label       string
	Description string
}

// Action declares one action of the capability interface C. Run gets the
// driver already asserted to C and the action as the API received it.
type Action[C any] struct {
	Type   string
	Label  string
	Params []Param
	// Executor pins the executor for actions that run in the pod's netns
	// whatever the driver. Empty leaves the choice to the driver.
	Executor string
	// AllowProtectedIface lets the action name eth0, the pod's cluster
	// interface. Only the SNAT toggles do.
	AllowProtectedIface bool
	Run                 func(c C, a types.ActionEntry) ([][]string, error)
}

// Descriptor is a registered action, usable without knowing C.
type Descriptor struct {
	Type                string
	Label               string
	Capability          string
	Params              []Param
	Executor            string
	AllowProtectedIface bool
	supports            func(driver any) bool
	run                 func(driver any, a types.ActionEntry) ([][]string, error)
}

// CapabilityActions is a capability with the actions it declares.
type CapabilityActions struct {
	Capability
	Actions []*Descriptor
}

type registered struct {
	CapabilityActions
	supports func(driver any) bool
}

var (
	mu           sync.RWMutex
	byType       = map[string]*Descriptor{}
	capabilities []*registered
)

// Register declares the actions of one capability. It panics on an action
// type registered twice or a param that is not a field of ActionEntry, so a
// bad declaration fails at startup and in every test, not on a request.
func Register[C any](capability Capability, acts ...Action[C]) {
	mu.Lock()
	defer mu.Unlock()
	supports := func(driver any) bool {
		_, ok := driver.(C)
		return ok
	}
	entry := &registered{CapabilityActions: CapabilityActions{Capability: capability}, supports: supports}
	for _, act := range acts {
		if _, dup := byType[act.Type]; dup {
			panic(fmt.Sprintf("actions: %q is registered twice", act.Type))
		}
		for _, p := range act.Params {
			if !hasField(p.Name) {
				panic(fmt.Sprintf("actions: %s declares param %q, which is not a field of ActionEntry", act.Type, p.Name))
			}
		}
		run := act.Run
		d := &Descriptor{
			Type:                act.Type,
			Label:               act.Label,
			Capability:          capability.ID,
			Params:              act.Params,
			Executor:            act.Executor,
			AllowProtectedIface: act.AllowProtectedIface,
			supports:            supports,
			run: func(driver any, a types.ActionEntry) ([][]string, error) {
				c, ok := driver.(C)
				if !ok {
					return nil, fmt.Errorf("driver does not implement %s", capability.ID)
				}
				return run(c, a)
			},
		}
		byType[act.Type] = d
		entry.Actions = append(entry.Actions, d)
	}
	capabilities = append(capabilities, entry)
}

// Lookup returns the action registered under actionType.
func Lookup(actionType string) (*Descriptor, bool) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := byType[actionType]
	return d, ok
}

// Types lists every registered action type, sorted.
func Types() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(byType))
	for t := range byType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// ForDriver returns the capabilities the driver implements with their
// actions, sorted by ID. Universal capabilities match every driver.
func ForDriver(driver any) []CapabilityActions {
	mu.RLock()
	defer mu.RUnlock()
	var out []CapabilityActions
	for _, c := range capabilities {
		if c.supports(driver) {
			out = append(out, c.CapabilityActions)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Supports reports whether the driver implements the action's capability.
func (d *Descriptor) Supports(driver any) bool { return d.supports(driver) }

// Run builds the commands of the action on the driver. Validate first, Run
// trusts the request.
func (d *Descriptor) Run(driver any, a types.ActionEntry) ([][]string, error) {
	return d.run(driver, a)
}
