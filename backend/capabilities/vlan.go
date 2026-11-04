package capabilities

import (
	"fmt"

	"kubendt/actions"
	"kubendt/types"
)

// VLANCapable puts the ports of a bridge into VLANs: an access port carries
// one VLAN untagged, a trunk a list tagged with an optional native VLAN
// untagged, and a cleared port goes back to the plain bridging of
// SwitchCapable. A port has one VLAN setting, so the last set wins.
type VLANCapable interface {
	VLANSetAccess(port string, vlan int) [][]string
	VLANSetTrunk(port string, vlans []int, native int) [][]string
	VLANClearPort(port string) [][]string
}

func checkVLAN(name string, id int) error {
	if id < 1 || id > 4094 {
		return fmt.Errorf("%s must be between 1 and 4094, got %d", name, id)
	}
	return nil
}

func init() {
	actions.Register[VLANCapable](
		actions.Capability{ID: "VLANCapable", Label: "VLANs", Description: "Access and trunk ports on a bridge."},
		actions.Action[VLANCapable]{Type: "vlan_set_access", Label: "Set access port", Params: actions.P(actions.Iface, actions.VLAN), State: "vlan_port", Key: keyIface,
			Run: func(c VLANCapable, a types.ActionEntry) ([][]string, error) {
				if err := checkVLAN("vlan", a.VLAN); err != nil {
					return nil, err
				}
				return c.VLANSetAccess(a.Iface, a.VLAN), nil
			}},
		actions.Action[VLANCapable]{Type: "vlan_set_trunk", Label: "Set trunk port", Params: actions.P(actions.Iface, actions.VLANs, actions.NativeVLAN), State: "vlan_port", Key: keyIface,
			Run: func(c VLANCapable, a types.ActionEntry) ([][]string, error) {
				for _, v := range a.VLANs {
					if err := checkVLAN("vlans", v); err != nil {
						return nil, err
					}
				}
				if a.NativeVLAN != 0 {
					if err := checkVLAN("native_vlan", a.NativeVLAN); err != nil {
						return nil, err
					}
				}
				return c.VLANSetTrunk(a.Iface, a.VLANs, a.NativeVLAN), nil
			}},
		actions.Action[VLANCapable]{Type: "vlan_clear_port", Label: "Clear port VLANs", Params: actions.P(actions.Iface), State: "vlan_port", Key: keyIface, Clears: true,
			Run: func(c VLANCapable, a types.ActionEntry) ([][]string, error) { return c.VLANClearPort(a.Iface), nil }},
	)
}
