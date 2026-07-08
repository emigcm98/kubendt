package capabilities_base

import (
	"fmt"
	"reflect"
)

// MethodMetadata describes a method with its parameters
type MethodMetadata struct {
	Name   string
	Params []ParameterInfo
}

// ParameterInfo describes a function parameter
type ParameterInfo struct {
	Name string
	Type string
}

// methodRegistry maps method names to their parameter metadata
var methodRegistry = map[string]MethodMetadata{
	// L2 Methods
	"LinkUp": {
		Name: "LinkUp",
		Params: []ParameterInfo{
			{Name: "iface", Type: "string"},
		},
	},
	"LinkDown": {
		Name: "LinkDown",
		Params: []ParameterInfo{
			{Name: "iface", Type: "string"},
		},
	},

	// L3 Methods
	"SetIP": {
		Name: "SetIP",
		Params: []ParameterInfo{
			{Name: "iface", Type: "string"},
			{Name: "cidr", Type: "string"},
		},
	},
	"ReplaceIP": {
		Name: "ReplaceIP",
		Params: []ParameterInfo{
			{Name: "iface", Type: "string"},
			{Name: "cidr", Type: "string"},
		},
	},
	"RemoveIP": {
		Name: "RemoveIP",
		Params: []ParameterInfo{
			{Name: "iface", Type: "string"},
			{Name: "cidr", Type: "string"},
		},
	},
	"SetDefaultRoute": {
		Name: "SetDefaultRoute",
		Params: []ParameterInfo{
			{Name: "gateway", Type: "string"},
		},
	},
	"RemoveDefaultRoute": {
		Name:   "RemoveDefaultRoute",
		Params: []ParameterInfo{},
	},
	"AddStaticRoute": {
		Name: "AddStaticRoute",
		Params: []ParameterInfo{
			{Name: "dstCIDR", Type: "string"},
			{Name: "via", Type: "string"},
			{Name: "dev", Type: "string"},
		},
	},
	"RemoveStaticRoute": {
		Name: "RemoveStaticRoute",
		Params: []ParameterInfo{
			{Name: "dstCIDR", Type: "string"},
			{Name: "via", Type: "string"},
			{Name: "dev", Type: "string"},
		},
	},
	"AddDNSNameserver": {
		Name:   "AddDNSNameserver",
		Params: []ParameterInfo{{Name: "server", Type: "string"}},
	},
	"RemoveDNSNameserver": {
		Name:   "RemoveDNSNameserver",
		Params: []ParameterInfo{{Name: "server", Type: "string"}},
	},
	"AddDNSSearch": {
		Name:   "AddDNSSearch",
		Params: []ParameterInfo{{Name: "domain", Type: "string"}},
	},
	"RemoveDNSSearch": {
		Name:   "RemoveDNSSearch",
		Params: []ParameterInfo{{Name: "domain", Type: "string"}},
	},

	// Switch Methods
	"SetupBridge": {
		Name: "SetupBridge",
		Params: []ParameterInfo{
			{Name: "bridge", Type: "string"},
			{Name: "ifaces", Type: "[]string"},
		},
	},
	"TeardownBridge": {
		Name:   "TeardownBridge",
		Params: []ParameterInfo{{Name: "bridge", Type: "string"}},
	},
	"AddInterfaceToBridge": {
		Name: "AddInterfaceToBridge",
		Params: []ParameterInfo{
			{Name: "iface", Type: "string"},
			{Name: "bridge", Type: "string"},
		},
	},
	"RemoveInterfaceFromBridge": {
		Name: "RemoveInterfaceFromBridge",
		Params: []ParameterInfo{
			{Name: "iface", Type: "string"},
			{Name: "bridge", Type: "string"},
		},
	},

	// TC Methods
	"AddQdisc": {
		Name: "AddQdisc",
		Params: []ParameterInfo{
			{Name: "iface", Type: "string"},
			{Name: "qdiscType", Type: "string"},
			{Name: "params", Type: "[]string"},
		},
	},
	"DelQdisc": {
		Name:   "DelQdisc",
		Params: []ParameterInfo{{Name: "iface", Type: "string"}},
	},

	// NAT Methods
	"EnableSNAT": {
		Name:   "EnableSNAT",
		Params: []ParameterInfo{{Name: "iface", Type: "string"}},
	},
	"DisableSNAT": {
		Name:   "DisableSNAT",
		Params: []ParameterInfo{{Name: "iface", Type: "string"}},
	},
	"EnableDNAT": {
		Name: "EnableDNAT",
		Params: []ParameterInfo{
			{Name: "iface", Type: "string"},
			{Name: "externalPort", Type: "int"},
			{Name: "internalIP", Type: "string"},
			{Name: "internalPort", Type: "int"},
			{Name: "protocol", Type: "string"},
		},
	},
	"DisableDNAT": {
		Name: "DisableDNAT",
		Params: []ParameterInfo{
			{Name: "iface", Type: "string"},
			{Name: "externalPort", Type: "int"},
			{Name: "internalIP", Type: "string"},
			{Name: "internalPort", Type: "int"},
			{Name: "protocol", Type: "string"},
		},
	},

	// OSPF Methods
	"OSPFAddNetwork": {
		Name: "OSPFAddNetwork",
		Params: []ParameterInfo{
			{Name: "network", Type: "string"},
			{Name: "area", Type: "string"},
		},
	},
	"OSPFRemoveNetwork": {
		Name: "OSPFRemoveNetwork",
		Params: []ParameterInfo{
			{Name: "network", Type: "string"},
			{Name: "area", Type: "string"},
		},
	},
	"OSPFSetRouterID": {
		Name:   "OSPFSetRouterID",
		Params: []ParameterInfo{{Name: "routerID", Type: "string"}},
	},
	"OSPFRemoveRouterID": {
		Name:   "OSPFRemoveRouterID",
		Params: []ParameterInfo{},
	},
	"OSPFPassiveDefault": {
		Name:   "OSPFPassiveDefault",
		Params: []ParameterInfo{},
	},
	"OSPFRemovePassiveDefault": {
		Name:   "OSPFRemovePassiveDefault",
		Params: []ParameterInfo{},
	},
	"OSPFNoPassive": {
		Name:   "OSPFNoPassive",
		Params: []ParameterInfo{{Name: "iface", Type: "string"}},
	},
	"OSPFRemoveNoPassive": {
		Name:   "OSPFRemoveNoPassive",
		Params: []ParameterInfo{{Name: "iface", Type: "string"}},
	},
	"OSPFOriginateDefault": {
		Name:   "OSPFOriginateDefault",
		Params: []ParameterInfo{},
	},
	"OSPFRemoveOriginateDefault": {
		Name:   "OSPFRemoveOriginateDefault",
		Params: []ParameterInfo{},
	},
	"OSPFMTUIgnore": {
		Name:   "OSPFMTUIgnore",
		Params: []ParameterInfo{{Name: "iface", Type: "string"}},
	},
	"OSPFRemoveMTUIgnore": {
		Name:   "OSPFRemoveMTUIgnore",
		Params: []ParameterInfo{{Name: "iface", Type: "string"}},
	},
}

// GetMethodMetadata returns metadata for a method by name
func GetMethodMetadata(methodName string) *MethodMetadata {
	meta, ok := methodRegistry[methodName]
	if !ok {
		// If not in registry, try to extract from the method name
		// This is a fallback for methods that don't have explicit metadata
		return &MethodMetadata{
			Name:   methodName,
			Params: extractParametersFromMethodName(methodName),
		}
	}
	return &meta
}

// extractParametersFromMethodName attempts to guess parameters from method name
// This is a fallback and should be supplemented by explicit metadata
func extractParametersFromMethodName(methodName string) []ParameterInfo {
	// Fallback: no parameters guessed
	return []ParameterInfo{}
}

// ExtractMethodParameters uses reflection to extract parameter info from a method
// This is an alternative approach using reflection on actual method signatures
func ExtractMethodParameters(iface any, methodName string) []ParameterInfo {
	t := reflect.TypeOf(iface)
	if t == nil {
		return []ParameterInfo{}
	}

	// Look for the method
	method, ok := t.MethodByName(methodName)
	if !ok {
		return []ParameterInfo{}
	}

	params := []ParameterInfo{}
	methodType := method.Type

	// Skip receiver (index 0) and result (last parameter)
	// For methods returning [][]string, skip the return value
	for i := 1; i < methodType.NumIn(); i++ {
		param := methodType.In(i)
		params = append(params, ParameterInfo{
			Name: fmt.Sprintf("param%d", i),
			Type: param.String(),
		})
	}

	return params
}
