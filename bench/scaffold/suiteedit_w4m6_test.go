package scaffold

import (
	"strings"
	"testing"
)

func TestSetSuiteGraderRewritesQADescription(t *testing.T) {
	src := []byte("id: faq\nversion: 0.1.0\ndescription: " + QAExactDescription + "\ngrader: answer_match\n")
	judged, err := SetSuiteGrader(src, RowGraderJudge, &JudgeConfig{Provider: "openai", Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(judged), "description: "+QAJudgeDescription) || strings.Contains(string(judged), "no model in the grade") {
		t.Fatalf("judge description not set:\n%s", judged)
	}
	back, err := SetSuiteGrader(judged, RowGraderExact, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(back), "description: "+QAExactDescription) {
		t.Fatalf("exact description not restored:\n%s", back)
	}

	own := []byte("id: faq\ndescription: My own words.\ngrader: answer_match\n")
	kept, err := SetSuiteGrader(own, RowGraderJudge, &JudgeConfig{Provider: "openai", Model: "gpt-4o-mini"})
	if err != nil || !strings.Contains(string(kept), "description: My own words.") {
		t.Fatalf("own description changed: %v\n%s", err, kept)
	}
}
