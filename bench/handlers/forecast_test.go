package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// fakeForecast is a canned ForecastSource that records the last query.
type fakeForecast struct {
	estimateErr error
	matrixErr   error
	lastQuery   leaderboard.MatrixQuery
	lastExp     *schemas.Experiment
}

func (f *fakeForecast) Estimate(_ context.Context, exp *schemas.Experiment) (*schemas.Forecast, error) {
	f.lastExp = exp
	if f.estimateErr != nil {
		return nil, f.estimateErr
	}
	lo, mid, hi := 1.0, 2.0, 3.0
	r := schemas.ForecastRange{Kind: schemas.RangeHistory, Low: &lo, Median: &mid, High: &hi}
	judge := &schemas.JudgeForecast{Runs: 4, HistoryRuns: 2, CostUSD: r, BasisText: "2 past judge records"}
	return &schemas.Forecast{ExpID: "e_x", Suite: "s@1", GeneratedAt: time.Unix(0, 0).UTC(), PriceRef: "p@sha256:x",
		Arms: []schemas.ArmForecast{{Arm: "A", Label: "a", Agent: "codex", Model: "m", ConfigHash: "h", Runs: 4,
			Basis: schemas.BasisConfigHash, BasisText: "4 sealed runs", HistoryRuns: 4, Experiments: []string{"e1"},
			PerRun: schemas.PerRunRanges{CostUSD: r, Tokens: r, WallS: r}, CostUSD: r, Tokens: r, AgentTimeS: r}},
		CostUSD: r, AgentTimeS: r, WallClockS: schemas.ForecastRange{Kind: schemas.RangeUnknown, Reason: "x"}, Concurrency: 2,
		Judge: judge, BudgetUSD: 5, Fit: schemas.FitFits, FitReason: "fits", Basis: "history", Notes: []string{}}, nil
}

func (f *fakeForecast) Matrix(_ context.Context, expID string, q leaderboard.MatrixQuery) (*schemas.ModelMatrix, error) {
	f.lastQuery = q
	if f.matrixErr != nil {
		return nil, f.matrixErr
	}
	if expID != "e_test" {
		return nil, fmt.Errorf("experiment %s: %w", expID, schemas.ErrNotFound)
	}
	p := 0.5
	entry := schemas.LeaderboardEntry{Rank: 1, Tier: 1, TierReason: "leader", ConfigHash: "h", Agent: "codex", Model: "m",
		Experiments: []string{"e_test"}, Success: schemas.Observed(0.5, schemas.EvidenceOutcome),
		Reliability: schemas.Unknown("k", schemas.EvidenceOutcome), Metrics: map[string]schemas.Value{}, Resilience: map[string]schemas.Value{}}
	return &schemas.ModelMatrix{ExpID: expID, Suite: "s@1", Condition: "clean", Conditions: []string{"clean"}, GradeSource: "original",
		Alpha: 0.05, Note: "Cannot separate", Rows: []schemas.MatrixRow{{Arm: "A", Provider: "openai", LeaderboardEntry: entry}},
		Pairs:    []schemas.MatrixPair{{ArmX: "A", ArmY: "B", Tasks: 2, P: &p, PAdjusted: &p, Reason: "cannot separate"}},
		Excluded: []schemas.LeaderboardExclusion{{Reason: "infra_error", Runs: 1, Experiments: []string{"e_test"}}}}, nil
}

// serveForecastRoute runs one request through the forecast routes mounted
// the way the server mounts them.
func serveForecastRoute(t *testing.T, src ForecastSource, method, path string, body []byte, token bool) (int, map[string]any) {
	t.Helper()
	r := router.New()
	NewForecastHandler(testMode, src, lib.RequireWriteToken(testMode, testToken)).
		RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	var ctx fasthttp.RequestCtx
	ctx.Request.SetRequestURI("http://127.0.0.1" + path)
	ctx.Request.Header.SetMethod(method)
	if body != nil {
		ctx.Request.SetBody(body)
		ctx.Request.Header.SetContentType("application/yaml")
	}
	if token {
		ctx.Request.Header.Set(fasthttp.HeaderAuthorization, "Bearer "+testToken)
	}
	r.Handler(&ctx)
	var out map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatalf("%s %s: body %q: %v", method, path, ctx.Response.Body(), err)
	}
	return ctx.Response.StatusCode(), out
}

func TestEstimateRoute(t *testing.T) {
	body := []byte("name: demo\nsuite: s@1\n")
	t.Run("forecast matches the contract", func(t *testing.T) {
		src := &fakeForecast{}
		status, out := serveForecastRoute(t, src, "POST", APIPrefix+"/estimate", body, true)
		if status != 200 || src.lastExp == nil || src.lastExp.Name != "demo" {
			t.Fatalf("status %d exp %+v", status, src.lastExp)
		}
		checkKeys(t, "estimate", out, loadKeys(t, "Forecast"), true)
		for path, typ := range map[string]string{"arms[]": "ArmForecast", "cost_usd": "ForecastRange", "judge": "JudgeForecast",
			"arms[].per_run": "PerRunRanges", "arms[].per_run.wall_s": "ForecastRange", "judge.cost_usd": "ForecastRange", "wall_clock_s": "ForecastRange"} {
			for _, obj := range objectsAt(t, out, path) {
				checkKeys(t, "estimate "+path, obj, loadKeys(t, typ), false)
			}
		}
	})
	t.Run("a refused plan is part of the answer", func(t *testing.T) {
		src := &fakeForecast{estimateErr: fmt.Errorf("%w: suite s@1 has no lock", ErrPlanRefused)}
		status, out := serveForecastRoute(t, src, "POST", APIPrefix+"/estimate", body, true)
		if status != 200 || out["fit"] != schemas.FitUnknown || !strings.Contains(fmt.Sprint(out["notes"]), "no lock") {
			t.Fatalf("status %d body %v", status, out)
		}
		checkKeys(t, "refused estimate", out, loadKeys(t, "Forecast"), true)
	})
	t.Run("other errors", func(t *testing.T) {
		src := &fakeForecast{estimateErr: errors.New("store broke")}
		if status, _ := serveForecastRoute(t, src, "POST", APIPrefix+"/estimate", body, true); status != 500 {
			t.Fatalf("status %d", status)
		}
	})
	t.Run("needs the write token", func(t *testing.T) {
		if status, _ := serveForecastRoute(t, &fakeForecast{}, "POST", APIPrefix+"/estimate", body, false); status != 401 {
			t.Fatalf("status %d", status)
		}
	})
	t.Run("bad body", func(t *testing.T) {
		if status, _ := serveForecastRoute(t, &fakeForecast{}, "POST", APIPrefix+"/estimate", []byte("name: [x"), true); status != 400 {
			t.Fatalf("status %d", status)
		}
	})
	t.Run("no source", func(t *testing.T) {
		if status, _ := serveForecastRoute(t, nil, "POST", APIPrefix+"/estimate", body, true); status != 503 {
			t.Fatalf("status %d", status)
		}
	})
}

func TestMatrixRoute(t *testing.T) {
	t.Run("matrix matches the contract", func(t *testing.T) {
		src := &fakeForecast{}
		status, out := serveForecastRoute(t, src, "GET", APIPrefix+"/experiments/e_test/matrix?condition=model-outage&grade_source=rg_1&k=2&alpha=0.1", nil, false)
		if status != 200 {
			t.Fatalf("status %d body %v", status, out)
		}
		want := leaderboard.MatrixQuery{Condition: "model-outage", GradeSource: "rg_1", K: 2, Alpha: 0.1}
		if src.lastQuery != want {
			t.Fatalf("query %+v, want %+v", src.lastQuery, want)
		}
		checkKeys(t, "matrix", out, loadKeys(t, "ModelMatrix"), true)
		for path, typ := range map[string]string{"rows[]": "MatrixRow", "pairs[]": "MatrixPair", "excluded[]": "LeaderboardExclusion"} {
			for _, obj := range objectsAt(t, out, path) {
				checkKeys(t, "matrix "+path, obj, loadKeys(t, typ), false)
			}
		}
	})
	tests := []struct {
		name, path string
		src        *fakeForecast
		status     int
	}{
		{"unknown experiment", "/experiments/e_nope/matrix", &fakeForecast{}, 404},
		{"bad k", "/experiments/e_test/matrix?k=x", &fakeForecast{}, 400},
		{"bad alpha", "/experiments/e_test/matrix?alpha=x", &fakeForecast{}, 400},
		{"bad query", "/experiments/e_test/matrix", &fakeForecast{matrixErr: fmt.Errorf("%w: k 1", leaderboard.ErrBadQuery)}, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if status, _ := serveForecastRoute(t, tt.src, "GET", APIPrefix+tt.path, nil, false); status != tt.status {
				t.Fatalf("status %d, want %d", status, tt.status)
			}
		})
	}
}
