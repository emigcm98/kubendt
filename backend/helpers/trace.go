package helpers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"

	capsreg "kubendt/capabilities/registry"
	drvreg "kubendt/drivers/registry"
	"kubendt/kubeclient"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Helpers for the traceroute path visualization. A real traceroute runs in the
// shared debug container inside the source pod's netns, and each L3 hop is
// mapped back to a topology node so the frontend can animate the packet.

var (
	// An IPv4 literal or a DNS hostname. The destination is always passed as a
	// single argv element, never through a shell, so this is just defence in depth.
	traceDestRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,253}[A-Za-z0-9])?$`)
	// TTL and responding IP of a hop line in "-n" mode. It stops at the IP so it
	// also matches tcptraceroute lines that put "[open]" before the RTT. The RTT
	// is pulled out separately by traceRTTRe.
	traceHopRe = regexp.MustCompile(`^\s*(\d+)\s+(\d+\.\d+\.\d+\.\d+)`)
	// First "<n> ms" reading on the line.
	traceRTTRe = regexp.MustCompile(`([0-9.]+)\s*ms`)
	// Just the leading TTL, so timeout lines get a hop too.
	traceTTLRe = regexp.MustCompile(`^\s*(\d+)\s`)
	// The ICMP unreachable flag a router appends when it drops the probe, like
	// "!N", "!H", "!X" or "!13".
	traceUnreachRe = regexp.MustCompile(`\s!([A-Za-z0-9]+)`)
	// Target IP from a banner line. traceroute writes "traceroute to host
	// (1.2.3.4), ...", tcptraceroute writes "Tracing the path to 8.8.8.8 on TCP
	// port 80 ...". Prefer an IP in parentheses, otherwise take the first bare IP.
	traceParenIPRe = regexp.MustCompile(`\((\d+\.\d+\.\d+\.\d+)\)`)
	traceAnyIPRe   = regexp.MustCompile(`(\d+\.\d+\.\d+\.\d+)`)
	// One "ip -o -4 addr show" line into (iface, ipv4).
	traceAddrRe = regexp.MustCompile(`^\d+:\s+(\S+)\s+inet\s+(\d+\.\d+\.\d+\.\d+)`)
)

// podIPv4Addresses returns ip to iface for every IPv4 on the pod. It keeps the
// MAC-less TUN devices (ogstun, uesimtun0) that the standard interface listing
// drops, which is what lets a tunnel endpoint like 192.168.10.1 resolve to the
// UPF even though it is a runtime interface not in the declared topology. One
// cheap exec per pod.
func podIPv4Addresses(namespace, podName string) map[string]string {
	out := map[string]string{}
	stdout, _, err := ExecInPod(namespace, podName, []string{"ip", "-o", "-4", "addr", "show"})
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(strings.NewReader(stdout))
	for sc.Scan() {
		m := traceAddrRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		iface := m[1]
		if i := strings.IndexByte(iface, '@'); i >= 0 {
			iface = iface[:i]
		}
		ip := m[2]
		if iface == "lo" || strings.HasPrefix(ip, "127.") {
			continue
		}
		out[ip] = iface
	}
	return out
}

// MaxConsecutiveTraceTimeouts is how many no-reply hops in a row we tolerate
// before giving up. Stops a silent black hole from dragging on to -m 30.
const MaxConsecutiveTraceTimeouts = 3

// ParseTraceUnreachable returns the ICMP unreachable flag ("!N", "!H") a router
// appended to a hop line, or "" for a normal reply.
func ParseTraceUnreachable(line string) string {
	if m := traceUnreachRe.FindStringSubmatch(line); m != nil {
		return "!" + m[1]
	}
	return ""
}

// ParseTraceTarget extracts the resolved target IP from the traceroute banner,
// or "" for any non-banner line.
func ParseTraceTarget(line string) string {
	if !strings.HasPrefix(line, "traceroute to") && !strings.HasPrefix(line, "Tracing the path to") {
		return ""
	}
	if m := traceParenIPRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	if m := traceAnyIPRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// ValidTraceDest reports whether dest is a safe IPv4/hostname to trace toward.
func ValidTraceDest(dest string) bool {
	dest = strings.TrimSpace(dest)
	return dest != "" && len(dest) <= 255 && traceDestRe.MatchString(dest)
}

// BuildTraceCommand returns the argv for the requested probe method. ICMP ("-I")
// is the default. TCP needs a different binary, because the container's
// traceroute is BusyBox and BusyBox has no TCP mode, so TCP goes through
// tcptraceroute (SYN probes to port 80). ICMP and UDP stay on BusyBox traceroute.
func BuildTraceCommand(dest, method string) []string {
	if method == "tcp" {
		return []string{"tcptraceroute", "-n", "-q", "1", "-w", "2", "-m", "30", dest, "80"}
	}
	cmd := []string{"traceroute", "-n", "-q", "1", "-m", "30", "-w", "2"}
	if method == "icmp" {
		cmd = append(cmd, "-I")
	}
	// udp is the BusyBox traceroute default, no extra flag
	return append(cmd, dest)
}

// TraceHop is one parsed hop, tagged with the topology node it resolves to.
// Kind is "l3" for a resolved node, "external" for a real IP that belongs to no
// node (a physical gateway or the internet), or "timeout" for no reply.
type TraceHop struct {
	TTL   int     `json:"ttl"`
	IP    string  `json:"ip,omitempty"`
	RTT   float64 `json:"rtt,omitempty"`
	Node  string  `json:"node,omitempty"`  // resolved pod name, matches the graph node id
	Iface string  `json:"iface,omitempty"` // ingress interface on that node
	Kind  string  `json:"kind"`
	// Unreachable holds the ICMP flag ("!N", "!H") when a router dropped the
	// probe, empty otherwise.
	Unreachable string   `json:"unreachable,omitempty"`
	Path        []string `json:"path,omitempty"` // pods to cross from the previous hop to this one
	// Segment is "link" for a real topology path (maybe through switches) or
	// "tunnel" for an overlay jump like a GTP-U tunnel, which the frontend draws
	// dashed.
	Segment string `json:"segment,omitempty"`
	// Metrics is only set in metrics (mtr) mode.
	Metrics *TraceMetrics `json:"metrics,omitempty"`
}

// TraceMetrics holds one hop's mtr stats. Times in milliseconds, loss in percent.
type TraceMetrics struct {
	Loss  float64 `json:"loss"`
	Avg   float64 `json:"avg"`
	Best  float64 `json:"best"`
	Worst float64 `json:"worst"`
	Last  float64 `json:"last"`
	StDev float64 `json:"stdev"` // jitter
	Gmean float64 `json:"gmean"`
	Sent  int     `json:"sent"`
}

// AnnotateTraceHop resolves an IP to its node and rebuilds the path from prevPod,
// either a link path through switches or a tunnel when there is no path. Both
// the traceroute and mtr paths use it so they annotate the same way.
func AnnotateTraceHop(ip, prevPod string, ipIndex map[string]TraceIPNode, adjacency map[string][]string) (kind, node, iface, segment string, path []string) {
	if n, found := ipIndex[ip]; found {
		kind = "l3"
		node = n.Pod
		iface = n.Iface
		if p := BFSPath(adjacency, prevPod, node); p != nil {
			path = p
			segment = "link"
		} else {
			path = []string{prevPod, node}
			segment = "tunnel"
		}
		return
	}
	kind = "external"
	return
}

// BuildMtrCommand returns the mtr argv for a one-shot JSON report over `cycles`
// probe rounds. ICMP by default, -u for UDP, -T -P 80 for TCP, matching the
// traceroute method choice.
func BuildMtrCommand(dest, method string, cycles int) []string {
	if cycles < 1 {
		cycles = 5
	}
	if cycles > 60 {
		cycles = 60
	}
	cmd := []string{"mtr", "-n", "--json", "-c", strconv.Itoa(cycles)}
	switch method {
	case "udp":
		cmd = append(cmd, "-u")
	case "tcp":
		cmd = append(cmd, "-T", "-P", "80")
	}
	return append(cmd, dest)
}

// MtrHub is one hop from an mtr --json report.
type MtrHub struct {
	Count int
	Host  string
	Loss  float64
	Avg   float64
	Best  float64
	Worst float64
	Last  float64
	StDev float64
	Gmean float64
	Sent  int
}

// ParseMtrReport parses mtr --json output into its ordered hub list.
func ParseMtrReport(b []byte) ([]MtrHub, error) {
	var r struct {
		Report struct {
			Hubs []struct {
				Count int     `json:"count"`
				Host  string  `json:"host"`
				Loss  float64 `json:"Loss%"`
				Snt   int     `json:"Snt"`
				Last  float64 `json:"Last"`
				Avg   float64 `json:"Avg"`
				Best  float64 `json:"Best"`
				Wrst  float64 `json:"Wrst"`
				StDev float64 `json:"StDev"`
				Gmean float64 `json:"Gmean"`
			} `json:"hubs"`
		} `json:"report"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	hubs := make([]MtrHub, 0, len(r.Report.Hubs))
	for _, h := range r.Report.Hubs {
		hubs = append(hubs, MtrHub{
			Count: h.Count, Host: h.Host, Loss: h.Loss,
			Avg: h.Avg, Best: h.Best, Worst: h.Wrst,
			Last: h.Last, StDev: h.StDev, Gmean: h.Gmean, Sent: h.Snt,
		})
	}
	return hubs, nil
}

// RunTrace runs a traceroute (or mtr in metrics mode) inside the debug
// container, calls emit for each resolved hop as it comes, and returns the
// outcome ("delivered", "unreachable" or "unreached"). It is the shared core
// behind the live WebSocket stream and the one-shot REST report. The WebSocket
// emit forwards each hop to the browser, the REST one appends to a slice.
// Cancelling ctx (client disconnect or timeout) aborts the probe.
func RunTrace(ctx context.Context, namespace, pod, container, dest, method string, metrics bool, cycles int,
	ipIndex map[string]TraceIPNode, adjacency map[string][]string, emit func(TraceHop)) (string, error) {

	// Metrics mode: one batched mtr report, then annotate each hub.
	if metrics {
		var buf bytes.Buffer
		if err := ExecStreamIntoContainer(ctx, namespace, pod, container, BuildMtrCommand(dest, method, cycles), &buf); err != nil && ctx.Err() == nil {
			return "", err
		}
		hubs, err := ParseMtrReport(buf.Bytes())
		if err != nil {
			return "", fmt.Errorf("could not parse mtr output: %w", err)
		}
		prevPod := pod
		reached := false
		for _, hub := range hubs {
			hop := TraceHop{TTL: hub.Count}
			// Every hub carries stats (a timeout hub still reports 100% loss).
			hop.Metrics = &TraceMetrics{
				Loss: hub.Loss, Avg: hub.Avg, Best: hub.Best, Worst: hub.Worst,
				Last: hub.Last, StDev: hub.StDev, Gmean: hub.Gmean, Sent: hub.Sent,
			}
			if hub.Host == "" || hub.Host == "???" {
				hop.Kind = "timeout"
			} else {
				hop.IP = hub.Host
				hop.RTT = hub.Avg
				kind, node, iface, seg, path := AnnotateTraceHop(hub.Host, prevPod, ipIndex, adjacency)
				hop.Kind, hop.Node, hop.Iface, hop.Segment, hop.Path = kind, node, iface, seg, path
				if kind == "l3" {
					prevPod = node
				}
			}
			emit(hop)
		}
		if n := len(hubs); n > 0 {
			last := hubs[n-1]
			if last.Host != "" && last.Host != "???" && last.Loss < 100 {
				reached = true
			}
		}
		if reached {
			return "delivered", nil
		}
		return "unreached", nil
	}

	// Trace mode: stream traceroute line by line and emit per hop. A child
	// context lets us stop early on a black hole or an explicit unreachable
	// without cancelling the caller's ctx.
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	stdoutReader, stdoutWriter := io.Pipe()
	prevPod := pod
	targetIP := ""
	consecutive := 0
	reached := false
	dropped := false
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		defer stdoutReader.Close()
		sc := bufio.NewScanner(stdoutReader)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if targetIP == "" {
				if t := ParseTraceTarget(line); t != "" {
					targetIP = t
					continue
				}
			}
			ttl, ip, rtt, isTimeout, ok := ParseTraceHop(line)
			if !ok {
				continue
			}
			hop := TraceHop{TTL: ttl}
			if isTimeout {
				consecutive++
				hop.Kind = "timeout"
				emit(hop)
				if consecutive >= MaxConsecutiveTraceTimeouts {
					runCancel() // silent black hole: give up early
					return
				}
				continue
			}
			consecutive = 0
			hop.IP = ip
			hop.RTT = rtt
			if u := ParseTraceUnreachable(line); u != "" {
				hop.Unreachable = u
				dropped = true
			}
			kind, node, iface, seg, path := AnnotateTraceHop(ip, prevPod, ipIndex, adjacency)
			hop.Kind, hop.Node, hop.Iface, hop.Segment, hop.Path = kind, node, iface, seg, path
			if kind == "l3" {
				prevPod = node
			}
			if targetIP != "" && ip == targetIP {
				reached = true
			}
			// tcptraceroute marks the destination hop [open]/[closed].
			if strings.Contains(line, "[open]") || strings.Contains(line, "[closed]") {
				reached = true
			}
			emit(hop)
			if dropped {
				runCancel() // explicit unreachable: definitive stop
				return
			}
		}
	}()

	execErr := ExecStreamIntoContainer(runCtx, namespace, pod, container, BuildTraceCommand(dest, method), stdoutWriter)
	_ = stdoutWriter.Close()
	<-streamDone

	// A cancel we triggered (early stop or drop), or the caller's, is not an
	// error. Only a real exec failure with the context still live is.
	if execErr != nil && runCtx.Err() == nil {
		return "", execErr
	}
	outcome := "unreached"
	if dropped {
		outcome = "unreachable"
	} else if reached {
		outcome = "delivered"
	}
	return outcome, nil
}

// ParseTraceHop parses one traceroute output line into a TTL and, when present,
// an IP + RTT. Returns ok=false for the banner line and other non-hop lines.
func ParseTraceHop(line string) (ttl int, ip string, rtt float64, isTimeout, ok bool) {
	if m := traceHopRe.FindStringSubmatch(line); m != nil {
		ttl, _ = strconv.Atoi(m[1])
		if r := traceRTTRe.FindStringSubmatch(line); r != nil {
			rtt, _ = strconv.ParseFloat(r[1], 64)
		}
		return ttl, m[2], rtt, false, true
	}
	// A line that starts with a hop number but has no IP is a timeout ("* * *").
	if m := traceTTLRe.FindStringSubmatch(line); m != nil {
		if strings.Contains(line, "*") {
			ttl, _ = strconv.Atoi(m[1])
			return ttl, "", 0, true, true
		}
	}
	return 0, "", 0, false, false
}

// TraceIPNode maps an IP to the node (pod) and interface that owns it.
type TraceIPNode struct {
	Pod   string
	Iface string
}

func stripMask(cidr string) string {
	if i := strings.IndexByte(cidr, '/'); i >= 0 {
		return cidr[:i]
	}
	return cidr
}

// BuildTraceIPIndex builds an IP to {pod, iface} index for the whole namespace
// from two sources, so tunnels and config-applied addresses are all covered.
// One is the IPs declared on the topology links, which catch guest addresses a
// driver keeps inside a VM. The other is the live interfaces of every pod, where
// TUN devices (ogstun, uesimtun0) and config-applied addresses show up. The live
// pass wins on conflict since it reflects the running dataplane.
func BuildTraceIPIndex(namespace string) (map[string]TraceIPNode, error) {
	idx := make(map[string]TraceIPNode)

	// Declared link IPs first, as a fallback.
	if links, err := BuildLinksFromTopologyCRDs(namespace); err == nil {
		for _, l := range links {
			if l.LocalIP != "" && l.Node != "" && l.Node != "external" {
				idx[stripMask(l.LocalIP)] = TraceIPNode{Pod: l.Node, Iface: l.LocalIntf}
			}
			if l.PeerIP != "" && l.PeerNode != "" && l.PeerNode != "external" {
				idx[stripMask(l.PeerIP)] = TraceIPNode{Pod: l.PeerNode, Iface: l.PeerIntf}
			}
		}
	}

	// Then the live IPv4 addresses of every pod, in parallel. This keeps the
	// MAC-less TUN devices (ogstun, uesimtun0) so tunnel endpoints resolve.
	pods, err := kubeclient.Clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("error listing pods for trace index: %w", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, pod := range pods.Items {
		if pod.Status.Phase != "Running" {
			continue
		}
		wg.Add(1)
		go func(podName string) {
			defer wg.Done()
			addrs := podIPv4Addresses(namespace, podName)
			if len(addrs) == 0 {
				return
			}
			mu.Lock()
			for ip, iface := range addrs {
				idx[ip] = TraceIPNode{Pod: podName, Iface: iface}
			}
			mu.Unlock()
		}(pod.Name)
	}
	wg.Wait()
	return idx, nil
}

// BuildPodAdjacency returns an undirected pod to neighbour-pods map from the
// topology links, external endpoints left out. Used to rebuild the L2 segment
// (through switches) between two consecutive L3 hops.
func BuildPodAdjacency(namespace string) map[string][]string {
	adj := make(map[string][]string)
	links, err := BuildLinksFromTopologyCRDs(namespace)
	if err != nil {
		return adj
	}
	add := func(a, b string) {
		if a == "" || b == "" || a == "external" || b == "external" {
			return
		}
		adj[a] = append(adj[a], b)
	}
	for _, l := range links {
		add(l.Node, l.PeerNode)
		add(l.PeerNode, l.Node)
	}
	return adj
}

// BFSPath returns the shortest path of pod names from src to dst, both ends
// included, or nil when they are not connected (the tunnel case). src==dst
// returns [src].
func BFSPath(adj map[string][]string, src, dst string) []string {
	if src == "" || dst == "" {
		return nil
	}
	if src == dst {
		return []string{src}
	}
	prev := map[string]string{src: ""}
	queue := []string{src}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range adj[cur] {
			if _, seen := prev[nb]; seen {
				continue
			}
			prev[nb] = cur
			if nb == dst {
				// reconstruct
				path := []string{dst}
				for p := cur; p != ""; p = prev[p] {
					path = append([]string{p}, path...)
				}
				return path
			}
			queue = append(queue, nb)
		}
	}
	return nil
}

// FindRunningDebugContainer returns the name of a running debug container in the
// pod (the ephemeral netshoot husk capture and trace inject), or "" if there is
// none. Trace and capture share one per pod. A pod can only accumulate ephemeral
// containers, Kubernetes never lets us remove them, so reusing keeps them from
// piling up.
func FindRunningDebugContainer(namespace, podName string) string {
	pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	for _, cs := range pod.Status.EphemeralContainerStatuses {
		if ValidCaptureContainer(cs.Name) && cs.State.Running != nil {
			return cs.Name
		}
	}
	return ""
}

// EnsureDebugContainer reuses a running debug container in the pod when one
// exists (injected by a previous trace or by capture), or injects a fresh one.
// Returns the container name and whether it was reused.
func EnsureDebugContainer(namespace, podName string) (string, bool, error) {
	if existing := FindRunningDebugContainer(namespace, podName); existing != "" {
		return existing, true, nil
	}
	name, err := InjectCaptureContainer(namespace, podName)
	return name, false, err
}

// DriverIsL3Capable reports whether the named driver exposes the L3Capable
// capability. An empty or unknown driver counts as not L3-capable.
func DriverIsL3Capable(driverName string) bool {
	if strings.TrimSpace(driverName) == "" {
		return false
	}
	inst, err := drvreg.NewByName(driverName)
	if err != nil {
		return false
	}
	for _, cd := range capsreg.ForDriver(inst) {
		if cd.ID() == "L3Capable" {
			return true
		}
	}
	return false
}
