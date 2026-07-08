// pkg/helpers/drivers.go
package helpers

import (
	"fmt"

	cap_metadata "kubendt/capabilities"
	capsreg "kubendt/capabilities/registry"
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
	Capabilities             []capsreg.CapabilityDescriptor
	InterfaceNameConstraints *drivers_meta.InterfaceNameConstraints
}

// MethodParamInfo describes parameters for a method - internal helper struct
type MethodParamInfo struct {
	Name   string
	Params []struct {
		Name string
		Type string
	}
}

// ExtractMethodParametersMap returns a map of method names to their parameter information
func ExtractMethodParametersMap(methodsMap map[string]string) map[string][]struct {
	Name string
	Type string
} {
	result := make(map[string][]struct {
		Name string
		Type string
	})

	for apiMethodName, implMethodName := range methodsMap {
		methodMeta := cap_metadata.GetMethodMetadata(implMethodName)
		if methodMeta != nil {
			params := make([]struct {
				Name string
				Type string
			}, len(methodMeta.Params))
			for i, p := range methodMeta.Params {
				params[i].Name = p.Name
				params[i].Type = p.Type
			}
			result[apiMethodName] = params
		}
	}
	return result
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
		Capabilities:             capsreg.ForDriver(inst),
		InterfaceNameConstraints: constraints,
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
