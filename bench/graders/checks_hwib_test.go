package graders

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Fix wave 3, N273 (group B): the reply judge of a checks-graded task names
// the reply cap when the judge's reply was cut by it, not "no majority".
func TestChecksReplyJudgeLengthCutReason(t *testing.T) {
	srv := reasoningJudge(t)
	spec := checksYAML + "  - id: reply\n    reply_rubric: Apologises and confirms the refund amount.\n"
	g := newChecks(ChecksConfig{Judge: JudgeConfig{Gateway: &fakeGateway{keyValue: "sk-judge"}, URL: srv.URL + "/", Price: testPrice}})
	rc := checksRC(t, spec, toolRows(t, goodLedger...), refundedDB)
	rc.Suite.Grading = schemas.GradingJudged
	rc.Suite.Judge = &schemas.JudgeSpec{Provider: "openai", Model: "judge-model", PromptVersion: PromptQAv1, Votes: 1, MaxTokens: 16}
	grade, err := g.Grade(context.Background(), rc, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := lengthCutReason(16)
	if !grade.InvalidEvaluator || !strings.Contains(grade.Reason, want) {
		t.Fatalf("invalid=%v reason %q, want it to contain %q", grade.InvalidEvaluator, grade.Reason, want)
	}
	if strings.Contains(grade.Reason, "no majority") {
		t.Fatalf("reason still says no majority: %q", grade.Reason)
	}
}
