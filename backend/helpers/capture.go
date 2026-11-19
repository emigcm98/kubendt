package helpers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	CaptureMaxMBEnv     = "KUBENDT_CAPTURE_MAX_MB"
	DefaultCaptureMaxMB = 100
)

// CaptureMaxMB is the size a capture's pcap may reach before tshark stops it.
// The file lives in the toolbox's writable layer, which counts as the pod's
// ephemeral storage on the worker, so without a cap a capture left running
// fills the disk and the kubelet evicts the lab. An unusable value is an
// error, as with the MTU: it is a cluster setting and a silent default would
// hide the typo.
func CaptureMaxMB() (int, error) {
	return parseCaptureMaxMB(os.Getenv(CaptureMaxMBEnv))
}

func parseCaptureMaxMB(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultCaptureMaxMB, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 100000 {
		return 0, fmt.Errorf("%s=%q is not a size in MB between 1 and 100000", CaptureMaxMBEnv, raw)
	}
	return n, nil
}

// CapturePcapPathFor returns the in-container path where a capture run writes
// its pcap. Each run has its own file so a freshly started tshark never shares
// one with a previous run that has not fully exited, which corrupts the pcap.
// Classic pcap (not pcapng) so the download opens anywhere.
func CapturePcapPathFor(runID string) string {
	return "/tmp/kubendt-capture-" + runID + ".pcap"
}

var (
	ifaceNameRe        = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
	pcapIDRe           = regexp.MustCompile(`^[a-z0-9]{8,20}$`)
	frameNumberRe      = regexp.MustCompile(`^[0-9]{1,12}$`)
	frameRangeRe       = regexp.MustCompile(`^[0-9]{1,12}(-[0-9]{1,12})?$`)
	maxCaptureFilexLen = 512
)

// ValidIfaceName reports whether s is a safe interface name to hand to tshark.
func ValidIfaceName(s string) bool { return ifaceNameRe.MatchString(s) }

// ValidFrameNumber reports whether s is a plausible frame number.
func ValidFrameNumber(s string) bool { return frameNumberRe.MatchString(s) }

// ValidCaptureFilter reports whether a BPF capture filter is within bounds.
// The filter is always passed as a single argv element (never through a
// shell), so length is the only real concern.
func ValidCaptureFilter(s string) bool { return len(s) <= maxCaptureFilexLen }

// ParseFrameRanges validates a comma-separated list of frame numbers/ranges
// (e.g. "1-5,9,12-20") and returns the tokens as separate argv elements for
// editcap. Returns false if any token is malformed or the list is too large.
func ParseFrameRanges(s string) ([]string, bool) {
	if s == "" {
		return nil, false
	}
	tokens := strings.Split(s, ",")
	if len(tokens) > 4096 {
		return nil, false
	}
	for _, t := range tokens {
		if !frameRangeRe.MatchString(t) {
			return nil, false
		}
	}
	return tokens, true
}

// BuildCaptureCommand returns the tshark argv that both writes the pcapng to
// disk (for later download / re-dissection) and streams the standard Wireshark
// columns (No/Time/Source/Destination/Protocol/Length/Info) as tab-separated
// lines on stdout. An optional BPF capture filter is appended as a single argv
// element, so it never touches a shell. tshark stops by itself once the file
// reaches maxMB.
func BuildCaptureCommand(iface, bpf, pcapPath string, maxMB int) []string {
	cmd := []string{
		"tshark",
		"-i", iface,
		"-w", pcapPath, // write the pcap alongside the live stream
		"-a", fmt.Sprintf("filesize:%d", maxMB*1000), // tshark counts kB
		"-F", "pcap", // classic pcap (not pcapng) so it opens anywhere
		"-P", // force per-packet printing even though -w is set
		"-l", // line-buffer stdout so rows arrive live
		"-n", // no name resolution (faster, no DNS noise)
		"-T", "fields",
		"-E", "separator=/t",
		"-e", "frame.number",
		"-e", "frame.time_relative",
		"-e", "_ws.col.Source",
		"-e", "_ws.col.Destination",
		"-e", "_ws.col.Protocol",
		"-e", "frame.len",
		"-e", "_ws.col.Info",
	}
	if bpf != "" {
		cmd = append(cmd, "-f", bpf)
	}
	return cmd
}

// PacketDetailCommand returns the tshark argv that re-dissects a single packet
// from the saved pcap into a full JSON protocol tree.
func PacketDetailCommand(pcapPath, frameNumber string) []string {
	return []string{
		"tshark",
		"-r", pcapPath,
		"-Y", "frame.number==" + frameNumber,
		"-T", "json",
	}
}

// FilteredPcapCommand returns the editcap argv that extracts the given frame
// numbers/ranges from the saved pcap and writes a pcap to stdout ("-").
func FilteredPcapCommand(pcapPath string, frameTokens []string) []string {
	cmd := []string{"editcap", "-F", "pcap", "-r", pcapPath, "-"}
	return append(cmd, frameTokens...)
}

// CatCommand streams a file to stdout (used to download a whole pcap).
func CatCommand(pcapPath string) []string {
	return []string{"cat", pcapPath}
}

// TruncateCaptureCommand empties the saved pcap so a later export reflects only
// what was captured after a Clear. Only safe while no tshark holds the file
// open (callers stop the capture first).
func TruncateCaptureCommand(pcapPath string) []string {
	return []string{"sh", "-c", ": > " + pcapPath}
}

// NewCaptureRunID returns a short random id identifying one capture run, used
// to name its pcap file so concurrent/overlapping runs never share a file.
func NewCaptureRunID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano()&0xffffffffffff)
	}
	return hex.EncodeToString(b)
}

// ValidPcapId reports whether s is a capture run id we minted.
func ValidPcapId(s string) bool { return pcapIDRe.MatchString(s) }
