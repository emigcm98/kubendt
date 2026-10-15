package helpers

import (
	"fmt"

	drivers_meta "kubendt/drivers/meta"

	v1 "k8s.io/api/core/v1"
)

// Where a tool runs on a node. A container node runs every tool in its
// toolbox, which shares the pod netns. A guest VM node has no IP stack in
// that netns: a tool runs inside the guest when the driver declares it,
// wrapped by the driver's prefix, and is otherwise not offered. The pod
// listing and GET /drivers publish the result, so the UI only asks.

// ToolRun is where to exec a tool: the container, and the prefix that puts
// the argv inside a guest (nil for the toolbox).
type ToolRun struct {
	Container string
	Wrapper   []string
	Reused    bool // a toolbox that was already running
}

// Wrap prefixes an argv for the run.
func (r ToolRun) Wrap(argv []string) []string {
	if len(r.Wrapper) == 0 {
		return argv
	}
	return append(append([]string{}, r.Wrapper...), argv...)
}

func isGuestVM(driver any) bool {
	rp, ok := driver.(drivers_meta.RuntimeProvider)
	return ok && rp.Runtime() == drivers_meta.RuntimeQEMU
}

// DriverTools lists the tools a driver's nodes offer.
func DriverTools(driver any) []string {
	if !isGuestVM(driver) {
		return drivers_meta.AllTools()
	}
	if gp, ok := driver.(drivers_meta.GuestToolProvider); ok {
		return append([]string{}, gp.GuestTools()...)
	}
	return []string{}
}

// PodTools lists the tools a pod offers, none when its driver is unknown.
func PodTools(pod *v1.Pod) []string {
	drv, err := GetDriverForListedPod(pod)
	if err != nil {
		return []string{}
	}
	return DriverTools(drv)
}

// guestToolRun decides the guest side: the wrapper when the driver ships the
// tool, an error naming the node and driver when it does not.
func guestToolRun(pod *v1.Pod, driver any, tool string) ([]string, error) {
	name := pod.Labels["kubendt/driver"]
	if gp, ok := driver.(drivers_meta.GuestToolProvider); ok {
		for _, t := range gp.GuestTools() {
			if t == tool {
				return gp.GuestWrapper(), nil
			}
		}
	}
	return nil, fmt.Errorf("%s has no %s: %s runs a guest VM that does not ship it", pod.Name, tool, name)
}

// ToolRunner readies where tool runs on the pod: the toolbox for a container
// node, injected and awaited when missing (onInject reports a fresh one), or
// the pod's main container with the driver's wrapper for a guest VM.
func ToolRunner(pod *v1.Pod, tool string, onInject func()) (ToolRun, error) {
	drv, err := GetDriverForListedPod(pod)
	if err != nil {
		return ToolRun{}, err
	}
	if isGuestVM(drv) {
		wrapper, err := guestToolRun(pod, drv, tool)
		if err != nil {
			return ToolRun{}, err
		}
		return ToolRun{Container: pod.Spec.Containers[0].Name, Wrapper: wrapper, Reused: true}, nil
	}
	container, reused, err := EnsureToolbox(pod.Namespace, pod.Name, onInject)
	if err != nil {
		return ToolRun{}, err
	}
	return ToolRun{Container: container, Reused: reused}, nil
}
