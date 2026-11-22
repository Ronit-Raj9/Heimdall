package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// evalResultFixture is a two arm evaluation over the leaderboard test index,
// stored as running with a finished child, so the next refresh ends it.
func evalResultFixture(t *testing.T, id string, status string, created time.Time) (*evaluationsHandler, *evalstore.Store, *schemas.Evaluation) {
	t.Helper()
	evalRefreshChildren = false
	t.Cleanup(func() { evalRefreshChildren = true })
	root := t.TempDir()
	ev := evalResultEvaluation(id, status, created)
	st := evalstore.Open(root)
	if err := st.Save(ev); err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: leaderboardIndex()}}
	return &evaluationsHandler{env: env, now: time.Now}, st, ev
}

func evalResultEvaluation(id, status string, created time.Time) *schemas.Evaluation {
	return &schemas.Evaluation{ID: id, Status: status, CreatedAt: created,
		Spec: schemas.EvaluationSpec{Name: "v2 vs v1", Mode: schemas.EvaluationModeVersions, Benchmarks: []string{"coding-core"},
			Arms: []schemas.EvaluationArm{
				{Label: "A", Agent: "codex", Version: 1, Model: "gpt-5.4-mini"},
				{Label: "B", Agent: "codex", Version: 2, Model: "gpt-5.4-mini"},
			}},
		Children: []schemas.EvaluationChild{{Benchmark: "coding-core", ExperimentID: testExp, Status: string(schemas.ExperimentDone)}},
	}
}

// wantResult checks a stored result against the report card's totals.
func wantResult(t *testing.T, h *evaluationsHandler, ev *schemas.Evaluation, got *schemas.EvaluationResult) {
	t.Helper()
	if got == nil {
		t.Fatal("no result stored")
	}
	rep, err := reportcard.Build(context.Background(), LeaderboardSource(h.env.Services.Index), ev, reportcard.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Arms) != 2 || got.At.IsZero() {
		t.Fatalf("result %+v", got)
	}
	var cost *float64
	sum, known := 0.0, true
	for i, a := range rep.Arms {
		tot := rep.Totals[a.Label]
		g := got.Arms[i]
		if g.Label != a.Label || g.Agent != a.Agent || g.Version != a.Version || g.Model != a.Model || g.Score != tot.Score || g.Runs != tot.Runs {
			t.Fatalf("arm %d: got %+v, report arm %+v totals %+v", i, g, a, tot)
		}
		if tot.CostUSD == nil {
			known = false
		} else {
			sum += *tot.CostUSD
		}
	}
	if known {
		cost = &sum
	}
	if (cost == nil) != (got.CostUSD == nil) || (cost != nil && *cost != *got.CostUSD) {
		t.Fatalf("cost %v, want %v", got.CostUSD, cost)
	}
	if got.Arms[0].Runs == 0 {
		t.Fatalf("the fixture has runs: %+v", got.Arms)
	}
}

func TestEvaluationResultStoredOnDone(t *testing.T) {
	h, st, ev := evalResultFixture(t, "ev_20261123_000000_res", schemas.EvaluationRunning, time.Now())
	h.refresh(ev)
	saved, err := st.Load(ev.ID)
	if err != nil || saved.Status != schemas.EvaluationDone {
		t.Fatalf("saved %+v %v", saved, err)
	}
	wantResult(t, h, saved, saved.Result)
	// Stored once: a later refresh keeps the first result.
	at := saved.Result.At
	h.refresh(saved)
	again, _ := st.Load(ev.ID)
	if again.Result == nil || !again.Result.At.Equal(at) {
		t.Fatalf("result rebuilt: %+v", again.Result)
	}
	// The JSON carries it under "result".
	b, _ := json.Marshal(again)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["result"]; !ok {
		t.Fatalf("no result key: %s", b)
	}
}

func TestEvaluationResultBackfilledOnGet(t *testing.T) {
	// Finished before results were stored: done, no result.
	h, st, ev := evalResultFixture(t, "ev_20261123_000000_old", schemas.EvaluationDone, time.Now())
	r := router.New()
	r.GET(APIPrefix+"/evaluations/{id}", h.get)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(APIPrefix + "/evaluations/" + ev.ID)
	r.Handler(&ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var got schemas.Evaluation
	if err := json.Unmarshal(ctx.Response.Body(), &got); err != nil {
		t.Fatal(err)
	}
	wantResult(t, h, ev, got.Result)
	saved, _ := st.Load(ev.ID)
	if saved.Result == nil {
		t.Fatal("backfilled result not saved")
	}
}

func TestEvaluationListBackfillsAtMostThree(t *testing.T) {
	h, st, _ := evalResultFixture(t, "ev_20261123_000000_aa0", schemas.EvaluationDone, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	for i := 1; i < 5; i++ {
		ev := evalResultEvaluation(fmt.Sprintf("ev_20261123_000000_aa%d", i), schemas.EvaluationDone, time.Date(2026, 11, 1+i, 0, 0, 0, 0, time.UTC))
		if err := st.Save(ev); err != nil {
			t.Fatal(err)
		}
	}
	list := func() map[string]bool {
		r := router.New()
		r.GET(APIPrefix+"/evaluations", h.list)
		var ctx fasthttp.RequestCtx
		ctx.Request.Header.SetMethod(fasthttp.MethodGet)
		ctx.Request.SetRequestURI(APIPrefix + "/evaluations")
		r.Handler(&ctx)
		var out EvaluationList
		if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
			t.Fatal(err)
		}
		has := map[string]bool{}
		for _, ev := range out.Evaluations {
			has[ev.ID] = ev.Result != nil
		}
		return has
	}
	has := list()
	// Oldest first: aa0, aa1 and aa2 fill on this read, aa3 and aa4 wait.
	for i, want := range []bool{true, true, true, false, false} {
		if id := fmt.Sprintf("ev_20261123_000000_aa%d", i); has[id] != want {
			t.Fatalf("first read: %s has result %v, want %v (%v)", id, has[id], want, has)
		}
	}
	has = list()
	for i := 0; i < 5; i++ {
		if id := fmt.Sprintf("ev_20261123_000000_aa%d", i); !has[id] {
			t.Fatalf("second read: %s has no result (%v)", id, has)
		}
	}
}

// Four old evaluations whose report card cannot be built must not hold the
// three backfill slots: after one failed attempt they wait
// evalResultRetryAfter, so the next list fills the good one, and after the
// hold they are tried again.
func TestEvaluationListBackfillSkipsFailedOnes(t *testing.T) {
	h, st, _ := evalResultFixture(t, "ev_20261123_000000_bad0", schemas.EvaluationDone, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	for i := 1; i < 4; i++ {
		ev := evalResultEvaluation(fmt.Sprintf("ev_20261123_000000_bad%d", i), schemas.EvaluationDone, time.Date(2026, 11, 1+i, 0, 0, 0, 0, time.UTC))
		if err := st.Save(ev); err != nil {
			t.Fatal(err)
		}
	}
	good := evalResultEvaluation("ev_20261123_000000_good", schemas.EvaluationDone, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC))
	if err := st.Save(good); err != nil {
		t.Fatal(err)
	}
	tries := map[string]int{}
	build := evalResultBuild
	evalResultBuild = func(ctx context.Context, src leaderboard.Source, ev *schemas.Evaluation, opt reportcard.Options) (*reportcard.Report, error) {
		tries[ev.ID]++
		if ev.ID != good.ID {
			return nil, fmt.Errorf("broken child")
		}
		return build(ctx, src, ev, opt)
	}
	t.Cleanup(func() { evalResultBuild = build })
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	list := func() {
		r := router.New()
		r.GET(APIPrefix+"/evaluations", h.list)
		var ctx fasthttp.RequestCtx
		ctx.Request.Header.SetMethod(fasthttp.MethodGet)
		ctx.Request.SetRequestURI(APIPrefix + "/evaluations")
		r.Handler(&ctx)
		if ctx.Response.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("status %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
		}
	}
	hasResult := func(id string) bool {
		ev, err := st.Load(id)
		if err != nil {
			t.Fatal(err)
		}
		return ev.Result != nil
	}
	// First read: the three oldest (broken) take the slots and fail.
	list()
	if tries["ev_20261123_000000_bad0"] != 1 || tries["ev_20261123_000000_bad2"] != 1 || tries["ev_20261123_000000_bad3"] != 0 || hasResult(good.ID) {
		t.Fatalf("first read tries %v", tries)
	}
	// Second read: those three are on hold, so bad3 and the good one get slots.
	list()
	if !hasResult(good.ID) {
		t.Fatalf("good evaluation not filled on the second read (tries %v)", tries)
	}
	if tries["ev_20261123_000000_bad0"] != 1 || tries["ev_20261123_000000_bad3"] != 1 {
		t.Fatalf("second read tries %v", tries)
	}
	// Within the hold nothing broken is tried again.
	list()
	if tries["ev_20261123_000000_bad0"] != 1 || tries["ev_20261123_000000_bad3"] != 1 {
		t.Fatalf("third read tries %v", tries)
	}
	// After the hold the oldest are tried again.
	now = now.Add(evalResultRetryAfter)
	list()
	if tries["ev_20261123_000000_bad0"] != 2 {
		t.Fatalf("after the hold tries %v", tries)
	}
}
