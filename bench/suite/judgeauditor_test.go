package suite

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestLintJudgeAuditor(t *testing.T) {
	j := &schemas.JudgeSpec{Provider: "openai", Model: "m", PromptVersion: "qa-v1"}
	if got := lintJudgeAuditor(j); got != "" {
		t.Fatalf("no auditor: %q", got)
	}
	j.Auditor = &schemas.JudgeAuditorSpec{Provider: "anthropic", Model: "a", Votes: 3}
	if got := lintJudgeAuditor(j); got != "" {
		t.Fatalf("a fine auditor: %q", got)
	}
	j.Auditor.Provider = "openai"
	if got := lintJudgeAuditor(j); !strings.Contains(got, "another vendor") {
		t.Fatalf("same vendor: %q", got)
	}
	j.Auditor.Provider, j.Auditor.Votes = "anthropic", 2
	if got := lintJudgeAuditor(j); !strings.Contains(got, "even") {
		t.Fatalf("even votes: %q", got)
	}
}
