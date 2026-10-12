package executor

import (
	"errors"
	"testing"
)

func TestMissingProgram(t *testing.T) {
	cases := []struct {
		name    string
		command []string
		err     string
		output  string
		want    string
	}{
		{"runtime cannot find argv0", []string{"iptables", "-t", "nat"}, `OCI runtime exec failed: exec failed: unable to start container process: exec: "iptables": executable file not found in $PATH: unknown`, "", "iptables"},
		{"busybox sh", []string{"sh", "-c", "iptables -t nat -C ..."}, "command terminated with exit code 127", "sh: iptables: not found", "iptables"},
		{"dash", []string{"sh", "-c", "iptables"}, "command terminated with exit code 127", "sh: 1: iptables: not found", "iptables"},
		{"bash", []string{"bash", "-c", "iptables"}, "command terminated with exit code 127", "bash: line 1: iptables: command not found", "iptables"},
		{"other failure", []string{"ip", "addr", "add"}, "command terminated with exit code 2", `Error: any valid prefix is expected rather than "".`, ""},
		{"127 without the shell line", []string{"sh", "-c", "exit 127"}, "command terminated with exit code 127", "", ""},
	}
	for _, c := range cases {
		if got := missingProgram(c.command, errors.New(c.err), c.output); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
