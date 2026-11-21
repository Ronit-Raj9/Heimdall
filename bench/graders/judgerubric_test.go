package graders

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRubricReply(t *testing.T) {
	rub := "[1] Finds revenue.\n[3] Calculates margin.\n"
	if got := rubricForJudge(rub); got != "1. Finds revenue.\n2. Calculates margin." {
		t.Fatalf("rubricForJudge = %q", got)
	}
	if _, err := ParseRubricLines("no points"); err == nil {
		t.Fatal("want error for a line without points")
	}
	reply := "```json\n{\"final_answer_correct\": false, \"rubric\": [{\"index\": 2, \"satisfied\": true, \"explanation\": \"x\"}, {\"index\": 2, \"satisfied\": true}]}\n```"
	if v := replyVerdict(PromptBigFinanceV1, reply); v != VoteIncorrect {
		t.Fatalf("verdict %s", v)
	}
	if v := replyVerdict(PromptBigFinanceV1, "correct"); v != VoteUnparsable {
		t.Fatalf("verdict %s", v)
	}
	g := &schemas.Grade{}
	applyRubricScore(g, &schemas.AnswerSpec{Rubric: rub}, []string{reply, "junk"})
	if g.Dimensions["rubric_score"] != 0.75 || g.Dimensions["final_answer_correct"] != 0 {
		t.Fatalf("dimensions %v", g.Dimensions)
	}
}
