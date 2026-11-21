package handlers

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// runListClient mounts only the run list over index and suites.
func runListClient(t *testing.T, index ReadIndex, suites SuiteSource) *testClient {
	t.Helper()
	r := router.New()
	NewRunListHandler(testMode, index, suites).RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

// runListIndex is testIndex with start times (t02 runs newer) and costs on the B arm.
func runListIndex() *fakeIndex {
	idx := testIndex()
	base := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	for i := range idx.runs {
		rv := &idx.runs[i]
		rv.Run.Timing.Started = base.Add(time.Duration(i) * time.Minute)
		rv.Run.Agent.ConfigID = "cfg-" + rv.Slot.Arm
		if rv.Slot.Arm == "B" {
			idx.files[rv.Run.RunID][FileMetrics] = []byte(`{"cost_usd":{"state":"observed","v":0.25,"evidence":"gateway"}}`)
		}
	}
	return idx
}

func runListSuites() *fakeSuites {
	return &fakeSuites{ids: []string{"coding-core"}, suites: map[string]*schemas.Suite{
		"coding-core": {ID: "coding-core", Track: schemas.TrackQA, Grading: "judged", Output: &schemas.OutputSpec{Kind: "answer"},
			Tasks: []*schemas.Task{{ID: "t02", Output: &schemas.OutputSpec{Kind: "state"}}}},
	}}
}

func getRunList(t *testing.T, c *testClient, query string) RunList {
	t.Helper()
	resp := c.do(t, "GET", APIPrefix+"/runs"+query, nil, nil)
	if resp.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("GET /runs%s = %d: %s", query, resp.StatusCode(), resp.Body())
	}
	var out RunList
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestRunListOrderFacetsAndSuiteFacts(t *testing.T) {
	c := runListClient(t, runListIndex(), runListSuites())
	out := getRunList(t, c, "")
	if out.Total != 4 || len(out.Runs) != 4 || out.Limit != runListDefaultLimit || out.Offset != 0 {
		t.Fatalf("total %d, rows %d, limit %d, offset %d", out.Total, len(out.Runs), out.Limit, out.Offset)
	}
	var ids []string
	for _, row := range out.Runs {
		ids = append(ids, row.RunID)
	}
	if want := []string{"r_t02_B", "r_t02_A", "r_t01_B", "r_t01_A"}; !slices.Equal(ids, want) {
		t.Errorf("order = %v, want newest first %v", ids, want)
	}
	byID := map[string]RunListRow{}
	for _, row := range out.Runs {
		byID[row.RunID] = row
	}
	if r := byID["r_t01_A"]; r.Track != schemas.TrackQA || r.Output != "answer" || r.Grading != "judged" || r.ExpID != testExp || r.PairID != "p_t01" || !r.Sealed || r.ConfigID != "cfg-A" {
		t.Errorf("r_t01_A = %+v, want the suite's track and output", r)
	}
	if r := byID["r_t02_A"]; r.Output != "state" {
		t.Errorf("r_t02_A output = %q, want the task's own state", r.Output)
	}
	// metrics.json without cost_usd reads as an unknown, never as zero.
	if c := byID["r_t01_A"].CostUSD; c == nil || c.State != schemas.ValueUnknown || c.Reason != "cost_not_measured" {
		t.Errorf("r_t01_A cost = %+v, want unknown cost_not_measured", c)
	}
	if c := byID["r_t01_B"].CostUSD; c == nil || c.V == nil || *c.V != 0.25 {
		t.Errorf("r_t01_B cost = %+v, want 0.25", c)
	}
	f := out.Facets
	if !slices.Equal(f.Arms, []string{"A", "B"}) || !slices.Equal(f.Tasks, []string{"t01", "t02"}) ||
		!slices.Equal(f.Outcomes, []string{"fail", "pass"}) || !slices.Equal(f.Tracks, []string{schemas.TrackQA}) ||
		!slices.Equal(f.Experiments, []string{testExp}) || !slices.Equal(f.Conditions, []string{"clean"}) {
		t.Errorf("facets = %+v", f)
	}
}

func TestRunListFiltersAndPaging(t *testing.T) {
	c := runListClient(t, runListIndex(), runListSuites())
	cases := []struct {
		query string
		want  []string
	}{
		{"?arm=A", []string{"r_t02_A", "r_t01_A"}},
		{"?outcome=fail", []string{"r_t01_A"}},
		{"?task=t01&arm=B", []string{"r_t01_B"}},
		{"?exp=e_other", nil},
		{"?track=qa&condition=clean&limit=1&offset=1", []string{"r_t02_A"}},
		{"?track=coding", nil},
		{"?agent=cfg-B", []string{"r_t02_B", "r_t01_B"}},
		{"?agent=cfg-B&outcome=fail", nil},
		{"?agent=nobody", nil},
	}
	for _, tc := range cases {
		out := getRunList(t, c, tc.query)
		var ids []string
		for _, row := range out.Runs {
			ids = append(ids, row.RunID)
		}
		if !slices.Equal(ids, tc.want) {
			t.Errorf("%s = %v, want %v", tc.query, ids, tc.want)
		}
	}
	if out := getRunList(t, c, "?track=qa&limit=1&offset=1"); out.Total != 4 || out.Limit != 1 || out.Offset != 1 {
		t.Errorf("paged total %d limit %d offset %d, want total of every match", out.Total, out.Limit, out.Offset)
	}
	if out := getRunList(t, c, "?limit=100000"); out.Limit != runListMaxLimit {
		t.Errorf("limit = %d, want the cap %d", out.Limit, runListMaxLimit)
	}
	// Facets describe every run, not the filtered page, so the menus keep their options.
	if out := getRunList(t, c, "?arm=A"); !slices.Equal(out.Facets.Arms, []string{"A", "B"}) {
		t.Errorf("filtered facets = %v", out.Facets.Arms)
	}
}

func TestRunListRefusals(t *testing.T) {
	c := runListClient(t, runListIndex(), nil)
	for _, query := range []string{"?arm=..", "?limit=-1", "?offset=x"} {
		if resp := c.do(t, "GET", APIPrefix+"/runs"+query, nil, nil); resp.StatusCode() != fasthttp.StatusBadRequest {
			t.Errorf("%s = %d, want 400", query, resp.StatusCode())
		}
	}
	// Without a suite source the rows carry no track or output, and a track filter matches none.
	if out := getRunList(t, c, ""); out.Runs[0].Track != "" || out.Runs[0].Output != "" {
		t.Errorf("row without suites = %+v", out.Runs[0])
	}
	if resp := runListClient(t, nil, nil).do(t, "GET", APIPrefix+"/runs", nil, nil); resp.StatusCode() != fasthttp.StatusServiceUnavailable {
		t.Errorf("nil index = %d, want 503", resp.StatusCode())
	}
}

func TestExperimentListCarriesSuiteArmsAndDecision(t *testing.T) {
	d := fullDeps()
	d.index = verdictIndex{fakeIndex: testIndex(), decisions: map[string]schemas.Decision{testExp: schemas.DecisionImproved}}
	c := newTestClient(t, d)
	var out schemas.ExperimentList
	if err := json.Unmarshal(c.do(t, "GET", APIPrefix+"/experiments", nil, nil).Body(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Experiments) != 1 {
		t.Fatalf("experiments = %+v", out.Experiments)
	}
	st := out.Experiments[0]
	if st.Suite != "coding-core@1.0.0" || !slices.Equal(st.Arms, []string{"A", "B"}) || st.Decision != schemas.DecisionImproved {
		t.Errorf("status = suite %q arms %v decision %q", st.Suite, st.Arms, st.Decision)
	}
}

// TestRunListMetricsModelVersionAndFault: the row carries steps, tool calls,
// tokens, model, agent version and fault from existing run data, and the
// agent, benchmark and fault filters run on the server before paging.
func TestRunListMetricsModelVersionAndFault(t *testing.T) {
	idx := runListIndex()
	root := t.TempDir()
	m := idx.manifests[testExp]
	m.Arms = map[string]schemas.ArmManifest{
		"A": {Config: schemas.AgentConfig{ID: "cfg-A", Model: "gpt-x", AgentVersion: "1.2.0"}, ConfigHash: "sha256:aaa"},
		"B": {Config: schemas.AgentConfig{ID: "cfg-B", Model: "claude-y"}, ConfigHash: "sha256:bbb"},
	}
	if err := os.MkdirAll(filepath.Join(root, ".heimdall", "agent-versions", "cfg-B"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Versions match by agent hash (the config without model and provider).
	ah, err := agentversions.AgentHash(m.Arms["B"].Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".heimdall", "agent-versions", "cfg-B", "versions.json"), []byte(`[{"version":3,"hash":"bbb","agent_hash":"`+strings.TrimPrefix(ah, "sha256:")+`"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range idx.runs {
		rv := &idx.runs[i]
		if rv.Slot.Arm == "B" {
			idx.files[rv.Run.RunID][FileMetrics] = []byte(`{"cost_usd":{"state":"observed","v":0.25},"steps":{"state":"observed","v":12},"tool_calls":{"state":"observed","v":7},"tokens":{"state":"observed","v":3400}}`)
		} else {
			idx.files[rv.Run.RunID][FileMetrics] = []byte(`{"steps":{"state":"unknown","v":null,"reason":"no trace"}}`)
		}
		if rv.Slot.Task == "t02" {
			rv.Slot.Condition = "model-outage"
		}
	}
	suites := runListSuites()
	suites.suites["coding-core"].Probes = []*schemas.ProbeSpec{{ID: "model-outage", PS2Family: schemas.FamilyUnavailableModel}}

	r := router.New()
	NewRunListHandler(testMode, idx, suites).WithRoot(root).RegisterRoutes(r)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	c := &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}

	out := getRunList(t, c, "")
	byID := map[string]RunListRow{}
	for _, row := range out.Runs {
		byID[row.RunID] = row
	}
	b := byID["r_t02_B"]
	if b.Steps == nil || *b.Steps != 12 || b.ToolCalls == nil || *b.ToolCalls != 7 || b.Tokens == nil || *b.Tokens != 3400 {
		t.Errorf("r_t02_B metrics = steps %v tool calls %v tokens %v", b.Steps, b.ToolCalls, b.Tokens)
	}
	if b.Model != "claude-y" || b.Version != 3 || b.Fault != "model-down" {
		t.Errorf("r_t02_B = model %q version %d fault %q", b.Model, b.Version, b.Fault)
	}
	a := byID["r_t01_A"]
	if a.Steps != nil || a.Tokens != nil || a.Model != "gpt-x" || a.AgentVer != "1.2.0" || a.Version != 0 || a.Fault != "" {
		t.Errorf("r_t01_A = %+v, want no unobserved metric, its model and agent version, no fault", a)
	}
	if f := out.Facets; !slices.Equal(f.Agents, []string{"cfg-A", "cfg-B"}) || !slices.Equal(f.Faults, []string{"clean", "model-down"}) || !slices.Equal(f.Models, []string{"claude-y", "gpt-x"}) {
		t.Errorf("facets = %+v", f)
	}
	cases := []struct {
		query string
		want  []string
	}{
		{"?agent=cfg-B", []string{"r_t02_B", "r_t01_B"}},
		{"?agent=cfg-B&limit=1&offset=1", []string{"r_t01_B"}},
		{"?fault=model-down", []string{"r_t02_B", "r_t02_A"}},
		{"?fault=clean&agent=cfg-A", []string{"r_t01_A"}},
		{"?benchmark=coding-core&agent=cfg-A&fault=model-down", []string{"r_t02_A"}},
		{"?benchmark=other", nil},
		{"?model=claude-y&fault=clean", []string{"r_t01_B"}},
	}
	for _, tc := range cases {
		got := getRunList(t, c, tc.query)
		var ids []string
		for _, row := range got.Runs {
			ids = append(ids, row.RunID)
		}
		if !slices.Equal(ids, tc.want) {
			t.Errorf("%s = %v, want %v", tc.query, ids, tc.want)
		}
	}
	if got := getRunList(t, c, "?agent=cfg-B&limit=1"); got.Total != 2 {
		t.Errorf("agent filter total = %d, want 2 across pages", got.Total)
	}
}
