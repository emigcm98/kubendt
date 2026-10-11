package helpers

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"kubendt/executor"
	"kubendt/types"
)

// tcToolboxStartTimeout bounds the wait for the netshoot toolbox to come up.
// The first pull of the image dominates it; later shaping reuses the container.
const tcToolboxStartTimeout = 120 * time.Second

// tcUseToolbox remembers per pod whether shaping goes through the netshoot
// toolbox (true) or the pod's own tc (false). A pod lands on the toolbox when
// its image ships no tc, or when a toolbox left by capture or trace was
// already running at first contact, which saves the exec that would only
// prove tc is missing. A running pod never gains or loses the binary, so the
// choice is stable; after a recreation the toolbox path re-injects on its own.
var tcUseToolbox sync.Map // "namespace/pod" -> bool

// tcToolboxLocks serializes toolbox creation per pod, so concurrent shaping
// calls reuse one container instead of each injecting its own.
var tcToolboxLocks sync.Map // "namespace/pod" -> *sync.Mutex

func init() {
	// Wire the tc_universal executor to the pod-exec + toolbox machinery here,
	// since the executor package must not import helpers.
	executor.RunTCHook = RunTC
}

// RunTC runs a tc command in a pod's network namespace. Shaping is universal:
// it prefers the pod's own tc (one exec, the common case) and falls back to an
// ephemeral netshoot toolbox (ships tc, has NET_ADMIN) for images without it.
// The qdisc lives in the shared pod netns, so it persists after the exec ends.
func RunTC(namespace, podName string, args []string) (stdout, stderr string, err error) {
	key := namespace + "/" + podName
	choice, known := tcUseToolbox.Load(key)
	if !known && FindRunningDebugContainer(namespace, podName) != "" {
		choice, known = true, true
		tcUseToolbox.Store(key, true)
	}
	if known && choice.(bool) {
		return runTCInToolbox(namespace, podName, args)
	}
	stdout, stderr, err = ExecInPod(namespace, podName, args)
	switch {
	case err == nil:
		tcUseToolbox.Store(key, false)
	case isBinaryNotFound(err, stderr):
		tcUseToolbox.Store(key, true)
		return runTCInToolbox(namespace, podName, args)
	}
	return stdout, stderr, err
}

// runTCInToolbox runs the tc command inside a shared netshoot debug container,
// injecting one and waiting for it to start if the pod has none yet. Creation
// is serialized per pod so parallel shaping calls converge on one container.
func runTCInToolbox(namespace, podName string, args []string) (string, string, error) {
	container, err := ensureTCToolbox(namespace, podName)
	if err != nil {
		return "", "", err
	}
	return ExecInContainer(namespace, podName, container, args)
}

func ensureTCToolbox(namespace, podName string) (string, error) {
	key := namespace + "/" + podName
	lkAny, _ := tcToolboxLocks.LoadOrStore(key, &sync.Mutex{})
	lk := lkAny.(*sync.Mutex)
	lk.Lock()
	defer lk.Unlock()

	container, reused, err := EnsureDebugContainer(namespace, podName)
	if err != nil {
		return "", fmt.Errorf("could not ensure tc toolbox container: %w", err)
	}
	// A freshly injected container is not exec-ready yet; block until it runs so
	// the caller does not race a "container not found" exec.
	if !reused {
		if werr := WaitEphemeralRunning(namespace, podName, container, tcToolboxStartTimeout); werr != nil {
			return "", fmt.Errorf("tc toolbox container did not start: %w", werr)
		}
	}
	return container, nil
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

// buildTCParams validates a shaping request and returns the qdisc kind and
// the tc arguments that follow it. Every problem is an error naming the field
// and the accepted values, nothing is dropped on the way: a netem missing one
// parameter is a different impairment, not a correction of the request.
func buildTCParams(p *types.TCParamEntry) (qdisc string, args []string, err error) {
	if p == nil {
		return "", nil, fmt.Errorf("tcparams is required")
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
		err = fmt.Errorf("tcparams.qdisc %q is not supported (netem, tbf)", p.Qdisc)
	}
	return qdisc, args, err
}

func netemArgs(p *types.TCParamEntry) ([]string, error) {
	for _, f := range []struct{ name, val string }{{"rate", p.Rate}, {"burst", p.Burst}, {"latency", p.Latency}} {
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
	loss, err := tcPercent("loss", p.Loss)
	if err != nil {
		return nil, err
	}
	duplicate, err := tcPercent("duplicate", p.Duplicate)
	if err != nil {
		return nil, err
	}
	corrupt, err := tcPercent("corrupt", p.Corrupt)
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
		}
	}
	for _, v := range []struct {
		name string
		val  tcValue
	}{{"loss", loss}, {"duplicate", duplicate}, {"corrupt", corrupt}} {
		if v.val.n > 0 {
			args = append(args, v.name, v.val.canon)
		}
	}
	if p.Limit != nil {
		args = append(args, "limit", strconv.Itoa(*p.Limit))
	}
	return args, nil
}

func tbfArgs(p *types.TCParamEntry) ([]string, error) {
	for _, f := range []struct{ name, val string }{{"delay", p.Delay}, {"jitter", p.Jitter}, {"loss", p.Loss}, {"duplicate", p.Duplicate}, {"corrupt", p.Corrupt}} {
		if f.val != "" {
			return nil, fmt.Errorf("tcparams.%s is a netem parameter and does not apply to tbf", f.name)
		}
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
