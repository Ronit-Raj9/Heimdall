package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func reportCardServe(t *testing.T, env OrgEnv, uri string) (int, map[string]any) {
	t.Helper()
	r := router.New()
	registerReportCard(env, r)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(uri)
	r.Handler(&ctx)
	var body map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
		t.Fatalf("%s: %v: %s", uri, err, ctx.Response.Body())
	}
	return ctx.Response.StatusCode(), body
}

func TestReportCardRoutes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(ReportCardEvaluationsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ev := schemas.Evaluation{ID: "ev_1", Status: "done", CreatedAt: time.Now(),
		Spec: schemas.EvaluationSpec{Mode: "versions", Arms: []schemas.EvaluationArm{
			{Label: "A", Agent: "agents/codex.yaml@v1", Model: "gpt-5.4-mini"},
			{Label: "B", Agent: "codex", Version: 2, Model: "gpt-5.4-mini"},
		}},
		Children: []schemas.EvaluationChild{{Benchmark: "coding-core", ExperimentID: testExp, Status: "done"}},
	}
	data, _ := json.Marshal(ev)
	if err := os.WriteFile(filepath.Join(dir, "ev_1.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: leaderboardIndex()}}

	code, body := reportCardServe(t, env, APIPrefix+"/evaluations/ev_1/report")
	if code != fasthttp.StatusOK || body["evaluation"] != "ev_1" {
		t.Fatalf("report %d %v", code, body)
	}
	benches, _ := body["benchmarks"].([]any)
	if len(benches) != 1 {
		t.Fatalf("benchmarks %v", body["benchmarks"])
	}
	row := benches[0].(map[string]any)
	arms := row["arms"].(map[string]any)
	if row["capability"] != "coding" || arms["A"] == nil || arms["B"] == nil || row["verdict"] == nil || body["overall"] == nil {
		t.Fatalf("row %v overall %v", row, body["overall"])
	}
	if runs := arms["A"].(map[string]any)["runs"].(float64); runs < 1 {
		t.Fatalf("arm A has no runs: %v", arms["A"])
	}

	if code, _ := reportCardServe(t, env, APIPrefix+"/evaluations/ev_missing/report"); code != fasthttp.StatusNotFound {
		t.Fatalf("missing evaluation: %d", code)
	}

	code, body = reportCardServe(t, env, APIPrefix+"/agents/codex/report-card?version=2")
	if code != fasthttp.StatusOK {
		t.Fatalf("agent card %d %v", code, body)
	}
	rep := body["report"].(map[string]any)
	if armsList := rep["arms"].([]any); len(armsList) != 1 || armsList[0].(map[string]any)["label"] != "B" || rep["overall"] != nil {
		t.Fatalf("agent card arms %v", rep)
	}
	if _, body = reportCardServe(t, env, APIPrefix+"/agents/other/report-card"); body["report"] != nil {
		t.Fatalf("unknown agent has no report: %v", body)
	}
	if code, _ = reportCardServe(t, env, APIPrefix+"/agents/codex/report-card?version=x"); code != fasthttp.StatusBadRequest {
		t.Fatalf("bad version: %d", code)
	}
	if code, _ = reportCardServe(t, OrgEnv{Mode: testMode}, APIPrefix+"/evaluations/ev_1/report"); code != fasthttp.StatusServiceUnavailable {
		t.Fatalf("no index: %d", code)
	}
}

// TestAgentReportCardListsModels: the agent report card answer lists the
// models the agent's evaluations used, for the Model filter (N34).
func TestAgentReportCardListsModels(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(ReportCardEvaluationsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []schemas.Evaluation{
		{ID: "ev_1", Status: "done", CreatedAt: time.Now(), Spec: schemas.EvaluationSpec{Mode: "models", Arms: []schemas.EvaluationArm{
			{Label: "A", Agent: "codex", Model: "gpt-6-luna"}, {Label: "B", Agent: "codex", Model: "gpt-6.2-sol"},
		}}},
		{ID: "ev_2", Status: "done", CreatedAt: time.Now(), Spec: schemas.EvaluationSpec{Mode: "score", Arms: []schemas.EvaluationArm{
			{Label: "A", Agent: "other", Model: "claude-x"},
		}}},
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
	models, _ := body["models"].([]any)
	if len(models) != 2 || models[0] != "gpt-6-luna" || models[1] != "gpt-6.2-sol" {
		t.Fatalf("models %v, want [gpt-6-luna gpt-6.2-sol]", body["models"])
	}
}
