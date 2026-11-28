package handlers

import (
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A stored approver is written by signedActor (actor.go userLabel) as
// "email (user id)", optionally followed by " via token <token id>", or as
// "token <token id>". The release log is hash chained, so stored records are
// never rewritten; every response shows a display form instead (K3).
var (
	approverWithID = regexp.MustCompile(`^(.*?)\s*\(([^()]*)\)(\s+via token \S+)?$`)
	approverToken  = regexp.MustCompile(`^token \S+$`)
	approverBareID = regexp.MustCompile(`^(usr|user|u)[_-][A-Za-z0-9_-]+$`)
)

// approverDisplay turns a stored approver into a name for display: never an
// email address, a user id or a token id.
func approverDisplay(stored string) string {
	s := strings.TrimSpace(stored)
	if s == "" {
		return ""
	}
	if approverToken.MatchString(s) {
		return "an API token"
	}
	viaToken := false
	if m := approverWithID.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
		viaToken = m[3] != ""
	}
	if approverBareID.MatchString(s) {
		s = ""
	}
	if at := strings.IndexByte(s, '@'); at >= 0 {
		s = strings.TrimSpace(s[:at])
	}
	if s == "" {
		s = "a member"
	}
	if viaToken {
		s += " (API token)"
	}
	return s
}

// redactRelease replaces the approver of a record copy with its display form.
func redactRelease(rec *schemas.ReleaseRecord) {
	if rec != nil {
		rec.Approver = approverDisplay(rec.Approver)
	}
}
