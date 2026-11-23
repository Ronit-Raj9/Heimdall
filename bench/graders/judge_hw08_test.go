package graders

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// reasoningJudge is a chat endpoint shaped like a reasoning model: with
// fewer than 64 reply tokens it spends them all on reasoning and answers
// nothing (finish_reason length), with more it answers "correct".
func reasoningJudge(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body chatRequest
		_ = json.Unmarshal(raw, &body)
		content, finish, out := "correct", "stop", 40
		if body.MaxTokens < 64 {
			content, finish, out = "", "length", body.MaxTokens
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "judge-model",
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": finish}},
			"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": out,
				"completion_tokens_details": map[string]any{"reasoning_tokens": out}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Fix wave 3, N273: the default reply cap leaves a reasoning model room to
// answer, so a right answer is graded.
func TestJudgeDefaultTokensFitReasoningModel(t *testing.T) {
	srv := reasoningJudge(t)
	rc := judgedRC(t, qaSpec, strp("Your dog is Max."), &schemas.JudgeSpec{Provider: "openai", Model: "judge-model", PromptVersion: PromptQAv1, Votes: 1})
	got, err := newTestJudge(&fakeGateway{keyValue: "sk-bf-judge-key"}, srv.URL+"/").Grade(context.Background(), rc, &schemas.Diff{SHA256: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Passed || got.InvalidEvaluator {
		t.Fatalf("default cap %d: passed=%v invalid=%v reason %q", defaultJudgeTokens, got.Passed, got.InvalidEvaluator, got.Reason)
	}
	if defaultJudgeTokens != 1024 || customJudgeTokens != 2048 {
		t.Fatalf("caps %d and %d, want 1024 and 2048", defaultJudgeTokens, customJudgeTokens)
	}
}

// Fix wave 3, N273: an empty reply cut by the token cap says so instead of
// "no majority".
func TestJudgeEmptyLengthReplyReason(t *testing.T) {
	srv := reasoningJudge(t)
	rc := judgedRC(t, qaSpec, strp("Your dog is Max."), &schemas.JudgeSpec{Provider: "openai", Model: "judge-model", PromptVersion: PromptQAv1, Votes: 1, MaxTokens: 16})
	got, err := newTestJudge(&fakeGateway{keyValue: "sk-bf-judge-key"}, srv.URL+"/").Grade(context.Background(), rc, &schemas.Diff{SHA256: "d"})
	if err != nil {
		t.Fatal(err)
	}
	want := "judge: the judge model gave no answer within 16 reply tokens (it spent them on reasoning). Raise judge max_tokens in suite.yaml or pin a non-reasoning model."
	if !got.InvalidEvaluator || got.Reason != want {
		t.Fatalf("invalid=%v reason %q, want %q", got.InvalidEvaluator, got.Reason, want)
	}
}
