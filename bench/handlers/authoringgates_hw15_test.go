package handlers

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A review card counts each assist call once (fix wave 3, N278), for fields
// drafted before the fix (every field of a call carries its whole cost) and
// after it (the cost sits on the first field only).
func TestAssistCallCostCountedOnceOnCards(t *testing.T) {
	legacy := []schemas.GeneratedField{
		{Field: "answer.wrong_answers[0]", Op: "criterion_wrong", At: "2026-11-22T21:00:00Z", PromptSHA: "a", CostUSD: 0.005},
		{Field: "answer.wrong_answers[1]", Op: "criterion_wrong", At: "2026-11-22T21:00:00Z", PromptSHA: "a", CostUSD: 0.005},
		{Field: "answer.rubric", Op: "rubric", At: "2026-11-22T21:00:01Z", PromptSHA: "b", CostUSD: 0.002},
	}
	fixed := []schemas.GeneratedField{
		{Field: "answer.wrong_answers[0]", Op: "criterion_wrong", At: "2026-11-22T21:00:00Z", PromptSHA: "a", CostUSD: 0.005},
		{Field: "answer.wrong_answers[1]", Op: "criterion_wrong", At: "2026-11-22T21:00:00Z", PromptSHA: "a"},
		{Field: "answer.rubric", Op: "rubric", At: "2026-11-22T21:00:01Z", PromptSHA: "b", CostUSD: 0.002},
	}
	for name, fs := range map[string][]schemas.GeneratedField{"legacy": legacy, "fixed": fixed} {
		if got := generatedCost(fs); math.Abs(got-0.007) > 1e-9 {
			t.Errorf("%s: $%v, want $0.007", name, got)
		}
	}
}
