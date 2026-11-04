package capabilities

import (
	"fmt"
	"slices"
	"strings"

	"kubendt/actions"
	"kubendt/types"
)

// STPCapable runs spanning tree on a bridge: the Linux bridge speaks classic
// STP, Open vSwitch STP or RSTP. The first mode a node lists is its default.
// Priority and cost are plain numbers, the node rejects what it cannot take.
type STPCapable interface {
	STPModes() []string
	STPEnable(bridge, mode string) [][]string
	STPDisable(bridge string) [][]string
	STPSetBridgePriority(bridge string, priority int) [][]string
	// STPSetPort leaves a value alone when it is 0 for the cost and -1 for
	// the priority, since 0 is a priority.
	STPSetPort(port string, cost, priority int) [][]string
}

type STPInspector interface {
	STPBridges(namespace, pod string) (Rows, error)
	STPPorts(namespace, pod string) (Rows, error)
}

func init() {
	actions.Register[STPCapable](
		actions.Capability{ID: "STPCapable", Label: "Spanning tree", Description: "STP or RSTP on a bridge, its priority, and the cost and priority of its ports."},
		actions.Action[STPCapable]{Type: "stp_enable", Label: "Enable spanning tree", Params: actions.P(actions.Bridge, actions.Mode), State: "stp", Key: keyBridge,
			Run: func(c STPCapable, a types.ActionEntry) ([][]string, error) {
				modes := c.STPModes()
				mode := a.Mode
				if mode == "" {
					mode = modes[0]
				}
				if !slices.Contains(modes, mode) {
					return nil, fmt.Errorf("mode must be one of %s, got %q", strings.Join(modes, ", "), a.Mode)
				}
				return c.STPEnable(a.Bridge, mode), nil
			}},
		actions.Action[STPCapable]{Type: "stp_disable", Label: "Disable spanning tree", Params: actions.P(actions.Bridge), State: "stp", Key: keyBridge, Clears: true, ClearsAlso: []string{"stp_priority"},
			Run: func(c STPCapable, a types.ActionEntry) ([][]string, error) { return c.STPDisable(a.Bridge), nil }},
		actions.Action[STPCapable]{Type: "stp_set_bridge_priority", Label: "Set bridge priority", Params: actions.P(actions.Bridge, actions.Priority), State: "stp_priority", Key: keyBridge,
			Run: func(c STPCapable, a types.ActionEntry) ([][]string, error) {
				if *a.Priority > 65535 {
					return nil, fmt.Errorf("priority must be at most 65535, got %d", *a.Priority)
				}
				return c.STPSetBridgePriority(a.Bridge, *a.Priority), nil
			}},
		actions.Action[STPCapable]{Type: "stp_set_port", Label: "Set port cost and priority", Params: actions.P(actions.Iface, actions.Cost.Optional(), actions.Priority.Optional()), State: "stp_port", Key: keyIface,
			Run: func(c STPCapable, a types.ActionEntry) ([][]string, error) {
				priority := -1
				if a.Priority != nil {
					priority = *a.Priority
				}
				if a.Cost == 0 && priority < 0 {
					return nil, fmt.Errorf("a cost or a priority is required")
				}
				if priority > 255 {
					return nil, fmt.Errorf("priority must be at most 255, got %d", priority)
				}
				return c.STPSetPort(a.Iface, a.Cost, priority), nil
			}},
	)
	actions.RegisterInspections[STPInspector]("STPCapable",
		actions.Inspection[STPInspector]{ID: "stp_bridges", Label: "Spanning tree bridges", Columns: cols("bridge", "Bridge", "mode", "Mode", "priority", "Priority", "bridge_id", "Bridge ID", "root_id", "Root ID", "root", "Root", "root_port", "Root port"),
			Run: func(c STPInspector, ns, pod string) (Rows, error) { return c.STPBridges(ns, pod) }},
		actions.Inspection[STPInspector]{ID: "stp_ports", Label: "Spanning tree ports", Columns: cols("bridge", "Bridge", "port", "Port", "role", "Role", "state", "State", "cost", "Cost", "priority", "Priority"),
			Run: func(c STPInspector, ns, pod string) (Rows, error) { return c.STPPorts(ns, pod) }})
}
