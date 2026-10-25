package actions

import (
	"fmt"
	"net"
	"reflect"
	"sort"
	"strings"

	"kubendt/executor"
	"kubendt/types"
)

// Kind is what a parameter holds. It drives validation and tells the UI
// what to offer: an interface picker, an address field, a port.
type Kind string

const (
	KindIface     Kind = "iface"
	KindIfaceList Kind = "iface_list"
	KindCIDR      Kind = "cidr" // an address with a prefix length, or a bare address
	KindIP        Kind = "ip"
	KindPort      Kind = "port"
	KindText      Kind = "text"
	KindObject    Kind = "object"   // a nested spec the action validates itself
	KindCommand   Kind = "command"  // a string or an argv array
	KindExecutor  Kind = "executor" // the name of a registered executor
	KindFamily    Kind = "family"   // "4" or "6"
	KindNumber    Kind = "number"   // a non-negative integer, the action checks its range
)

// Param is one field of ActionEntry an action reads, by its JSON name.
type Param struct {
	Name     string
	Kind     Kind
	Required bool
}

// Optional returns the same param without the required flag.
func (p Param) Optional() Param {
	p.Required = false
	return p
}

// P builds a param list.
func P(params ...Param) []Param { return params }

// The fields of ActionEntry, by the JSON name the API takes.
var (
	Iface         = Param{Name: "iface", Kind: KindIface, Required: true}
	Ifaces        = Param{Name: "ifaces", Kind: KindIfaceList, Required: true}
	CIDR          = Param{Name: "cidr", Kind: KindCIDR, Required: true}
	Gateway       = Param{Name: "gateway", Kind: KindIP, Required: true}
	DstCIDR       = Param{Name: "dst_cidr", Kind: KindCIDR, Required: true}
	Device        = Param{Name: "device", Kind: KindIface, Required: true}
	Bridge        = Param{Name: "bridge", Kind: KindText, Required: true}
	ExternalPort  = Param{Name: "externalPort", Kind: KindPort, Required: true}
	InternalIP    = Param{Name: "internalIP", Kind: KindIP, Required: true}
	InternalPort  = Param{Name: "internalPort", Kind: KindPort, Required: true}
	Protocol      = Param{Name: "protocol", Kind: KindText, Required: true}
	DNSServer     = Param{Name: "dns_server", Kind: KindIP, Required: true}
	DNSDomain     = Param{Name: "dns_domain", Kind: KindText, Required: true}
	TCParams      = Param{Name: "tcparams", Kind: KindObject, Required: true}
	OSPFArea      = Param{Name: "ospf_area", Kind: KindText, Required: true}
	RouterID      = Param{Name: "router_id", Kind: KindIP, Required: true}
	Command       = Param{Name: "command", Kind: KindCommand, Required: true}
	Executor      = Param{Name: "executor", Kind: KindExecutor, Required: false}
	Family        = Param{Name: "family", Kind: KindFamily, Required: false}
	Redistribute  = Param{Name: "redistribute", Kind: KindText, Required: true}
	Timers        = Param{Name: "timers", Kind: KindObject, Required: true}
	Cost          = Param{Name: "cost", Kind: KindNumber, Required: true}
	Priority      = Param{Name: "priority", Kind: KindNumber, Required: true}
	HelloInterval = Param{Name: "hello_interval", Kind: KindNumber, Required: true}
	DeadInterval  = Param{Name: "dead_interval", Kind: KindNumber, Required: true}
	NetworkType   = Param{Name: "network_type", Kind: KindText, Required: true}
	AreaType      = Param{Name: "area_type", Kind: KindText, Required: true}
	AuthType      = Param{Name: "auth_type", Kind: KindText, Required: true}
	AuthKey       = Param{Name: "auth_key", Kind: KindText, Required: true}
	AuthKeyID     = Param{Name: "auth_key_id", Kind: KindNumber, Required: false}
)

// ProtectedInterface is the pod's cluster interface. An action on it would
// cut the pod off, so only the actions that say so may name it.
const ProtectedInterface = "eth0"

// fieldIndex maps every JSON name of ActionEntry to its field, built once so
// a param is read by the name the API uses and never by a second copy.
var fieldIndex = func() map[string][]int {
	out := map[string][]int{}
	t := reflect.TypeOf(types.ActionEntry{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out[name] = f.Index
	}
	return out
}()

func hasField(name string) bool {
	_, ok := fieldIndex[name]
	return ok
}

// fieldValue returns a param's value and whether the request set it: a
// non-empty string or slice, a non-zero number, a non-nil pointer.
func fieldValue(a types.ActionEntry, name string) (reflect.Value, bool) {
	v := reflect.ValueOf(a).FieldByIndex(fieldIndex[name])
	switch v.Kind() {
	case reflect.String:
		return v, v.String() != ""
	case reflect.Int:
		return v, v.Int() != 0
	case reflect.Slice:
		return v, v.Len() > 0
	case reflect.Pointer:
		return v, !v.IsNil()
	default:
		return v, !v.IsZero()
	}
}

// Validate checks a request against the declared params before anything
// runs: required fields present, each value of its kind, and no interface
// param naming eth0 unless the action may. The error names the field.
func (d *Descriptor) Validate(a types.ActionEntry) error {
	for _, p := range d.Params {
		v, present := fieldValue(a, p.Name)
		if !present {
			if p.Required {
				return fmt.Errorf("%s: missing required field %q", d.Type, p.Name)
			}
			continue
		}
		if err := checkKind(p.Kind, v); err != nil {
			return fmt.Errorf("%s: field %q %w", d.Type, p.Name, err)
		}
		if d.AllowProtectedIface {
			continue
		}
		for _, name := range ifaceNames(p.Kind, v) {
			if name == ProtectedInterface {
				return fmt.Errorf("action %q is not allowed on protected interface %s (only %s)", d.Type, ProtectedInterface, strings.Join(allowedOnProtected(), ", "))
			}
		}
	}
	return nil
}

func checkKind(k Kind, v reflect.Value) error {
	switch k {
	case KindIface:
		return types.ValidateLinuxInterfaceName(v.String())
	case KindIfaceList:
		for i := 0; i < v.Len(); i++ {
			if err := types.ValidateLinuxInterfaceName(v.Index(i).String()); err != nil {
				return err
			}
		}
	case KindCIDR:
		s := v.String()
		if _, _, err := net.ParseCIDR(s); err != nil && net.ParseIP(s) == nil {
			return fmt.Errorf("must be an address with a prefix length, got %q", s)
		}
	case KindIP:
		if net.ParseIP(v.String()) == nil {
			return fmt.Errorf("must be an IP address, got %q", v.String())
		}
	case KindPort:
		if n := v.Int(); n < 1 || n > 65535 {
			return fmt.Errorf("must be a port between 1 and 65535, got %d", n)
		}
	case KindExecutor:
		if _, err := executor.Get(v.String()); err != nil {
			return fmt.Errorf("must name a registered executor, got %q", v.String())
		}
	case KindFamily:
		if s := v.String(); s != "4" && s != "6" {
			return fmt.Errorf("must be 4 or 6, got %q", s)
		}
	case KindNumber:
		n := v
		if n.Kind() == reflect.Pointer {
			n = n.Elem()
		}
		if n.Int() < 0 {
			return fmt.Errorf("must not be negative, got %d", n.Int())
		}
	}
	return nil
}

// MapInterfaces returns the action with every interface param passed through
// f, for drivers whose dataplane names interfaces differently from the pod.
// The request itself is left untouched.
func (d *Descriptor) MapInterfaces(a types.ActionEntry, f func(string) (string, error)) (types.ActionEntry, error) {
	v := reflect.ValueOf(&a).Elem()
	for _, p := range d.Params {
		field := v.FieldByIndex(fieldIndex[p.Name])
		switch p.Kind {
		case KindIface:
			if field.String() == "" {
				continue
			}
			mapped, err := f(field.String())
			if err != nil {
				return a, err
			}
			field.SetString(mapped)
		case KindIfaceList:
			copied := reflect.MakeSlice(field.Type(), field.Len(), field.Len())
			reflect.Copy(copied, field)
			for i := 0; i < copied.Len(); i++ {
				mapped, err := f(copied.Index(i).String())
				if err != nil {
					return a, err
				}
				copied.Index(i).SetString(mapped)
			}
			field.Set(copied)
		}
	}
	return a, nil
}

func ifaceNames(k Kind, v reflect.Value) []string {
	switch k {
	case KindIface:
		return []string{v.String()}
	case KindIfaceList:
		out := make([]string, v.Len())
		for i := range out {
			out[i] = v.Index(i).String()
		}
		return out
	}
	return nil
}

// allowedOnProtected lists the actions that may name eth0, for the message.
func allowedOnProtected() []string {
	mu.RLock()
	defer mu.RUnlock()
	var out []string
	for t, d := range byType {
		if d.AllowProtectedIface {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}
