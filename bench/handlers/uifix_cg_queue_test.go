package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestEvaluationQueue: a queued evaluation gets queue with the org's own
// queued evaluations ahead (never another org's) and the worker's state;
// a running one gets none; the shared page drops it.
func TestEvaluationQueue(t *testing.T) {
	t0 := time.Date(2026, 11, 27, 8, 0, 0, 0, time.UTC)
	ev := &schemas.Evaluation{ID: "ev_3", Org: "org_a", Status: schemas.EvaluationQueued, CreatedAt: t0.Add(3 * time.Minute)}
	evs := []*schemas.Evaluation{
		{ID: "ev_1", Org: "org_a", Status: schemas.EvaluationQueued, CreatedAt: t0},
		{ID: "ev_2", Org: "org_a", Status: schemas.EvaluationRunning, CreatedAt: t0},
		{ID: "ev_b", Org: "org_b", Status: schemas.EvaluationQueued, CreatedAt: t0},
		{ID: "ev_4", Org: "org_a", Status: schemas.EvaluationQueued, CreatedAt: t0.Add(time.Hour)},
		ev,
	}
	if n := queuedAhead(ev, evs, "org_a"); n != 1 {
		t.Fatalf("ahead %d, want 1 (another org's never counts)", n)
	}
	seen := t0.Add(-time.Minute)
	var target string
	h := &evaluationsHandler{env: OrgEnv{Cfg: nil, Services: Services{OrgID: "org_a", WorkerStatus: func(_ context.Context, tg string) (WorkerStatus, error) {
		target = tg
		return WorkerStatus{Known: true, Online: true, LastSeen: &seen}, nil
	}}}}
	var ctx fasthttp.RequestCtx
	b, _ := json.Marshal(h.withQueue(&ctx, ev))
	var out struct {
		ID    string          `json:"id"`
		Queue EvaluationQueue `json:"queue"`
	}
	if json.Unmarshal(b, &out) != nil || out.ID != "ev_3" || out.Queue.WorkerOnline == nil || !*out.Queue.WorkerOnline || target != "shared" {
		t.Fatalf("%s target=%q", b, target)
	}
	running := &schemas.Evaluation{ID: "ev_2", Status: schemas.EvaluationRunning, Children: []schemas.EvaluationChild{{SpentUSD: 0.01}}}
	if got := h.withQueue(&ctx, running); got != running {
		t.Fatal("running evaluation that spent got a queue")
	}
	// N2: running with nothing spent and no runner row at all says "no worker".
	h.env.Services.WorkerStatus = func(context.Context, string) (WorkerStatus, error) { return WorkerStatus{}, nil }
	waiting := &schemas.Evaluation{ID: "ev_5", Status: schemas.EvaluationRunning, Children: []schemas.EvaluationChild{{}}}
	b, _ = json.Marshal(h.withQueue(&ctx, waiting))
	out.Queue = EvaluationQueue{}
	if json.Unmarshal(b, &out) != nil || out.Queue.WorkerOnline == nil || *out.Queue.WorkerOnline {
		t.Fatalf("no worker not said: %s", b)
	}
	shared := withoutNewSharedFields("evaluation", map[string]any{"id": "ev_3", "queue": map[string]any{"ahead": 1.0}}).(map[string]any)
	if _, ok := shared["queue"]; ok {
		t.Fatal("queue on the shared page")
	}
}
