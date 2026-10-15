package helpers

import (
	drivers_meta "kubendt/drivers/meta"

	v1 "k8s.io/api/core/v1"
)

// The shells a node offers: the pod shell for every container, the serial
// console for a guest VM or a node that asked for it, and what the driver
// declares. The pod listing publishes them, the menu shows them and the
// shell endpoint looks a mode up here.

var (
	podShell    = drivers_meta.ShellSpec{Mode: "sh", Label: "pod shell (sh)"}
	serialShell = drivers_meta.ShellSpec{Mode: "serial", Label: "serial console (attach)"}
)

func hasSerialConsole(pod *v1.Pod) bool {
	return IsGuestVM(pod.Labels) || pod.Labels["kubendt/shell-mode"] == "serial"
}

// PodShells lists the shells of a pod, the pod shell first.
func PodShells(pod *v1.Pod) []drivers_meta.ShellSpec {
	drv, _ := GetDriverForListedPod(pod)
	return shellsFor(drv, hasSerialConsole(pod))
}

func shellsFor(driver any, serial bool) []drivers_meta.ShellSpec {
	out := []drivers_meta.ShellSpec{podShell}
	if serial {
		out = append(out, serialShell)
	}
	if sp, ok := driver.(drivers_meta.ShellProvider); ok {
		out = append(out, sp.Shells()...)
	}
	return out
}

// FindShell returns the shell of the mode, or false when the node lacks it.
func FindShell(shells []drivers_meta.ShellSpec, mode string) (drivers_meta.ShellSpec, bool) {
	for _, s := range shells {
		if s.Mode == mode {
			return s, true
		}
	}
	return drivers_meta.ShellSpec{}, false
}

// DefaultShell is what mode=auto opens: the serial console when there is
// one, the pod shell otherwise.
func DefaultShell(shells []drivers_meta.ShellSpec) drivers_meta.ShellSpec {
	if s, ok := FindShell(shells, "serial"); ok {
		return s
	}
	return podShell
}
