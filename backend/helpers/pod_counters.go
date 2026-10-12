package helpers

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"kubendt/types"
)

// ReadInterfaceCounters returns the kernel counters of every data interface
// of a pod, from /proc/net/dev: one cat, works on any image, BusyBox included
// (its ip has neither -s nor -j). At is when the read was taken, so two reads
// give rates.
func ReadInterfaceCounters(namespace, podName string) (types.PodCountersResponse, error) {
	stdout, stderr, err := ExecInPod(namespace, podName, []string{"cat", "/proc/net/dev"})
	if err != nil {
		return types.PodCountersResponse{}, fmt.Errorf("reading /proc/net/dev in %s/%s: %w (%s)", namespace, podName, err, strings.TrimSpace(stderr))
	}
	return types.PodCountersResponse{
		Pod:        podName,
		At:         time.Now().UnixMilli(),
		Interfaces: parseProcNetDev(stdout),
	}, nil
}

// parseProcNetDev reads the per-interface rows of /proc/net/dev: "name:
// rx_bytes rx_packets rx_errs rx_drop fifo frame compressed multicast
// tx_bytes tx_packets tx_errs tx_drop fifo colls carrier compressed".
// Loopback and kernel pseudo-interfaces are left out, and so are the tap
// devices of a QEMU pod, whose traffic shows on the ethN they are tied to.
func parseProcNetDev(text string) map[string]types.InterfaceCounters {
	out := map[string]types.InterfaceCounters{}
	for _, line := range strings.Split(text, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		fields := strings.Fields(rest)
		if name == "" || name == "lo" || len(fields) < 12 || types.IsPseudoInterface(name) || strings.HasPrefix(name, "tap") {
			continue
		}
		n := func(i int) int64 {
			v, _ := strconv.ParseInt(fields[i], 10, 64)
			return v
		}
		out[name] = types.InterfaceCounters{
			RxBytes: n(0), RxPackets: n(1), RxErrors: n(2), RxDropped: n(3),
			TxBytes: n(8), TxPackets: n(9), TxErrors: n(10), TxDropped: n(11),
		}
	}
	return out
}
