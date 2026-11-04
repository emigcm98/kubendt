package capabilities

import (
	"strconv"

	"kubendt/actions"
	"kubendt/types"
)

// SubinterfaceCapable hangs 802.1Q subinterfaces off a node's interface,
// the router on a stick: eth1.10 carries VLAN 10 of the trunk on eth1, with
// its own address if given. The subinterface is named after its parent and
// VLAN, so the routing capabilities see it as any other interface.
type SubinterfaceCapable interface {
	AddVLANSubinterface(parent string, vlan int, cidr string) [][]string
	RemoveVLANSubinterface(parent string, vlan int) [][]string
}

// SubinterfaceName is the kernel name of parent's VLAN subinterface.
func SubinterfaceName(parent string, vlan int) string { return parent + "." + strconv.Itoa(vlan) }

func keySubinterface(a types.ActionEntry) string { return SubinterfaceName(a.Iface, a.VLAN) }

func init() {
	actions.Register[SubinterfaceCapable](
		actions.Capability{ID: "SubinterfaceCapable", Label: "VLAN subinterfaces", Description: "802.1Q subinterfaces on an interface, the router on a stick."},
		actions.Action[SubinterfaceCapable]{Type: "add_vlan_subinterface", Label: "Add VLAN subinterface", Params: actions.P(actions.Iface, actions.VLAN, actions.CIDR.Optional()), State: "subinterface", Key: keySubinterface,
			Run: func(c SubinterfaceCapable, a types.ActionEntry) ([][]string, error) {
				if err := checkVLAN("vlan", a.VLAN); err != nil {
					return nil, err
				}
				return c.AddVLANSubinterface(a.Iface, a.VLAN, a.CIDR), nil
			}},
		actions.Action[SubinterfaceCapable]{Type: "remove_vlan_subinterface", Label: "Remove VLAN subinterface", Params: actions.P(actions.Iface, actions.VLAN), State: "subinterface", Key: keySubinterface, Clears: true,
			Run: func(c SubinterfaceCapable, a types.ActionEntry) ([][]string, error) {
				if err := checkVLAN("vlan", a.VLAN); err != nil {
					return nil, err
				}
				return c.RemoveVLANSubinterface(a.Iface, a.VLAN), nil
			}},
	)
}

// SubinterfaceBase is the Linux implementation, a vlan link on the parent.
type SubinterfaceBase struct{}

func (SubinterfaceBase) AddVLANSubinterface(parent string, vlan int, cidr string) [][]string {
	name := SubinterfaceName(parent, vlan)
	cmds := [][]string{
		{"sh", "-c", "ip link show " + name + " >/dev/null 2>&1 || ip link add link " + parent + " name " + name + " type vlan id " + strconv.Itoa(vlan)},
		{"ip", "link", "set", name, "up"},
	}
	if cidr != "" {
		cmds = append(cmds, []string{"ip", "addr", "replace", cidr, "dev", name})
	}
	return cmds
}

func (SubinterfaceBase) RemoveVLANSubinterface(parent string, vlan int) [][]string {
	return [][]string{{"ip", "link", "del", SubinterfaceName(parent, vlan)}}
}
