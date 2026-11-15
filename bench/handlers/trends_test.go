package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/trends"
	"github.com/valyala/fasthttp"
)

// fakeTrends builds trends over a fixed index snapshot, or fails.
type fakeTrends struct {
	snap *resultsindex.Snapshot
	err  error
	got  trends.Query
}

func (f *fakeTrends) Trends(_ context.Context, q trends.Query) (*trends.Response, error) {
	f.got = q
	if f.err != nil {
		return nil, f.err
	}
	return trends.Build(f.snap, q, time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC))
}

// trendSnapshot is one lineage with two points: e_1 then e_2 with a new
// config hash, a regressed verdict and every task failing.
func trendSnapshot() *resultsindex.Snapshot {
	one, zero, cost := 1.0, 0.0, 0.2
	var rows []resultsindex.RunRow
	for i := 1; i <= 6; i++ {
		for _, p := range []struct {
			exp, hash string
			day       int
			success   *float64
		}{{"e_1", "sha256:aaa", 1, &one}, {"e_2", "sha256:bbb", 3, &zero}} {
			rows = append(rows, resultsindex.RunRow{Experiment: p.exp, ExperimentCreated: time.Date(2026, 11, p.day, 0, 0, 0, 0, time.UTC),
				RunID: fmt.Sprintf("r_%s_%d", p.exp, i), Suite: "core@1", Task: fmt.Sprintf("t%d", i), Arm: "A", Agent: "codex",
				Model: "m1", ConfigHash: p.hash, Condition: "clean", Success: p.success, CostUSD: &cost, Metrics: map[string]float64{}})
		}
	}
	return &resultsindex.Snapshot{RunRows: rows, AnalysisRows: []resultsindex.AnalysisRow{
		{Experiment: "e_2", AnalysisID: "a_1", Decision: "regressed", Policy: "block", CreatedAt: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)},
	}}
}

// getTrendsRoute serves one trends request on a fresh router.
func getTrendsRoute(t *testing.T, src TrendSource, query string) (int, map[string]any) {
	t.Helper()
	h := NewTrendsHandler(testMode, src)
	r := router.New()
	h.RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(APIPrefix + "/trends" + query)
	r.Handler(&ctx)
	var body map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
		t.Fatalf("decode %q: %v", ctx.Response.Body(), err)
	}
	if body["mode"] != string(testMode) {
		t.Errorf("mode = %v", body["mode"])
	}
	return ctx.Response.StatusCode(), body
}

func TestTrendsRoute(t *testing.T) {
	tests := []struct {
		name   string
		src    TrendSource
		query  string
		status int
	}{
		{"defaults", &fakeTrends{snap: trendSnapshot()}, "", 200},
		{"filters", &fakeTrends{snap: trendSnapshot()}, "?suite=core@1&agent=codex&metric=cost_per_success&condition=clean&synthetic=include", 200},
		{"bad metric", &fakeTrends{snap: trendSnapshot()}, "?metric=score", 400},
		{"bad synthetic", &fakeTrends{snap: trendSnapshot()}, "?synthetic=maybe", 400},
		{"source fails", &fakeTrends{err: errors.New("disk on fire")}, "", 500},
		{"no source", nil, "", 503},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var src TrendSource
			if tt.src != nil {
				src = tt.src
			}
			status, body := getTrendsRoute(t, src, tt.query)
			if status != tt.status {
				t.Fatalf("status = %d, want %d: %v", status, tt.status, body)
			}
		})
	}
}

func TestTrendsMatchContract(t *testing.T) {
	src := &fakeTrends{snap: trendSnapshot()}
	status, body := getTrendsRoute(t, src, "?agent=codex")
	if status != 200 || src.got.Agent != "codex" {
		t.Fatalf("status %d, query %+v", status, src.got)
	}
	checkKeys(t, "Trends", body, loadKeys(t, "Trends"), true)
	for i, obj := range objectsAt(t, body, "lineages[]") {
		checkKeys(t, fmt.Sprintf("Trends.lineages #%d", i), obj, loadKeys(t, "TrendLineage"), false)
	}
	for i, obj := range objectsAt(t, body, "lineages[].points[]") {
		checkKeys(t, fmt.Sprintf("Trends.lineages.points #%d", i), obj, loadKeys(t, "TrendPoint"), false)
	}
	for _, path := range []string{"lineages[].regression", "lineages[].points[].regression"} {
		for i, obj := range objectsAt(t, body, path) {
			checkKeys(t, fmt.Sprintf("Trends.%s #%d", path, i), obj, loadKeys(t, "TrendRegression"), false)
		}
	}
	points := objectsAt(t, body, "lineages[].points[]")
	if len(points) != 2 || points[1]["config_changed"] != true || points[1]["release"] != "regressed" {
		t.Fatalf("points = %v", points)
	}
	if reg := objectsAt(t, body, "lineages[].regression")[0]; reg["status"] != trends.StatusRegressed {
		t.Fatalf("lineage regression = %v", reg)
	}
}
