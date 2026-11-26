package handlers

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestVerdictBadgeRunningWhilePartial: a comparison that has not finished
// gets a neutral "running" badge, never its provisional action.
func TestVerdictBadgeRunningWhilePartial(t *testing.T) {
	v := schemas.VerdictPayload{Partial: true}
	v.Verdict.Action = "FIX HARNESS"
	svg := verdictBadgeSVG(v)
	if !strings.Contains(svg, "verdict: running") || strings.Contains(svg, "fix harness") {
		t.Fatalf("partial badge: %s", svg)
	}
	v.Partial = false
	if svg := verdictBadgeSVG(v); !strings.Contains(svg, "fix harness") {
		t.Fatalf("final badge: %s", svg)
	}
}
