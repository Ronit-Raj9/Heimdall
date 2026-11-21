package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/valyala/fasthttp"
)

// agentBoardIndex is leaderboardIndex on a real (non-practice) benchmark,
// with the two codex arms differing in their argv (two agent versions) and
// set to different models, as evaluation arms are.
func agentBoardIndex() *fakeIndex {
	idx := leaderboardIndex()
	m := idx.manifests[testExp]
	m.SuiteID = "hd-swe-verified"
	a, b := m.Arms["A"], m.Arms["B"]
	a.Config.Argv = []string{"codex", "exec"}
	b.Config.Argv = []string{"codex", "exec", "--notes"}
	b.Config.Gateway.Provider = "openai"
	m.Arms["A"], m.Arms["B"] = a, b
	for i := range idx.runs {
		idx.runs[i].Slot.Suite = "hd-swe-verified"
	}
	return idx
}

// agentHashOf is the agent hash of one arm of idx.
func agentHashOf(t *testing.T, idx *fakeIndex, arm string) string {
	t.Helper()
	h, err := agentversions.AgentHash(idx.manifests[testExp].Arms[arm].Config)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// getAgentBoard serves one GET /leaderboard/agents request.
func getAgentBoard(t *testing.T, index ReadIndex, root, query string) (int, AgentLeaderboard) {
	t.Helper()
	return getAgentBoardWith(t, NewLeaderboardHandler(testMode, index).WithAgentBoard(root, nil), query)
}

func getAgentBoardWith(t *testing.T, h *LeaderboardHandler, query string) (int, AgentLeaderboard) {
	t.Helper()
	h.now = func() time.Time { return time.Date(2026, 11, 15, 9, 30, 0, 0, time.UTC) }
	r := router.New()
	h.RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(APIPrefix + "/leaderboard/agents" + query)
	r.Handler(&ctx)
	var body AgentLeaderboard
	if ctx.Response.StatusCode() == fasthttp.StatusOK {
		if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
			t.Fatalf("decode %q: %v", ctx.Response.Body(), err)
		}
	}
	return ctx.Response.StatusCode(), body
}

// TestAgentLeaderboardRows (N6): rows map to agent versions by agent hash,
// whatever model and provider the arm set; below the minimum sample a score
// is shown but not ranked (N7).
func TestAgentLeaderboardRows(t *testing.T) {
	idx := agentBoardIndex()
	root := t.TempDir()
	dir := filepath.Join(root, ".heimdall", "agent-versions", "codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	index := fmt.Sprintf(`[{"version":1,"hash":"x","agent_hash":%q},{"version":2,"hash":"y","agent_hash":%q}]`, agentHashOf(t, idx, "A"), agentHashOf(t, idx, "B"))
	if err := os.WriteFile(filepath.Join(dir, "versions.json"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	status, b := getAgentBoard(t, idx, root, "?capability=coding")
	if status != fasthttp.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if len(b.Rows) != 2 || b.MinRuns != AgentBoardMinRuns || b.MinTasks != AgentBoardMinTasks {
		t.Fatalf("board = %+v, want 2 rows (two versions of codex)", b)
	}
	top := b.Rows[0]
	if top.Agent != "codex" || top.Version != 2 || top.Model != "gpt-5.4-mini" || !top.Baseline || top.Rank != 0 {
		t.Errorf("top = %+v, want codex v2 baseline, unranked", top)
	}
	c, ok := top.Capabilities["coding"]
	if !ok || c.Score != 1 || c.Runs != 2 || c.Tasks != 2 || c.Ranked || c.Note == "" || c.CILow <= 0 || c.CIHigh != 1 || len(c.Benchmarks) != 1 || c.Benchmarks[0] != "hd-swe-verified" {
		t.Errorf("coding = %+v", c)
	}
	if b.Rows[1].Version != 1 || b.Rows[1].Capabilities["coding"].Score != 0 {
		t.Errorf("second = %+v", b.Rows[1])
	}
	if len(b.Capabilities) != 1 || b.Capabilities[0] != "coding" || len(b.Models) != 1 {
		t.Errorf("capabilities = %v models = %v", b.Capabilities, b.Models)
	}
	if top.LastExperiment != testExp {
		t.Errorf("last_experiment = %q", top.LastExperiment)
	}
	// No cost evidence: not known, never $0.
	if top.CostUSD != nil || top.CostRuns != 0 || top.CostNote == "" {
		t.Errorf("cost = %v runs %d note %q, want not known", top.CostUSD, top.CostRuns, top.CostNote)
	}
}

func TestAgentLeaderboardNoVersions(t *testing.T) {
	_, b := getAgentBoard(t, agentBoardIndex(), t.TempDir(), "?capability=coding")
	// Without an index each agent hash is its own row, version 0.
	if len(b.Rows) != 2 || b.Rows[0].Version != 0 || b.Rows[0].ConfigHash != "sha256:bbb" || b.Rows[0].AgentHash == "" {
		t.Errorf("rows = %+v", b.Rows)
	}
}

// TestAgentLeaderboardOnlyRealRuns: practice benchmarks (Acme examples,
// coding-core) never reach the agent board.
func TestAgentLeaderboardOnlyRealRuns(t *testing.T) {
	if _, b := getAgentBoard(t, leaderboardIndex(), "", ""); len(b.Rows) != 0 {
		t.Errorf("coding-core rows = %+v, want none", b.Rows)
	}
	for id, want := range map[string]bool{"coding-core": true, "acme-helpdesk@1.0.0": true, "hd-swe-verified": false, "acmecorp-support": false} {
		if PracticeBenchmark(id) != want {
			t.Errorf("PracticeBenchmark(%s) = %v", id, !want)
		}
	}
}

// TestAgentLeaderboardKeyPrice (N2): a run of a model the snapshot does not
// price is costed from the price on the org's key; without that price the
// row's cost is not known.
func TestAgentLeaderboardKeyPrice(t *testing.T) {
	idx := agentBoardIndex()
	for _, run := range []string{"r_t01_A", "r_t01_B", "r_t02_B"} {
		idx.files[run][FileMetrics] = []byte(`{"cost_usd":{"state":"unknown","reason":"1 of 1 cost rows unpriced","evidence":"gateway"}}`)
		idx.files[run]["cost.json"] = []byte(`[{"request_id":"q1","attempt":1,"model":"gpt-5.4-mini","tokens":{"fresh":1000000,"cache_read":0,"cache_write":0,"out":1000000},"usd":{"state":"unknown","reason":"no price for model \"gpt-5.4-mini\" in snapshot s1","evidence":"gateway"}}]`)
	}
	keys := keystore.NewMemory()
	h := NewLeaderboardHandler(testMode, idx).WithAgentBoard("", nil)
	_, b := getAgentBoardWith(t, h, "")
	if len(b.Rows) != 2 || b.Rows[0].CostUSD != nil || b.Rows[0].CostNote == "" {
		t.Fatalf("unpriced rows = %+v", b.Rows)
	}
	if _, err := keys.Create(context.Background(), "org_a", keystore.KindProvider, keystore.Put{Provider: "xcodebest", BaseURL: "https://x.example/v1", Value: "sk-1234567890",
		Models: []string{"gpt-5.4-mini"}, Price: &keystore.Price{InputPerMTok: 1, OutputPerMTok: 3}}, "u"); err != nil {
		t.Fatal(err)
	}
	h = NewLeaderboardHandler(testMode, idx).WithAgentBoard("", nil).WithKeyPrices(keys, "org_a")
	_, b = getAgentBoardWith(t, h, "")
	for _, r := range b.Rows {
		if r.CostUSD == nil || math.Abs(*r.CostUSD-4) > 1e-9 || r.CostRuns != r.Runs || r.CostNote != "" {
			t.Errorf("priced row = %+v cost %v, want 4 USD per run", r, r.CostUSD)
		}
	}
}

func TestAgentLeaderboardFilters(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		status int
		rows   int
	}{
		{"capability", "?capability=coding", fasthttp.StatusOK, 2},
		{"capability without rows", "?capability=voice", fasthttp.StatusOK, 0},
		{"unknown capability", "?capability=juggling", fasthttp.StatusBadRequest, 0},
		{"model", "?model=gpt-5.4-mini", fasthttp.StatusOK, 2},
		{"other model", "?model=claude-x", fasthttp.StatusOK, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, b := getAgentBoard(t, agentBoardIndex(), "", tt.query)
			if status != tt.status || len(b.Rows) != tt.rows {
				t.Errorf("status = %d rows = %d, want %d %d", status, len(b.Rows), tt.status, tt.rows)
			}
			if status == fasthttp.StatusOK && len(b.Models) != 1 {
				t.Errorf("models = %v, want the filter list unaffected by the filter", b.Models)
			}
		})
	}
	if status, _ := getAgentBoard(t, nil, "", ""); status != fasthttp.StatusServiceUnavailable {
		t.Errorf("no index status = %d", status)
	}
}

// TestAgentLeaderboardNeverBlendsCapabilities: rows rank only by the
// selected capability. A row strong on coding but weak on memory must not
// lose to a row with a higher mean over whatever capabilities it has; with
// no capability selected there is no score order at all.
func TestAgentLeaderboardNeverBlendsCapabilities(t *testing.T) {
	mk := func(agent string, runs int, caps map[string][2]int) *rowAcc {
		acc := &rowAcc{row: AgentLeaderboardRow{Agent: agent, Model: "m", ConfigHash: agent, Runs: runs}, caps: map[string]*capAcc{}}
		for c, sn := range caps {
			ca := &capAcc{succ: sn[0], n: sn[1], benchmarks: []string{c + "-bench"}}
			// sn[1] runs over sn[1] tasks, the first sn[0] passed: ranked.
			for i := 0; i < sn[1]; i++ {
				ca.tasks = append(ca.tasks, reportcard.TaskTally{Key: fmt.Sprintf("t%02d", i), Succ: b2i(i < sn[0]), Runs: 1})
			}
			acc.caps[c] = ca
		}
		return acc
	}
	rows := func() map[string]*rowAcc {
		return map[string]*rowAcc{
			// coding 0.9, memory 0.1: mean 0.5
			"zeta": mk("zeta", 20, map[string][2]int{"coding": {9, 10}, "memory": {1, 10}}),
			// coding 0.6 only: mean 0.6
			"alpha": mk("alpha", 10, map[string][2]int{"coding": {6, 10}}),
			// memory 0.5 only
			"mid": mk("mid", 30, map[string][2]int{"memory": {5, 10}}),
		}
	}
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	order := func(b *AgentLeaderboard) []string {
		var out []string
		for _, r := range b.Rows {
			out = append(out, r.Agent)
		}
		return out
	}
	eq := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	if got := order(foldRows(rows(), "coding", "", now)); !eq(got, []string{"zeta", "alpha"}) {
		t.Errorf("by coding = %v, want [zeta alpha] (coding 0.9 over 0.6, memory ignored)", got)
	}
	if got := order(foldRows(rows(), "memory", "", now)); !eq(got, []string{"mid", "zeta"}) {
		t.Errorf("by memory = %v, want [mid zeta]", got)
	}
	// No capability: listed by agent name, not by a blended mean (which
	// would put alpha 0.6 > zeta 0.5 > mid 0.5 or similar).
	if got := order(foldRows(rows(), "", "", now)); !eq(got, []string{"alpha", "mid", "zeta"}) {
		t.Errorf("no capability = %v, want by agent [alpha mid zeta]", got)
	}
	// Scores themselves stay per capability.
	b := foldRows(rows(), "", "", now)
	for _, r := range b.Rows {
		if r.Agent == "zeta" && (math.Abs(r.Capabilities["coding"].Score-0.9) > 1e-9 || math.Abs(r.Capabilities["memory"].Score-0.1) > 1e-9) {
			t.Errorf("zeta capabilities = %+v", r.Capabilities)
		}
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestAgentLeaderboardMinimumSample (N7): a row under 10 runs or 5 tasks
// on the filtered capability is never ranked above, or numbered among, the
// rows with enough runs, however high its score.
func TestAgentLeaderboardMinimumSample(t *testing.T) {
	mk := func(agent string, succ, runs, tasks int) *rowAcc {
		ca := &capAcc{succ: succ, n: runs, benchmarks: []string{"b"}}
		for i := 0; i < tasks; i++ {
			per := runs / tasks
			s := 0
			if i*per < succ {
				s = min(per, succ-i*per)
			}
			ca.tasks = append(ca.tasks, reportcard.TaskTally{Key: fmt.Sprintf("t%d", i), Succ: s, Runs: per})
		}
		return &rowAcc{row: AgentLeaderboardRow{Agent: agent, Model: "m", ConfigHash: agent, Runs: runs}, caps: map[string]*capAcc{"coding": ca}}
	}
	rows := map[string]*rowAcc{
		"lucky":  mk("lucky", 1, 1, 1),    // 1 of 1: perfect but one run
		"narrow": mk("narrow", 12, 12, 2), // 12 runs, 2 tasks
		"solid":  mk("solid", 14, 20, 10), // 0.7 over 20 runs, 10 tasks
		"steady": mk("steady", 10, 20, 5), // 0.5 over 20 runs, 5 tasks
	}
	b := foldRows(rows, "coding", "", time.Now())
	var got []string
	for _, r := range b.Rows {
		got = append(got, fmt.Sprintf("%s:%d", r.Agent, r.Rank))
	}
	want := []string{"solid:1", "steady:2", "narrow:0", "lucky:0"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for _, r := range b.Rows {
		c := r.Capabilities["coding"]
		if c.Ranked != (r.Rank > 0) || (c.Ranked == (c.Note != "")) {
			t.Errorf("%s: ranked %v note %q", r.Agent, c.Ranked, c.Note)
		}
		if !(c.CILow <= c.Score && c.Score <= c.CIHigh) {
			t.Errorf("%s: interval [%v, %v] misses %v", r.Agent, c.CILow, c.CIHigh, c.Score)
		}
	}
}
