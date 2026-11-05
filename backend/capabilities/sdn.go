package capabilities

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"

	"kubendt/actions"
	"kubendt/types"
)

// SDNControllerCapable is a controller node: the switches point their
// OpenFlow side at it and it runs what its configuration says. The
// configuration is a file of the namespace's file manager, read when the
// action runs, so a replay sends the file as it is then.
type SDNControllerCapable interface {
	// ApplyConfig writes the configuration into the node and reloads it.
	ApplyConfig(content string) [][]string
}

type SDNControllerInspector interface {
	// Datapaths lists the switches the controller knows, connected or not.
	Datapaths(namespace, pod string) (Rows, error)
	DatapathPorts(namespace, pod string) (Rows, error)
	LearnedHosts(namespace, pod string) (Rows, error)
}

const maxControllerConfig = 256 << 10

func checkControllerConfig(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("the configuration file is empty")
	}
	if len(content) > maxControllerConfig {
		return fmt.Errorf("the configuration file is over %d KiB", maxControllerConfig>>10)
	}
	if strings.ContainsRune(content, 0) {
		return fmt.Errorf("the configuration file is not text")
	}
	if _, err := yaml.YAMLToJSON([]byte(content)); err != nil {
		return fmt.Errorf("the configuration file is not valid YAML: %w", err)
	}
	return nil
}

func init() {
	actions.Register[SDNControllerCapable](
		actions.Capability{ID: "SDNControllerCapable", Label: "SDN controller", Description: "The configuration an OpenFlow controller runs, from the file manager."},
		actions.Action[SDNControllerCapable]{Type: "controller_apply_config", Label: "Apply controller configuration", Params: actions.P(actions.ConfigFile), State: "controller_config",
			Run: func(c SDNControllerCapable, a types.ActionEntry) ([][]string, error) {
				if err := checkControllerConfig(a.FileContent); err != nil {
					return nil, err
				}
				return c.ApplyConfig(a.FileContent), nil
			}},
	)
	actions.RegisterInspections[SDNControllerInspector]("SDNControllerCapable",
		actions.Inspection[SDNControllerInspector]{ID: "datapaths", Label: "Datapaths", Columns: cols("datapath", "Datapath", "dp_id", "DP ID", "status", "Status", "connections", "Connections"),
			Run: func(c SDNControllerInspector, ns, pod string) (Rows, error) { return c.Datapaths(ns, pod) }},
		actions.Inspection[SDNControllerInspector]{ID: "datapath_ports", Label: "Datapath ports", Columns: cols("datapath", "Datapath", "port", "Port", "status", "Status"),
			Run: func(c SDNControllerInspector, ns, pod string) (Rows, error) { return c.DatapathPorts(ns, pod) }},
		actions.Inspection[SDNControllerInspector]{ID: "learned_hosts", Label: "Learned hosts", Columns: cols("datapath", "Datapath", "port", "Port", "vlan", "VLAN", "mac", "MAC"),
			Run: func(c SDNControllerInspector, ns, pod string) (Rows, error) { return c.LearnedHosts(ns, pod) }})
}
