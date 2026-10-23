package drivers_meta

// CapabilityPrecondition is what a capability needs on the pod before its
// actions can run, a daemon up for instance. The command runs in the pod and
// exits 0 when the need is met; otherwise the actions of the capability fail
// with the message instead of running. Checked on the kubectl executor only.
type CapabilityPrecondition interface {
	CapabilityPrecondition(capability string) (command []string, failure string, ok bool)
}
