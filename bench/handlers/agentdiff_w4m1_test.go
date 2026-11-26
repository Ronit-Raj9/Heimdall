package handlers

import (
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
)

// TestAgentDiffBadQueryHasNoPrefix checks that a 400 from the agent diff
// carries the plain sentence without the internal error prefix (wave 4,
// p01-1).
func TestAgentDiffBadQueryHasNoPrefix(t *testing.T) {
	for _, path := range []string{"/agents/diff", "/agents/diff?agent=nobody"} {
		status, body := getAgentDiff(t, agentDiffIndex(), path)
		if status != fasthttp.StatusBadRequest {
			t.Fatalf("%s: status %d", path, status)
		}
		msg, _ := body["error"].(map[string]any)["message"].(string)
		if msg == "" || strings.HasPrefix(msg, "bad agent diff query") {
			t.Fatalf("%s: message %q", path, msg)
		}
	}
}
