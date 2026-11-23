package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestAgentReportCardMatchingCount: the agent card names the evaluation it
// was built from, counts every done evaluation that matches and lists them
// for the picker; ?evaluation= picks an older one (fix wave 3, N206).
func TestAgentReportCardMatchingCount(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(ReportCardEvaluationsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	for _, ev := range []schemas.Evaluation{
		{ID: "ev_old", Status: "done", CreatedAt: now.Add(-time.Hour), Spec: schemas.EvaluationSpec{Name: "codex first look", Mode: "score",
			Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Model: "gpt-6-luna"}}}},
		{ID: "ev_new", Status: "done", CreatedAt: now, Spec: schemas.EvaluationSpec{Name: "codex: two models", Mode: "models",
			Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Model: "gpt-6-luna"}, {Label: "B", Agent: "codex", Model: "gpt-6.2-sol"}}}},
		{ID: "ev_other", Status: "done", CreatedAt: now, Spec: schemas.EvaluationSpec{Name: "other", Mode: "score",
			Arms: []schemas.EvaluationArm{{Label: "A", Agent: "other", Model: "claude-x"}}}},
	} {
		data, _ := json.Marshal(ev)
		if err := os.WriteFile(filepath.Join(dir, ev.ID+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: leaderboardIndex()}}
	code, body := reportCardServe(t, env, APIPrefix+"/agents/codex/report-card")
	if code != fasthttp.StatusOK {
		t.Fatalf("agent card %d %v", code, body)
	}
	if body["evaluation_id"] != "ev_new" || body["evaluation_name"] != "codex: two models" || body["matching_evaluations"] != float64(2) {
		t.Fatalf("card names %v %v, matching %v", body["evaluation_id"], body["evaluation_name"], body["matching_evaluations"])
	}
	if at, _ := body["evaluation_created_at"].(string); at != "2026-11-22T12:00:00Z" {
		t.Fatalf("evaluation_created_at %v", body["evaluation_created_at"])
	}
	list, _ := body["evaluations"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["id"] != "ev_new" || list[1].(map[string]any)["name"] != "codex first look" {
		t.Fatalf("evaluations %v", body["evaluations"])
	}
	_, body = reportCardServe(t, env, APIPrefix+"/agents/codex/report-card?evaluation=ev_old")
	if body["evaluation_id"] != "ev_old" || body["matching_evaluations"] != float64(2) {
		t.Fatalf("?evaluation=ev_old: %v %v", body["evaluation_id"], body["matching_evaluations"])
	}
	_, body = reportCardServe(t, env, APIPrefix+"/agents/codex/report-card?model=gpt-6.2-sol")
	if body["evaluation_id"] != "ev_new" || body["matching_evaluations"] != float64(1) {
		t.Fatalf("model filter: %v %v", body["evaluation_id"], body["matching_evaluations"])
	}
}
