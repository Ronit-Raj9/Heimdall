package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// failureIndex is triageIndex with both arms running agent codex: arm A on
// an unsaved config, arm B on saved version 2.
func failureIndex() *fakeIndex {
	idx := triageIndex()
	m := idx.manifests[testExp]
	a, b := m.Arms["A"], m.Arms["B"]
	a.Config, a.ConfigHash = schemas.AgentConfig{ID: "codex", Model: "gpt-x"}, "sha256:aaaaaaaaaaaaaaaaaaaa"
	b.Config, b.ConfigHash = failureConfigB, "sha256:bbbbbbbbbbbbbbbbbbbb"
	m.Arms["A"], m.Arms["B"] = a, b
	return idx
}

// failureConfigB is arm B's agent config: it differs from arm A's beyond
// the model, so the two have different agent hashes.
var failureConfigB = schemas.AgentConfig{ID: "codex", Model: "gpt-x", AgentVersion: "2.0.0"}

// failureRoot is a project root whose codex versions index names arm B's
// agent hash as v2 (versions match by agent hash).
func failureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".heimdall", "agent-versions", "codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "versions.json"), []byte(`[{"version":2,"hash":"sha256:bbbbbbbbbbbbbbbbbbbb","agent_hash":"`+armAgentHash(failureConfigB)+`"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func patternOf(t *testing.T, g FailurePatternGroup, name string) FailurePatternCount {
	t.Helper()
	for _, p := range g.Patterns {
		if p.Pattern == name {
			return p
		}
	}
	t.Fatalf("group %s has no pattern %s: %+v", g.Key, name, g.Patterns)
	return FailurePatternCount{}
}

func exampleIDs(p FailurePatternCount) []string {
	var ids []string
	for _, e := range p.Examples {
		ids = append(ids, e.RunID)
	}
	return ids
}

func TestBuildAgentFailurePatterns(t *testing.T) {
	out := BuildAgentFailurePatterns(failureIndex(), "codex", newVersionIndex(failureRoot(t), ""), nil, withPractice)
	if out.Agent != "codex" || len(out.Groups) != 2 {
		t.Fatalf("groups = %+v", out.Groups)
	}
	byKey := map[string]FailurePatternGroup{}
	for _, g := range out.Groups {
		byKey[g.Key] = g
	}
	v2, unsaved := byKey["v2"], byKey["config:aaaaaaaaaaaa"]
	if v2.Version != 2 || v2.Runs != 2 || v2.Failed != 0 || !slices.Equal(v2.Models, []string{"gpt-x"}) || !slices.Equal(v2.Experiments, []string{testExp}) {
		t.Fatalf("v2 = %+v", v2)
	}
	if unsaved.Version != 0 || unsaved.Runs != 2 || unsaved.Failed != 1 || unsaved.Label != "unsaved config aaaaaaaaaaaa" {
		t.Fatalf("unsaved = %+v", unsaved)
	}
	loop := patternOf(t, v2, "pattern_loop")
	if loop.Count != 1 || loop.Observed != 2 || loop.Unknown != 0 || !slices.Equal(exampleIDs(loop), []string{"r_t02_B"}) || loop.Label != "Loop" {
		t.Fatalf("v2 loop = %+v", loop)
	}
	if ex := loop.Examples[0]; ex.ExpID != testExp || ex.Benchmark != "coding-core" || ex.Task != "t02" || ex.Outcome != string(schemas.OutcomePass) {
		t.Fatalf("example = %+v", ex)
	}
	if fin := patternOf(t, v2, "pattern_unverified_finish"); fin.Count != 2 || !slices.Equal(exampleIDs(fin), []string{"r_t01_B", "r_t02_B"}) {
		t.Fatalf("v2 unverified finish = %+v", fin)
	}
	if loopA := patternOf(t, unsaved, "pattern_loop"); loopA.Count != 1 || loopA.Observed != 1 || loopA.Unknown != 1 {
		t.Fatalf("unsaved loop = %+v", loopA)
	}

	if other := BuildAgentFailurePatterns(failureIndex(), "claude-code", newVersionIndex("", ""), nil, withPractice); len(other.Groups) != 0 {
		t.Fatalf("an agent that never ran has no groups: %+v", other.Groups)
	}
	// Without a versions index every config is unsaved.
	if plain := BuildAgentFailurePatterns(failureIndex(), "codex", newVersionIndex("", ""), nil, withPractice); len(plain.Groups) != 2 || plain.Groups[0].Version != 0 {
		t.Fatalf("no index: %+v", plain.Groups)
	}
	// A run folder with unreadable metrics skips its experiment, never fails the read.
	broken := failureIndex()
	broken.files["r_t01_A"][FileMetrics] = []byte(`{not json`)
	if out := BuildAgentFailurePatterns(broken, "codex", newVersionIndex("", ""), nil, withPractice); len(out.Groups) != 0 {
		t.Fatalf("broken experiment: %+v", out.Groups)
	}
}

// withPractice reads every run, practice benchmarks included (the test
// index runs coding-core).
var withPractice = AgentFailureFilter{IncludePractice: true}

// TestFailurePatternFilters (J13.filters): practice benchmarks are left out
// by default; version and benchmark narrow the groups while the filter
// choices stay whole; an evaluation's recorded version names the group.
func TestFailurePatternFilters(t *testing.T) {
	versions := func() *versionIndex { return newVersionIndex(failureRoot(t), "") }
	if out := BuildAgentFailurePatterns(failureIndex(), "codex", versions(), nil, AgentFailureFilter{}); len(out.Groups) != 0 || len(out.Benchmarks) != 0 {
		t.Fatalf("practice runs read by default: %+v", out)
	}
	all := BuildAgentFailurePatterns(failureIndex(), "codex", versions(), nil, withPractice)
	if len(all.Groups) != 2 || !slices.Equal(all.Versions, []int{2}) || !slices.Equal(all.Benchmarks, []string{"coding-core"}) {
		t.Fatalf("with practice: groups %d versions %v benchmarks %v", len(all.Groups), all.Versions, all.Benchmarks)
	}
	v2 := BuildAgentFailurePatterns(failureIndex(), "codex", versions(), nil, AgentFailureFilter{IncludePractice: true, Version: 2})
	if len(v2.Groups) != 1 || v2.Groups[0].Key != "v2" || !slices.Equal(v2.Versions, []int{2}) {
		t.Fatalf("version 2: %+v", v2.Groups)
	}
	other := BuildAgentFailurePatterns(failureIndex(), "codex", versions(), nil, AgentFailureFilter{IncludePractice: true, Benchmark: "hd-swe-verified"})
	if len(other.Groups) != 0 || !slices.Equal(other.Benchmarks, []string{"coding-core"}) {
		t.Fatalf("other benchmark: %+v", other)
	}

	// A real benchmark is read by default; the arm versions come from the
	// evaluation that created the experiment.
	idx := failureIndex()
	m := idx.manifests[testExp]
	m.SuiteID = "hd-swe-verified"
	m.Experiment.EvaluationID = "ev_score"
	root := failureRoot(t)
	writeTestEvaluation(t, root, schemas.Evaluation{ID: "ev_score", Spec: schemas.EvaluationSpec{Mode: "score",
		Arms: []schemas.EvaluationArm{{Label: "codex", Agent: "codex", Version: 5, Model: "gpt-x"}}}})
	out := BuildAgentFailurePatterns(idx, "codex", newVersionIndex(root, ""), nil, AgentFailureFilter{})
	if len(out.Groups) != 1 || out.Groups[0].Key != "v5" || out.Groups[0].Label != "v5" || out.Groups[0].Runs != 4 || !slices.Equal(out.Benchmarks, []string{"hd-swe-verified"}) {
		t.Fatalf("evaluation version: %+v", out)
	}
	for _, p := range out.Groups[0].Patterns {
		for _, ex := range p.Examples {
			if ex.Benchmark == "" {
				t.Fatalf("example without benchmark: %+v", ex)
			}
		}
	}
}

func TestBuildEvaluationFailurePatterns(t *testing.T) {
	ev := &schemas.Evaluation{ID: "ev_1", Spec: schemas.EvaluationSpec{Mode: "versions", Arms: []schemas.EvaluationArm{
		{Label: "A", Agent: "codex", Version: 1, Model: "gpt-x"},
		{Label: "B", Agent: "agents/codex.yaml@v2", Model: "gpt-x"},
		{Label: "C", Agent: "codex", Version: 3, Model: "gpt-y"},
	}}, Children: []schemas.EvaluationChild{{Benchmark: "coding-core", ExperimentID: testExp}, {Benchmark: "nope", ExperimentID: "e_missing"}, {Benchmark: "skipped"}}}
	out := BuildEvaluationFailurePatterns(failureIndex(), ev, nil)
	if out.Evaluation != "ev_1" || len(out.Groups) != 3 {
		t.Fatalf("groups = %+v", out.Groups)
	}
	a, b, c := out.Groups[0], out.Groups[1], out.Groups[2]
	if a.Key != "A" || a.Version != 1 || a.Runs != 2 || a.Failed != 1 || a.ConfigHash == "" {
		t.Fatalf("A = %+v", a)
	}
	if b.Agent != "codex" || b.Version != 2 || patternOf(t, b, "pattern_ignored_error").Count != 1 {
		t.Fatalf("B = %+v", b)
	}
	if c.Runs != 0 || !slices.Equal(c.Models, []string{"gpt-y"}) || len(c.Patterns) != len(triagePatterns) {
		t.Fatalf("C (no runs) = %+v", c)
	}
}

func failureServe(t *testing.T, env OrgEnv, uri string) (int, map[string]any) {
	t.Helper()
	r := router.New()
	registerFailurePatterns(env, r)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(uri)
	r.Handler(&ctx)
	var body map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
		t.Fatalf("%s: %v: %s", uri, err, ctx.Response.Body())
	}
	return ctx.Response.StatusCode(), body
}

func TestFailurePatternRoutes(t *testing.T) {
	root := failureRoot(t)
	dir := filepath.Join(root, filepath.FromSlash(ReportCardEvaluationsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ev := schemas.Evaluation{ID: "ev_1", Status: "done", CreatedAt: time.Now(),
		Spec:     schemas.EvaluationSpec{Mode: "score", Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Model: "gpt-x"}}},
		Children: []schemas.EvaluationChild{{Benchmark: "coding-core", ExperimentID: testExp, Status: "done"}}}
	data, _ := json.Marshal(ev)
	if err := os.WriteFile(filepath.Join(dir, "ev_1.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: failureIndex(), Catalog: fakeCatalog{}}}

	code, body := failureServe(t, env, APIPrefix+"/agents/codex/failure-patterns?include_practice=true")
	if groups, _ := body["groups"].([]any); code != fasthttp.StatusOK || body["agent"] != "codex" || len(groups) != 2 {
		t.Fatalf("agent %d %v", code, body)
	}
	// coding-core is a practice benchmark: left out by default.
	code, body = failureServe(t, env, APIPrefix+"/agents/codex/failure-patterns")
	if groups, _ := body["groups"].([]any); code != fasthttp.StatusOK || len(groups) != 0 {
		t.Fatalf("agent default %d %v", code, body)
	}
	code, body = failureServe(t, env, APIPrefix+"/agents/codex/failure-patterns?include_practice=true&version=2")
	if groups, _ := body["groups"].([]any); code != fasthttp.StatusOK || len(groups) != 1 {
		t.Fatalf("agent version 2 %d %v", code, body)
	}
	for _, q := range []string{"version=two", "version=0", "include_practice=maybe"} {
		if code, _ := failureServe(t, env, APIPrefix+"/agents/codex/failure-patterns?"+q); code != fasthttp.StatusBadRequest {
			t.Fatalf("%s: %d, want 400", q, code)
		}
	}
	code, body = failureServe(t, env, APIPrefix+"/evaluations/ev_1/failure-patterns")
	if groups, _ := body["groups"].([]any); code != fasthttp.StatusOK || body["evaluation"] != "ev_1" || len(groups) != 1 {
		t.Fatalf("evaluation %d %v", code, body)
	}
	if code, _ := failureServe(t, env, APIPrefix+"/evaluations/ev_missing/failure-patterns"); code != fasthttp.StatusNotFound {
		t.Fatalf("missing evaluation: %d", code)
	}
	if code, _ := failureServe(t, env, APIPrefix+"/agents/a%20b/failure-patterns"); code != fasthttp.StatusNotFound {
		t.Fatalf("invalid agent id: %d", code)
	}
	if code, _ := failureServe(t, OrgEnv{Mode: testMode}, APIPrefix+"/agents/codex/failure-patterns"); code != fasthttp.StatusServiceUnavailable {
		t.Fatalf("no index: %d", code)
	}
	if code, _ := failureServe(t, OrgEnv{Mode: testMode, Services: Services{Index: failureIndex()}}, APIPrefix+"/evaluations/ev_1/failure-patterns"); code != fasthttp.StatusServiceUnavailable {
		t.Fatalf("no evaluation store: %d", code)
	}
}
