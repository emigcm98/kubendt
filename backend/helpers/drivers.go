package helpers

import (
	"fmt"

	"kubendt/actions"
	drivers_meta "kubendt/drivers/meta"
	drvreg "kubendt/drivers/registry"
	"kubendt/executor"
)

// DriverCaps is a internal DTO for transporting necessary information to the handlers.
// InterfaceNameConstraints is nil for drivers that don't implement
// drivers_meta.InterfaceNameConstrainer, keeps the JSON output free of an
// empty object for those.
type DriverCaps struct {
	Name                     string
	Type                     string
	Executor                 string
	IsDefault                bool
	Capabilities             []actions.CapabilityActions
	InterfaceNameConstraints *drivers_meta.InterfaceNameConstraints
	Tools                    []string
	KnownImages              []string
}

// ResolveDriverCaps: resolves type + capabilities for a driver by name.
func ResolveDriverCaps(name string) (DriverCaps, error) {
	typ, ok := drvreg.TypeOf(name)
	if !ok {
		return DriverCaps{}, fmt.Errorf("driver %q not registered", name)
	}
	inst, err := drvreg.NewByName(name)
	if err != nil {
		return DriverCaps{}, err
	}
	_, executorName, err := executor.ResolveForDriver(inst)
	if err != nil {
		return DriverCaps{}, err
	}
	var constraints *drivers_meta.InterfaceNameConstraints
	if c, ok := inst.(drivers_meta.InterfaceNameConstrainer); ok {
		v := c.InterfaceNameConstraints()
		constraints = &v
	}

	return DriverCaps{
		Name:                     name,
		Type:                     typ,
		Executor:                 executorName,
		IsDefault:                drvreg.IsDefaultForType(name),
		Capabilities:             actions.ForDriver(inst),
		InterfaceNameConstraints: constraints,
		Tools:                    DriverTools(inst),
		KnownImages:              knownImages(inst),
	}, nil
}

// ListAllDriversCaps: lists all drivers with their resolved capabilities.
func ListAllDriversCaps() ([]DriverCaps, error) {
	all := drvreg.ListAll()
	out := make([]DriverCaps, 0, len(all))
	for _, d := range all {
		dc, err := ResolveDriverCaps(d.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, dc)
	}
	return out, nil
}

// knownImages is what the driver was tried with, empty for one that takes
// any image.
func knownImages(inst any) []string {
	if kp, ok := inst.(drivers_meta.KnownImagesProvider); ok {
		return append([]string(nil), kp.KnownImages()...)
	}
	return nil
}
