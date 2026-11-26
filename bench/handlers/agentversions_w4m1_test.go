package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestAgentVersionsNoFileIsEmpty checks that an agent known only from its
// runs (no settings file) answers 200 with an empty list and no_file, while
// an id with no file and no runs stays 404 (wave 4, p01-2).
func TestAgentVersionsNoFileIsEmpty(t *testing.T) {
	cfg := schemas.AgentConfig{ID: "runs-only", Adapter: "command", Model: "m"}
	r, _, _ := versionsRouter(t, &versionsIndex{cfg: cfg}, "")
	got := versionsOf(t, r, "runs-only")
	if !got.NoFile || len(got.Versions) != 0 || got.Versions == nil {
		t.Fatalf("got %+v", got)
	}
	if code, _ := abCall(r, "GET", APIPrefix+"/agents/never-seen/versions", nil); code != 404 {
		t.Fatalf("unknown agent: %d", code)
	}
}
