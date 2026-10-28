package capabilities

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// VtyshRunner runs one vtysh command on a node and returns its output; FRR
// execs vtysh in the pod, VyOS runs the same vtysh inside its guest, so one
// set of parsers serves both.
type VtyshRunner interface {
	Vtysh(namespace, pod, command string) ([]byte, error)
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case bool:
		if x {
			return "yes"
		}
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// A zebra route table, `show ip route json`: prefix to its routes, each
// with its nexthops; one row per nexthop.
func parseFRRRoutes(data []byte, family string) (Rows, error) {
	var table map[string][]map[string]any
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &table); err != nil {
		return nil, err
	}
	var rows Rows
	prefixes := make([]string, 0, len(table))
	for p := range table {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		for _, route := range table[prefix] {
			hops, _ := route["nexthops"].([]any)
			if len(hops) == 0 {
				hops = []any{map[string]any{}}
			}
			for _, h := range hops {
				hop, _ := h.(map[string]any)
				rows = append(rows, map[string]string{
					"destination": prefix, "family": family, "protocol": str(route["protocol"]),
					"gateway": str(hop["ip"]), "interface": str(hop["interfaceName"]),
					"distance": str(route["distance"]), "metric": str(route["metric"]), "selected": str(route["selected"]),
				})
			}
		}
	}
	return rows, nil
}

func parseFRROSPFNeighbors(data []byte) (Rows, error) {
	var doc struct {
		Neighbors map[string][]map[string]any `json:"neighbors"`
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var rows Rows
	ids := make([]string, 0, len(doc.Neighbors))
	for id := range doc.Neighbors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, n := range doc.Neighbors[id] {
			state := str(n["converged"])
			role := str(n["role"])
			if full := str(n["nbrState"]); full != "" {
				if s, r, ok := strings.Cut(full, "/"); ok {
					if state == "" {
						state = s
					}
					if role == "" {
						role = r
					}
				} else if state == "" {
					state = full
				}
			}
			iface, _, _ := strings.Cut(str(n["ifaceName"]), ":")
			rows = append(rows, map[string]string{
				"neighbor": id, "address": str(n["ifaceAddress"]), "interface": iface, "state": state, "role": role,
				"priority": str(n["nbrPriority"]), "dead_time": str(n["deadTime"]),
			})
		}
	}
	return rows, nil
}

func parseFRROSPFInterfaces(data []byte) (Rows, error) {
	var doc struct {
		Interfaces map[string]map[string]any `json:"interfaces"`
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var rows Rows
	for _, name := range sortedKeysOf(doc.Interfaces) {
		i := doc.Interfaces[name]
		address := str(i["ipAddress"])
		if l := str(i["ipAddressPrefixlen"]); address != "" && l != "" {
			address += "/" + l
		}
		rows = append(rows, map[string]string{
			"interface": name, "address": address, "area": str(i["area"]), "network_type": strings.ToLower(str(i["networkType"])),
			"state": str(i["state"]), "cost": str(i["cost"]), "priority": str(i["priority"]), "neighbors": str(i["nbrAdjacentCount"]),
		})
	}
	return rows, nil
}

func sortedKeysOf(m map[string]map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func parseFRROSPF6Neighbors(data []byte) (Rows, error) {
	var doc struct {
		Neighbors []map[string]any `json:"neighbors"`
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var rows Rows
	for _, n := range doc.Neighbors {
		rows = append(rows, map[string]string{
			"neighbor": str(n["neighborId"]), "interface": str(n["interfaceName"]), "state": str(n["state"]), "role": str(n["interfaceState"]),
			"priority": str(n["priority"]), "dead_time": str(n["deadTime"]),
		})
	}
	return rows, nil
}

// `show bgp summary json`: one block per address family, peers keyed by
// address.
func parseFRRBGPSummary(data []byte) (Rows, error) {
	var doc map[string]any
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var rows Rows
	for _, af := range []struct{ key, family string }{{"ipv4Unicast", "4"}, {"ipv6Unicast", "6"}} {
		block, _ := doc[af.key].(map[string]any)
		peers, _ := block["peers"].(map[string]any)
		for _, addr := range sortedKeys(peers) {
			p, _ := peers[addr].(map[string]any)
			rows = append(rows, map[string]string{
				"family": af.family, "neighbor": addr, "remote_as": str(p["remoteAs"]), "state": str(p["state"]),
				"prefixes_received": str(p["pfxRcd"]), "prefixes_sent": str(p["pfxSnt"]), "uptime": str(p["peerUptime"]),
			})
		}
	}
	return rows, nil
}

// `show isis neighbor json`: areas, each with circuits; a circuit with an
// adjacency carries it flat (adj, interface, level, state, expires-in),
// older releases nested the interface as an object.
func parseFRRISISNeighbors(data []byte) (Rows, error) {
	var doc struct {
		Areas []struct {
			Area     string           `json:"area"`
			Circuits []map[string]any `json:"circuits"`
		} `json:"areas"`
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var rows Rows
	for _, a := range doc.Areas {
		for _, c := range a.Circuits {
			adj := str(c["adj"])
			if adj == "" {
				continue
			}
			iface, level, state, hold := str(c["interface"]), str(c["level"]), str(c["state"]), str(c["expires-in"])
			if nested, ok := c["interface"].(map[string]any); ok {
				iface, level, state, hold = str(nested["name"]), str(nested["level"]), str(nested["state"]), str(nested["expires-in"])
			}
			rows = append(rows, map[string]string{"neighbor": adj, "interface": iface, "level": level, "state": state, "hold_time": hold})
		}
	}
	return rows, nil
}

// The reads, each one vtysh command and its parser.

func FRRRoutes(r VtyshRunner, namespace, pod string) (Rows, error) {
	v4, err := r.Vtysh(namespace, pod, "show ip route json")
	if err != nil {
		return nil, err
	}
	rows, err := parseFRRRoutes(v4, "4")
	if err != nil {
		return nil, err
	}
	v6, err := r.Vtysh(namespace, pod, "show ipv6 route json")
	if err != nil {
		return nil, err
	}
	rows6, err := parseFRRRoutes(v6, "6")
	if err != nil {
		return nil, err
	}
	return append(rows, rows6...), nil
}

func FRROSPFNeighbors(r VtyshRunner, namespace, pod string) (Rows, error) {
	out, err := r.Vtysh(namespace, pod, "show ip ospf neighbor json")
	if err != nil {
		return nil, err
	}
	return parseFRROSPFNeighbors(out)
}

func FRROSPFInterfaces(r VtyshRunner, namespace, pod string) (Rows, error) {
	out, err := r.Vtysh(namespace, pod, "show ip ospf interface json")
	if err != nil {
		return nil, err
	}
	return parseFRROSPFInterfaces(out)
}

func FRROSPF6Neighbors(r VtyshRunner, namespace, pod string) (Rows, error) {
	out, err := r.Vtysh(namespace, pod, "show ipv6 ospf6 neighbor json")
	if err != nil {
		return nil, err
	}
	return parseFRROSPF6Neighbors(out)
}

// RIP has no JSON of its own in FRR; the routes it put in zebra are the
// table that matters, v4 and v6.
func FRRRIPRoutes(r VtyshRunner, namespace, pod string) (Rows, error) {
	var rows Rows
	for _, q := range []struct{ cmd, family string }{{"show ip route rip json", "4"}, {"show ipv6 route ripng json", "6"}} {
		out, err := r.Vtysh(namespace, pod, q.cmd)
		if err != nil {
			return nil, err
		}
		parsed, err := parseFRRRoutes(out, q.family)
		if err != nil {
			return nil, err
		}
		for _, row := range parsed {
			rows = append(rows, map[string]string{"destination": row["destination"], "gateway": row["gateway"], "interface": row["interface"], "metric": row["metric"]})
		}
	}
	return rows, nil
}

func FRRBGPSummary(r VtyshRunner, namespace, pod string) (Rows, error) {
	out, err := r.Vtysh(namespace, pod, "show bgp summary json")
	if err != nil {
		return nil, err
	}
	return parseFRRBGPSummary(out)
}

func FRRISISNeighbors(r VtyshRunner, namespace, pod string) (Rows, error) {
	out, err := r.Vtysh(namespace, pod, "show isis neighbor json")
	if err != nil {
		return nil, err
	}
	return parseFRRISISNeighbors(out)
}
