package types

// Node types. host, switch and router are what the platform acts on: the
// type picks the default driver, the minimum capabilities, the sysctls and
// the readiness of a node. ue and gnb are radio roles of a host: a terminal
// and a base station of a radio access network. To the platform they are
// hosts in every respect (BaseNodeType), and the dashboard draws them as
// what they are.
const (
	NodeTypeHost   = "host"
	NodeTypeSwitch = "switch"
	NodeTypeRouter = "router"
	NodeTypeUE     = "ue"
	NodeTypeGNB    = "gnb"
)

// NodeTypesHuman is the list a validation error shows.
const NodeTypesHuman = "host|switch|router|ue|gnb"

// ValidNodeType reports whether t is a node type a topology may declare.
func ValidNodeType(t string) bool {
	switch t {
	case NodeTypeHost, NodeTypeSwitch, NodeTypeRouter, NodeTypeUE, NodeTypeGNB:
		return true
	}
	return false
}

// BaseNodeType is the type the platform treats t as: host for the radio
// roles, t itself otherwise.
func BaseNodeType(t string) string {
	switch t {
	case NodeTypeUE, NodeTypeGNB:
		return NodeTypeHost
	}
	return t
}
