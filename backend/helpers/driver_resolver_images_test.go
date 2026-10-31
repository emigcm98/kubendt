package helpers

import (
	"testing"

	drivers_base "kubendt/drivers"
	"kubendt/types"
)

// A node on an image its driver was not tried with deploys with a warning;
// the known one and a host on anything deploy quietly.
func TestResolveDriversWarnsOnUntestedImage(t *testing.T) {
	drivers_base.RegisterAllDrivers()
	nodes := []types.NodeSpec{
		{Name: "ovs", Type: "switch", Driver: "OpenVSwitchDriver", Image: "globocom/openvswitch:latest"},
		{Name: "ovs-other", Type: "switch", Driver: "OpenVSwitchDriver", Image: "globocom/openvswitch:2.17"},
		{Name: "r", Type: "router", Driver: "FRRRouterDriver", Image: "quay.io/frrouting/frr:9.1.0"},
		{Name: "h", Type: "host", Image: "mongo:8.0"},
	}
	warnings, err := ResolveDriversForNodes(nodes)
	if err != nil {
		t.Fatal(err)
	}
	var untested []string
	for _, w := range warnings {
		if w.Kind == "image_untested" {
			untested = append(untested, w.Node)
		}
	}
	if len(untested) != 2 || untested[0] != "ovs-other" || untested[1] != "r" {
		t.Errorf("untested images warned on %v, want ovs-other and r", untested)
	}
}
