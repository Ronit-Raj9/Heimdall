package onboard

import (
	"regexp"
	"strings"
)

// stagingSetRE is the set id format newSetID makes: date, time and six hex digits.
var stagingSetRE = regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{6}$`)

// IsStagingSuite reports whether id is a draft a hosted readiness run staged
// as a temporary benchmark: "draft-" followed by a draft set id (the id the
// onboard readiness job gives the staged copy). Such a benchmark is internal
// and never listed; a team benchmark merely named "draft-..." is not one.
func IsStagingSuite(id string) bool {
	rest, ok := strings.CutPrefix(id, "draft-")
	return ok && stagingSetRE.MatchString(rest)
}
