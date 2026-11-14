package handlers

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// verdictIndex is a fakeIndex that also knows each experiment's newest
// verdict decision.
type verdictIndex struct {
	*fakeIndex
	decisions map[string]schemas.Decision
}

func (v verdictIndex) LatestDecision(expID string) (schemas.Decision, bool) {
	d, ok := v.decisions[expID]
	return d, ok
}

// leaderboardIndex is testIndex with config hashes on the arms, a suite
// version, and s_t02_A still in flight.
func leaderboardIndex() *fakeIndex {
	idx := testIndex()
	m := idx.manifests[testExp]
	m.SuiteID, m.SuiteVersion = "coding-core", "1.0.0"
	m.Arms = map[string]schemas.ArmManifest{
		"A": {Label: "baseline", ConfigHash: "sha256:aaa", Config: schemas.AgentConfig{ID: "codex", Adapter: "command", Model: "gpt-5.4-mini"}},
		"B": {Label: "notes", ConfigHash: "sha256:bbb", Config: schemas.AgentConfig{ID: "codex", Adapter: "command", Model: "gpt-5.4-mini"}},
	}
	for i := range idx.runs {
		if idx.runs[i].Slot.SlotID == "s_t02_A" {
			idx.runs[i].Sealed, idx.runs[i].SealHash = false, ""
		}
	}
	return idx
}

// getBoard serves one leaderboard request on a fresh router.
func getBoard(t *testing.T, index ReadIndex, query string) (int, map[string]any) {
	t.Helper()
	h := NewLeaderboardHandler(testMode, index)
	h.now = func() time.Time { return time.Date(2026, 11, 15, 9, 30, 0, 0, time.UTC) }
	r := router.New()
	h.RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(APIPrefix + "/leaderboard" + query)
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

func TestLeaderboardRoute(t *testing.T) {
	broken := leaderboardIndex()
	broken.files["r_t01_A"][FileMetrics] = []byte(`{not json`)
	tests := []struct {
		name   string
		index  ReadIndex
		query  string
		status int
	}{
		{name: "defaults", index: leaderboardIndex(), query: "", status: fasthttp.StatusOK},
		{name: "explicit", index: leaderboardIndex(), query: "?suite=coding-core@1.0.0&condition=clean&scope=common&synthetic=exclude", status: fasthttp.StatusOK},
		{name: "unknown suite is an empty board", index: leaderboardIndex(), query: "?suite=other@2.0.0", status: fasthttp.StatusOK},
		{name: "suite without version", index: leaderboardIndex(), query: "?suite=coding-core", status: fasthttp.StatusBadRequest},
		{name: "bad condition", index: leaderboardIndex(), query: "?condition=a%20b", status: fasthttp.StatusBadRequest},
		{name: "bad scope", index: leaderboardIndex(), query: "?scope=some", status: fasthttp.StatusBadRequest},
		{name: "bad synthetic", index: leaderboardIndex(), query: "?synthetic=include", status: fasthttp.StatusBadRequest},
		{name: "no index", index: nil, query: "", status: fasthttp.StatusServiceUnavailable},
		{name: "unreadable metrics", index: broken, query: "", status: fasthttp.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _ := getBoard(t, tt.index, tt.query)
			if status != tt.status {
				t.Fatalf("status = %d, want %d", status, tt.status)
			}
		})
	}
}

func TestLeaderboardMatchesContract(t *testing.T) {
	status, body := getBoard(t, leaderboardIndex(), "")
	if status != fasthttp.StatusOK {
		t.Fatalf("status = %d: %v", status, body)
	}
	checkKeys(t, "Leaderboard", body, loadKeys(t, "Leaderboard"), true)
	for i, e := range objectsAt(t, body, "entries[]") {
		checkKeys(t, fmt.Sprintf("Leaderboard.entries #%d", i), e, loadKeys(t, "LeaderboardEntry"), false)
	}
	for i, x := range objectsAt(t, body, "excluded[]") {
		checkKeys(t, fmt.Sprintf("Leaderboard.excluded #%d", i), x, loadKeys(t, "LeaderboardExclusion"), false)
	}
	for _, key := range []string{"suites", "conditions", "common_tasks", "entries", "excluded"} {
		if _, ok := body[key].([]any); !ok {
			t.Errorf("%s = %v, want a list", key, body[key])
		}
	}
}

func TestLeaderboardValues(t *testing.T) {
	_, body := getBoard(t, leaderboardIndex(), "")
	if body["suite"] != "coding-core@1.0.0" || body["condition"] != "clean" || body["scope"] != "all" || body["synthetic"] != "exclude" {
		t.Errorf("defaults = %v %v %v %v", body["suite"], body["condition"], body["scope"], body["synthetic"])
	}
	if body["generated_at"] != "2026-11-15T09:30:00Z" {
		t.Errorf("generated_at = %v, want the injected clock", body["generated_at"])
	}
	entries := objectsAt(t, body, "entries[]")
	// B passed t01 and t02; A failed t01 and t02 is in flight.
	if len(entries) != 2 || entries[0]["config_hash"] != "sha256:bbb" || entries[0]["p_vs_leader"] != nil {
		t.Fatalf("entries = %v", entries)
	}
	if entries[1]["tier"] != float64(1) || entries[1]["p_vs_leader"] == nil {
		t.Errorf("second = %v", entries[1])
	}
	excluded := objectsAt(t, body, "excluded[]")
	if len(excluded) != 1 || excluded[0]["reason"] != "not sealed" || excluded[0]["runs"] != float64(1) {
		t.Errorf("excluded = %v", excluded)
	}

	invalid := verdictIndex{fakeIndex: leaderboardIndex(), decisions: map[string]schemas.Decision{testExp: schemas.DecisionInvalid}}
	_, body = getBoard(t, invalid, "")
	if entries, _ := body["entries"].([]any); len(entries) != 0 {
		t.Errorf("entries = %v, want none: the only experiment's verdict is invalid", entries)
	}
	excluded = objectsAt(t, body, "excluded[]")
	if len(excluded) != 1 || excluded[0]["reason"] != "experiment verdict invalid" || excluded[0]["runs"] != float64(4) {
		t.Errorf("excluded = %v", excluded)
	}
}

func TestLeaderboardSource(t *testing.T) {
	plain := LeaderboardSource(leaderboardIndex())
	if _, ok := plain.LatestDecision(testExp); ok {
		t.Error("a ReadIndex without VerdictIndex reported a decision")
	}
	withVerdict := LeaderboardSource(verdictIndex{fakeIndex: leaderboardIndex(), decisions: map[string]schemas.Decision{testExp: schemas.DecisionImproved}})
	if d, ok := withVerdict.LatestDecision(testExp); !ok || d != schemas.DecisionImproved {
		t.Errorf("decision = %q %v", d, ok)
	}
	runs := plain.Runs(testExp)
	if len(runs) != 4 {
		t.Fatalf("runs = %d, want 4", len(runs))
	}
	sealed := 0
	for _, r := range runs {
		if r.Sealed {
			sealed++
		}
	}
	if sealed != 3 {
		t.Errorf("sealed = %d, want 3", sealed)
	}
}
