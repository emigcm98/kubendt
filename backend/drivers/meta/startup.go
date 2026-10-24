package drivers_meta

// StartupCommandsProvider is the command a node runs when its spec names
// none, for a driver tied to an image that needs preparing before the
// actions work: daemons enabled, files the tools expect, then the image's
// own start. A node with its own `commands` keeps them.
type StartupCommandsProvider interface {
	StartupCommands() []string
}
