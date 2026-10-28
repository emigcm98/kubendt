package actions

import (
	"fmt"
	"sort"
	"sync"
)

// Column is one field of an inspection row.
type Column struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// Inspection declares one read of the interface C: live rows with the
// declared columns, never from the history. It is listed under a capability
// so the panel groups it with the actions of its protocol, and a driver
// offers it by implementing C, which need not be the capability's own
// interface.
type Inspection[C any] struct {
	ID      string
	Label   string
	Columns []Column
	Run     func(c C, namespace, pod string) ([]map[string]string, error)
}

// InspectionDescriptor is an inspection as registered, driver-agnostic.
type InspectionDescriptor struct {
	ID         string
	Label      string
	Capability string
	Columns    []Column
	supports   func(driver any) bool
	run        func(driver any, namespace, pod string) ([]map[string]string, error)
}

func (d *InspectionDescriptor) Supports(driver any) bool { return d.supports(driver) }

func (d *InspectionDescriptor) Run(driver any, namespace, pod string) ([]map[string]string, error) {
	return d.run(driver, namespace, pod)
}

var (
	inspMu      sync.RWMutex
	inspByID    = map[string]*InspectionDescriptor{}
	inspections []*InspectionDescriptor
)

// RegisterInspections declares the inspections of a capability, by its ID
// since the capability may register in another file. It panics on an ID
// registered twice.
func RegisterInspections[C any](capabilityID string, insp ...Inspection[C]) {
	inspMu.Lock()
	defer inspMu.Unlock()
	for _, in := range insp {
		if _, dup := inspByID[in.ID]; dup {
			panic(fmt.Sprintf("actions: inspection %q is registered twice", in.ID))
		}
		run := in.Run
		d := &InspectionDescriptor{
			ID:         in.ID,
			Label:      in.Label,
			Capability: capabilityID,
			Columns:    in.Columns,
			supports: func(driver any) bool {
				_, ok := driver.(C)
				return ok
			},
			run: func(driver any, namespace, pod string) ([]map[string]string, error) {
				c, ok := driver.(C)
				if !ok {
					return nil, fmt.Errorf("driver does not offer %s", in.ID)
				}
				return run(c, namespace, pod)
			},
		}
		inspByID[in.ID] = d
		inspections = append(inspections, d)
	}
}

// LookupInspection returns the inspection registered under id.
func LookupInspection(id string) (*InspectionDescriptor, bool) {
	inspMu.RLock()
	defer inspMu.RUnlock()
	d, ok := inspByID[id]
	return d, ok
}

// inspectionsOf is what the driver offers of a capability, sorted by ID.
func inspectionsOf(capabilityID string, driver any) []*InspectionDescriptor {
	inspMu.RLock()
	defer inspMu.RUnlock()
	var out []*InspectionDescriptor
	for _, d := range inspections {
		if d.Capability == capabilityID && d.supports(driver) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// InspectionsForDriver lists every inspection the driver offers, sorted by
// capability and ID.
func InspectionsForDriver(driver any) []*InspectionDescriptor {
	var out []*InspectionDescriptor
	for _, c := range ForDriver(driver) {
		out = append(out, c.Inspections...)
	}
	return out
}
