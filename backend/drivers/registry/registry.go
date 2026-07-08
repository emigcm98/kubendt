package drivers_registry

import (
	"fmt"

	caps "kubendt/capabilities/capabilities"
)

type Driver interface {
	Name() string
	Type() string // "host" | "switch" | "router"
}

// checkTypeContract enforces the minimum capabilities a driver must implement
// for its type: every driver needs L2, routers and hosts need L3, switches need
// bridge ops. These are minimums, not prohibitions (a router may also do
// OSPF/NAT). Register panics on a violation so it surfaces at startup.
func checkTypeContract(inst any, name, typ string) error {
	if _, ok := inst.(caps.L2Capable); !ok {
		return fmt.Errorf("driver %q (type %q) must implement L2Capable", name, typ)
	}
	switch typ {
	case "router", "host":
		if _, ok := inst.(caps.L3Capable); !ok {
			return fmt.Errorf("driver %q (type %q) must implement L3Capable", name, typ)
		}
	case "switch":
		if _, ok := inst.(caps.SwitchCapable); !ok {
			return fmt.Errorf("driver %q (type %q) must implement SwitchCapable", name, typ)
		}
	}
	return nil
}

var (
	registryCtor = map[string]func() any{} // name -> constructor
	registryType = map[string]string{}     // name -> logical type
)

var DefaultDriverByType = map[string]string{
	"host":   "BasicHostDriver",
	"switch": "LinuxSwitchDriver",
	"router": "LinuxRouterDriver",
}

// Register: PASA UN CONSTRUCTOR TIPADO (func() T) donde T satisface Driver.
func Register[T Driver](ctor func() T) {
	inst := ctor()
	name := inst.Name()
	typ := inst.Type()
	if name == "" || typ == "" {
		panic("drivers_registry.Register: constructor must set Meta (Name/Type) before returning")
	}
	if err := checkTypeContract(any(inst), name, typ); err != nil {
		panic("drivers_registry.Register: " + err.Error())
	}
	registryCtor[name] = func() any {
		return ctor()
	}
	registryType[name] = typ
}

func IsRegistered(name string) bool     { _, ok := registryCtor[name]; return ok }
func TypeOf(name string) (string, bool) { v, ok := registryType[name]; return v, ok }

func ValidDriversForType(t string) []string {
	out := []string{}
	for name, typ := range registryType {
		if typ == t {
			out = append(out, name)
		}
	}
	return out
}

// NewByName: creates a NEW instance using the registered constructor.
func NewByName(name string) (any, error) {
	ctor, ok := registryCtor[name]
	if !ok {
		return nil, fmt.Errorf("driver %q not registered", name)
	}
	return ctor(), nil
}

func ResolveDefaultForType(t string) (string, error) {
	if d, ok := DefaultDriverByType[t]; ok {
		return d, nil
	}
	return "", fmt.Errorf("no hay driver por defecto para type=%q", t)
}

func ListAll() []struct {
	Name string
	Type string
} {
	out := make([]struct {
		Name string
		Type string
	}, 0, len(registryType))
	for name, t := range registryType {
		out = append(out, struct {
			Name string
			Type string
		}{Name: name, Type: t})
	}
	return out
}

// IsDefaultForType indicates whether 'name' is the default driver for its logical type.
func IsDefaultForType(name string) bool {
	t, ok := registryType[name]
	if !ok {
		return false
	}
	def, ok := DefaultDriverByType[t]
	return ok && def == name
}
