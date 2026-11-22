package webhooks

import (
	"slices"
	"testing"
)

func TestMonitorIncidentSubscribable(t *testing.T) {
	if !slices.Contains(Events, EventMonitorIncident) {
		t.Fatalf("Events = %v", Events)
	}
	h := Hook{URL: "https://example.com/hook", Enabled: true}
	ev := []string{EventMonitorIncident}
	if err := apply(&h, Input{Events: &ev}, false); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
}
