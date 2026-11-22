package handlers

import (
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
)

// TestAgentIDRefusalNamesLimit: a name that breaks the agent id rule is
// refused with the whole rule, the 63-character limit included (N36).
func TestAgentIDRefusalNamesLimit(t *testing.T) {
	r, _ := builderRouter(t)
	long := strings.Repeat("a", 120)
	code, body := abCall(r, "PUT", APIPrefix+"/agents/"+long+"/config", AgentConfigPut{YAML: "id: " + long + "\n"})
	if code != fasthttp.StatusBadRequest {
		t.Fatalf("status %d, want 400 (%s)", code, body)
	}
	msg := string(body)
	for _, want := range []string{"lowercase letters, digits, '.', '_' or '-'", "starting with a letter or digit", "at most 63 characters"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal %s does not say %q", msg, want)
		}
	}
}
