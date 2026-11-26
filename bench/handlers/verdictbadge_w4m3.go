package handlers

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharelinks"
)

// verdictBadgeSVG is the badge of a shared comparison. While the comparison
// is still running its verdict is provisional, so the badge says "running"
// on the neutral grey instead of an action nobody should act on yet.
func verdictBadgeSVG(v schemas.VerdictPayload) string {
	if v.Partial {
		return sharelinks.Badge("verdict", "running", "")
	}
	return sharelinks.VerdictBadge(string(v.Verdict.Action), string(v.Verdict.Decision))
}
