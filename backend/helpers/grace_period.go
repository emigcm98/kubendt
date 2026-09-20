package helpers

import (
	"fmt"

	"kubendt/types"
)

// NormalizeTerminationGracePeriod fills in the platform default when the node
// does not set a grace period and rejects negative values. Zero means unset,
// not a force delete: with StatefulSets a grace of 0 lets the replacement pod
// take the name while the old one may still be running.
func NormalizeTerminationGracePeriod(node *types.NodeSpec) error {
	switch {
	case node.TerminationGracePeriodSeconds == 0:
		node.TerminationGracePeriodSeconds = types.DefaultTerminationGracePeriodSeconds
	case node.TerminationGracePeriodSeconds < 0:
		return fmt.Errorf("node '%s' has invalid terminationGracePeriodSeconds (%d), must be >= 1", node.Name, node.TerminationGracePeriodSeconds)
	}
	return nil
}
