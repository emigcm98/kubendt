package drivers

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"kubendt/capabilities"
	drivers_meta "kubendt/drivers/meta"
	"kubendt/executor"
)

// FaucetControllerDriver is Faucet on its official image: an OpenFlow 1.3
// controller run by a YAML, a host node the switches point at. Its state
// comes from the Prometheus metrics it serves on 9302.
type FaucetControllerDriver struct {
	drivers_meta.Meta
	capabilities.L2Base
	capabilities.L3Base
	capabilities.SubinterfaceBase
}

func NewFaucetControllerDriver() *FaucetControllerDriver {
	return &FaucetControllerDriver{Meta: drivers_meta.NewMeta("FaucetControllerDriver", "host")}
}

var _ capabilities.L2Capable = (*FaucetControllerDriver)(nil)
var _ capabilities.L3Capable = (*FaucetControllerDriver)(nil)
var _ capabilities.SubinterfaceCapable = (*FaucetControllerDriver)(nil)
var _ capabilities.SDNControllerCapable = (*FaucetControllerDriver)(nil)
var _ capabilities.SDNControllerInspector = (*FaucetControllerDriver)(nil)
var _ drivers_meta.KnownImagesProvider = (*FaucetControllerDriver)(nil)
var _ drivers_meta.ReadinessProbeProvider = (*FaucetControllerDriver)(nil)

const (
	faucetConfigPath = "/etc/faucet/faucet.yaml"
	faucetMetricsURL = "http://127.0.0.1:9302/"
)

func (FaucetControllerDriver) KnownImages() []string { return []string{"faucet/faucet:1.10.12"} }

// ReadinessProbeCommands makes the pod Ready once the OpenFlow port listens.
// The image has python and no curl or ss.
func (FaucetControllerDriver) ReadinessProbeCommands() drivers_meta.ReadinessProbeSpec {
	return drivers_meta.ReadinessProbeSpec{
		Command:             []string{"python3", "-c", "import socket,sys; sys.exit(socket.socket().connect_ex(('127.0.0.1', 6653)))"},
		InitialDelaySeconds: 3,
		PeriodSeconds:       3,
		TimeoutSeconds:      5,
		FailureThreshold:    40,
	}
}

// ApplyConfig writes the YAML and sends SIGHUP to Faucet, PID 1 in its
// image, which reloads it; a bad file is logged there and the old one stays.
func (FaucetControllerDriver) ApplyConfig(content string) [][]string {
	return [][]string{{"sh", "-c", "printf '%s\\n' " + shellSingleQuote(content) + " > " + faucetConfigPath + " && kill -HUP 1"}}
}

func shellSingleQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (FaucetControllerDriver) Datapaths(namespace, pod string) (capabilities.Rows, error) {
	samples, err := faucetMetrics(namespace, pod)
	if err != nil {
		return nil, err
	}
	return faucetDatapathRows(samples), nil
}

func (FaucetControllerDriver) DatapathPorts(namespace, pod string) (capabilities.Rows, error) {
	samples, err := faucetMetrics(namespace, pod)
	if err != nil {
		return nil, err
	}
	return faucetPortRows(samples), nil
}

func (FaucetControllerDriver) LearnedHosts(namespace, pod string) (capabilities.Rows, error) {
	samples, err := faucetMetrics(namespace, pod)
	if err != nil {
		return nil, err
	}
	return faucetHostRows(samples), nil
}

func faucetMetrics(namespace, pod string) ([]promSample, error) {
	out, err := executor.ExecAndGet(namespace, pod, []string{"python3", "-c",
		"import urllib.request,sys; sys.stdout.write(urllib.request.urlopen('" + faucetMetricsURL + "', timeout=5).read().decode())"})
	if err != nil {
		return nil, err
	}
	return parsePromText(string(out)), nil
}

// A sample of the Prometheus text format: name{label="value",...} value.
type promSample struct {
	name   string
	labels map[string]string
	value  float64
}

func parsePromText(text string) []promSample {
	var out []promSample
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		s, ok := parsePromLine(line)
		if ok {
			out = append(out, s)
		}
	}
	return out
}

func parsePromLine(line string) (promSample, bool) {
	s := promSample{labels: map[string]string{}}
	rest := line
	if i := strings.IndexByte(rest, '{'); i >= 0 {
		s.name = rest[:i]
		j := strings.LastIndexByte(rest, '}')
		if j < i {
			return s, false
		}
		s.labels = parsePromLabels(rest[i+1 : j])
		rest = rest[j+1:]
	} else {
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			return s, false
		}
		s.name = rest[:i]
		rest = rest[i:]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return s, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return s, false
	}
	s.value = v
	return s, true
}

// parsePromLabels reads k="v" pairs; a value may hold a comma, a quote or
// a backslash escaped the Prometheus way.
func parsePromLabels(text string) map[string]string {
	labels := map[string]string{}
	i := 0
	for i < len(text) {
		eq := strings.IndexByte(text[i:], '=')
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(text[i : i+eq])
		i += eq + 1
		if i >= len(text) || text[i] != '"' {
			break
		}
		i++
		var val strings.Builder
		for i < len(text) && text[i] != '"' {
			if text[i] == '\\' && i+1 < len(text) {
				i++
				switch text[i] {
				case 'n':
					val.WriteByte('\n')
				default:
					val.WriteByte(text[i])
				}
			} else {
				val.WriteByte(text[i])
			}
			i++
		}
		labels[key] = val.String()
		i++ // the closing quote
		for i < len(text) && (text[i] == ',' || text[i] == ' ') {
			i++
		}
	}
	return labels
}

func promCount(samples []promSample, name string, dpID string) int {
	for _, s := range samples {
		if s.name == name && s.labels["dp_id"] == dpID {
			return int(s.value)
		}
	}
	return 0
}

func upDown(v float64) string {
	if v == 1 {
		return "up"
	}
	return "down"
}

func faucetDatapathRows(samples []promSample) capabilities.Rows {
	rows := capabilities.Rows{}
	for _, s := range samples {
		if s.name != "dp_status" {
			continue
		}
		rows = append(rows, map[string]string{
			"datapath":    s.labels["dp_name"],
			"dp_id":       s.labels["dp_id"],
			"status":      upDown(s.value),
			"connections": strconv.Itoa(promCount(samples, "of_dp_connections_total", s.labels["dp_id"])),
		})
	}
	sortRows(rows, "datapath", "dp_id")
	return rows
}

func faucetPortRows(samples []promSample) capabilities.Rows {
	rows := capabilities.Rows{}
	for _, s := range samples {
		if s.name != "port_status" {
			continue
		}
		rows = append(rows, map[string]string{
			"datapath": s.labels["dp_name"],
			"port":     s.labels["port"],
			"status":   upDown(s.value),
		})
	}
	sortRows(rows, "datapath", "port")
	return rows
}

// learned_macs carries the MAC as the sample's value, an integer that a
// float64 holds exactly; an empty slot is zero.
func faucetHostRows(samples []promSample) capabilities.Rows {
	rows := capabilities.Rows{}
	for _, s := range samples {
		if s.name != "learned_macs" || s.value == 0 {
			continue
		}
		rows = append(rows, map[string]string{
			"datapath": s.labels["dp_name"],
			"port":     s.labels["port"],
			"vlan":     s.labels["vlan"],
			"mac":      macFromInt(uint64(s.value)),
		})
	}
	sortRows(rows, "datapath", "port", "mac")
	return rows
}

func macFromInt(n uint64) string {
	b := make([]string, 6)
	for i := 5; i >= 0; i-- {
		b[i] = fmt.Sprintf("%02x", n&0xff)
		n >>= 8
	}
	return strings.Join(b, ":")
}

// sortRows orders rows by the given columns, numbers as numbers.
func sortRows(rows capabilities.Rows, columns ...string) {
	sort.SliceStable(rows, func(i, j int) bool {
		for _, c := range columns {
			a, b := rows[i][c], rows[j][c]
			if a == b {
				continue
			}
			na, errA := strconv.Atoi(a)
			nb, errB := strconv.Atoi(b)
			if errA == nil && errB == nil {
				return na < nb
			}
			return a < b
		}
		return false
	})
}
