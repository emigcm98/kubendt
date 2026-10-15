package helpers

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"kubendt/types"
)

func parseStream(t *testing.T, path string) ([]types.IperfInterval, *types.IperfSummary, string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var intervals []types.IperfInterval
	var summary *types.IperfSummary
	var failure string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		ev, err := ParseIperfEvent(sc.Bytes())
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		switch ev.Kind {
		case "interval":
			intervals = append(intervals, *ev.Interval)
		case "end":
			if ev.Summary != nil {
				summary = ev.Summary
			}
		case "error":
			failure = ev.Message
		}
	}
	return intervals, summary, failure
}

func TestParseIperfTCPStream(t *testing.T) {
	intervals, summary, failure := parseStream(t, "testdata/iperf_tcp.jsonl")
	if failure != "" {
		t.Fatalf("unexpected error event %q", failure)
	}
	if len(intervals) != 2 {
		t.Fatalf("intervals = %d, want 2", len(intervals))
	}
	if intervals[0].Retransmits == nil || *intervals[0].Retransmits != 0 || intervals[0].Packets != nil {
		t.Errorf("tcp interval should carry retransmits and no packets: %+v", intervals[0])
	}
	if intervals[1].Start < 1 || intervals[1].BitsPerSecond < 1e9 {
		t.Errorf("second interval out of shape: %+v", intervals[1])
	}
	if summary == nil {
		t.Fatal("no summary")
	}
	if summary.SentBytes != 15616835584 || summary.ReceivedBytes != 15616835584 {
		t.Errorf("bytes: %+v", summary)
	}
	if summary.Retransmits == nil || summary.JitterMs != nil || summary.LostPackets != nil {
		t.Errorf("tcp summary should have retransmits and no udp fields: %+v", summary)
	}
	if summary.CPUClientPercent < 1 || summary.CPUServerPercent < 1 {
		t.Errorf("cpu missing: %+v", summary)
	}
}

func TestParseIperfUDPStream(t *testing.T) {
	intervals, summary, _ := parseStream(t, "testdata/iperf_udp.jsonl")
	if len(intervals) != 2 || intervals[0].Packets == nil || *intervals[0].Packets != 20 {
		t.Fatalf("udp intervals: %+v", intervals)
	}
	if summary == nil {
		t.Fatal("no summary")
	}
	if summary.Packets == nil || *summary.Packets != 39 || summary.LostPackets == nil || *summary.LostPackets != 0 {
		t.Errorf("udp packets: %+v", summary)
	}
	if summary.JitterMs == nil || *summary.JitterMs <= 0 || summary.LostPercent == nil {
		t.Errorf("udp jitter and loss: %+v", summary)
	}
	if summary.Retransmits != nil {
		t.Errorf("udp has no retransmits: %+v", summary)
	}
}

func TestParseIperfRefused(t *testing.T) {
	intervals, summary, failure := parseStream(t, "testdata/iperf_refused.jsonl")
	if len(intervals) != 0 || summary != nil {
		t.Fatalf("a refused run has no data: %v %v", intervals, summary)
	}
	if !strings.Contains(failure, "Connection refused") {
		t.Errorf("failure = %q", failure)
	}
}

func TestBuildIperfCommands(t *testing.T) {
	got := strings.Join(BuildIperfServerCommand(5312), " ")
	if got != "iperf3 -s -1 -p 5312 --forceflush" {
		t.Errorf("server: %s", got)
	}
	tcp := IperfOptions{Dest: "192.168.3.10", Proto: "tcp", Duration: 10, Parallel: 1}
	got = strings.Join(BuildIperfClientCommand(tcp, 5312), " ")
	if got != "iperf3 -c 192.168.3.10 -p 5312 -t 10 --json-stream --forceflush" {
		t.Errorf("tcp client: %s", got)
	}
	udp := IperfOptions{Dest: "192.168.3.10", Proto: "udp", Duration: 5, Bandwidth: "10M", Parallel: 2, Reverse: true}
	got = strings.Join(BuildIperfClientCommand(udp, 5201), " ")
	if got != "iperf3 -c 192.168.3.10 -p 5201 -t 5 --json-stream --forceflush -u -b 10M -P 2 -R" {
		t.Errorf("udp client: %s", got)
	}
}

func TestIperfOptionsValidate(t *testing.T) {
	ok := IperfOptions{Dest: "10.0.0.1", Proto: "tcp", Duration: 10, Parallel: 1}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}
	for name, o := range map[string]IperfOptions{
		"no dest":        {Proto: "tcp", Duration: 10, Parallel: 1},
		"bad proto":      {Dest: "10.0.0.1", Proto: "sctp", Duration: 10, Parallel: 1},
		"too long":       {Dest: "10.0.0.1", Proto: "tcp", Duration: 121, Parallel: 1},
		"bad bandwidth":  {Dest: "10.0.0.1", Proto: "udp", Duration: 10, Parallel: 1, Bandwidth: "10 Mbit"},
		"too parallel":   {Dest: "10.0.0.1", Proto: "tcp", Duration: 10, Parallel: 9},
		"port too large": {Dest: "10.0.0.1", Proto: "tcp", Duration: 10, Parallel: 1, Port: 70000},
	} {
		if err := o.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for i := 0; i < 50; i++ {
		if p := PickIperfPort(); p < iperfManagedPortFrom || p > iperfManagedPortTo {
			t.Fatalf("port %d out of the managed range", p)
		}
	}
}
