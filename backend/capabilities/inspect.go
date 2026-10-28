package capabilities

import (
	"regexp"

	"kubendt/actions"
)

// Rows is what an inspection returns: one map per row, the keys the
// declared columns, every value a string so the panel renders any of them.
type Rows = []map[string]string

// The inspector interfaces. A driver offers an inspection by implementing
// the interface; the bases below give the Linux reads, FRR and VyOS read
// through vtysh.
type RouteInspector interface {
	Routes(namespace, pod string) (Rows, error)
}

type NeighborInspector interface {
	Neighbors(namespace, pod string) (Rows, error)
}

type NATRuleInspector interface {
	NATRules(namespace, pod string) (Rows, error)
}

type FDBInspector interface {
	BridgeFDB(namespace, pod string) (Rows, error)
}

type OSPFInspector interface {
	OSPFNeighbors(namespace, pod string) (Rows, error)
	OSPFInterfaces(namespace, pod string) (Rows, error)
}

type OSPF6Inspector interface {
	OSPF6Neighbors(namespace, pod string) (Rows, error)
}

type RIPInspector interface {
	RIPRoutes(namespace, pod string) (Rows, error)
}

type BGPInspector interface {
	BGPSummary(namespace, pod string) (Rows, error)
}

type ISISInspector interface {
	ISISNeighbors(namespace, pod string) (Rows, error)
}

// DaemonNotRunningError is what a read answers when the daemon behind it is
// not up on the node, a router deployed before its capability existed for
// instance; the endpoint turns it into an empty result with a note.
type DaemonNotRunningError struct{ Daemon string }

func (e *DaemonNotRunningError) Error() string { return e.Daemon + " is not running" }

var notRunningRe = regexp.MustCompile(`\b([a-z0-9]+d) is not running`)

// VtyshError maps vtysh's "<daemon> is not running" to its own error and
// leaves any other failure as it came.
func VtyshError(out []byte, err error) error {
	if err == nil {
		return nil
	}
	for _, text := range []string{string(out), err.Error()} {
		if m := notRunningRe.FindStringSubmatch(text); m != nil {
			return &DaemonNotRunningError{Daemon: m[1]}
		}
	}
	return err
}

func cols(pairs ...string) []actions.Column {
	out := make([]actions.Column, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, actions.Column{Name: pairs[i], Label: pairs[i+1]})
	}
	return out
}

func init() {
	actions.RegisterInspections[RouteInspector]("L3Capable",
		actions.Inspection[RouteInspector]{ID: "routes", Label: "Routing table", Columns: append(cols("destination", "Destination", "protocol", "Protocol", "gateway", "Gateway", "interface", "Interface"), actions.Column{Name: "distance", Label: "AD", Description: "Administrative distance"}, actions.Column{Name: "metric", Label: "Metric", Description: "The protocol's own metric"}, actions.Column{Name: "status", Label: "Status"}),
			Run: func(c RouteInspector, ns, pod string) (Rows, error) { return c.Routes(ns, pod) }})
	actions.RegisterInspections[NeighborInspector]("L3Capable",
		actions.Inspection[NeighborInspector]{ID: "neighbors", Label: "ARP and NDP", Columns: cols("address", "Address", "interface", "Interface", "mac", "MAC", "state", "State"),
			Run: func(c NeighborInspector, ns, pod string) (Rows, error) { return c.Neighbors(ns, pod) }})
	actions.RegisterInspections[NATRuleInspector]("NATCapable",
		actions.Inspection[NATRuleInspector]{ID: "nat_rules", Label: "NAT rules", Columns: cols("family", "Family", "chain", "Chain", "rule", "Rule"),
			Run: func(c NATRuleInspector, ns, pod string) (Rows, error) { return c.NATRules(ns, pod) }})
	actions.RegisterInspections[FDBInspector]("SwitchCapable",
		actions.Inspection[FDBInspector]{ID: "bridge_fdb", Label: "MAC table", Columns: cols("bridge", "Bridge", "mac", "MAC", "interface", "Interface", "vlan", "VLAN", "flags", "Flags"),
			Run: func(c FDBInspector, ns, pod string) (Rows, error) { return c.BridgeFDB(ns, pod) }})
	actions.RegisterInspections[OSPFInspector]("OSPFCapable",
		actions.Inspection[OSPFInspector]{ID: "ospf_neighbors", Label: "OSPF neighbors", Columns: cols("neighbor", "Neighbor", "address", "Address", "interface", "Interface", "state", "State", "role", "Role", "priority", "Priority", "dead_time", "Dead time"),
			Run: func(c OSPFInspector, ns, pod string) (Rows, error) { return c.OSPFNeighbors(ns, pod) }},
		actions.Inspection[OSPFInspector]{ID: "ospf_interfaces", Label: "OSPF interfaces", Columns: cols("interface", "Interface", "address", "Address", "area", "Area", "network_type", "Network type", "state", "State", "cost", "Cost", "priority", "Priority", "neighbors", "Neighbors", "passive", "Passive"),
			Run: func(c OSPFInspector, ns, pod string) (Rows, error) { return c.OSPFInterfaces(ns, pod) }})
	actions.RegisterInspections[OSPF6Inspector]("OSPF6Capable",
		actions.Inspection[OSPF6Inspector]{ID: "ospf6_neighbors", Label: "OSPFv3 neighbors", Columns: cols("neighbor", "Neighbor", "interface", "Interface", "state", "State", "role", "Role", "priority", "Priority", "dead_time", "Dead time"),
			Run: func(c OSPF6Inspector, ns, pod string) (Rows, error) { return c.OSPF6Neighbors(ns, pod) }})
	actions.RegisterInspections[RIPInspector]("RIPCapable",
		actions.Inspection[RIPInspector]{ID: "rip_routes", Label: "RIP routes", Columns: cols("destination", "Destination", "gateway", "Gateway", "interface", "Interface", "metric", "Metric"),
			Run: func(c RIPInspector, ns, pod string) (Rows, error) { return c.RIPRoutes(ns, pod) }})
	actions.RegisterInspections[BGPInspector]("BGPCapable",
		actions.Inspection[BGPInspector]{ID: "bgp_summary", Label: "BGP neighbors", Columns: cols("family", "Family", "neighbor", "Neighbor", "remote_as", "Remote AS", "state", "State", "prefixes_received", "Received", "prefixes_sent", "Sent", "uptime", "Uptime"),
			Run: func(c BGPInspector, ns, pod string) (Rows, error) { return c.BGPSummary(ns, pod) }})
	actions.RegisterInspections[ISISInspector]("ISISCapable",
		actions.Inspection[ISISInspector]{ID: "isis_neighbors", Label: "IS-IS neighbors", Columns: cols("neighbor", "Neighbor", "interface", "Interface", "level", "Level", "state", "State", "hold_time", "Hold time"),
			Run: func(c ISISInspector, ns, pod string) (Rows, error) { return c.ISISNeighbors(ns, pod) }})
}
