package modelswitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// fakeSource is a leaderboard.Source over in-memory runs.
type fakeSource struct {
	manifests map[string]*schemas.Manifest
	runs      map[string][]leaderboard.Run
	files     map[string]map[string][]byte // run id -> file -> data
}

func newSource() *fakeSource {
	return &fakeSource{manifests: map[string]*schemas.Manifest{}, runs: map[string][]leaderboard.Run{}, files: map[string]map[string][]byte{}}
}

func (f *fakeSource) Experiments() []string { return nil }
func (f *fakeSource) Manifest(id string) (*schemas.Manifest, bool) {
	m, ok := f.manifests[id]
	return m, ok
}
func (f *fakeSource) Runs(id string) []leaderboard.Run { return f.runs[id] }
func (f *fakeSource) RunFile(runID, name string) ([]byte, error) {
	if b, ok := f.files[runID][name]; ok {
		return b, nil
	}
	return nil, fs.ErrNotExist
}
func (f *fakeSource) LatestDecision(string) (schemas.Decision, bool) { return "", false }

// armSpec is one arm of a fixture: its provider and per-run numbers.
type armSpec struct {
	id, provider, model string
	cost                *float64
	latency             float64
	failEvery           int // every n-th model call fails with a 503; 0 never
}

func ptr(x float64) *float64 { return &x }

// add records one experiment whose arm id passes the tasks pass returns
// true for, two trials each.
func (f *fakeSource) add(exp, suite string, arms []armSpec, tasks []string, pass func(arm, task string) bool) {
	m := &schemas.Manifest{ExpID: exp, SuiteID: suite, SuiteVersion: "1", Arms: map[string]schemas.ArmManifest{}}
	for _, a := range arms {
		cfg := schemas.AgentConfig{ID: "support-bot", Model: a.model}
		cfg.Gateway.Provider = a.provider
		m.Arms[a.id] = schemas.ArmManifest{Label: "support-bot on " + a.model, Config: cfg}
	}
	f.manifests[exp] = m
	n := 0
	for _, a := range arms {
		for _, task := range tasks {
			for trial := 1; trial <= 2; trial++ {
				n++
				id := fmt.Sprintf("r_%s_%d", exp, n)
				slot := fmt.Sprintf("%s-%s-%s-%d", exp, a.id, task, trial)
				out := schemas.OutcomeFail
				if pass(a.id, task) {
					out = schemas.OutcomePass
				}
				metrics := map[string]schemas.Value{metricWallTime: schemas.Observed(30, schemas.EvidenceOutcome)}
				if a.cost != nil {
					metrics[metricCostUSD] = schemas.Observed(*a.cost, schemas.EvidenceGateway)
				}
				mb, _ := json.Marshal(metrics)
				var rows strings.Builder
				for call := 1; call <= 4; call++ {
					row := schemas.GatewayRequest{ID: fmt.Sprintf("%s-%d", id, call), Kind: "message", Status: "success", Provider: a.provider, LatencyMS: a.latency}
					if a.failEvery > 0 && call%a.failEvery == 0 {
						row.Status, row.ErrorStatus, row.LatencyMS = "error", 503, 0
					}
					b, _ := json.Marshal(row)
					rows.Write(b)
					rows.WriteByte('\n')
				}
				f.files[id] = map[string][]byte{fileMetrics: mb, "gateway.requests.jsonl": []byte(rows.String())}
				f.runs[exp] = append(f.runs[exp], leaderboard.Run{
					Slot:   schemas.Slot{SlotID: slot, ExpID: exp, Task: task, Arm: a.id, Condition: schemas.ConditionClean, Trial: trial, Counted: true},
					Run:    schemas.Run{RunID: id, SlotID: slot, Outcome: out, Attempt: 1},
					Sealed: true,
				})
			}
		}
	}
}

// fixture: 16 tasks, 8 sql and 8 chat. A (current, $0.10/run) passes every
// sql task and 2 chat tasks; B ($0.02/run) passes 2 sql tasks and every
// chat task; C ($0.01/run) passes one task. A and B tie overall at 10/16,
// B is cheaper; on sql A beats B 8 to 2.
func fixture() (*fakeSource, *schemas.Evaluation, Options) {
	src := newSource()
	var tasks []string
	for i := 1; i <= 8; i++ {
		tasks = append(tasks, fmt.Sprintf("s%d", i))
	}
	for i := 1; i <= 8; i++ {
		tasks = append(tasks, fmt.Sprintf("c%d", i))
	}
	arms := []armSpec{
		{id: "A", provider: "anthropic", model: "claude-sonnet-4-5", cost: ptr(0.10), latency: 800},
		{id: "B", provider: "openai", model: "gpt-5-mini", cost: ptr(0.02), latency: 400, failEvery: 4},
		{id: "C", provider: "vllm", model: "qwen3-8b", cost: ptr(0.01), latency: 300},
	}
	src.add("exp_1", "support-core", arms, tasks, func(arm, task string) bool {
		sql := strings.HasPrefix(task, "s")
		n := task[1] - '0'
		switch arm {
		case "A":
			return sql || n >= 7
		case "B":
			return !sql || n >= 7
		}
		return task == "s1"
	})
	ev := &schemas.Evaluation{ID: "ev_1", Status: schemas.EvaluationDone,
		Spec: schemas.EvaluationSpec{Mode: schemas.EvaluationModeModelSwitch, Trials: 2, Arms: []schemas.EvaluationArm{
			{Label: "A", Agent: "support-bot", Model: "claude-sonnet-4-5", Provider: "anthropic"},
			{Label: "B", Agent: "support-bot", Model: "gpt-5-mini", Provider: "openai"},
			{Label: "C", Agent: "support-bot", Model: "qwen3-8b", Provider: "vllm"},
		}},
		Children: []schemas.EvaluationChild{{Benchmark: "support-core", ExperimentID: "exp_1", Status: "done"}, {Benchmark: "voice-core", Skipped: "agent has no voice"}},
	}
	opt := Options{Segments: func(_, task string) map[string]string {
		kind := "chat"
		if strings.HasPrefix(task, "s") {
			kind = "sql"
		}
		return map[string]string{"task_kind": kind, "capability": "support"}
	}}
	return src, ev, opt
}

func TestBuildReport(t *testing.T) {
	src, ev, opt := fixture()
	rep, err := Build(context.Background(), src, ev, opt)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Best != "B" || rep.Models[0].Label != "B" || rep.Models[1].Label != "A" || rep.Models[2].Label != "C" {
		t.Fatalf("rank: best %s, models %+v", rep.Best, rep.Models)
	}
	b := rep.Models[0]
	if q, _ := b.Quality.Float(); q != 0.625 || b.Quality.Bounds == nil || b.Quality.Bounds[0] >= 0.625 || b.Quality.Bounds[1] <= 0.625 {
		t.Fatalf("quality %+v", b.Quality)
	}
	if c, _ := b.CostPerSuccess.Float(); c != 0.032 {
		t.Fatalf("cost per success %v", b.CostPerSuccess)
	}
	if a, _ := b.Availability.Float(); a != 0.75 || b.CallFailures != 32 {
		t.Fatalf("availability %v failures %d", b.Availability, b.CallFailures)
	}
	if l, _ := b.LatencyP50MS.Float(); l != 400 {
		t.Fatalf("latency %v", b.LatencyP50MS)
	}
	if !rep.Models[2].Open {
		t.Fatal("vllm model not marked open")
	}
	var a, c Comparison
	for _, x := range rep.Comparisons {
		switch x.Label {
		case "A":
			a = x
		case "C":
			c = x
		}
	}
	if a.Result != ResultCannotTell || a.Tasks != 16 || a.MeanDiff != 0 {
		t.Fatalf("A vs B %+v", a)
	}
	if c.Result != ResultWorse || c.PAdjusted == nil || *c.PAdjusted >= 0.05 {
		t.Fatalf("C vs B %+v", c)
	}
	r := rep.Recommendation
	if r.Action != ActionSwitch || r.Model != "B" || r.Strength != StrengthWeak || len(r.Tied) != 2 {
		t.Fatalf("recommendation %+v", r)
	}
	if !strings.Contains(r.Summary, "openai/gpt-5-mini") || !strings.Contains(strings.Join(r.Reasons, " "), "5.0 times less") {
		t.Fatalf("words %q %q", r.Summary, r.Reasons)
	}
	if len(rep.Benchmarks) != 1 || rep.Benchmarks[0].Tasks != 16 || len(rep.Missing) != 1 || !strings.Contains(rep.Missing[0], "voice-core") {
		t.Fatalf("benchmarks %+v missing %v", rep.Benchmarks, rep.Missing)
	}
	var sql *Group
	for i := range rep.Segments {
		if rep.Segments[i].Key == "capability" {
			t.Fatal("a key with one value splits nothing")
		}
		if rep.Segments[i].Value == "sql" {
			sql = &rep.Segments[i]
		}
	}
	if sql == nil || sql.Best != "A" || sql.VsDefault == nil || sql.VsDefault.Result != "better" {
		t.Fatalf("sql segment %+v", sql)
	}
	if len(rep.Pareto.Points) != 3 || len(rep.Pareto.Frontier) == 0 || !strings.Contains(strings.Join(rep.Notes, " "), "open model") {
		t.Fatalf("pareto %+v notes %v", rep.Pareto, rep.Notes)
	}
}

func TestRoutingConfig(t *testing.T) {
	src, ev, opt := fixture()
	rep, err := Build(context.Background(), src, ev, opt)
	if err != nil {
		t.Fatal(err)
	}
	ex := Routing(rep, RoutingOptions{})
	rules := ex.Config.Governance.RoutingRules
	if !ex.Ready || len(rules) != 2 || len(ex.Rules) != 2 {
		t.Fatalf("rules %+v", rules)
	}
	seg, def := rules[0], rules[1]
	if seg.Name != "heimdall-ev-1-task-kind-sql" || seg.CelExpression != `headers["x-heimdall-task-kind"] == "sql"` || !seg.Enabled ||
		seg.Targets[0].Model != "claude-sonnet-4-5" || seg.Priority != 100 {
		t.Fatalf("segment rule %+v", seg)
	}
	if def.CelExpression != "true" || len(def.Targets) != 2 || def.Targets[0].Model != "gpt-5-mini" || def.Targets[0].Weight != 0.83 || def.Targets[1].Weight != 0.17 || def.Priority != 101 {
		t.Fatalf("default rule %+v", def)
	}
	// Fallbacks by quality times availability: A (0.625) before C.
	if len(def.Fallbacks) != 1 || def.Fallbacks[0] != "vllm/qwen3-8b" || seg.Fallbacks[0] != "openai/gpt-5-mini" {
		t.Fatalf("fallbacks %v %v", def.Fallbacks, seg.Fallbacks)
	}
	if len(ex.Headers) != 1 || ex.Headers[0].Header != "x-heimdall-task-kind" || len(ex.Budget) != 3 {
		t.Fatalf("headers %+v budget %+v", ex.Headers, ex.Budget)
	}
	validateBifrostConfig(t, ex.Config)

	// A budget saver appears when the default is not the cheapest.
	rep.Recommendation.Model, rep.Recommendation.Tied = "A", []string{"A"}
	rep.Recommendation.Strength = StrengthStrong
	ex = Routing(rep, RoutingOptions{Scope: "virtual_key", ScopeID: "vk-1"})
	rules = ex.Config.Governance.RoutingRules
	if rules[0].Name != "heimdall-ev-1-budget-saver" || rules[0].CelExpression != "budget_used >= 80" || rules[0].Enabled || rules[0].Targets[0].Model != "gpt-5-mini" {
		t.Fatalf("budget rule %+v", rules[0])
	}
	if last := rules[len(rules)-1]; len(last.Targets) != 1 || last.Targets[0].Weight != 1 || last.ScopeID != "vk-1" {
		t.Fatalf("strong default %+v", last)
	}
	validateBifrostConfig(t, ex.Config)
}

// validateBifrostConfig checks cfg against Bifrost's own config schema.
func validateBifrostConfig(t *testing.T, cfg BifrostConfig) {
	t.Helper()
	path := filepath.Join("..", "..", "transports", "config.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("Bifrost schema not found: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("https://www.getbifrost.ai/schema", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("https://www.getbifrost.ai/schema")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cfg)
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("config does not match Bifrost's schema: %v\n%s", err, data)
	}
}

func TestNotModelSwitch(t *testing.T) {
	src, ev, opt := fixture()
	ev.Spec.Arms[1].Agent = "other-bot"
	if _, err := Build(context.Background(), src, ev, opt); !errors.Is(err, ErrNotModelSwitch) {
		t.Fatalf("different agents: %v", err)
	}
	ev.Spec.Arms = ev.Spec.Arms[:1]
	if _, err := Build(context.Background(), src, ev, opt); !errors.Is(err, ErrNotModelSwitch) {
		t.Fatalf("one arm: %v", err)
	}
}

func TestUnknownCostAndNoRuns(t *testing.T) {
	src := newSource()
	tasks := []string{"t1", "t2", "t3", "t4"}
	src.add("exp_u", "s", []armSpec{{id: "A", provider: "openai", model: "m1", cost: ptr(0.1), latency: 100}, {id: "B", provider: "acme", model: "m2", latency: 100}}, tasks,
		func(string, string) bool { return true })
	ev := &schemas.Evaluation{ID: "ev_u", Status: schemas.EvaluationRunning, Spec: schemas.EvaluationSpec{Trials: 2, Arms: []schemas.EvaluationArm{
		{Label: "A", Agent: "bot", Model: "m1", Provider: "openai"}, {Label: "B", Agent: "bot", Model: "m2", Provider: "acme"}, {Label: "C", Agent: "bot", Model: "m3"},
	}}, Children: []schemas.EvaluationChild{{Benchmark: "s", ExperimentID: "exp_u"}}}
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var b, c Model
	for _, m := range rep.Models {
		switch m.Label {
		case "B":
			b = m
		case "C":
			c = m
		}
	}
	if b.CostPerSuccess.State != schemas.ValueUnknown || !strings.Contains(b.CostPerSuccess.Reason, "0 of 8") {
		t.Fatalf("unpriced cost %+v", b.CostPerSuccess)
	}
	if c.Runs != 0 || c.Quality.State != schemas.ValueUnknown {
		t.Fatalf("no-run model %+v", c)
	}
	if rep.Recommendation.Action != ActionStillMoving || !strings.HasPrefix(rep.Recommendation.Summary, "The evaluation is still running") {
		t.Fatalf("running %+v", rep.Recommendation)
	}
	ex := Routing(rep, RoutingOptions{})
	def := ex.Config.Governance.RoutingRules[len(ex.Config.Governance.RoutingRules)-1]
	if len(def.Targets) != 1 || def.Targets[0].Weight != 1 {
		t.Fatalf("unknown cost must not be split: %+v", def)
	}
	validateBifrostConfig(t, ex.Config)
}

// fakeBifrost is the routing rules API of a Bifrost.
type fakeBifrost struct {
	mu     sync.Mutex
	rules  []map[string]any
	writes []map[string]any
	auth   []string
}

func (f *fakeBifrost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/routing/rules":
		if r.URL.Query().Get("scope") != "virtual_key" || r.URL.Query().Get("scope_id") != "vk-1" {
			http.Error(w, `{"error":{"message":"bad scope"}}`, 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rules": f.rules})
	case r.Method == http.MethodPost || r.Method == http.MethodPut:
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		body["_method"], body["_path"] = r.Method, r.URL.Path
		f.writes = append(f.writes, body)
		_ = json.NewEncoder(w).Encode(map[string]any{"rule": map[string]any{"id": fmt.Sprintf("id-%d", len(f.writes)), "name": body["name"]}})
	default:
		http.Error(w, "no", 404)
	}
}

func TestApply(t *testing.T) {
	src, ev, opt := fixture()
	rep, err := Build(context.Background(), src, ev, opt)
	if err != nil {
		t.Fatal(err)
	}
	fb := &fakeBifrost{rules: []map[string]any{
		{"id": "own-1", "name": "org-rule", "priority": 120, "scope": "virtual_key"},
		{"id": "old-1", "name": "heimdall-ev-1-default", "priority": 5, "scope": "virtual_key"},
		{"id": "old-2", "name": "heimdall-ev-1-tag-gone", "priority": 6, "scope": "virtual_key"},
	}}
	srv := httptest.NewServer(fb)
	defer srv.Close()
	g := Gateway{BaseURL: srv.URL, Credential: "admin:pw", Client: srv.Client()}

	if _, err := g.Apply(context.Background(), Routing(rep, RoutingOptions{}), ApplyRequest{}); !errors.Is(err, ErrGatewayRefused) {
		t.Fatalf("global without allow_global: %v", err)
	}
	ex := Routing(rep, RoutingOptions{Scope: "virtual_key", ScopeID: "vk-1"})
	res, err := g.Apply(context.Background(), ex, ApplyRequest{DryRun: true})
	if err != nil || len(res.Steps) != 2 || len(fb.writes) != 0 || res.Steps[0].Status != "planned" {
		t.Fatalf("dry run %+v %v writes %d", res, err, len(fb.writes))
	}
	res, err = g.Apply(context.Background(), ex, ApplyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fb.writes) != 2 {
		t.Fatalf("writes %+v", fb.writes)
	}
	seg, def := fb.writes[0], fb.writes[1]
	if seg["_method"] != http.MethodPost || seg["priority"].(float64) != 130 || seg["enabled"] != false || seg["scope_id"] != "vk-1" {
		t.Fatalf("segment write %+v", seg)
	}
	if def["_method"] != http.MethodPut || def["_path"] != "/api/routing/rules/old-1" || def["priority"].(float64) != 131 {
		t.Fatalf("default write %+v", def)
	}
	if len(res.Stale) != 1 || res.Stale[0] != "heimdall-ev-1-tag-gone" || res.Steps[1].Action != "update" || res.Steps[1].Status != "done" {
		t.Fatalf("result %+v", res)
	}
	if fb.auth[0] != "Basic YWRtaW46cHc=" {
		t.Fatalf("auth %q", fb.auth[0])
	}
	fb.writes = nil
	if _, err := g.Apply(context.Background(), ex, ApplyRequest{Enable: true}); err != nil || fb.writes[0]["enabled"] != true {
		t.Fatalf("enable keeps measured flag: %v %+v", err, fb.writes)
	}
}

func TestCheckGatewayURL(t *testing.T) {
	for _, bad := range []string{"", "ftp://x", "https://u:p@gw.example.com", "http://gw.example.com", "https://127.0.0.1", "https://10.0.0.2", "https://bifrost.internal"} {
		if _, err := CheckGatewayURL(bad, true); err == nil {
			t.Errorf("hosted accepted %q", bad)
		}
	}
	if _, err := CheckGatewayURL("https://gw.example.com/", true); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckGatewayURL("http://localhost:8080", false); err != nil {
		t.Fatalf("local mode refuses its own gateway: %v", err)
	}
}

func TestWeights(t *testing.T) {
	w := weights([]float64{50, 10})
	if w[0] != 0.83 || w[1] != 0.17 {
		t.Fatalf("weights %v", w)
	}
	w = weights([]float64{1000, 1, 1})
	if w[1] < minTargetWeight-0.01 || w[0]+w[1]+w[2] < 0.999 || w[0]+w[1]+w[2] > 1.001 {
		t.Fatalf("floor %v", w)
	}
}
