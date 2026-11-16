package types

// Node types a topology may declare; ue and gnb are radio roles of a host.
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

// BaseNodeType is the type the platform treats t as: host for ue and gnb.
func BaseNodeType(t string) string {
	switch t {
	case NodeTypeUE, NodeTypeGNB:
		return NodeTypeHost
	}
	return t
}
