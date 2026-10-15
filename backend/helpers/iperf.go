package helpers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kubendt/types"
)

// A throughput test runs iperf3 between two toolboxes: the server on the
// destination pod when the backend manages it, the client on the source. The
// client prints one JSON object per line (--json-stream), parsed as it comes.

const (
	IperfMinDuration = 1
	IperfMaxDuration = 120
	IperfMaxParallel = 8
	// IperfDefaultPort is iperf3's own, used when the lab runs its own server.
	IperfDefaultPort = 5201
	// Managed servers take a port of their own so they never collide with a
	// server the lab runs in the pod, since the toolbox shares its netns.
	iperfManagedPortFrom = 5301
	iperfManagedPortTo   = 5399
	// iperfListenTimeout bounds the wait for the server's listening line.
	iperfListenTimeout = 15 * time.Second
)

var iperfBandwidthRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[kKmMgG]?$`)

// IperfOptions is what a test takes from the request.
type IperfOptions struct {
	Dest      string // IP or hostname the client connects to
	Proto     string // tcp or udp
	Duration  int    // seconds
	Bandwidth string // iperf3 -b, bits per second with an optional k, M or G
	Parallel  int
	Reverse   bool
	Port      int // the server's port; 0 picks one for a managed server
}

// IperfServer is a server the backend runs for the test.
type IperfServer struct {
	Pod       string
	Container string
	Port      int
}

func (o IperfOptions) Validate() error {
	if !ValidTraceDest(o.Dest) {
		return errors.New("dest must be an IPv4 address or a hostname")
	}
	if o.Proto != "tcp" && o.Proto != "udp" {
		return errors.New("proto must be tcp or udp")
	}
	if o.Duration < IperfMinDuration || o.Duration > IperfMaxDuration {
		return fmt.Errorf("duration must be between %d and %d seconds", IperfMinDuration, IperfMaxDuration)
	}
	if o.Bandwidth != "" && !iperfBandwidthRe.MatchString(o.Bandwidth) {
		return errors.New("bandwidth must be a number of bits per second with an optional k, M or G")
	}
	if o.Parallel < 1 || o.Parallel > IperfMaxParallel {
		return fmt.Errorf("parallel must be between 1 and %d", IperfMaxParallel)
	}
	if o.Port < 0 || o.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}

// PickIperfPort returns a port for a managed server.
func PickIperfPort() int {
	return iperfManagedPortFrom + rand.Intn(iperfManagedPortTo-iperfManagedPortFrom+1)
}

// BuildIperfServerCommand returns the argv of a server that serves one test
// and exits. Flushed output lets the backend read the listening line.
func BuildIperfServerCommand(port int) []string {
	return []string{"iperf3", "-s", "-1", "-p", strconv.Itoa(port), "--forceflush"}
}

// BuildIperfClientCommand returns the client argv, one JSON event per line.
func BuildIperfClientCommand(o IperfOptions, port int) []string {
	cmd := []string{"iperf3", "-c", o.Dest, "-p", strconv.Itoa(port), "-t", strconv.Itoa(o.Duration), "--json-stream", "--forceflush"}
	if o.Proto == "udp" {
		cmd = append(cmd, "-u")
	}
	if o.Bandwidth != "" {
		cmd = append(cmd, "-b", o.Bandwidth)
	}
	if o.Parallel > 1 {
		cmd = append(cmd, "-P", strconv.Itoa(o.Parallel))
	}
	if o.Reverse {
		cmd = append(cmd, "-R")
	}
	return cmd
}

// IperfEvent is one parsed line of the client stream.
type IperfEvent struct {
	Kind     string // start, interval, end, error, or "" for a line to ignore
	Interval *types.IperfInterval
	Summary  *types.IperfSummary
	Message  string // the error text
}

// iperfSum is the shape iperf3 gives a summed interval or an end total.
type iperfSum struct {
	Start         float64  `json:"start"`
	End           float64  `json:"end"`
	Seconds       float64  `json:"seconds"`
	Bytes         int64    `json:"bytes"`
	BitsPerSecond float64  `json:"bits_per_second"`
	Retransmits   *int64   `json:"retransmits"`
	Packets       *int64   `json:"packets"`
	LostPackets   *int64   `json:"lost_packets"`
	LostPercent   *float64 `json:"lost_percent"`
	JitterMs      *float64 `json:"jitter_ms"`
}

// ParseIperfEvent reads one line of --json-stream output.
func ParseIperfEvent(line []byte) (IperfEvent, error) {
	var raw struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return IperfEvent{}, fmt.Errorf("not an iperf3 event: %w", err)
	}
	switch raw.Event {
	case "start":
		return IperfEvent{Kind: "start"}, nil
	case "error":
		var msg string
		_ = json.Unmarshal(raw.Data, &msg)
		return IperfEvent{Kind: "error", Message: msg}, nil
	case "interval":
		var d struct {
			Sum iperfSum `json:"sum"`
		}
		if err := json.Unmarshal(raw.Data, &d); err != nil {
			return IperfEvent{}, fmt.Errorf("bad interval: %w", err)
		}
		return IperfEvent{Kind: "interval", Interval: &types.IperfInterval{
			Start: d.Sum.Start, End: d.Sum.End, Bytes: d.Sum.Bytes, BitsPerSecond: d.Sum.BitsPerSecond,
			Retransmits: d.Sum.Retransmits, Packets: d.Sum.Packets,
		}}, nil
	case "end":
		var d struct {
			Sum         *iperfSum `json:"sum"`
			SumSent     *iperfSum `json:"sum_sent"`
			SumReceived *iperfSum `json:"sum_received"`
			CPU         struct {
				Host   float64 `json:"host_total"`
				Remote float64 `json:"remote_total"`
			} `json:"cpu_utilization_percent"`
		}
		if err := json.Unmarshal(raw.Data, &d); err != nil {
			return IperfEvent{}, fmt.Errorf("bad end report: %w", err)
		}
		// An end after an error carries no totals.
		if d.SumSent == nil && d.Sum == nil {
			return IperfEvent{Kind: "end"}, nil
		}
		sent, received := d.SumSent, d.SumReceived
		if sent == nil {
			sent = d.Sum
		}
		if received == nil {
			received = d.Sum
		}
		s := &types.IperfSummary{
			Seconds: received.Seconds, SentBytes: sent.Bytes, SentBitsPerSecond: sent.BitsPerSecond,
			ReceivedBytes: received.Bytes, ReceivedBitsPerSecond: received.BitsPerSecond,
			Retransmits: sent.Retransmits, CPUClientPercent: d.CPU.Host, CPUServerPercent: d.CPU.Remote,
		}
		// UDP: the receiver's view of jitter and loss is the one that counts.
		if udp := received; udp.JitterMs != nil || udp.LostPackets != nil {
			s.JitterMs, s.Packets, s.LostPackets, s.LostPercent = udp.JitterMs, udp.Packets, udp.LostPackets, udp.LostPercent
		}
		return IperfEvent{Kind: "end", Summary: s}, nil
	}
	return IperfEvent{}, nil
}

// RunIperf runs the test and calls emit for every interval as it arrives.
// With a server, it is started first and the client waits for its listening
// line, so the client never races it. Cancelling ctx stops both: iperf3
// ignores the closed stream, so a cancelled run is killed by name.
func RunIperf(ctx context.Context, namespace, clientPod, clientContainer string, server *IperfServer, o IperfOptions, emit func(types.IperfInterval)) (*types.IperfSummary, error) {
	port := o.Port
	if server != nil {
		port = server.Port
		serverCtx, stopServer := context.WithCancel(ctx)
		defer stopServer()
		if err := startIperfServer(serverCtx, namespace, server); err != nil {
			return nil, err
		}
	} else if port == 0 {
		port = IperfDefaultPort
	}

	pr, pw := io.Pipe()
	var summary *types.IperfSummary
	var failure string
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			ev, err := ParseIperfEvent(sc.Bytes())
			if err != nil {
				continue
			}
			switch ev.Kind {
			case "interval":
				if emit != nil {
					emit(*ev.Interval)
				}
			case "end":
				if ev.Summary != nil {
					summary = ev.Summary
				}
			case "error":
				failure = ev.Message
			}
		}
	}()
	clientCmd := BuildIperfClientCommand(o, port)
	execErr := ExecStreamIntoContainer(ctx, namespace, clientPod, clientContainer, clientCmd, pw)
	_ = pw.Close()
	<-done

	if ctx.Err() != nil {
		killIperf(namespace, clientPod, clientContainer, clientCmd[:5])
		if server != nil {
			killIperf(namespace, server.Pod, server.Container, BuildIperfServerCommand(port)[:5])
		}
		return summary, ctx.Err()
	}
	if failure != "" {
		return nil, errors.New(failure)
	}
	if summary == nil {
		if execErr != nil {
			return nil, fmt.Errorf("iperf3 failed: %w", execErr)
		}
		return nil, errors.New("iperf3 produced no result")
	}
	return summary, nil
}

// killIperf ends the iperf3 whose command line starts with prefix, after a
// cancelled run. The port makes the prefix specific to this test.
func killIperf(namespace, pod, container string, prefix []string) {
	_, _, _ = ExecInContainer(namespace, pod, container, []string{"pkill", "-f", "^" + strings.Join(prefix, " ")})
}

// startIperfServer execs the server in its toolbox and returns once it
// listens, or with the reason it did not (a port in use, no iperf3).
func startIperfServer(ctx context.Context, namespace string, server *IperfServer) error {
	pr, pw := io.Pipe()
	listening := make(chan struct{})
	exited := make(chan error, 1)
	go func() {
		err := ExecStreamIntoContainer(ctx, namespace, server.Pod, server.Container, BuildIperfServerCommand(server.Port), pw)
		_ = pw.Close()
		exited <- err
	}()
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "Server listening on") {
				close(listening)
				break
			}
		}
		// Keep draining so the exec never blocks on a full pipe.
		_, _ = io.Copy(io.Discard, pr)
	}()
	select {
	case <-listening:
		return nil
	case err := <-exited:
		if err != nil {
			return fmt.Errorf("iperf3 server on %s did not start: %w", server.Pod, err)
		}
		return fmt.Errorf("iperf3 server on %s exited before listening", server.Pod)
	case <-time.After(iperfListenTimeout):
		return fmt.Errorf("iperf3 server on %s did not start listening in %s", server.Pod, iperfListenTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}
