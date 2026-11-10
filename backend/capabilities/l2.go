package capabilities

import (
	"fmt"

	"kubendt/actions"
	"kubendt/types"
)

type L2Capable interface {
	LinkUp(iface string) [][]string
	LinkDown(iface string) [][]string
}

// MACSetter is the optional side of set_mac: a Linux interface or a VyOS
// ethernet takes a MAC of its own, a switch port on EXOS does not.
type MACSetter interface {
	SetMAC(iface, mac string) [][]string
}

func init() {
	actions.Register[L2Capable](
		actions.Capability{ID: "L2Capable", Label: "Level 2: links", Description: "Up & down network interfaces"},
		actions.Action[L2Capable]{Type: "link_up", Label: "Bring interface up", Params: actions.P(actions.Iface), State: "link", Key: keyIface,
			Run: func(c L2Capable, a types.ActionEntry) ([][]string, error) { return c.LinkUp(a.Iface), nil }},
		actions.Action[L2Capable]{Type: "link_down", Label: "Bring interface down", Params: actions.P(actions.Iface), State: "link", Key: keyIface, Clears: true, UndoPersists: true,
			Run: func(c L2Capable, a types.ActionEntry) ([][]string, error) { return c.LinkDown(a.Iface), nil }},
		actions.Action[L2Capable]{Type: "set_mac", Label: "Set MAC address", Params: actions.P(actions.Iface, actions.MAC), State: "mac", Key: keyIface,
			Run: func(c L2Capable, a types.ActionEntry) ([][]string, error) {
				m, ok := c.(MACSetter)
				if !ok {
					return nil, fmt.Errorf("the driver does not support set_mac, its interfaces have no MAC of their own")
				}
				return m.SetMAC(a.Iface, a.MAC), nil
			}},
	)
}
