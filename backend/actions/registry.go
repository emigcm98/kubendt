// Package actions is the one declaration of every configuration action: its
// type, label, parameters and the capability method that builds its commands.
// Dispatch, validation and GET /drivers derive from it, so an action is added
// in one place and a driver supports it by implementing the interface.
package actions

import (
	"fmt"
	"reflect"
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
	// ExecutorFromRequest lets the request pick the executor in its
	// `executor` field, checked against the registered ones.
	ExecutorFromRequest bool
	// Repeatable actions are never skipped as already applied: the user
	// asked for the command to run, not for a state.
	Repeatable bool
	// AllowProtectedIface lets the action name eth0, the pod's cluster
	// interface. Only the SNAT toggles do.
	AllowProtectedIface bool
	// State names what the action sets or clears on the node (an address,
	// the default route, the link state of an interface), so the history
	// keeps the last word on each piece of state instead of stacking a set,
	// its undo and a set again. Key tells two pieces of the same state
	// apart; nil when a node has one. An action without a State is
	// independent of the others.
	State string
	Key   func(a types.ActionEntry) string
	// Clears marks the side of a pair that undoes the state.
	Clears bool
	// UndoPersists keeps a clearing action in the history, for state the
	// platform or the cluster would put back on their own: an address the
	// link declares, the cluster's default route, an interface that comes
	// up by itself. Otherwise an undo and the action it undoes cancel each
	// other and neither is kept, since nothing would put the state back.
	UndoPersists bool
	// ClearsAlso names the states this action takes down with its own: on
	// its own key when it has one (an interface leaving IS-IS takes its
	// passive flag and metric), on every key when it has none (stopping a
	// routing process removes its neighbours and what it announced).
	ClearsAlso []string
	Run        func(c C, a types.ActionEntry) ([][]string, error)
}

// Descriptor is a registered action, usable without knowing C.
type Descriptor struct {
	Type                string
	Label               string
	Capability          string
	Params              []Param
	Executor            string
	ExecutorFromRequest bool
	Repeatable          bool
	AllowProtectedIface bool
	State               string
	Key                 func(a types.ActionEntry) string
	Clears              bool
	UndoPersists        bool
	ClearsAlso          []string
	// Universal actions are declared against any driver: they match every
	// driver and a pod without one.
	Universal bool
	supports  func(driver any) bool
	run       func(driver any, a types.ActionEntry) ([][]string, error)
}

// StateKey is the key of the state a sets or clears, "" when the state is
// one per node or the action has none.
func (d *Descriptor) StateKey(a types.ActionEntry) string {
	if d.Key == nil {
		return ""
	}
	return d.Key(a)
}

// CapabilityActions is a capability with the actions it declares and, for
// one driver, the inspections it offers of it.
type CapabilityActions struct {
	Capability
	Actions     []*Descriptor
	Inspections []*InspectionDescriptor
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
	universal := isUniversal[C]()
	supports := func(driver any) bool {
		if universal {
			return true
		}
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
		if (act.Clears || act.UndoPersists) && act.State == "" {
			panic(fmt.Sprintf("actions: %s clears a state it does not name", act.Type))
		}
		if act.UndoPersists && !act.Clears {
			panic(fmt.Sprintf("actions: %s is not an undo, UndoPersists means nothing on it", act.Type))
		}
		run := act.Run
		d := &Descriptor{
			Type:                act.Type,
			Label:               act.Label,
			Capability:          capability.ID,
			Params:              act.Params,
			Executor:            act.Executor,
			ExecutorFromRequest: act.ExecutorFromRequest,
			Repeatable:          act.Repeatable,
			AllowProtectedIface: act.AllowProtectedIface,
			State:               act.State,
			Key:                 act.Key,
			Clears:              act.Clears,
			UndoPersists:        act.UndoPersists,
			ClearsAlso:          act.ClearsAlso,
			Universal:           universal,
			supports:            supports,
			run: func(driver any, a types.ActionEntry) ([][]string, error) {
				c, ok := driver.(C)
				if !ok && !universal {
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

// isUniversal reports whether C is the empty interface, the capability of
// the actions that need no driver.
func isUniversal[C any]() bool {
	t := reflect.TypeOf((*C)(nil)).Elem()
	return t.Kind() == reflect.Interface && t.NumMethod() == 0
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
			ca := c.CapabilityActions
			ca.Inspections = inspectionsOf(c.ID, driver)
			out = append(out, ca)
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
