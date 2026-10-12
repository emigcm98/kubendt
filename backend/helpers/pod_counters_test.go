package helpers

import (
	"reflect"
	"testing"

	"kubendt/types"
)

func TestParseProcNetDev(t *testing.T) {
	text := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
  eth0: 3026700   12795    0    0    0     0          0         0   412345    5212    0    0    0     0       0          0
  eth1:   32308     507    2    5    0     0          0         0      696      10    0    1    0     0       0          0
  tap1:    1000      10    0    0    0     0          0         0     1000      10    0    0    0     0       0          0
tunl0:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
`
	got := parseProcNetDev(text)
	want := map[string]types.InterfaceCounters{
		"eth0": {RxBytes: 3026700, RxPackets: 12795, TxBytes: 412345, TxPackets: 5212},
		"eth1": {RxBytes: 32308, RxPackets: 507, RxErrors: 2, RxDropped: 5, TxBytes: 696, TxPackets: 10, TxDropped: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if len(parseProcNetDev("garbage\n")) != 0 {
		t.Errorf("garbage should parse to nothing")
	}
}
