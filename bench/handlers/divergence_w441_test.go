package handlers

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// newW441Client serves the divergence and behaviour areas over idx and a
// temporary project root.
func newW441Client(t *testing.T, idx ReadIndex) *authoringClient {
	t.Helper()
	root := t.TempDir()
	r := router.New()
	env := OrgEnv{
		Cfg: &lib.Config{Root: root}, Mode: testMode,
		Services: Services{Index: idx},
		Write:    lib.RequireWriteToken(testMode, testToken),
		Common:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)},
	}
	orgAreasMu.Lock()
	areas := []OrgArea{orgAreas["divergence"], orgAreas["behaviour"]}
	orgAreasMu.Unlock()
	for _, a := range areas {
		if a == nil {
			t.Fatal("org area not registered")
		}
		a(env, r)
	}
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &authoringClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}, root: root}
}

func w441Run(exp, pair, task, arm, id string, out schemas.Outcome) RunView {
	return RunView{Slot: schemas.Slot{ExpID: exp, PairID: pair, Task: task, Arm: arm}, Run: schemas.Run{RunID: id, ExpID: exp, Outcome: out}, Sealed: true}
}

func w441Steps(rows ...string) []byte { return []byte(strings.Join(rows, "\n") + "\n") }

func w441Index() *fakeIndex {
	return &fakeIndex{
		manifests: map[string]*schemas.Manifest{"exp1": {}},
		runs: []RunView{
			w441Run("exp1", "p1", "t1", "base", "r1", schemas.OutcomePass),
			w441Run("exp1", "p1", "t1", "cand", "r2", schemas.OutcomeFail),
			w441Run("exp1", "p2", "t2", "base", "r3", schemas.OutcomePass),
			w441Run("exp1", "p3", "t1", "base", "old", schemas.OutcomeFail),
		},
		files: map[string]map[string][]byte{
			"r1": {
				"steps.jsonl": w441Steps(`{"seq":1,"kind":"model_turn","request_id":"a1"}`, `{"seq":2,"kind":"command","summary":"pytest -q","proof":"verified","exit_code":0}`, `{"seq":3,"kind":"model_turn","request_id":"a2","summary":"All tests pass"}`),
				"cost.json":   []byte(`[{"request_id":"a1","usd":{"state":"observed","v":0.1}},{"request_id":"a2","usd":{"state":"observed","v":0.2}}]`),
			},
			"r2": {
				"steps.jsonl": w441Steps(`{"seq":1,"kind":"model_turn","request_id":"b1"}`, `{"seq":2,"kind":"file_change","path":"tests/test_x.py","op":"write","proof":"verified"}`, `{"seq":3,"kind":"model_turn","request_id":"b2","summary":"Fixed, done."}`),
				"cost.json":   []byte(`[{"request_id":"b1","usd":{"state":"observed","v":0.1}},{"request_id":"b2","usd":{"state":"observed","v":0.4}}]`),
			},
			"r3": {"steps.jsonl": w441Steps(`{"seq":1,"kind":"command","summary":"ls"}`)},
		},
	}
}

func TestPairDivergenceRoute(t *testing.T) {
	c := newW441Client(t, w441Index())
	code, body := c.do(t, "GET", "/pairs/p1/divergence", nil, nil)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var out PairDivergence
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	d := out.Divergence
	if !out.Available || d == nil || !d.Found || d.Category != "test_run" || d.SeqA != 2 || d.SeqB != 2 {
		t.Fatalf("got %s", body)
	}
	if out.Outcomes != [2]string{"pass", "fail"} || out.Arms != [2]string{"base", "cand"} {
		t.Fatalf("outcomes %v arms %v", out.Outcomes, out.Arms)
	}
	if d.After[0].CostUSD == nil || *d.After[0].CostUSD != 0.2 || d.After[1].CostUSD == nil || *d.After[1].CostUSD != 0.4 {
		t.Fatalf("cost after %s", body)
	}
	code, body = c.do(t, "GET", "/runs/r1/divergence/r2", nil, nil)
	if code != 200 || !strings.Contains(string(body), `"pair_id":"p1"`) {
		t.Fatalf("runs route: %d %s", code, body)
	}
}

func TestDivergenceDifferentTasks409(t *testing.T) {
	c := newW441Client(t, w441Index())
	code, body := c.do(t, "GET", "/runs/r1/divergence/r3", nil, nil)
	if code != 409 || !strings.Contains(string(body), "These runs are of different tasks.") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestDivergenceOldRunReason(t *testing.T) {
	c := newW441Client(t, w441Index())
	code, body := c.do(t, "GET", "/runs/r1/divergence/old", nil, nil)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var out PairDivergence
	_ = json.Unmarshal(body, &out)
	if out.Available || out.Divergence != nil || out.Reason != "Run old was recorded before step timelines existed." {
		t.Fatalf("got %s", body)
	}
}
