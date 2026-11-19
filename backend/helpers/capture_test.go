package helpers

import (
	"strings"
	"testing"
)

func TestBuildCaptureCommandCapsTheFile(t *testing.T) {
	cmd := strings.Join(BuildCaptureCommand("eth1", "", "/tmp/x.pcap", 100), " ")
	if !strings.Contains(cmd, "-a filesize:100000") {
		t.Fatalf("the command should stop tshark at the cap, got %s", cmd)
	}
	if strings.HasSuffix(cmd, "-f ") || strings.Contains(cmd, " -f") {
		t.Fatalf("no filter should add no -f, got %s", cmd)
	}
	with := BuildCaptureCommand("eth1", "tcp port 80", "/tmp/x.pcap", 5)
	if with[len(with)-2] != "-f" || with[len(with)-1] != "tcp port 80" {
		t.Fatalf("the filter should be the last argv element, got %v", with)
	}
}

func TestParseCaptureMaxMB(t *testing.T) {
	cases := []struct {
		raw  string
		want int
		bad  bool
	}{
		{"", DefaultCaptureMaxMB, false},
		{" 250 ", 250, false},
		{"0", 0, true},
		{"-5", 0, true},
		{"lots", 0, true},
		{"100001", 0, true},
	}
	for _, c := range cases {
		got, err := parseCaptureMaxMB(c.raw)
		if c.bad {
			if err == nil {
				t.Errorf("%q: want an error, got %d", c.raw, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: got %d, %v, want %d", c.raw, got, err, c.want)
		}
	}
}
