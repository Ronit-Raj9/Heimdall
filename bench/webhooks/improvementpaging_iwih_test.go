package webhooks

import "testing"

// D5 C2: a PagerDuty hook takes improvement.paused (someone must approve)
// and never the routine improvement.finished.
func TestPagerDutyTakesImprovementPausedOnly(t *testing.T) {
	if !Accepts(FormatPagerDuty, EventImprovementPaused) {
		t.Error("PagerDuty refuses improvement.paused")
	}
	if Accepts(FormatPagerDuty, EventImprovementFinished) {
		t.Error("PagerDuty takes improvement.finished")
	}
	if !Accepts(FormatSlack, EventImprovementFinished) || !Accepts(FormatSlack, EventImprovementPaused) {
		t.Error("Slack refuses an improvement event")
	}
}
