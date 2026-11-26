// Package imagepub publishes platform images to the shared worker's private
// registry (wave 4, F1.1). The publisher service holds the registry's write
// identity and has no Docker socket; the shared runner, which has the
// socket, qualifies an image on its host and streams it to the publisher.
// Only refs computed from the read-only checkout and library are accepted,
// so an organization's own image is never copied.
package imagepub

import (
	"regexp"
	"strings"
)

// refRe is a plain bench/ tag: no registry host, no digest, no org- namespace.
var refRe = regexp.MustCompile(`^bench/[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)*:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// ValidRef reports whether ref is a plain platform image tag the publisher
// may handle.
func ValidRef(ref string) bool {
	if !refRe.MatchString(ref) {
		return false
	}
	// A path component of dots only ("..") is never a platform name.
	repo := ref[:strings.LastIndex(ref, ":")]
	for _, part := range strings.Split(repo, "/") {
		if strings.Trim(part, ".") == "" {
			return false
		}
	}
	return true
}
