package capabilities

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"kubendt/actions"
	"kubendt/types"
)

// P4Capable is a BMv2 switch: the program it runs, a compiled JSON from the
// file manager swapped in place, and the entries of its tables. A new program
// comes up with empty tables, so load_program takes the entries' state down
// and they are applied again after it.
type P4Capable interface {
	LoadProgram(content string) [][]string
	TableAdd(table, action, match, params string, priority int) [][]string
	TableDelete(table, match string, priority int) [][]string
	TableSetDefault(table, action, params string) [][]string
}

type P4Inspector interface {
	P4Ports(namespace, pod string) (Rows, error)
	P4Tables(namespace, pod string) (Rows, error)
	P4Entries(namespace, pod string) (Rows, error)
}

const maxP4Program = 4 << 20

var p4NameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

func checkP4Program(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("the program file is empty")
	}
	if len(content) > maxP4Program {
		return fmt.Errorf("the program file is over %d MiB", maxP4Program>>20)
	}
	if !json.Valid([]byte(content)) {
		return fmt.Errorf("the program file is not the JSON p4c produces for bmv2")
	}
	return nil
}

func checkP4Name(what, name string) error {
	if !p4NameRe.MatchString(name) {
		return fmt.Errorf("%s must be a P4 name like MyIngress.ipv4_lpm, got %q", what, name)
	}
	return nil
}

// checkP4Text keeps the match fields and action parameters on one CLI line:
// values separated by spaces, as simple_switch_CLI takes them.
func checkP4Text(what, s string) error {
	if strings.ContainsAny(s, "\n\r\"'`|;&$") || strings.Contains(s, "=>") {
		return fmt.Errorf("%s must be values separated by spaces, got %q", what, s)
	}
	return nil
}

func p4Priority(a types.ActionEntry) int {
	if a.Priority == nil {
		return -1
	}
	return *a.Priority
}

func keyProgram(types.ActionEntry) string { return "program" }
func keyTable(a types.ActionEntry) string { return a.Table }
func keyTableEntry(a types.ActionEntry) string {
	k := a.Table + " " + a.Match
	if p := p4Priority(a); p >= 0 {
		k += " " + strconv.Itoa(p)
	}
	return k
}

func init() {
	actions.Register[P4Capable](
		actions.Capability{ID: "P4Capable", Label: "P4", Description: "The program a BMv2 switch runs and the entries of its tables."},
		actions.Action[P4Capable]{Type: "load_program", Label: "Load program", Params: actions.P(actions.ConfigFile), State: "p4_program", Key: keyProgram, ClearsAlso: []string{"p4_table_entry", "p4_table_default"},
			Run: func(c P4Capable, a types.ActionEntry) ([][]string, error) {
				if err := checkP4Program(a.FileContent); err != nil {
					return nil, err
				}
				return c.LoadProgram(a.FileContent), nil
			}},
		actions.Action[P4Capable]{Type: "table_add", Label: "Add table entry", Params: actions.P(actions.Table, actions.P4Action, actions.Match, actions.ActionParams.Optional(), actions.Priority.Optional()), State: "p4_table_entry", Key: keyTableEntry,
			Run: func(c P4Capable, a types.ActionEntry) ([][]string, error) {
				if err := checkP4Name("table", a.Table); err != nil {
					return nil, err
				}
				if err := checkP4Name("action", a.Action); err != nil {
					return nil, err
				}
				if err := checkP4Text("match", a.Match); err != nil {
					return nil, err
				}
				if err := checkP4Text("params", a.Params); err != nil {
					return nil, err
				}
				return c.TableAdd(a.Table, a.Action, a.Match, a.Params, p4Priority(a)), nil
			}},
		actions.Action[P4Capable]{Type: "table_delete", Label: "Delete table entry", Params: actions.P(actions.Table, actions.Match, actions.Priority.Optional()), State: "p4_table_entry", Key: keyTableEntry, Clears: true,
			Run: func(c P4Capable, a types.ActionEntry) ([][]string, error) {
				if err := checkP4Name("table", a.Table); err != nil {
					return nil, err
				}
				if err := checkP4Text("match", a.Match); err != nil {
					return nil, err
				}
				return c.TableDelete(a.Table, a.Match, p4Priority(a)), nil
			}},
		actions.Action[P4Capable]{Type: "table_set_default", Label: "Set default action", Params: actions.P(actions.Table, actions.P4Action, actions.ActionParams.Optional()), State: "p4_table_default", Key: keyTable,
			Run: func(c P4Capable, a types.ActionEntry) ([][]string, error) {
				if err := checkP4Name("table", a.Table); err != nil {
					return nil, err
				}
				if err := checkP4Name("action", a.Action); err != nil {
					return nil, err
				}
				if err := checkP4Text("params", a.Params); err != nil {
					return nil, err
				}
				return c.TableSetDefault(a.Table, a.Action, a.Params), nil
			}},
	)
	actions.RegisterInspections[P4Inspector]("P4Capable",
		actions.Inspection[P4Inspector]{ID: "p4_ports", Label: "P4 ports", Columns: cols("port", "Port", "iface", "Interface", "status", "Status"),
			Run: func(c P4Inspector, ns, pod string) (Rows, error) { return c.P4Ports(ns, pod) }},
		actions.Inspection[P4Inspector]{ID: "p4_tables", Label: "P4 tables", Columns: cols("table", "Table", "match", "Match fields"),
			Run: func(c P4Inspector, ns, pod string) (Rows, error) { return c.P4Tables(ns, pod) }},
		// The match values come as the CLI prints them, hex of the field's width.
		actions.Inspection[P4Inspector]{ID: "p4_entries", Label: "P4 table entries", Columns: cols("match", "Match", "table", "Table", "action", "Action", "params", "Parameters", "handle", "Handle"),
			Run: func(c P4Inspector, ns, pod string) (Rows, error) { return c.P4Entries(ns, pod) }})
}
