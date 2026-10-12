package capabilities

import (
	"kubendt/actions"
	"kubendt/types"
)

type L2Capable interface {
	LinkUp(iface string) [][]string
	LinkDown(iface string) [][]string
}

func init() {
	actions.Register[L2Capable](
		actions.Capability{ID: "L2Capable", Label: "Level 2: links", Description: "Up & down network interfaces"},
		actions.Action[L2Capable]{Type: "link_up", Label: "Bring interface up", Params: actions.P(actions.Iface),
			Run: func(c L2Capable, a types.ActionEntry) ([][]string, error) { return c.LinkUp(a.Iface), nil }},
		actions.Action[L2Capable]{Type: "link_down", Label: "Bring interface down", Params: actions.P(actions.Iface),
			Run: func(c L2Capable, a types.ActionEntry) ([][]string, error) { return c.LinkDown(a.Iface), nil }},
	)
}
