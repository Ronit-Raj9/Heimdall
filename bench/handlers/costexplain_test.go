package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/costexplain"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/migration"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// w6Manifest is a two-arm experiment: the same agent on two models.
func w6Manifest() *schemas.Manifest {
	m := &schemas.Manifest{ExpID: "e_test", SuiteID: "s", SuiteVersion: "1", Arms: map[string]schemas.ArmManifest{
		"A": {Label: "old", ConfigHash: "ha", Config: schemas.AgentConfig{ID: "agent", Model: "old-model"}},
		"B": {Label: "new", ConfigHash: "hb", Config: schemas.AgentConfig{ID: "agent", Model: "new-model"}},
	}}
	m.Experiment.Primary.Condition = schemas.ConditionClean
	return m
}

// w6Runs: B passes t01-t03 where A fails t03, plus a gate firing and a
// cost unknown in one B run, so every nested list of the payloads has an
// element.
func w6Runs() []schemas.SealedRun {
	var runs []schemas.SealedRun
	for i, task := range []string{"t01", "t02", "t03"} {
		for _, arm := range []string{"A", "B"} {
			outcome := schemas.OutcomePass
			if arm == "A" && task == "t03" {
				outcome = schemas.OutcomeFail
			}
			sr := schemas.SealedRun{
				Slot:    schemas.Slot{SlotID: "s_" + arm + task, PairID: "p_" + task, Task: task, Arm: arm, Condition: schemas.ConditionClean, Trial: 1, Counted: true},
				Run:     schemas.Run{RunID: "r_" + arm + task, Outcome: outcome, Timing: schemas.RunTiming{Started: time.Unix(int64(i), 0).UTC()}},
				Metrics: map[string]schemas.Value{"cost_usd": schemas.Observed(0.1, schemas.EvidenceGateway)},
			}
			if arm == "B" && task == "t01" {
				sr.Gates = []schemas.GateResult{{Gate: "egress", Effect: "invalidate", Fired: true}}
			}
			runs = append(runs, sr)
		}
	}
	return runs
}

// w6Files serves cost rows and gateway rows with a raw detail, B with a
// colder cache and a different system prompt, so the cache finding shows.
func w6Files(sr *schemas.SealedRun, name string) ([]byte, bool, error) {
	read, fresh := int64(9000), int64(1000)
	prompt := "p1"
	if sr.Slot.Arm == "B" {
		read, fresh, prompt = 1000, 9000, "p2"
	}
	switch name {
	case costexplain.FileCost:
		rows := []schemas.CostRow{{RequestID: "q", Attempt: 1, Model: "m", Tokens: schemas.Tokens{Fresh: fresh, CacheRead: read, Out: 100}, USD: schemas.Observed(0.01, schemas.EvidenceGateway)}}
		if sr.Run.RunID == "r_Bt02" {
			rows = append(rows, schemas.CostRow{RequestID: "u", Attempt: 1, Model: "x", USD: schemas.UnknownWithin("no price", schemas.EvidenceGateway, 0, 0.2)})
		}
		b, _ := json.Marshal(rows)
		return b, true, nil
	case costexplain.FileRequests:
		row := map[string]any{"id": "q", "kind": "message", "status": "success", "latency_ms": 50,
			"raw": map[string]any{"input_history": []map[string]any{{"role": "system", "content": prompt}}}}
		b, _ := json.Marshal(row)
		return b, true, nil
	}
	return nil, false, nil
}

// fakeW6 answers both W6 sources from the real packages over the fixture.
type fakeW6 struct {
	lastCost      costexplain.Query
	lastMigration migration.Query
	lastSource    string
	err           error
}

func (f *fakeW6) CostExplain(ctx context.Context, expID, gradeSource string, q costexplain.Query) (*costexplain.Explanation, error) {
	f.lastCost, f.lastSource = q, gradeSource
	if f.err != nil {
		return nil, f.err
	}
	if expID != "e_test" {
		return nil, fmt.Errorf("experiment %s: %w", expID, schemas.ErrNotFound)
	}
	return costexplain.Explain(ctx, costexplain.Input{Manifest: w6Manifest(), Runs: w6Runs(), Read: w6Files}, q)
}

func (f *fakeW6) Pareto(_ context.Context, expID, condition, x string) (*costexplain.Pareto, error) {
	if expID != "e_test" {
		return nil, fmt.Errorf("experiment %s: %w", expID, schemas.ErrNotFound)
	}
	entry := func(succ, cps float64) schemas.LeaderboardEntry {
		return schemas.LeaderboardEntry{Success: schemas.Observed(succ, schemas.EvidenceOutcome), Metrics: map[string]schemas.Value{"cost_per_success": schemas.Observed(cps, schemas.EvidenceGateway)}}
	}
	return costexplain.ParetoFromMatrix(&schemas.ModelMatrix{Rows: []schemas.MatrixRow{{Arm: "A", LeaderboardEntry: entry(0.6, 0.2)}, {Arm: "B", LeaderboardEntry: entry(0.9, 0.3)}}}, x)
}

func (f *fakeW6) Migration(ctx context.Context, expID, gradeSource string, q migration.Query) (*migration.Report, error) {
	f.lastMigration, f.lastSource = q, gradeSource
	if f.err != nil {
		return nil, f.err
	}
	if expID != "e_test" {
		return nil, fmt.Errorf("experiment %s: %w", expID, schemas.ErrNotFound)
	}
	return migration.Build(ctx, migration.Input{Manifest: w6Manifest(), Runs: w6Runs(), Read: w6Files}, q)
}

func serveW6(t *testing.T, src *fakeW6, index ReadIndex, path string) (int, map[string]any) {
	t.Helper()
	r := router.New()
	mw := lib.BodyLimit(testMode, lib.MaxBodyBytes)
	var cs CostExplainSource
	var ms MigrationSource
	if src != nil {
		cs, ms = src, src
	}
	NewCostExplainHandler(testMode, cs, index).RegisterRoutes(r, mw)
	NewMigrationHandler(testMode, ms).RegisterRoutes(r, mw)
	var ctx fasthttp.RequestCtx
	ctx.Request.SetRequestURI("http://127.0.0.1" + path)
	ctx.Request.Header.SetMethod("GET")
	r.Handler(&ctx)
	var out map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatalf("GET %s: body %q: %v", path, ctx.Response.Body(), err)
	}
	return ctx.Response.StatusCode(), out
}

// checkNested checks every object at each path against its key list.
func checkNested(t *testing.T, where string, out map[string]any, nested map[string]string) {
	t.Helper()
	for path, typ := range nested {
		for i, obj := range objectsAt(t, out, path) {
			checkKeys(t, fmt.Sprintf("%s.%s #%d", where, path, i), obj, loadKeys(t, typ), false)
		}
	}
}

func TestCostExplainRouteMatchesTheContract(t *testing.T) {
	src := &fakeW6{}
	status, out := serveW6(t, src, nil, APIPrefix+"/experiments/e_test/cost-explain?a=A&b=B&condition=clean&grade_source=rg_1")
	if status != 200 {
		t.Fatalf("status %d: %v", status, out)
	}
	if src.lastCost != (costexplain.Query{A: "A", B: "B", Condition: "clean"}) || src.lastSource != "rg_1" {
		t.Errorf("query %+v source %s", src.lastCost, src.lastSource)
	}
	checkKeys(t, "cost explain", out, loadKeys(t, "CostExplanation"), true)
	checkNested(t, "cost explain", out, map[string]string{
		"a": "CostArm", "b": "CostArm", "per_run": "CostWaterfall", "per_success": "CostWaterfall",
		"per_run.parts[]": "CostPart", "per_success.parts[]": "CostPart", "apart[]": "CostApart", "cache[]": "CostCacheFinding",
	})
	// The unknown row keeps the total unknown, with bounds, never zero.
	total := out["per_run"].(map[string]any)["total"].(map[string]any)
	if total["state"] != "unknown" || total["bounds"] == nil {
		t.Errorf("per run total = %v, want unknown with bounds", total)
	}
}

func TestParetoRoutesMatchTheContract(t *testing.T) {
	status, out := serveW6(t, &fakeW6{}, nil, APIPrefix+"/experiments/e_test/pareto?x=cost_per_success")
	if status != 200 {
		t.Fatalf("status %d: %v", status, out)
	}
	checkKeys(t, "pareto", out, loadKeys(t, "Pareto"), true)
	checkNested(t, "pareto", out, map[string]string{"points[]": "ParetoPoint"})
	status, out = serveW6(t, nil, contractIndex(), APIPrefix+"/leaderboard/pareto?synthetic=only")
	if status != 200 {
		t.Fatalf("board pareto status %d: %v", status, out)
	}
	checkKeys(t, "board pareto", out, loadKeys(t, "Pareto"), true)
	if pts, _ := out["points"].([]any); len(pts) > 0 {
		checkNested(t, "board pareto", out, map[string]string{"points[]": "ParetoPoint"})
	}
}

func TestMigrationRouteMatchesTheContract(t *testing.T) {
	src := &fakeW6{}
	status, out := serveW6(t, src, nil, APIPrefix+"/experiments/e_test/migration?from=old-model&to=new-model&alpha=0.1")
	if status != 200 {
		t.Fatalf("status %d: %v", status, out)
	}
	if src.lastMigration.From != "old-model" || len(src.lastMigration.To) != 1 || src.lastMigration.Alpha != 0.1 || src.lastSource != schemas.GradeSourceOriginal {
		t.Errorf("query %+v source %s", src.lastMigration, src.lastSource)
	}
	checkKeys(t, "migration", out, loadKeys(t, "MigrationReport"), true)
	checkNested(t, "migration", out, map[string]string{
		"from": "MigrationArmRef", "pairs[]": "MigrationPair", "pairs[].from": "MigrationArmRef", "pairs[].overall": "MigrationVerdict",
		"pairs[].strata[]": "MigrationVerdict", "pairs[].recommendation": "MigrationRecommendation", "pairs[].behaviour": "MigrationBehaviour",
		"pairs[].behaviour.false_completion": "MigrationChange", "pairs[].behaviour.gates[]": "MigrationGateChange",
		"pairs[].behaviour.conditions[]": "MigrationConditionDiff", "pairs[].flipped[]": "MigrationFlippedTask",
		"pairs[].flipped[].runs[]": "MigrationRunRef", "pairs[].cost": "CostExplanation", "models[]": "MigrationModelSummary",
		"pareto": "Pareto", "pareto.points[]": "ParetoPoint",
	})
}

func TestW6RouteErrors(t *testing.T) {
	tests := []struct {
		name, path string
		src        *fakeW6
		status     int
	}{
		{"cost explain unknown experiment", "/experiments/e_nope/cost-explain", &fakeW6{}, 404},
		{"cost explain bad arm", "/experiments/e_test/cost-explain?a=Z", &fakeW6{}, 400},
		{"cost explain without a source", "/experiments/e_test/cost-explain", nil, 503},
		{"pareto bad axis", "/experiments/e_test/pareto?x=tokens", &fakeW6{}, 400},
		{"pareto unknown experiment", "/experiments/e_nope/pareto", &fakeW6{}, 404},
		{"board pareto without an index", "/leaderboard/pareto", nil, 503},
		{"migration unknown experiment", "/experiments/e_nope/migration?from=A", &fakeW6{}, 404},
		{"migration bad alpha", "/experiments/e_test/migration?alpha=x", &fakeW6{}, 400},
		{"migration unknown model", "/experiments/e_test/migration?from=gpt-9", &fakeW6{}, 400},
		{"migration without a source", "/experiments/e_test/migration", nil, 503},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if status, out := serveW6(t, tt.src, nil, APIPrefix+tt.path); status != tt.status {
				t.Fatalf("status %d, want %d: %v", status, tt.status, out)
			}
		})
	}
}
