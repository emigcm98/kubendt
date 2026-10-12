package helpers_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	caps "kubendt/capabilities/capabilities"
	drivers_base "kubendt/drivers"
	drivers_registry "kubendt/drivers/registry"
	"kubendt/helpers"
	"kubendt/types"
)

var update = flag.Bool("update", false, "rewrite the expected plans under testdata/plans")

// Drivers whose plan resolves without a cluster. A driver that reads an
// interface map from the pod for every interface action is left out.
var plannedDrivers = []string{
	"BasicHostDriver",
	"HostDriver",
	"LinuxSwitchDriver",
	"OpenVSwitchDriver",
	"LinuxRouterDriver",
	"FRRRouterDriver",
	"VyOSRouterDriver",
}

// plan is what one action resolves to on one driver: the executor and the
// command rows, or nothing because the driver lacks the capability.
type plan struct {
	Executor    string     `json:"executor,omitempty"`
	Commands    [][]string `json:"commands,omitempty"`
	Unsupported bool       `json:"unsupported,omitempty"`
}

var registerDrivers sync.Once

func loadDriver(t *testing.T, name string) any {
	t.Helper()
	registerDrivers.Do(drivers_base.RegisterAllDrivers)
	drv, err := drivers_registry.NewByName(name)
	if err != nil {
		t.Fatalf("driver %s: %v", name, err)
	}
	return drv
}

// knownActionTypes lists every driver action plus the universal tc ones.
// custom is left out, it bypasses the driver system by design.
func knownActionTypes() []string {
	var out []string
	for _, m := range []map[string]string{caps.L2Methods, caps.L3Methods, caps.SwitchMethods, caps.NATMethods, caps.OSPFMethods} {
		for actionType := range m {
			out = append(out, actionType)
		}
	}
	out = append(out, "add_qdisc", "del_qdisc")
	sort.Strings(out)
	return out
}

// loadFixtures reads testdata/actions.json and checks it holds exactly one
// sample per known action type, so a new action cannot land without one.
func loadFixtures(t *testing.T) map[string]types.ActionEntry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "actions.json"))
	if err != nil {
		t.Fatalf("reading fixtures: %v", err)
	}
	var entries []types.ActionEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("parsing fixtures: %v", err)
	}
	byType := map[string]types.ActionEntry{}
	for _, e := range entries {
		if _, dup := byType[e.Type]; dup {
			t.Errorf("fixture %q appears twice in testdata/actions.json", e.Type)
		}
		byType[e.Type] = e
	}
	known := knownActionTypes()
	for _, actionType := range known {
		if _, ok := byType[actionType]; !ok {
			t.Errorf("action %q has no sample in testdata/actions.json", actionType)
		}
	}
	knownSet := map[string]bool{}
	for _, actionType := range known {
		knownSet[actionType] = true
	}
	for actionType := range byType {
		if !knownSet[actionType] {
			t.Errorf("fixture %q is not a known action", actionType)
		}
	}
	return byType
}

func planFor(t *testing.T, driverName string, driver any, action types.ActionEntry) plan {
	t.Helper()
	executorName, commands, err := helpers.ResolveDriverExecutionPlanForPod("", "", driver, action)
	if err != nil {
		if strings.Contains(err.Error(), "does not support") {
			return plan{Unsupported: true}
		}
		t.Fatalf("%s/%s: %v", driverName, action.Type, err)
	}
	if commands == nil {
		t.Fatalf("%s/%s: no commands and no error", driverName, action.Type)
	}
	return plan{Executor: executorName, Commands: commands}
}

// encodePlans writes one driver's plans with each command row on a single
// line, so the files stay readable and diffs show the row that changed.
func encodePlans(plans map[string]plan) []byte {
	keys := make([]string, 0, len(plans))
	for k := range plans {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{\n")
	for i, k := range keys {
		p := plans[k]
		fmt.Fprintf(&b, "  %q: {", k)
		if p.Unsupported {
			b.WriteString(` "unsupported": true }`)
		} else {
			fmt.Fprintf(&b, "\n    \"executor\": %q,\n    \"commands\": [\n", p.Executor)
			for j, row := range p.Commands {
				line, _ := json.Marshal(row)
				b.WriteString("      ")
				b.Write(line)
				if j < len(p.Commands)-1 {
					b.WriteString(",")
				}
				b.WriteString("\n")
			}
			b.WriteString("    ]\n  }")
		}
		if i < len(keys)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

func rows(p plan) string {
	if p.Unsupported {
		return "unsupported"
	}
	out := []string{"executor " + p.Executor}
	for _, row := range p.Commands {
		line, _ := json.Marshal(row)
		out = append(out, string(line))
	}
	return strings.Join(out, "\n      ")
}

// TestActionPlans pins, for every driver and every action, the executor and
// the exact command rows the pod receives. It is the safety net for the
// action registry refactor: dispatch can move, the output cannot.
func TestActionPlans(t *testing.T) {
	fixtures := loadFixtures(t)
	types := knownActionTypes()

	for _, driverName := range plannedDrivers {
		driver := loadDriver(t, driverName)
		got := map[string]plan{}
		for _, actionType := range types {
			got[actionType] = planFor(t, driverName, driver, fixtures[actionType])
		}

		path := filepath.Join("testdata", "plans", driverName+".json")
		if *update {
			if err := os.WriteFile(path, encodePlans(got), 0o644); err != nil {
				t.Fatalf("writing %s: %v", path, err)
			}
			continue
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("no expected plans for %s, run: go test ./helpers -run TestActionPlans -update", driverName)
			continue
		}
		var want map[string]plan
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, actionType := range types {
			w, ok := want[actionType]
			if !ok {
				t.Errorf("%s: no expected plan for %s, run with -update", driverName, actionType)
				continue
			}
			if !reflect.DeepEqual(w, got[actionType]) {
				t.Errorf("%s/%s changed\n    want: %s\n    got:  %s", driverName, actionType, rows(w), rows(got[actionType]))
			}
		}
		for actionType := range want {
			if _, ok := got[actionType]; !ok {
				t.Errorf("%s: testdata has %s, which is no longer an action", driverName, actionType)
			}
		}
	}
}

// TestPlanProtectsEth0 pins the one rule that runs before any capability:
// eth0 is the pod's cluster interface and only the SNAT toggles may touch it.
func TestPlanProtectsEth0(t *testing.T) {
	driver := loadDriver(t, "FRRRouterDriver")
	cases := []struct {
		action  types.ActionEntry
		allowed bool
	}{
		{types.ActionEntry{Type: "enable_snat", Iface: "eth0"}, true},
		{types.ActionEntry{Type: "disable_snat", Iface: "eth0"}, true},
		{types.ActionEntry{Type: "set_ip", Iface: "eth0", CIDR: "10.0.0.1/24"}, false},
		{types.ActionEntry{Type: "link_down", Iface: "eth0"}, false},
		{types.ActionEntry{Type: "add_static_route", DstCIDR: "10.1.0.0/16", Gateway: "10.0.0.254", Device: "eth0"}, false},
		{types.ActionEntry{Type: "setup_bridge", Bridge: "br0", Ifaces: []string{"eth1", "eth0"}}, false},
	}
	for _, c := range cases {
		_, _, err := helpers.ResolveDriverExecutionPlanForPod("", "", driver, c.action)
		if c.allowed && err != nil {
			t.Errorf("%s on eth0 should be allowed, got %v", c.action.Type, err)
		}
		if !c.allowed && (err == nil || !strings.Contains(err.Error(), "protected interface eth0")) {
			t.Errorf("%s on eth0 should be rejected as protected, got %v", c.action.Type, err)
		}
	}
}
