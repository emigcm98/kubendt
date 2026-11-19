package drivers_meta

// PodBoundPlanner is implemented by drivers whose commands resolve against
// the running pod, an interface map read at boot for instance. They have no
// offline plan: the plan test leaves them out and the drivers listing shows
// no command examples for them.
type PodBoundPlanner interface {
	PlansFromPod() bool
}
