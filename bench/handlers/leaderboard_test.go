package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/leaderboard"
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

// regradeIndex is a fakeIndex with one regrade of testExp, rg_1, which lifts
// the failure of r_t01_A.
type regradeIndex struct {
	*fakeIndex
}

func (regradeIndex) Regrades(_ context.Context, expID string) ([]leaderboard.Regrade, error) {
	if expID != testExp {
		return nil, nil
	}
	return []leaderboard.Regrade{{ID: "rg_1", CreatedAt: time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)}}, nil
}

func (regradeIndex) RegradedRuns(_ context.Context, expID, regradeID string) ([]schemas.SealedRun, error) {
	if expID != testExp || regradeID != "rg_1" {
		return nil, fmt.Errorf("regrade %s: %w", regradeID, schemas.ErrNotFound)
	}
	return []schemas.SealedRun{{
		Run:         schemas.Run{RunID: "r_t01_A", Outcome: schemas.OutcomePass, Y: schemas.Observed(1, schemas.EvidenceOutcome)},
		GradeSource: "rg_1",
		Metrics:     map[string]schemas.Value{"verified_success": schemas.Observed(1, schemas.EvidenceOutcome)},
	}}, nil
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
		{name: "grade source latest", index: regradeIndex{leaderboardIndex()}, query: "?grade_source=latest", status: fasthttp.StatusOK},
		{name: "grade source latest without regrades", index: leaderboardIndex(), query: "?grade_source=latest", status: fasthttp.StatusOK},
		{name: "grade source original", index: leaderboardIndex(), query: "?grade_source=original", status: fasthttp.StatusOK},
		{name: "grade source is not a regrade id", index: regradeIndex{leaderboardIndex()}, query: "?grade_source=rg_1", status: fasthttp.StatusBadRequest},
		{name: "k", index: leaderboardIndex(), query: "?k=2", status: fasthttp.StatusOK},
		{name: "k not an integer", index: leaderboardIndex(), query: "?k=two", status: fasthttp.StatusBadRequest},
		{name: "k of 1", index: leaderboardIndex(), query: "?k=1", status: fasthttp.StatusBadRequest},
		{name: "negative k", index: leaderboardIndex(), query: "?k=-3", status: fasthttp.StatusBadRequest},
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
	for i, x := range objectsAt(t, body, "grade_sources[]") {
		checkKeys(t, fmt.Sprintf("Leaderboard.grade_sources #%d", i), x, loadKeys(t, "LeaderboardGradeSource"), false)
	}
	for _, key := range []string{"suites", "conditions", "common_tasks", "entries", "excluded", "grade_sources"} {
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
	if body["grade_source"] != "original" {
		t.Errorf("grade_source = %v, want original", body["grade_source"])
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

func TestLeaderboardGradeSource(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantSource string
		wantPasses float64 // A's verified successes
		wantRegrd  float64 // runs of testExp read from rg_1, of its 4 on the board (one in flight)
	}{
		{name: "original", query: "", wantSource: "original", wantPasses: 0, wantRegrd: 0},
		{name: "latest", query: "?grade_source=latest", wantSource: "rg_1", wantPasses: 1, wantRegrd: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, body := getBoard(t, regradeIndex{leaderboardIndex()}, tt.query)
			sources := objectsAt(t, body, "grade_sources[]")
			if len(sources) != 1 || sources[0]["experiment"] != testExp || sources[0]["source"] != tt.wantSource ||
				sources[0]["runs"] != float64(4) || sources[0]["regraded_runs"] != tt.wantRegrd {
				t.Errorf("grade_sources = %v", sources)
			}
			for _, e := range objectsAt(t, body, "entries[]") {
				passes, _ := e["passes"].(map[string]any)
				if e["config_hash"] == "sha256:aaa" && passes["num"] != tt.wantPasses {
					t.Errorf("A passes = %v, want %v", passes, tt.wantPasses)
				}
			}
		})
	}
}

func TestLeaderboardSource(t *testing.T) {
	ctx := context.Background()
	plain := LeaderboardSource(leaderboardIndex())
	if _, ok := plain.LatestDecision(testExp); ok {
		t.Error("a ReadIndex without VerdictIndex reported a decision")
	}
	rs := plain.(leaderboard.RegradeSource)
	if list, err := rs.Regrades(ctx, testExp); err != nil || len(list) != 0 {
		t.Errorf("a ReadIndex without RegradeIndex listed regrades %v, %v", list, err)
	}
	if _, err := rs.RegradedRuns(ctx, testExp, "rg_1"); !errors.Is(err, schemas.ErrNotFound) {
		t.Errorf("RegradedRuns err = %v, want ErrNotFound", err)
	}
	withRegrades := LeaderboardSource(regradeIndex{leaderboardIndex()}).(leaderboard.RegradeSource)
	if list, err := withRegrades.Regrades(ctx, testExp); err != nil || len(list) != 1 || list[0].ID != "rg_1" {
		t.Errorf("regrades = %v, %v", list, err)
	}
	if runs, err := withRegrades.RegradedRuns(ctx, testExp, "rg_1"); err != nil || len(runs) != 1 || runs[0].GradeSource != "rg_1" {
		t.Errorf("regraded runs = %v, %v", runs, err)
	}
	if err := plain.(leaderboard.SealSource).VerifySeal(ctx, "r_any"); err != nil {
		t.Errorf("an index without SealVerifier must trust the seal, got %v", err)
	}
	withSeals := LeaderboardSource(sealIndex{leaderboardIndex(), map[string]error{"r_bad": schemas.ErrSealMismatch}}).(leaderboard.SealSource)
	if err := withSeals.VerifySeal(ctx, "r_bad"); !errors.Is(err, schemas.ErrSealMismatch) {
		t.Errorf("VerifySeal err = %v, want ErrSealMismatch", err)
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
