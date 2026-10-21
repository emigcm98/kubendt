package helpers

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"kubendt/executor"
	"kubendt/kubeclient"
	"kubendt/types"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"kubendt/actions"
)

// tcUseToolbox remembers per pod whether shaping goes through the netshoot
// toolbox (true) or the pod's own tc (false). A pod lands on the toolbox when
// its image ships no tc, or when a toolbox left by capture or trace was
// already running at first contact, which saves the exec that would only
// prove tc is missing. A running pod never gains or loses the binary, so the
// choice is stable; after a recreation the toolbox path re-injects on its own.
var tcUseToolbox sync.Map // "namespace/pod" -> bool

func init() {
	// Wire the tc_universal executor to the pod-exec + toolbox machinery here,
	// since the executor package must not import helpers.
	executor.RunTCHook = RunTC
	executor.RunTCBatchHook = RunTCBatch
}

// RunTC runs a tc command in a pod's network namespace. Shaping is universal:
// it prefers the pod's own tc (one exec, the common case) and falls back to an
// ephemeral netshoot toolbox (ships tc, has NET_ADMIN) for images without it.
// The qdisc lives in the shared pod netns, so it persists after the exec ends.
func RunTC(namespace, podName string, args []string) (stdout, stderr string, err error) {
	return runTCWith(namespace, podName, args, "")
}

// RunTCBatch feeds lines (without the "tc" prefix) to `tc -b -` in one exec.
// tc stops at the first line that fails and reports it.
func RunTCBatch(namespace, podName string, lines []string) (stdout, stderr string, err error) {
	return runTCWith(namespace, podName, []string{"tc", "-b", "-"}, strings.Join(lines, "\n")+"\n")
}

func runTCWith(namespace, podName string, args []string, stdin string) (stdout, stderr string, err error) {
	key := namespace + "/" + podName
	choice, known := tcUseToolbox.Load(key)
	if !known && findRunningToolbox(namespace, podName) != "" {
		choice, known = true, true
		tcUseToolbox.Store(key, true)
	}
	if known && choice.(bool) {
		return runTCInToolbox(namespace, podName, args, stdin)
	}
	stdout, stderr, err = execTC(namespace, podName, "", args, stdin)
	switch {
	case err == nil:
		tcUseToolbox.Store(key, false)
	case isBinaryNotFound(err, stderr):
		tcUseToolbox.Store(key, true)
		return runTCInToolbox(namespace, podName, args, stdin)
	}
	return stdout, stderr, err
}

// runTCInToolbox runs the tc command inside the pod's toolbox, injecting one
// when the pod has none yet.
func runTCInToolbox(namespace, podName string, args []string, stdin string) (string, string, error) {
	container, _, err := EnsureToolbox(namespace, podName, nil)
	if err != nil {
		return "", "", fmt.Errorf("could not ensure the toolbox: %w", err)
	}
	return execTC(namespace, podName, container, args, stdin)
}

// execTC is the exec behind every tc call, in the pod's main container when
// container is empty. stdin, when given, is what a batch reads.
func execTC(namespace, podName, container string, args []string, stdin string) (string, string, error) {
	var outBuf, errBuf bytes.Buffer
	req := kubeclient.Clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Container: container,
			Command:   args,
			Stdin:     stdin != "",
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)
	exec, err := kubeclient.NewExecutor("POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("exec init error: %w", err)
	}
	opts := remotecommand.StreamOptions{Stdout: &outBuf, Stderr: &errBuf}
	if stdin != "" {
		opts.Stdin = strings.NewReader(stdin)
	}
	err = exec.StreamWithContext(context.TODO(), opts)
	return strings.TrimSpace(outBuf.String()), strings.TrimSpace(errBuf.String()), err
}

// isBinaryNotFound reports whether an exec failed because tc is absent from the
// image, the signal to retry inside the toolbox instead.
func isBinaryNotFound(err error, stderr string) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error() + " " + stderr)
	return strings.Contains(s, "executable file not found") ||
		strings.Contains(s, "no such file or directory")
}

// tcResetCommand puts the root of an interface back to an explicit noqueue.
// `tc qdisc replace` over a qdisc of the same kind changes it in place and the
// kernel keeps every parameter the request does not mention (corrupt, reorder,
// rate), so a re-apply has to start from a clean root. Replacing with noqueue
// succeeds whether or not something is installed, where `tc qdisc del` exits 2
// on an unshaped interface.
func tcResetCommand(iface string) []string {
	return []string{"tc", "qdisc", "replace", "dev", iface, "root", "noqueue"}
}

// buildAddQdiscCommand emits the tc CLI that installs a root qdisc on an
// interface: a reset, then the qdisc itself. Shaping is universal, so the
// commands are the same for every node and live here rather than on a driver
// capability.
func buildAddQdiscCommand(iface, qdisc string, params []string) [][]string {
	cmd := append([]string{"tc", "qdisc", "replace", "dev", iface, "root", qdisc}, params...)
	return [][]string{tcResetCommand(iface), cmd}
}

// Shaping is universal: tc runs in the pod's netns, so any pod can be shaped
// whatever its driver. The actions register against any driver and always
// run through the tc_universal executor (the pod's own tc, or the toolbox).
func init() {
	actions.Register[any](
		actions.Capability{ID: "tc", Label: "Traffic control", Description: "Shaping of any interface, in the pod's network namespace."},
		actions.Action[any]{Type: "add_qdisc", Label: "Shape interface", Params: actions.P(actions.Iface, actions.TCParams), Executor: executor.TCUniversalExecutorName, State: "qdisc", Key: func(a types.ActionEntry) string { return a.Iface },
			Run: func(_ any, a types.ActionEntry) ([][]string, error) {
				commands, err := buildShapingCommands(a.Iface, a.TCParams)
				if err != nil {
					return nil, fmt.Errorf("add_qdisc on %s: %w", a.Iface, err)
				}
				return commands, nil
			}},
		actions.Action[any]{Type: "del_qdisc", Label: "Clear shaping", Params: actions.P(actions.Iface), Executor: executor.TCUniversalExecutorName, State: "qdisc", Key: func(a types.ActionEntry) string { return a.Iface }, Clears: true,
			Run: func(_ any, a types.ActionEntry) ([][]string, error) { return buildDelQdiscCommand(a.Iface), nil }},
	)
}

// buildDelQdiscCommand removes whatever is on the root and leaves the
// interface on the kernel default. The reset first makes the delete succeed
// on an unshaped interface too, so removing nothing is not an error.
func buildDelQdiscCommand(iface string) [][]string {
	return [][]string{
		tcResetCommand(iface),
		{"tc", "qdisc", "del", "dev", iface, "root"},
	}
}

// Accepted ranges. Delay and jitter in ms, percentages 0 to 100, limit in
// packets. netem's default limit is 1000.
const (
	tcMaxDelayMs   = 60000
	tcMaxJitterMs  = 10000
	tcMaxLatencyMs = 5000
	tcMinLimit     = 1
	tcMaxLimit     = 1000000
)

// Defaults for the tbf parameters tc insists on when the request only gives a
// rate: a small bucket and a short queue, enough for any rate the examples use.
const (
	tcDefaultBurst   = "32kbit"
	tcDefaultLatency = "50ms"
)

// tcValue is one parsed shaping field: whether it was given, its magnitude in
// the unit the range is expressed in, and the text handed to tc.
type tcValue struct {
	set   bool
	n     float64
	canon string
}

var (
	tcTimeRe    = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*(us|usec|usecs|ms|msec|msecs|s|sec|secs)?\s*$`)
	tcPercentRe = regexp.MustCompile(`^\s*([0-9]+(?:\.[0-9]+)?)\s*%?\s*$`)
	tcRateRe    = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*([kmgt]?(?:bit|bps))?\s*$`)
	tcSizeRe    = regexp.MustCompile(`(?i)^\s*([0-9]+(?:\.[0-9]+)?)\s*(b|k|kb|m|mb|g|gb|kbit|mbit|gbit)?\s*$`)
)

// tcTime parses a tc time ("50ms", "1.5s", "200us", a bare number means ms)
// and checks it against a range in milliseconds.
func tcTime(field, s string, minMs, maxMs float64) (tcValue, error) {
	if strings.TrimSpace(s) == "" {
		return tcValue{}, nil
	}
	m := tcTimeRe.FindStringSubmatch(s)
	if m == nil {
		return tcValue{}, fmt.Errorf("tcparams.%s %q is not a time (use ms, us or s)", field, s)
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	unit := strings.ToLower(m[2])
	ms := n
	switch {
	case strings.HasPrefix(unit, "us"):
		ms = n / 1000
	case strings.HasPrefix(unit, "s"):
		ms = n * 1000
	}
	if ms < minMs || ms > maxMs {
		return tcValue{}, fmt.Errorf("tcparams.%s %q is out of range (%g to %g ms)", field, s, minMs, maxMs)
	}
	if unit == "" {
		unit = "ms"
	}
	return tcValue{set: true, n: ms, canon: m[1] + unit}, nil
}

// tcPercent parses "1%", "0.5" (the sign is optional) within 0 to 100.
func tcPercent(field, s string) (tcValue, error) {
	if strings.TrimSpace(s) == "" {
		return tcValue{}, nil
	}
	m := tcPercentRe.FindStringSubmatch(s)
	if m == nil {
		return tcValue{}, fmt.Errorf("tcparams.%s %q is not a percentage", field, s)
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	if n > 100 {
		return tcValue{}, fmt.Errorf("tcparams.%s %q is out of range (0 to 100%%)", field, s)
	}
	return tcValue{set: true, n: n, canon: m[1] + "%"}, nil
}

// tcRate parses a tc rate ("500kbit", "1mbit", "1Gbit", a bare number means
// bit/s). The unit is lowercased, tc reads them case-insensitively.
func tcRate(field, s string) (tcValue, error) {
	if strings.TrimSpace(s) == "" {
		return tcValue{}, nil
	}
	m := tcRateRe.FindStringSubmatch(s)
	if m == nil {
		return tcValue{}, fmt.Errorf("tcparams.%s %q is not a rate (use bit, kbit, mbit or gbit)", field, s)
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	if n <= 0 {
		return tcValue{}, fmt.Errorf("tcparams.%s must be greater than zero", field)
	}
	unit := strings.ToLower(m[2])
	if unit == "" {
		unit = "bit"
	}
	return tcValue{set: true, n: n, canon: m[1] + unit}, nil
}

// tcSize parses a tc size ("32kbit", "4kb", "1600" bytes), as tbf's burst.
func tcSize(field, s string) (tcValue, error) {
	if strings.TrimSpace(s) == "" {
		return tcValue{}, nil
	}
	m := tcSizeRe.FindStringSubmatch(s)
	if m == nil {
		return tcValue{}, fmt.Errorf("tcparams.%s %q is not a size (use b, kb, mb, kbit or mbit)", field, s)
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	if n <= 0 {
		return tcValue{}, fmt.Errorf("tcparams.%s must be greater than zero", field)
	}
	return tcValue{set: true, n: n, canon: m[1] + strings.ToLower(m[2])}, nil
}

// buildShapingCommands validates a shaping request and returns the tc rows
// that install it: a reset, then one qdisc for netem and tbf, or the htb tree
// with its classes, leaves and filters. Every problem is an error naming the
// field and the accepted values, nothing is dropped on the way.
func buildShapingCommands(iface string, p *types.TCParamEntry) ([][]string, error) {
	if p == nil {
		return nil, fmt.Errorf("tcparams is required")
	}
	if strings.TrimSpace(p.Qdisc) == "htb" || (p.Qdisc == "" && len(p.Classes) > 0) {
		return htbCommands(iface, p)
	}
	qdisc, params, err := buildTCParams(p)
	if err != nil {
		return nil, err
	}
	return buildAddQdiscCommand(iface, qdisc, params), nil
}

// buildTCParams validates a classless shaping request and returns the qdisc
// kind and the tc arguments that follow it.
func buildTCParams(p *types.TCParamEntry) (qdisc string, args []string, err error) {
	if p == nil {
		return "", nil, fmt.Errorf("tcparams is required")
	}
	if len(p.Classes) > 0 {
		return "", nil, fmt.Errorf("tcparams.classes needs qdisc htb")
	}
	qdisc = strings.TrimSpace(p.Qdisc)
	if qdisc == "" {
		qdisc = "netem"
	}
	switch qdisc {
	case "netem":
		args, err = netemArgs(p)
	case "tbf":
		args, err = tbfArgs(p)
	default:
		err = fmt.Errorf("tcparams.qdisc %q is not supported (netem, tbf, htb)", p.Qdisc)
	}
	return qdisc, args, err
}

// netemDistributions are the jitter distributions tc ships tables for.
// uniform is tc's default and has no table, so it is never sent.
var netemDistributions = map[string]bool{"uniform": true, "normal": true, "pareto": true, "paretonormal": true}

func netemArgs(p *types.TCParamEntry) ([]string, error) {
	for _, f := range []struct{ name, val string }{{"burst", p.Burst}, {"latency", p.Latency}} {
		if f.val != "" {
			return nil, fmt.Errorf("tcparams.%s is a tbf parameter and does not apply to netem", f.name)
		}
	}
	delay, err := tcTime("delay", p.Delay, 0, tcMaxDelayMs)
	if err != nil {
		return nil, err
	}
	jitter, err := tcTime("jitter", p.Jitter, 0, tcMaxJitterMs)
	if err != nil {
		return nil, err
	}
	if jitter.n > 0 && delay.n <= 0 {
		return nil, fmt.Errorf("tcparams.jitter requires a delay greater than zero")
	}
	delayCorr, err := tcPercent("delay_correlation", p.DelayCorrelation)
	if err != nil {
		return nil, err
	}
	if delayCorr.n > 0 && jitter.n <= 0 {
		return nil, fmt.Errorf("tcparams.delay_correlation requires a jitter greater than zero")
	}
	distribution := strings.ToLower(strings.TrimSpace(p.Distribution))
	if distribution != "" {
		if !netemDistributions[distribution] {
			return nil, fmt.Errorf("tcparams.distribution %q is not supported (uniform, normal, pareto, paretonormal)", p.Distribution)
		}
		if jitter.n <= 0 {
			return nil, fmt.Errorf("tcparams.distribution requires a jitter greater than zero")
		}
	}
	loss, err := tcPercent("loss", p.Loss)
	if err != nil {
		return nil, err
	}
	lossCorr, err := tcPercent("loss_correlation", p.LossCorrelation)
	if err != nil {
		return nil, err
	}
	if lossCorr.n > 0 && loss.n <= 0 {
		return nil, fmt.Errorf("tcparams.loss_correlation requires a loss greater than zero")
	}
	gemodel, err := gemodelArgs(p.Gemodel)
	if err != nil {
		return nil, err
	}
	if gemodel != nil && loss.n > 0 {
		return nil, fmt.Errorf("tcparams.loss and tcparams.gemodel are exclusive, pick one loss model")
	}
	duplicate, err := tcPercent("duplicate", p.Duplicate)
	if err != nil {
		return nil, err
	}
	corrupt, err := tcPercent("corrupt", p.Corrupt)
	if err != nil {
		return nil, err
	}
	reorder, err := tcPercent("reorder", p.Reorder)
	if err != nil {
		return nil, err
	}
	if reorder.n > 0 && delay.n <= 0 {
		return nil, fmt.Errorf("tcparams.reorder requires a delay greater than zero")
	}
	reorderCorr, err := tcPercent("reorder_correlation", p.ReorderCorrelation)
	if err != nil {
		return nil, err
	}
	if reorderCorr.n > 0 && reorder.n <= 0 {
		return nil, fmt.Errorf("tcparams.reorder_correlation requires a reorder greater than zero")
	}
	rate, err := tcRate("rate", p.Rate)
	if err != nil {
		return nil, err
	}
	if p.Limit != nil && (*p.Limit < tcMinLimit || *p.Limit > tcMaxLimit) {
		return nil, fmt.Errorf("tcparams.limit %d is out of range (%d to %d packets)", *p.Limit, tcMinLimit, tcMaxLimit)
	}

	// Zero means "none" for every impairment, so it is left out rather than
	// sent as an explicit zero.
	args := []string{}
	if delay.n > 0 {
		args = append(args, "delay", delay.canon)
		if jitter.n > 0 {
			args = append(args, jitter.canon)
			if delayCorr.n > 0 {
				args = append(args, delayCorr.canon)
			}
			if distribution != "" && distribution != "uniform" {
				args = append(args, "distribution", distribution)
			}
		}
	}
	if loss.n > 0 {
		args = append(args, "loss", loss.canon)
		if lossCorr.n > 0 {
			args = append(args, lossCorr.canon)
		}
	}
	args = append(args, gemodel...)
	for _, v := range []struct {
		name string
		val  tcValue
	}{{"duplicate", duplicate}, {"corrupt", corrupt}} {
		if v.val.n > 0 {
			args = append(args, v.name, v.val.canon)
		}
	}
	if reorder.n > 0 {
		args = append(args, "reorder", reorder.canon)
		if reorderCorr.n > 0 {
			args = append(args, reorderCorr.canon)
		}
	}
	if rate.set {
		args = append(args, "rate", rate.canon)
	}
	if p.Limit != nil {
		args = append(args, "limit", strconv.Itoa(*p.Limit))
	}
	return args, nil
}

// gemodelArgs emits `loss gemodel p [r [1-h [1-k]]]`. tc reads the values by
// position, so a later one cannot be given without the ones before it.
func gemodelArgs(g *types.GemodelSpec) ([]string, error) {
	if g == nil {
		return nil, nil
	}
	args := []string{"loss", "gemodel"}
	prev := ""
	for _, f := range []struct{ name, val string }{{"p", g.P}, {"r", g.R}, {"1-h", g.H}, {"1-k", g.K}} {
		if f.val == "" {
			if f.name == "p" {
				return nil, fmt.Errorf("tcparams.gemodel.p is required")
			}
			prev = f.name
			continue
		}
		if prev != "" {
			return nil, fmt.Errorf("tcparams.gemodel.%s needs tcparams.gemodel.%s as well", f.name, prev)
		}
		v, err := tcPercent("gemodel."+f.name, f.val)
		if err != nil {
			return nil, err
		}
		args = append(args, v.canon)
	}
	return args, nil
}

func tbfArgs(p *types.TCParamEntry) ([]string, error) {
	for _, f := range []struct{ name, val string }{
		{"delay", p.Delay}, {"jitter", p.Jitter}, {"delay_correlation", p.DelayCorrelation}, {"distribution", p.Distribution},
		{"loss", p.Loss}, {"loss_correlation", p.LossCorrelation}, {"duplicate", p.Duplicate}, {"corrupt", p.Corrupt},
		{"reorder", p.Reorder}, {"reorder_correlation", p.ReorderCorrelation},
	} {
		if f.val != "" {
			return nil, fmt.Errorf("tcparams.%s is a netem parameter and does not apply to tbf", f.name)
		}
	}
	if p.Gemodel != nil {
		return nil, fmt.Errorf("tcparams.gemodel is a netem parameter and does not apply to tbf")
	}
	if p.Limit != nil {
		return nil, fmt.Errorf("tcparams.limit is a netem parameter and does not apply to tbf")
	}
	rate, err := tcRate("rate", p.Rate)
	if err != nil {
		return nil, err
	}
	if !rate.set {
		return nil, fmt.Errorf("tcparams.rate is required for tbf")
	}
	burstIn := p.Burst
	if burstIn == "" {
		burstIn = tcDefaultBurst
	}
	burst, err := tcSize("burst", burstIn)
	if err != nil {
		return nil, err
	}
	latencyIn := p.Latency
	if latencyIn == "" {
		latencyIn = tcDefaultLatency
	}
	latency, err := tcTime("latency", latencyIn, 1, tcMaxLatencyMs)
	if err != nil {
		return nil, err
	}
	return []string{"rate", rate.canon, "burst", burst.canon, "latency", latency.canon}, nil
}

// htb trees. The root class 1:1 carries the link rate every class shares, so
// a class can borrow what its siblings leave. Classes get 1:11, 1:12, ...,
// their leaf qdisc the same minor as handle, and flower filters on the root
// send matching traffic to them. One class takes everything else.

const (
	tcMaxClasses  = 50
	tcMinorBase   = 10
	tcLeafQueues  = "pfifo, fq_codel, sfq, netem"
	tcMatchProtos = "tcp, udp, icmp"
)

var (
	tcLeafQueueSet  = map[string]bool{"pfifo": true, "fq_codel": true, "sfq": true, "netem": true}
	tcMatchProtoSet = map[string]bool{"tcp": true, "udp": true, "icmp": true}
)

func htbCommands(iface string, p *types.TCParamEntry) ([][]string, error) {
	for _, f := range []struct{ name, val string }{
		{"delay", p.Delay}, {"jitter", p.Jitter}, {"distribution", p.Distribution}, {"loss", p.Loss},
		{"duplicate", p.Duplicate}, {"corrupt", p.Corrupt}, {"reorder", p.Reorder}, {"burst", p.Burst}, {"latency", p.Latency},
	} {
		if f.val != "" {
			return nil, fmt.Errorf("tcparams.%s does not apply to htb, put impairments in a class shaping", f.name)
		}
	}
	if p.Gemodel != nil || p.Limit != nil {
		return nil, fmt.Errorf("tcparams.gemodel and tcparams.limit do not apply to htb, put them in a class shaping")
	}
	link, err := tcRate("rate", p.Rate)
	if err != nil {
		return nil, err
	}
	if !link.set {
		return nil, fmt.Errorf("tcparams.rate is required for htb, it is the link rate the classes share")
	}
	linkBits := rateBits(link.canon)
	if len(p.Classes) == 0 {
		return nil, fmt.Errorf("tcparams.classes is required for htb")
	}
	if len(p.Classes) > tcMaxClasses {
		return nil, fmt.Errorf("tcparams.classes has %d entries, at most %d", len(p.Classes), tcMaxClasses)
	}

	rows := [][]string{tcResetCommand(iface)}
	defaultMinor := ""
	var sumBits float64
	var classRows, leafRows, filterRows [][]string
	filterPrio := 0
	for i, c := range p.Classes {
		where := fmt.Sprintf("tcparams.classes[%d]", i)
		minor := strconv.Itoa(tcMinorBase + i + 1)
		classid := "1:" + minor
		rate, err := tcRate(where+".rate", c.Rate)
		if err != nil {
			return nil, err
		}
		if !rate.set {
			return nil, fmt.Errorf("%s.rate is required", where)
		}
		ceil := link
		if c.Ceil != "" {
			if ceil, err = tcRate(where+".ceil", c.Ceil); err != nil {
				return nil, err
			}
		}
		rb, cb := rateBits(rate.canon), rateBits(ceil.canon)
		if rb > linkBits {
			return nil, fmt.Errorf("%s.rate %s is above the link rate %s", where, rate.canon, link.canon)
		}
		if cb > linkBits {
			return nil, fmt.Errorf("%s.ceil %s is above the link rate %s", where, ceil.canon, link.canon)
		}
		if cb < rb {
			return nil, fmt.Errorf("%s.ceil %s is below its rate %s", where, ceil.canon, rate.canon)
		}
		sumBits += rb
		if c.Prio != nil && (*c.Prio < 0 || *c.Prio > 7) {
			return nil, fmt.Errorf("%s.prio %d is out of range (0 to 7)", where, *c.Prio)
		}
		if c.Default {
			if defaultMinor != "" {
				return nil, fmt.Errorf("%s: only one class can be the default", where)
			}
			defaultMinor = minor
		} else if len(c.Match) == 0 {
			return nil, fmt.Errorf("%s needs a match, or default: true to take the rest of the traffic", where)
		}

		classRow := []string{"tc", "class", "add", "dev", iface, "parent", "1:1", "classid", classid, "htb", "rate", rate.canon, "ceil", ceil.canon}
		if c.Prio != nil {
			classRow = append(classRow, "prio", strconv.Itoa(*c.Prio))
		}
		classRows = append(classRows, classRow)

		leaf, err := leafQdiscArgs(where, c)
		if err != nil {
			return nil, err
		}
		leafRows = append(leafRows, append([]string{"tc", "qdisc", "add", "dev", iface, "parent", classid, "handle", minor + ":"}, leaf...))

		for j, m := range c.Match {
			protocol, keys, err := flowerArgs(fmt.Sprintf("%s.match[%d]", where, j), m)
			if err != nil {
				return nil, err
			}
			filterPrio++
			row := []string{"tc", "filter", "add", "dev", iface, "parent", "1:", "protocol", protocol, "prio", strconv.Itoa(filterPrio), "flower"}
			row = append(row, keys...)
			filterRows = append(filterRows, append(row, "flowid", classid))
		}
	}
	if defaultMinor == "" {
		return nil, fmt.Errorf("tcparams.classes: one class must have default: true, or unmatched traffic bypasses the tree")
	}
	if sumBits > linkBits {
		return nil, fmt.Errorf("tcparams.classes: the class rates add up to %s, above the link rate %s", fmtTCRate(sumBits), link.canon)
	}

	rows = append(rows,
		[]string{"tc", "qdisc", "replace", "dev", iface, "root", "handle", "1:", "htb", "default", defaultMinor},
		[]string{"tc", "class", "add", "dev", iface, "parent", "1:", "classid", "1:1", "htb", "rate", link.canon, "ceil", link.canon},
	)
	rows = append(rows, classRows...)
	rows = append(rows, leafRows...)
	rows = append(rows, filterRows...)
	return rows, nil
}

// leafQdiscArgs is the qdisc under a class: a netem with the composite
// impairments (its rate is the class rate), or a plain queue.
func leafQdiscArgs(where string, c types.TCClassEntry) ([]string, error) {
	queue := strings.TrimSpace(c.Queue)
	if queue == "" {
		queue = "pfifo"
		if c.Shaping != nil {
			queue = "netem"
		}
	}
	if !tcLeafQueueSet[queue] {
		return nil, fmt.Errorf("%s.queue %q is not supported (%s)", where, c.Queue, tcLeafQueues)
	}
	if c.Shaping != nil && queue != "netem" {
		return nil, fmt.Errorf("%s.shaping needs queue netem", where)
	}
	if c.Limit != nil && queue != "pfifo" {
		return nil, fmt.Errorf("%s.limit applies to the pfifo queue only", where)
	}
	switch queue {
	case "netem":
		if c.Shaping == nil {
			return nil, fmt.Errorf("%s.queue netem needs a shaping", where)
		}
		if c.Shaping.Rate != "" {
			return nil, fmt.Errorf("%s.shaping.rate does not apply, the class rate and ceil cap the class", where)
		}
		if c.Shaping.Qdisc != "" && c.Shaping.Qdisc != "netem" {
			return nil, fmt.Errorf("%s.shaping.qdisc must be netem", where)
		}
		args, err := netemArgs(c.Shaping)
		if err != nil {
			return nil, fmt.Errorf("%s.shaping: %w", where, err)
		}
		return append([]string{"netem"}, args...), nil
	case "pfifo":
		limit := 1000
		if c.Limit != nil {
			if *c.Limit < tcMinLimit || *c.Limit > tcMaxLimit {
				return nil, fmt.Errorf("%s.limit %d is out of range (%d to %d packets)", where, *c.Limit, tcMinLimit, tcMaxLimit)
			}
			limit = *c.Limit
		}
		return []string{"pfifo", "limit", strconv.Itoa(limit)}, nil
	case "sfq":
		return []string{"sfq", "perturb", "10"}, nil
	default:
		return []string{queue}, nil
	}
}

// flowerArgs turns a match into flower keys and the protocol the filter is
// registered under: ip, or 802.1Q with the IP keys behind vlan_ethtype.
func flowerArgs(where string, m types.TCMatch) (protocol string, keys []string, err error) {
	proto := strings.ToLower(strings.TrimSpace(m.Proto))
	if proto != "" && !tcMatchProtoSet[proto] {
		return "", nil, fmt.Errorf("%s.proto %q is not supported (%s)", where, m.Proto, tcMatchProtos)
	}
	ip := []string{}
	if proto != "" {
		ip = append(ip, "ip_proto", proto)
	}
	for _, f := range []struct{ name, key, val string }{{"src", "src_ip", m.Src}, {"dst", "dst_ip", m.Dst}} {
		if f.val == "" {
			continue
		}
		if _, _, cerr := net.ParseCIDR(f.val); cerr != nil && net.ParseIP(f.val) == nil {
			return "", nil, fmt.Errorf("%s.%s %q is not an IP address or CIDR", where, f.name, f.val)
		}
		ip = append(ip, f.key, f.val)
	}
	for _, f := range []struct {
		name, key string
		val       *int
	}{{"srcPort", "src_port", m.SrcPort}, {"dstPort", "dst_port", m.DstPort}} {
		if f.val == nil {
			continue
		}
		if proto != "tcp" && proto != "udp" {
			return "", nil, fmt.Errorf("%s.%s needs proto tcp or udp", where, f.name)
		}
		if *f.val < 1 || *f.val > 65535 {
			return "", nil, fmt.Errorf("%s.%s %d is out of range (1 to 65535)", where, f.name, *f.val)
		}
		ip = append(ip, f.key, strconv.Itoa(*f.val))
	}
	if m.DSCP != nil {
		if *m.DSCP < 0 || *m.DSCP > 63 {
			return "", nil, fmt.Errorf("%s.dscp %d is out of range (0 to 63)", where, *m.DSCP)
		}
		ip = append(ip, "ip_tos", fmt.Sprintf("0x%x/0xfc", *m.DSCP<<2))
	}
	if m.VLAN != nil {
		if *m.VLAN < 1 || *m.VLAN > 4094 {
			return "", nil, fmt.Errorf("%s.vlan %d is out of range (1 to 4094)", where, *m.VLAN)
		}
		keys = append(keys, "vlan_id", strconv.Itoa(*m.VLAN))
		if len(ip) > 0 {
			keys = append(keys, "vlan_ethtype", "ip")
		}
		return "802.1Q", append(keys, ip...), nil
	}
	if len(ip) == 0 {
		return "", nil, fmt.Errorf("%s needs at least one of proto, src, dst, srcPort, dstPort, dscp or vlan", where)
	}
	return "ip", ip, nil
}

// rateBits converts a canonical rate ("500kbit", "2mbit", "1gbit") to bits
// per second, for comparing class rates against the link rate.
func rateBits(canon string) float64 {
	m := tcRateRe.FindStringSubmatch(canon)
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	scale := map[string]float64{
		"": 1, "bit": 1, "kbit": 1e3, "mbit": 1e6, "gbit": 1e9, "tbit": 1e12,
		"bps": 8, "kbps": 8e3, "mbps": 8e6, "gbps": 8e9, "tbps": 8e12,
	}[strings.ToLower(m[2])]
	return n * scale
}
