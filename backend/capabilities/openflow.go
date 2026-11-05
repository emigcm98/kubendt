package capabilities

import (
	"fmt"
	"regexp"
	"strings"

	"kubendt/actions"
	"kubendt/types"
)

// OpenFlowCapable is the OpenFlow side of a switch: the controller it talks
// to, what it does without one, the versions it offers, and flows installed
// by hand, which make the switch programmable with no controller at all.
// The controller as a node is a capability of its own.
type OpenFlowCapable interface {
	SetController(bridge, target, protocols string) [][]string
	RemoveController(bridge string) [][]string
	SetFailMode(bridge, mode string) [][]string
	// SetDatapathID fixes the id the bridge presents to its controller, so
	// a controller's configuration can name it; id is up to 16 hex digits.
	SetDatapathID(bridge, id string) [][]string
	AddFlow(bridge, flow string) [][]string
	// DeleteFlow takes the match of the flow, priority included, as add_flow
	// got it without its actions.
	DeleteFlow(bridge, match string) [][]string
}

type OpenFlowInspector interface {
	OpenFlowBridges(namespace, pod string) (Rows, error)
	OpenFlowFlows(namespace, pod string) (Rows, error)
}

var (
	controllerTargetRe = regexp.MustCompile(`^(tcp|ssl):(\[[0-9A-Fa-f:.]+\]|[^\s\[\]:]+)(:\d{1,5})?$`)
	protocolsRe        = regexp.MustCompile(`^OpenFlow1[0-5](,OpenFlow1[0-5])*$`)
	flowActionsRe      = regexp.MustCompile(`[, ]*actions=.*$`)
	datapathIDRe       = regexp.MustCompile(`^(0x)?[0-9A-Fa-f]{1,16}$`)
)

// DatapathID is the id as a switch stores it: 16 lowercase hex digits, no
// prefix, what a controller's configuration gives as 0x1 among others.
func DatapathID(id string) string {
	id = strings.ToLower(strings.TrimPrefix(strings.ToLower(id), "0x"))
	return strings.Repeat("0", 16-len(id)) + id
}

// FlowMatch is the flow spec without its actions, what identifies a flow in
// the table and what del-flows takes.
func FlowMatch(flow string) string { return flowActionsRe.ReplaceAllString(flow, "") }

func keyFlow(a types.ActionEntry) string { return a.Bridge + " " + FlowMatch(a.Flow) }

func checkFlowText(flow string) error {
	if strings.ContainsAny(flow, "\n\r\"'`") {
		return fmt.Errorf("flow must be one line without quotes, got %q", flow)
	}
	return nil
}

func init() {
	actions.Register[OpenFlowCapable](
		actions.Capability{ID: "OpenFlowCapable", Label: "OpenFlow", Description: "The controller of a bridge, its fail mode and flows installed by hand."},
		actions.Action[OpenFlowCapable]{Type: "set_controller", Label: "Set controller", Params: actions.P(actions.Bridge, actions.Controller, actions.Protocols), State: "openflow_controller", Key: keyBridge,
			Run: func(c OpenFlowCapable, a types.ActionEntry) ([][]string, error) {
				if !controllerTargetRe.MatchString(a.Controller) {
					return nil, fmt.Errorf("controller must be tcp:<host>[:port] or ssl:<host>[:port], got %q", a.Controller)
				}
				if a.Protocols != "" && !protocolsRe.MatchString(a.Protocols) {
					return nil, fmt.Errorf("protocols must list OpenFlow10 to OpenFlow15 separated by commas, got %q", a.Protocols)
				}
				return c.SetController(a.Bridge, a.Controller, a.Protocols), nil
			}},
		actions.Action[OpenFlowCapable]{Type: "remove_controller", Label: "Remove controller", Params: actions.P(actions.Bridge), State: "openflow_controller", Key: keyBridge, Clears: true,
			Run: func(c OpenFlowCapable, a types.ActionEntry) ([][]string, error) {
				return c.RemoveController(a.Bridge), nil
			}},
		actions.Action[OpenFlowCapable]{Type: "set_fail_mode", Label: "Set fail mode", Params: actions.P(actions.Bridge, actions.FailMode), State: "openflow_fail_mode", Key: keyBridge,
			Run: func(c OpenFlowCapable, a types.ActionEntry) ([][]string, error) {
				if a.Mode != "secure" && a.Mode != "standalone" {
					return nil, fmt.Errorf("mode must be secure or standalone, got %q", a.Mode)
				}
				return c.SetFailMode(a.Bridge, a.Mode), nil
			}},
		actions.Action[OpenFlowCapable]{Type: "set_datapath_id", Label: "Set datapath id", Params: actions.P(actions.Bridge, actions.DatapathID), State: "openflow_datapath_id", Key: keyBridge,
			Run: func(c OpenFlowCapable, a types.ActionEntry) ([][]string, error) {
				if !datapathIDRe.MatchString(a.DatapathID) {
					return nil, fmt.Errorf("datapath_id must be up to 16 hex digits, got %q", a.DatapathID)
				}
				return c.SetDatapathID(a.Bridge, DatapathID(a.DatapathID)), nil
			}},
		actions.Action[OpenFlowCapable]{Type: "add_flow", Label: "Add flow", Params: actions.P(actions.Bridge, actions.Flow), State: "openflow_flow", Key: keyFlow,
			Run: func(c OpenFlowCapable, a types.ActionEntry) ([][]string, error) {
				if err := checkFlowText(a.Flow); err != nil {
					return nil, err
				}
				if !strings.Contains(a.Flow, "actions=") {
					return nil, fmt.Errorf("flow must end with its actions, got %q", a.Flow)
				}
				return c.AddFlow(a.Bridge, a.Flow), nil
			}},
		actions.Action[OpenFlowCapable]{Type: "delete_flow", Label: "Delete flow", Params: actions.P(actions.Bridge, actions.Flow), State: "openflow_flow", Key: keyFlow, Clears: true,
			Run: func(c OpenFlowCapable, a types.ActionEntry) ([][]string, error) {
				if err := checkFlowText(a.Flow); err != nil {
					return nil, err
				}
				return c.DeleteFlow(a.Bridge, FlowMatch(a.Flow)), nil
			}},
	)
	actions.RegisterInspections[OpenFlowInspector]("OpenFlowCapable",
		actions.Inspection[OpenFlowInspector]{ID: "openflow_bridges", Label: "OpenFlow bridges", Columns: append(cols("bridge", "Bridge", "controller", "Controller", "connected", "Connected", "fail_mode", "Fail mode", "protocols", "Protocols"), actions.Column{Name: "datapath_id", Label: "DPID", Description: "The datapath id the controller sees"}),
			Run: func(c OpenFlowInspector, ns, pod string) (Rows, error) { return c.OpenFlowBridges(ns, pod) }},
		actions.Inspection[OpenFlowInspector]{ID: "openflow_flows", Label: "OpenFlow flows", Columns: cols("bridge", "Bridge", "table", "Table", "priority", "Priority", "match", "Match", "actions", "Actions", "packets", "Packets"),
			Run: func(c OpenFlowInspector, ns, pod string) (Rows, error) { return c.OpenFlowFlows(ns, pod) }})
}
