package drivers_meta

// Runtime constants identify the execution model a driver expects for its
// pods. Drivers that don't implement RuntimeProvider are assumed to run as
// regular Linux processes inside the container.
const (
	RuntimeNative = "native"
	RuntimeQEMU   = "qemu"
)

// RuntimeProvider is implemented by drivers whose pods run on something other
// than a plain Linux container. VyOS for example boots a full guest VM via
// QEMU/KVM. The runtime declared here is the source of truth used by the
// scheduler and pod-spec builder, so users no longer need to flag pods as
// qemu in their topology JSON.
type RuntimeProvider interface {
	Runtime() string
}
