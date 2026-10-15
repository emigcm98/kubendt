package helpers

import (
	"strings"
	"testing"

	drivers_meta "kubendt/drivers/meta"
)

type consoleDriver struct{}

func (consoleDriver) Shells() []drivers_meta.ShellSpec {
	return []drivers_meta.ShellSpec{{Mode: "cli", Label: "vendor CLI", Command: []string{"cli"}}}
}

func modes(shells []drivers_meta.ShellSpec) string {
	out := []string{}
	for _, s := range shells {
		out = append(out, s.Mode)
	}
	return strings.Join(out, ",")
}

func TestShellsFor(t *testing.T) {
	if got := modes(shellsFor(struct{}{}, false)); got != "sh" {
		t.Errorf("a plain container has the pod shell only, got %s", got)
	}
	if got := modes(shellsFor(struct{}{}, true)); got != "sh,serial" {
		t.Errorf("a serial node adds the console, got %s", got)
	}
	if got := modes(shellsFor(consoleDriver{}, false)); got != "sh,cli" {
		t.Errorf("a driver adds what it declares, got %s", got)
	}
	shells := shellsFor(consoleDriver{}, true)
	if DefaultShell(shells).Mode != "serial" {
		t.Errorf("auto opens the serial console when there is one")
	}
	if DefaultShell(shellsFor(consoleDriver{}, false)).Mode != "sh" {
		t.Errorf("auto opens the pod shell otherwise")
	}
	if s, ok := FindShell(shells, "cli"); !ok || strings.Join(s.Command, " ") != "cli" {
		t.Errorf("a declared mode is found with its command, got %v %v", s, ok)
	}
	if _, ok := FindShell(shells, "vtysh"); ok {
		t.Errorf("an undeclared mode is not found")
	}
}
