package modelid

import "testing"

func TestFamilyStripsProviderRegionVendorRevisionDate(t *testing.T) {
	for _, id := range []string{
		"claude-sonnet-4-5",
		"us.anthropic.claude-sonnet-4-5-20250929-v1:0",
		"claude-sonnet-4-5@20250929",
		"anthropic/claude-sonnet-4-5",
		"Global.Anthropic.Claude-Sonnet-4-5-2025-09-29",
	} {
		if got := Family(id); got != "claude-sonnet-4-5" {
			t.Errorf("Family(%q) = %q, want claude-sonnet-4-5", id, got)
		}
	}
	if got := Family("gpt-5.4-mini"); got != "gpt-5.4-mini" {
		t.Errorf("Family(gpt-5.4-mini) = %q", got)
	}
}
