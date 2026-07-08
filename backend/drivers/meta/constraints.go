package drivers_meta

import "regexp"

// InterfaceNameConstraints describes the rules a driver imposes on pod-side
// interface names attached to its nodes. A zero value means the driver adds
// no constraints beyond Linux kernel rules.
type InterfaceNameConstraints struct {
	// Pattern an interface name must match. If nil, no pattern constraint applies.
	Pattern *regexp.Regexp
	// PatternHuman is a human-readable representation of Pattern used in errors.
	PatternHuman string
	// Reserved lists names that cannot be used even if they match Pattern.
	Reserved []string
}

// InterfaceNameConstrainer is implemented by drivers that impose extra rules
// on interface names beyond the Linux kernel-wide validator.
type InterfaceNameConstrainer interface {
	InterfaceNameConstraints() InterfaceNameConstraints
}
