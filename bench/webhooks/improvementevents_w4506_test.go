package webhooks

import (
	"slices"
	"testing"
)

// TestImprovementEventsSubscribable: both improvement events are in the
// list a hook subscribes from.
func TestImprovementEventsSubscribable(t *testing.T) {
	for _, e := range []string{EventImprovementFinished, EventImprovementPaused} {
		if !slices.Contains(Events, e) || !Accepts(FormatSlack, e) {
			t.Errorf("%s not subscribable: %v", e, Events)
		}
	}
}
