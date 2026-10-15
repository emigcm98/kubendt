package helpers

// The runtime a node was deployed with, qemu for a guest VM and k8s-linux
// for a container, lives in the kubendt/runtime label of its pods and
// StatefulSet. Everything that needs it reads it here. Pods from before
// that label carried kubendt/qemu, still read, no longer written.

const (
	RuntimeLabelQEMU      = "qemu"
	RuntimeLabelContainer = "k8s-linux"
)

// RuntimeLabelValue is the label a node's pods get from its runtime.
func RuntimeLabelValue(qemu bool) string {
	if qemu {
		return RuntimeLabelQEMU
	}
	return RuntimeLabelContainer
}

// RuntimeOf reads the runtime of a pod or a StatefulSet template from its
// labels.
func RuntimeOf(labels map[string]string) string {
	if r := labels["kubendt/runtime"]; r != "" {
		return r
	}
	if labels["kubendt/qemu"] == "true" {
		return RuntimeLabelQEMU
	}
	return RuntimeLabelContainer
}

// IsGuestVM says whether the labels belong to a guest VM node.
func IsGuestVM(labels map[string]string) bool {
	return RuntimeOf(labels) == RuntimeLabelQEMU
}
