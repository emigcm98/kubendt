package helpers

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"kubendt/types"
)

// tcJSONQdisc is one entry of `tc -j qdisc show`. Options differ per kind and
// are decoded by the kind-specific readers below.
type tcJSONQdisc struct {
	Kind    string          `json:"kind"`
	Handle  string          `json:"handle"`
	Dev     string          `json:"dev,omitempty"`
	Root    bool            `json:"root,omitempty"`
	Parent  string          `json:"parent,omitempty"`
	Options json.RawMessage `json:"options"`
}

// defaultQdiscs are what an interface carries when nobody shaped it.
var defaultQdiscs = map[string]bool{"noqueue": true, "pfifo_fast": true, "mq": true, "noop": true}

// ReadQdisc returns the shaping state of one interface: its root qdisc, and
// for an htb root the classes with their leaves and filters, read in one exec.
func ReadQdisc(namespace, podName, iface string) (*types.QdiscState, error) {
	docs, err := runTCJSONBatch(namespace, podName, []string{"qdisc show dev " + iface, "class show dev " + iface, "filter show dev " + iface})
	if err != nil {
		return nil, err
	}
	if len(docs) != 3 {
		return nil, fmt.Errorf("tc printed %d documents for 3 commands", len(docs))
	}
	states, err := ParseTCQdiscJSON(docs[0])
	if err != nil {
		return nil, err
	}
	state, ok := states[iface]
	if !ok {
		if state, ok = states[""]; !ok {
			state = unshapedQdisc("")
		}
	}
	if err := attachClasses(state, docs[1], docs[2]); err != nil {
		return nil, err
	}
	return state, nil
}

// ReadPodQdiscs returns the shaping state of every interface of a pod. One
// exec lists the roots; a second one, only when some root is classful, reads
// the classes and filters of those interfaces. The loopback and the kernel's
// pseudo-interfaces are left out.
func ReadPodQdiscs(namespace, podName string) (map[string]*types.QdiscState, error) {
	stdout, err := runTCJSON(namespace, podName, []string{"tc", "-j", "qdisc", "show"})
	if err != nil {
		return nil, err
	}
	states, err := ParseTCQdiscJSON(stdout)
	if err != nil {
		return nil, err
	}
	var classful []string
	for dev, st := range states {
		if dev == "" || dev == "lo" || types.IsPseudoInterface(dev) {
			delete(states, dev)
			continue
		}
		if st.Qdisc == "htb" {
			classful = append(classful, dev)
		}
	}
	if len(classful) == 0 {
		return states, nil
	}
	sort.Strings(classful)
	lines := make([]string, 0, 2*len(classful))
	for _, dev := range classful {
		lines = append(lines, "class show dev "+dev, "filter show dev "+dev)
	}
	docs, err := runTCJSONBatch(namespace, podName, lines)
	if err != nil {
		return nil, err
	}
	if len(docs) != len(lines) {
		return nil, fmt.Errorf("tc printed %d documents for %d commands", len(docs), len(lines))
	}
	for i, dev := range classful {
		if err := attachClasses(states[dev], docs[2*i], docs[2*i+1]); err != nil {
			return nil, err
		}
	}
	return states, nil
}

// runTCJSON runs a tc read that needs JSON output. A pod whose own tc predates
// -j (iproute2 4.16, or the busybox applet) is sent to the toolbox from then
// on, the same way a pod without tc is, so every read speaks one format.
func runTCJSON(namespace, podName string, args []string) (string, error) {
	stdout, stderr, err := RunTC(namespace, podName, args)
	if err != nil && tcLacksJSON(stderr) {
		tcUseToolbox.Store(namespace+"/"+podName, true)
		stdout, stderr, err = runTCInToolbox(namespace, podName, args, "")
	}
	if err != nil {
		return "", tcExecError(err, stderr)
	}
	return stdout, nil
}

// runTCJSONBatch runs several show commands as one `tc -j -b` batch and
// returns the JSON document each printed, in order.
func runTCJSONBatch(namespace, podName string, lines []string) ([]string, error) {
	stdin := strings.Join(lines, "\n") + "\n"
	args := []string{"tc", "-j", "-b", "-"}
	stdout, stderr, err := runTCWith(namespace, podName, args, stdin)
	if err != nil && tcLacksJSON(stderr) {
		tcUseToolbox.Store(namespace+"/"+podName, true)
		stdout, stderr, err = runTCInToolbox(namespace, podName, args, stdin)
	}
	if err != nil {
		return nil, tcExecError(err, stderr)
	}
	return splitJSONDocuments(stdout)
}

// splitJSONDocuments separates the concatenated JSON values a tc batch prints.
func splitJSONDocuments(raw string) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	var docs []string
	for dec.More() {
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("unparseable tc JSON output: %w", err)
		}
		docs = append(docs, string(v))
	}
	return docs, nil
}

// tcLacksJSON reports whether tc rejected the -j flag.
func tcLacksJSON(stderr string) bool {
	s := strings.ToLower(stderr)
	mentionsFlag := strings.Contains(s, "-j") || strings.Contains(s, "'j'")
	return mentionsFlag &&
		(strings.Contains(s, "unknown") || strings.Contains(s, "invalid option") || strings.Contains(s, "unrecognized"))
}

func tcExecError(err error, stderr string) error {
	if s := strings.TrimSpace(stderr); s != "" {
		return fmt.Errorf("%w: %s", err, s)
	}
	return err
}

func unshapedQdisc(raw string) *types.QdiscState {
	return &types.QdiscState{Qdisc: "noqueue", Raw: raw}
}

// ParseTCQdiscJSON turns the output of `tc -j qdisc show [dev X]` into one
// state per interface, keyed by device name. tc omits the dev field when it
// was asked about a single device, that state is keyed by "". Child qdiscs
// hang from the qdisc they are attached to.
func ParseTCQdiscJSON(raw string) (map[string]*types.QdiscState, error) {
	var entries []tcJSONQdisc
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &entries); err != nil {
		return nil, fmt.Errorf("unparseable tc JSON output: %w", err)
	}
	byDev := map[string][]tcJSONQdisc{}
	for _, e := range entries {
		byDev[e.Dev] = append(byDev[e.Dev], e)
	}
	out := make(map[string]*types.QdiscState, len(byDev))
	for dev, devEntries := range byDev {
		out[dev] = buildQdiscTree(devEntries)
	}
	return out, nil
}

// buildQdiscTree attaches every non-root qdisc to the one whose handle major
// matches its parent ("10:" under "1:1" hangs from "1:") and returns the root.
func buildQdiscTree(entries []tcJSONQdisc) *types.QdiscState {
	if len(entries) == 0 {
		return unshapedQdisc("")
	}
	states := make([]*types.QdiscState, len(entries))
	byMajor := map[string]*types.QdiscState{}
	var root *types.QdiscState
	for i, e := range entries {
		states[i] = qdiscStateFromJSON(e)
		byMajor[strings.TrimSuffix(e.Handle, ":")] = states[i]
		if e.Root && root == nil {
			root = states[i]
		}
	}
	for i, e := range entries {
		if e.Root {
			continue
		}
		major := e.Parent
		if j := strings.IndexByte(major, ':'); j >= 0 {
			major = major[:j]
		}
		if parent, ok := byMajor[major]; ok && parent != states[i] {
			parent.Children = append(parent.Children, states[i])
		}
	}
	if root == nil {
		root = states[0]
	}
	return root
}

func qdiscStateFromJSON(e tcJSONQdisc) *types.QdiscState {
	s := &types.QdiscState{
		Qdisc:  e.Kind,
		Shaped: !defaultQdiscs[e.Kind],
		Handle: strings.TrimSuffix(e.Handle, ":"),
		Parent: e.Parent,
	}
	if e.Root {
		s.Parent = "root"
	}
	if raw, err := json.Marshal(e); err == nil {
		s.Raw = string(raw)
	}
	switch e.Kind {
	case "netem":
		readNetemOptions(s, e.Options)
	case "tbf":
		readTBFOptions(s, e.Options)
	case "htb":
		readHTBOptions(s, e.Options)
	case "pfifo", "bfifo":
		readLimitOption(s, e.Options)
	}
	return s
}

// readLimitOption keeps the queue length of a plain fifo leaf.
func readLimitOption(s *types.QdiscState, opts json.RawMessage) {
	var o struct {
		Limit *int `json:"limit"`
	}
	if err := json.Unmarshal(opts, &o); err == nil {
		s.Limit = o.Limit
	}
}

// readHTBOptions keeps the default class, printed as a hex minor ("0x12").
func readHTBOptions(s *types.QdiscState, opts json.RawMessage) {
	var o struct {
		Default string `json:"default"`
	}
	if err := json.Unmarshal(opts, &o); err != nil || o.Default == "" {
		return
	}
	if n, err := strconv.ParseInt(strings.TrimPrefix(o.Default, "0x"), 16, 32); err == nil && n > 0 {
		s.DefaultClass = "1:" + strconv.FormatInt(n, 16)
	}
}

// readNetemOptions maps netem's JSON options: times come in seconds,
// probabilities as fractions, the rate in bytes per second.
func readNetemOptions(s *types.QdiscState, opts json.RawMessage) {
	var o struct {
		Limit     *int                                          `json:"limit"`
		Delay     *struct{ Delay, Jitter, Correlation float64 } `json:"delay"`
		Loss      *struct{ Loss, Correlation float64 }          `json:"loss-random"`
		Gemodel   map[string]float64                            `json:"loss-gemodel"`
		Duplicate *struct{ Duplicate, Correlation float64 }     `json:"duplicate"`
		Corrupt   *struct{ Corrupt, Correlation float64 }       `json:"corrupt"`
		Reorder   *struct{ Reorder, Correlation float64 }       `json:"reorder"`
		Rate      *struct{ Rate float64 }                       `json:"rate"`
	}
	if err := json.Unmarshal(opts, &o); err != nil {
		return
	}
	s.Limit = o.Limit
	if o.Delay != nil && o.Delay.Delay > 0 {
		s.Delay = fmtTCMs(o.Delay.Delay * 1000)
		if o.Delay.Jitter > 0 {
			s.Jitter = fmtTCMs(o.Delay.Jitter * 1000)
		}
		if o.Delay.Correlation > 0 {
			s.DelayCorrelation = fmtTCPercent(o.Delay.Correlation)
		}
	}
	if o.Loss != nil && o.Loss.Loss > 0 {
		s.Loss = fmtTCPercent(o.Loss.Loss)
		if o.Loss.Correlation > 0 {
			s.LossCorrelation = fmtTCPercent(o.Loss.Correlation)
		}
	}
	if len(o.Gemodel) > 0 {
		s.LossModel = "gemodel"
		s.Gemodel = make(map[string]string, len(o.Gemodel))
		for k, v := range o.Gemodel {
			s.Gemodel[k] = fmtTCPercent(v)
		}
	}
	if o.Duplicate != nil && o.Duplicate.Duplicate > 0 {
		s.Duplicate = fmtTCPercent(o.Duplicate.Duplicate)
	}
	if o.Corrupt != nil && o.Corrupt.Corrupt > 0 {
		s.Corrupt = fmtTCPercent(o.Corrupt.Corrupt)
	}
	if o.Reorder != nil && o.Reorder.Reorder > 0 {
		s.Reorder = fmtTCPercent(o.Reorder.Reorder)
		if o.Reorder.Correlation > 0 {
			s.ReorderCorrelation = fmtTCPercent(o.Reorder.Correlation)
		}
	}
	if o.Rate != nil && o.Rate.Rate > 0 {
		s.Rate = fmtTCRate(o.Rate.Rate * 8)
	}
}

// readTBFOptions maps tbf's JSON options: rate in bytes per second, burst in
// bytes, latency ("lat") in microseconds.
func readTBFOptions(s *types.QdiscState, opts json.RawMessage) {
	var o struct {
		Rate  float64 `json:"rate"`
		Burst float64 `json:"burst"`
		Lat   float64 `json:"lat"`
	}
	if err := json.Unmarshal(opts, &o); err != nil {
		return
	}
	if o.Rate > 0 {
		s.Rate = fmtTCRate(o.Rate * 8)
	}
	if o.Burst > 0 {
		s.Burst = strconv.FormatFloat(math.Round(o.Burst), 'f', -1, 64) + "b"
	}
	if o.Lat > 0 {
		s.Latency = fmtTCMs(o.Lat / 1000)
	}
}

// Canonical forms: milliseconds, percentages and the largest bit unit that
// keeps the rate a whole number (500kbit, 2mbit, 1500kbit).

func fmtTCMs(ms float64) string {
	return strconv.FormatFloat(math.Round(ms*1000)/1000, 'f', -1, 64) + "ms"
}

func fmtTCPercent(fraction float64) string {
	return strconv.FormatFloat(math.Round(fraction*100*1000)/1000, 'f', -1, 64) + "%"
}

func fmtTCRate(bits float64) string {
	bits = math.Round(bits)
	for _, u := range []struct {
		div  float64
		name string
	}{{1e9, "gbit"}, {1e6, "mbit"}, {1e3, "kbit"}} {
		if bits >= u.div && math.Mod(bits, u.div) == 0 {
			return strconv.FormatFloat(bits/u.div, 'f', -1, 64) + u.name
		}
	}
	return strconv.FormatFloat(bits, 'f', -1, 64) + "bit"
}

// Classes and filters of an htb root.

type tcJSONClass struct {
	Class  string  `json:"class"`
	Handle string  `json:"handle"`
	Parent string  `json:"parent"`
	Root   bool    `json:"root"`
	Prio   *int    `json:"prio"`
	Rate   float64 `json:"rate"`
	Ceil   float64 `json:"ceil"`
}

type tcJSONFilter struct {
	Parent  string `json:"parent"`
	Kind    string `json:"kind"`
	Pref    int    `json:"pref"`
	Options *struct {
		ClassID string `json:"classid"`
		Keys    struct {
			IPProto   string          `json:"ip_proto"`
			SrcIP     string          `json:"src_ip"`
			DstIP     string          `json:"dst_ip"`
			SrcPort   *int            `json:"src_port"`
			DstPort   *int            `json:"dst_port"`
			IPTos     *int            `json:"ip_tos"`
			IPTosMask json.RawMessage `json:"ip_tos_mask"`
			VlanID    *int            `json:"vlan_id"`
		} `json:"keys"`
	} `json:"options"`
}

// attachClasses turns the class and filter listings of an htb root into
// QdiscClass entries. The root class (1:1, the link rate) becomes the
// root's Rate; leaf qdiscs hang from the class whose id matches their parent;
// flower filters become matches on the class they send to.
func attachClasses(root *types.QdiscState, classesJSON, filtersJSON string) error {
	if root == nil || root.Qdisc != "htb" {
		return nil
	}
	var classes []tcJSONClass
	if err := json.Unmarshal([]byte(strings.TrimSpace(classesJSON)), &classes); err != nil {
		return fmt.Errorf("unparseable tc class output: %w", err)
	}
	var filters []tcJSONFilter
	if err := json.Unmarshal([]byte(strings.TrimSpace(filtersJSON)), &filters); err != nil {
		return fmt.Errorf("unparseable tc filter output: %w", err)
	}

	byID := map[string]*types.QdiscClass{}
	for _, c := range classes {
		if c.Class != "htb" {
			continue
		}
		if c.Root {
			if c.Rate > 0 {
				root.Rate = fmtTCRate(c.Rate * 8)
			}
			continue
		}
		qc := &types.QdiscClass{ClassID: c.Handle, Parent: c.Parent, Prio: c.Prio, Default: c.Handle == root.DefaultClass}
		if c.Rate > 0 {
			qc.Rate = fmtTCRate(c.Rate * 8)
		}
		if c.Ceil > 0 {
			qc.Ceil = fmtTCRate(c.Ceil * 8)
		}
		root.Classes = append(root.Classes, qc)
		byID[c.Handle] = qc
	}
	sort.Slice(root.Classes, func(i, j int) bool { return root.Classes[i].ClassID < root.Classes[j].ClassID })

	// Leaves were attached to the root by handle major; move them under their class.
	var rest []*types.QdiscState
	for _, child := range root.Children {
		if qc, ok := byID[child.Parent]; ok {
			qc.Leaf = child
			continue
		}
		rest = append(rest, child)
	}
	root.Children = rest

	sort.SliceStable(filters, func(i, j int) bool { return filters[i].Pref < filters[j].Pref })
	for _, f := range filters {
		if f.Kind != "flower" || f.Options == nil {
			continue
		}
		qc, ok := byID[f.Options.ClassID]
		if !ok {
			continue
		}
		k := f.Options.Keys
		m := types.TCMatch{Proto: k.IPProto, Src: k.SrcIP, Dst: k.DstIP, SrcPort: k.SrcPort, DstPort: k.DstPort, VLAN: k.VlanID}
		if k.IPTos != nil {
			dscp := *k.IPTos >> 2
			m.DSCP = &dscp
		}
		qc.Match = append(qc.Match, m)
	}
	return nil
}
