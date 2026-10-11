package helpers

import (
	"reflect"
	"strings"
	"testing"

	"kubendt/types"
)

func TestExpandLinkShaping(t *testing.T) {
	topo := []types.LinkSpec{
		{Node: "host-0", LocalIntf: "eth1", PeerNode: "switch-0", PeerIntf: "eth1"},
		{Node: "router1-0", LocalIntf: "eth2", PeerNode: "router2-0", PeerIntf: "eth2"},
		{Node: "edge-0", LocalIntf: "eth1", PeerNode: "external", PeerIntf: "eth1"},
	}
	// Base names resolve to the single pod, exact names pass through.
	resolve := func(ref string) (string, error) {
		switch ref {
		case "host", "host-0":
			return "host-0", nil
		case "switch-0", "router1-0", "router2-0", "edge-0":
			return ref, nil
		}
		return "", &notFound{ref}
	}
	delay := &types.TCParamEntry{Delay: "50ms"}
	rate := &types.TCParamEntry{Rate: "1mbit"}
	addAt := func(iface string, p *types.TCParamEntry) types.ActionEntry {
		return types.ActionEntry{Type: "add_qdisc", Iface: iface, TCParams: p}
	}

	cases := []struct {
		name    string
		links   []types.LinkShaping
		want    []types.PodConfig
		wantErr string
	}{
		{"one end only", []types.LinkShaping{{Node: "host", LocalIntf: "eth1", Shaping: delay}},
			[]types.PodConfig{{Pod: "host-0", Actions: []types.ActionEntry{addAt("eth1", delay)}}}, ""},
		{"symmetric", []types.LinkShaping{{Node: "host-0", LocalIntf: "eth1", Shaping: delay, Symmetric: true}},
			[]types.PodConfig{{Pod: "host-0", Actions: []types.ActionEntry{addAt("eth1", delay)}}, {Pod: "switch-0", Actions: []types.ActionEntry{addAt("eth1", delay)}}}, ""},
		{"named from the peer side", []types.LinkShaping{{Node: "switch-0", LocalIntf: "eth1", Shaping: delay, Symmetric: true}},
			[]types.PodConfig{{Pod: "switch-0", Actions: []types.ActionEntry{addAt("eth1", delay)}}, {Pod: "host-0", Actions: []types.ActionEntry{addAt("eth1", delay)}}}, ""},
		{"reverse", []types.LinkShaping{{Node: "router1-0", LocalIntf: "eth2", Shaping: delay, Reverse: rate}},
			[]types.PodConfig{{Pod: "router1-0", Actions: []types.ActionEntry{addAt("eth2", delay)}}, {Pod: "router2-0", Actions: []types.ActionEntry{addAt("eth2", rate)}}}, ""},
		{"reverse only", []types.LinkShaping{{Node: "router1-0", LocalIntf: "eth2", Reverse: rate}},
			[]types.PodConfig{{Pod: "router2-0", Actions: []types.ActionEntry{addAt("eth2", rate)}}}, ""},
		{"remove both ends", []types.LinkShaping{{Node: "host-0", LocalIntf: "eth1", Remove: true}},
			[]types.PodConfig{{Pod: "host-0", Actions: []types.ActionEntry{{Type: "del_qdisc", Iface: "eth1"}}}, {Pod: "switch-0", Actions: []types.ActionEntry{{Type: "del_qdisc", Iface: "eth1"}}}}, ""},
		{"peer checked", []types.LinkShaping{{Node: "host-0", LocalIntf: "eth1", PeerNode: "switch-0", PeerIntf: "eth1", Shaping: delay}},
			[]types.PodConfig{{Pod: "host-0", Actions: []types.ActionEntry{addAt("eth1", delay)}}}, ""},
		{"wrong peer", []types.LinkShaping{{Node: "host-0", LocalIntf: "eth1", PeerNode: "router1-0", Shaping: delay}}, nil, "is linked to switch-0, not to router1-0"},
		{"wrong peer interface", []types.LinkShaping{{Node: "host-0", LocalIntf: "eth1", PeerIntf: "eth9", Shaping: delay}}, nil, "not to eth9"},
		{"no such link", []types.LinkShaping{{Node: "host-0", LocalIntf: "eth7", Shaping: delay}}, nil, "no link on host-0:eth7"},
		{"unknown pod", []types.LinkShaping{{Node: "ghost", LocalIntf: "eth1", Shaping: delay}}, nil, "ghost"},
		{"external peer, one end is fine", []types.LinkShaping{{Node: "edge-0", LocalIntf: "eth1", Shaping: delay}},
			[]types.PodConfig{{Pod: "edge-0", Actions: []types.ActionEntry{addAt("eth1", delay)}}}, ""},
		{"external peer, symmetric", []types.LinkShaping{{Node: "edge-0", LocalIntf: "eth1", Shaping: delay, Symmetric: true}}, nil, "external network"},
		{"external peer, remove only touches the pod", []types.LinkShaping{{Node: "edge-0", LocalIntf: "eth1", Remove: true}},
			[]types.PodConfig{{Pod: "edge-0", Actions: []types.ActionEntry{{Type: "del_qdisc", Iface: "eth1"}}}}, ""},
		{"nothing to do", []types.LinkShaping{{Node: "host-0", LocalIntf: "eth1"}}, nil, "needs shaping, reverse or remove"},
		{"symmetric and reverse", []types.LinkShaping{{Node: "host-0", LocalIntf: "eth1", Shaping: delay, Symmetric: true, Reverse: rate}}, nil, "exclusive"},
		{"remove with shaping", []types.LinkShaping{{Node: "host-0", LocalIntf: "eth1", Remove: true, Shaping: delay}}, nil, "remove takes no shaping"},
		{"missing fields", []types.LinkShaping{{Node: "host-0"}}, nil, "node and localIntf are required"},
		{"same pod twice merges", []types.LinkShaping{{Node: "router1-0", LocalIntf: "eth2", Shaping: delay}, {Node: "router2-0", LocalIntf: "eth2", Shaping: rate, Symmetric: true}},
			[]types.PodConfig{{Pod: "router1-0", Actions: []types.ActionEntry{addAt("eth2", delay), addAt("eth2", rate)}}, {Pod: "router2-0", Actions: []types.ActionEntry{addAt("eth2", rate)}}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expandLinkShaping(tc.links, topo, resolve)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("targets = %+v, want %+v", got, tc.want)
			}
		})
	}
}

type notFound struct{ ref string }

func (e *notFound) Error() string { return "pod '" + e.ref + "' not found" }
