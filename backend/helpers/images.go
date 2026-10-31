package helpers

import "strings"

// imageRef splits an image reference into its name and its version, a tag
// or a digest, both normalized so `alpine:3.24.2` and
// `docker.io/library/alpine:3.24.2` meet and a missing tag reads as latest.
func imageRef(ref string) (name, version string) {
	ref = strings.TrimSpace(ref)
	if i := strings.Index(ref, "@"); i >= 0 {
		name, version = ref[:i], "@"+ref[i+1:]
	} else {
		name, version = ref, ":latest"
		slash := strings.LastIndex(ref, "/")
		if colon := strings.LastIndex(ref, ":"); colon > slash {
			name, version = ref[:colon], ":"+ref[colon+1:]
		}
	}
	first, rest, hasSlash := strings.Cut(name, "/")
	switch {
	case !hasSlash:
		name = "docker.io/library/" + name
	case first == "docker.io" && !strings.Contains(rest, "/"):
		name = "docker.io/library/" + rest
	case !strings.ContainsAny(first, ".:") && first != "localhost":
		name = "docker.io/" + name
	}
	return name, version
}

// ImageKnown reports whether image is one of the known references: the same
// name and the same tag, or the same digest. A tag never matches a digest,
// since what a tag points at is not known here.
func ImageKnown(known []string, image string) bool {
	name, version := imageRef(image)
	for _, k := range known {
		if kn, kv := imageRef(k); kn == name && kv == version {
			return true
		}
	}
	return false
}
