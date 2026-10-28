package capabilities

import (
	"strings"

	"kubendt/executor"
)

// The Linux reads parse the text of iproute2 and BusyBox alike (neither
// `-j` nor a fixed column order can be counted on across images), through a
// reader that runs where the tool is: the pod, its toolbox or a guest.
type linuxReader func(argv []string) ([]byte, error)

func netnsReader(namespace, pod string) linuxReader {
	return func(argv []string) ([]byte, error) { return executor.ExecAndGetNetns(namespace, pod, argv) }
}

// fields of a line as key, value pairs after the first token: `via X`,
// `dev X`, `proto X`, `metric N` and flags without a value.
func keyed(tokens []string, valued ...string) map[string]string {
	out := map[string]string{}
	isValued := map[string]bool{}
	for _, k := range valued {
		isValued[k] = true
	}
	for i := 0; i < len(tokens); i++ {
		if isValued[tokens[i]] && i+1 < len(tokens) {
			out[tokens[i]] = tokens[i+1]
			i++
		} else {
			out[tokens[i]] = ""
		}
	}
	return out
}

func parseIPRoute(text, family string) Rows {
	var rows Rows
	for _, line := range strings.Split(text, "\n") {
		tokens := strings.Fields(line)
		if len(tokens) == 0 {
			continue
		}
		dst := tokens[0]
		rest := tokens[1:]
		switch dst {
		case "unreachable", "blackhole", "prohibit", "unicast", "local", "broadcast", "multicast", "anycast":
			if len(rest) == 0 {
				continue
			}
			dst, rest = rest[0], rest[1:]
		}
		if dst == "default" {
			dst = "0.0.0.0/0"
			if family == "6" {
				dst = "::/0"
			}
		}
		kv := keyed(rest, "via", "dev", "proto", "metric", "scope", "src", "table", "pref", "expires")
		rows = append(rows, map[string]string{
			"destination": dst, "family": family, "protocol": kv["proto"], "gateway": kv["via"],
			"interface": kv["dev"], "distance": "", "metric": kv["metric"], "selected": "yes",
		})
	}
	return rows
}

var neighborStates = map[string]bool{"REACHABLE": true, "STALE": true, "DELAY": true, "PROBE": true, "FAILED": true, "PERMANENT": true, "INCOMPLETE": true, "NOARP": true}

func parseIPNeigh(text string) Rows {
	var rows Rows
	for _, line := range strings.Split(text, "\n") {
		tokens := strings.Fields(line)
		if len(tokens) < 3 {
			continue
		}
		kv := keyed(tokens[1:], "dev", "lladdr", "used", "probes", "proto", "ref")
		// The cluster interface only ever sees the workers' link-locals.
		if kv["dev"] == "eth0" {
			continue
		}
		state := ""
		for _, t := range tokens[1:] {
			if neighborStates[t] {
				state = t
			}
		}
		rows = append(rows, map[string]string{"address": tokens[0], "interface": kv["dev"], "mac": kv["lladdr"], "state": state})
	}
	return rows
}

func parseBridgeFDB(text string) Rows {
	var rows Rows
	for _, line := range strings.Split(text, "\n") {
		tokens := strings.Fields(line)
		if len(tokens) < 3 || !strings.Contains(tokens[0], ":") {
			continue
		}
		kv := keyed(tokens[1:], "dev", "vlan", "master", "dst", "port", "vni")
		// Only the bridge's own entries: every interface also lists its
		// multicast and own addresses as `self permanent`, which is noise here.
		if kv["master"] == "" {
			continue
		}
		var flags []string
		for _, f := range []string{"permanent", "static", "self", "offload", "extern_learn", "router", "sticky"} {
			if _, ok := kv[f]; ok {
				flags = append(flags, f)
			}
		}
		rows = append(rows, map[string]string{"bridge": kv["master"], "mac": tokens[0], "interface": kv["dev"], "vlan": kv["vlan"], "flags": strings.Join(flags, " ")})
	}
	return rows
}

func parseNATRules(text, family string) Rows {
	var rows Rows
	for _, line := range strings.Split(text, "\n") {
		tokens := strings.Fields(line)
		if len(tokens) < 3 || tokens[0] != "-A" {
			continue
		}
		rows = append(rows, map[string]string{"family": family, "chain": tokens[1], "rule": strings.Join(tokens[2:], " ")})
	}
	return rows
}

func linuxRoutes(read linuxReader) (Rows, error) {
	v4, err := read([]string{"ip", "-4", "route", "show"})
	if err != nil {
		return nil, err
	}
	v6, err := read([]string{"ip", "-6", "route", "show"})
	if err != nil {
		return nil, err
	}
	return append(parseIPRoute(string(v4), "4"), parseIPRoute(string(v6), "6")...), nil
}

func linuxNeighbors(read linuxReader) (Rows, error) {
	out, err := read([]string{"ip", "neigh", "show"})
	if err != nil {
		return nil, err
	}
	return parseIPNeigh(string(out)), nil
}

func linuxNATRules(read linuxReader) (Rows, error) {
	v4, err := read([]string{"iptables", "-t", "nat", "-S"})
	if err != nil {
		return nil, err
	}
	v6, err := read([]string{"ip6tables", "-t", "nat", "-S"})
	if err != nil {
		return nil, err
	}
	return append(parseNATRules(string(v4), "4"), parseNATRules(string(v6), "6")...), nil
}

func linuxFDB(read linuxReader) (Rows, error) {
	out, err := read([]string{"bridge", "fdb", "show"})
	if err != nil {
		return nil, err
	}
	return parseBridgeFDB(string(out)), nil
}

func (L3Base) Routes(namespace, pod string) (Rows, error) {
	return linuxRoutes(netnsReader(namespace, pod))
}

func (L3Base) Neighbors(namespace, pod string) (Rows, error) {
	return linuxNeighbors(netnsReader(namespace, pod))
}

func (NATBase) NATRules(namespace, pod string) (Rows, error) {
	return linuxNATRules(netnsReader(namespace, pod))
}

func (SwitchBase) BridgeFDB(namespace, pod string) (Rows, error) {
	return linuxFDB(netnsReader(namespace, pod))
}

// LinuxNeighborsThrough reads the neighbour table with a reader of the
// driver's own, a guest wrapper for a VM.
func LinuxNeighborsThrough(read func(argv []string) ([]byte, error)) (Rows, error) {
	return linuxNeighbors(read)
}
