package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func cheatCheckServe(t *testing.T, root, id, body string) (int, []byte) {
	t.Helper()
	r := router.New()
	h := &evaluationsHandler{env: OrgEnv{Mode: "local", Cfg: &lib.Config{Root: root}}, now: time.Now}
	r.POST(APIPrefix+"/evaluations/{id}/cheat-check", h.cheatCheck)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI(APIPrefix + "/evaluations/" + id + "/cheat-check")
	ctx.Request.SetBodyString(body)
	r.Handler(&ctx)
	return ctx.Response.StatusCode(), append([]byte(nil), ctx.Response.Body()...)
}

func cheatCheckParent(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	ev := &schemas.Evaluation{ID: "ev_20261127_000000_abc123", Status: "done", CreatedAt: time.Now(),
		Spec: schemas.EvaluationSpec{Name: "nightly", Benchmarks: []string{"s"}, Trials: 3}, Children: []schemas.EvaluationChild{{Benchmark: "s"}}}
	if err := evalstore.Open(root).Save(ev); err != nil {
		t.Fatal(err)
	}
	return root, ev.ID
}

// cheatCheckWebBody is the body the report card's cheat check sends
// (components/evaluations/paidCaps.ts startCheatCheckCapped).
const cheatCheckWebBody = `{"yes":true,"budget_usd":0.5}`

// TestCheatCheckRefusesWithoutCap: the round 3 web body had no cap and the
// child ran on the parent's whole budget; a start without one is refused.
func TestCheatCheckRefusesWithoutCap(t *testing.T) {
	root, id := cheatCheckParent(t)
	for _, body := range []string{`{"yes":true}`, `{"yes":true,"budget_usd":0}`, `{"yes":true,"budget_usd":-1}`} {
		code, out := cheatCheckServe(t, root, id, body)
		if code != 400 || !strings.Contains(string(out), cheatCheckNoBudgetMsg) {
			t.Fatalf("%s: %d %s", body, code, out)
		}
	}
}

func TestCheatCheckRefusesWithoutTwins(t *testing.T) {
	root, id := cheatCheckParent(t)
	code, body := cheatCheckServe(t, root, id, cheatCheckWebBody)
	if code != 409 || !strings.Contains(string(body), cheatCheckNoTwinsMsg) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestCheatCheckChildIsExploratory(t *testing.T) {
	root, id := cheatCheckParent(t)
	dir := filepath.Join(root, "benchmarks", "s-twins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock, _ := json.Marshal(schemas.SuiteLock{Suite: "s-twins", Tasks: map[string]schemas.TaskLock{"t1-twin": {Status: schemas.TaskAdmitted}}})
	if err := os.WriteFile(filepath.Join(dir, "suite.lock"), lock, 0o644); err != nil {
		t.Fatal(err)
	}
	var got schemas.EvaluationSpec
	old := cheatCheckLaunch
	cheatCheckLaunch = func(ctx context.Context, env OrgEnv, spec schemas.EvaluationSpec, who, sched string, prepare func(*schemas.Evaluation)) (*schemas.Evaluation, error) {
		got = spec
		ev := &schemas.Evaluation{ID: "ev_20261127_000001_def456", Org: "", Spec: spec, Status: "running", CreatedAt: time.Now()}
		prepare(ev) // the real launch runs it before the first save
		return ev, evalstore.Open(root).Save(ev)
	}
	defer func() { cheatCheckLaunch = old }()
	code, body := cheatCheckServe(t, root, id, cheatCheckWebBody)
	if code != 202 {
		t.Fatalf("%d %s", code, body)
	}
	if len(got.Benchmarks) != 1 || got.Benchmarks[0] != "s-twins" || got.Trials != 1 || got.Name != "nightly (cheat check)" || got.BudgetUSD != 0.5 {
		t.Fatalf("spec %+v", got)
	}
	child, err := evalstore.Open(root).Load("ev_20261127_000001_def456")
	if err != nil || child.CheatCheckOf != id {
		t.Fatalf("child %+v %v", child, err)
	}
}
