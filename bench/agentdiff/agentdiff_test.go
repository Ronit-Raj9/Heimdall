package agentdiff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// Hashes of the fixture versions.
var (
	hashA = strings.Repeat("a", 64)
	hashB = strings.Repeat("b", 64)
	hashC = strings.Repeat("c", 64)
)

// fakeSource is an in-memory run folder.
type fakeSource struct {
	manifests map[string]*schemas.Manifest
	runs      map[string][]Run
	files     map[string][]byte // run id + "/" + name
	mismatch  map[string]bool   // run ids whose seal does not verify
	decisions map[string]schemas.Decision
	seq       int
}

func newFake() *fakeSource {
	return &fakeSource{manifests: map[string]*schemas.Manifest{}, runs: map[string][]Run{}, files: map[string][]byte{},
		mismatch: map[string]bool{}, decisions: map[string]schemas.Decision{}}
}

func (f *fakeSource) Experiments() []string {
	out := make([]string, 0, len(f.manifests))
	for id := range f.manifests {
		out = append(out, id)
	}
	return out
}

func (f *fakeSource) Manifest(id string) (*schemas.Manifest, bool) {
	m, ok := f.manifests[id]
	return m, ok
}

func (f *fakeSource) Runs(id string) []Run { return f.runs[id] }

func (f *fakeSource) RunFile(runID, name string) ([]byte, error) {
	if b, ok := f.files[runID+"/"+name]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("run %s file %s: %w", runID, name, fs.ErrNotExist)
}

func (f *fakeSource) VerifySeal(_ context.Context, runID string) error {
	if f.mismatch[runID] {
		return fmt.Errorf("run %s: %w", runID, schemas.ErrSealMismatch)
	}
	return nil
}

func (f *fakeSource) LatestDecision(id string) (schemas.Decision, bool) {
	d, ok := f.decisions[id]
	return d, ok
}

// addExp adds an experiment on suite with the given arms.
func (f *fakeSource) addExp(id, suite string, created time.Time, arms map[string]schemas.ArmManifest) *schemas.Manifest {
	sid, ver, _ := strings.Cut(suite, "@")
	m := &schemas.Manifest{ExpID: id, CreatedAt: created, SuiteID: sid, SuiteVersion: ver, Arms: arms}
	f.manifests[id] = m
	return m
}

// runSpec is one run to add.
type runSpec struct {
	exp, arm, task, cond string
	trial                int
	outcome              schemas.Outcome
	metrics              map[string]float64
	fired                []string
	sealed               bool
}

// add adds one sealed run with metrics.json and gates.json, and returns its id.
func (f *fakeSource) add(s runSpec) string {
	f.seq++
	if s.cond == "" {
		s.cond = schemas.ConditionClean
	}
	if s.trial == 0 {
		s.trial = 1
	}
	runID := fmt.Sprintf("r_%03d", f.seq)
	pair := fmt.Sprintf("p_%s_%s_%s_%d", s.exp, s.task, s.cond, s.trial)
	slot := schemas.Slot{SlotID: fmt.Sprintf("s_%03d", f.seq), PairID: pair, ExpID: s.exp, Task: s.task, Arm: s.arm,
		Condition: s.cond, Trial: s.trial, Counted: true}
	run := schemas.Run{RunID: runID, SlotID: slot.SlotID, PairID: pair, ExpID: s.exp, Attempt: 1, Outcome: s.outcome}
	f.runs[s.exp] = append(f.runs[s.exp], Run{Slot: slot, Run: run, Sealed: s.sealed})
	metrics := map[string]schemas.Value{}
	for k, v := range s.metrics {
		metrics[k] = schemas.Observed(v, schemas.EvidenceGateway)
	}
	if b, err := json.Marshal(metrics); err == nil {
		f.files[runID+"/metrics.json"] = b
	}
	gates := []schemas.GateResult{{Gate: "egress", Effect: schemas.GateEffect("invalidate")}}
	for _, g := range s.fired {
		gates = append(gates, schemas.GateResult{Gate: g, Fired: true})
	}
	if b, err := json.Marshal(gates); err == nil {
		f.files[runID+"/gates.json"] = b
	}
	return runID
}

// botArm is a resolved arm of agent "bot".
func botArm(hash, label, model, prompt string) schemas.ArmManifest {
	return schemas.ArmManifest{
		Label: label, ConfigHash: hash,
		Config: schemas.AgentConfig{
			ID: "bot", Adapter: "command", Image: "bench/agent-bot:1", Model: model,
			Env:     map[string]string{"OPENAI_API_KEY": "sk-" + label, "BOT_MODEL": model},
			Files:   map[string]string{"/home/agent/prompt.md": prompt},
			Budgets: schemas.Budgets{WallS: 600, CPUs: 1, MemGB: 2},
			Gateway: schemas.GatewayPolicy{Provider: "openai"},
		},
		Files: map[string]map[string]string{schemas.ConditionClean: {
			"/home/agent/prompt.md":   utils.SHA256Bytes([]byte(prompt)),
			"t1:/workspace/AGENTS.md": "n1",
		}},
	}
}

// flipFixture is experiment e1 with arms A and B over four clean tasks:
// t1 both pass, t2 flips to pass, t3 flips to fail with a new gate, t4 both
// fail.
func flipFixture() *fakeSource {
	f := newFake()
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	f.addExp("e1", "s@1", t0, map[string]schemas.ArmManifest{
		"A": botArm(hashA, "v1", "m1", "Be careful.\nRun the tests.\n"),
		"B": botArm(hashB, "v2", "m2", "Be careful.\nRun every test.\n"),
	})
	pass, fail := schemas.OutcomePass, schemas.OutcomeFail
	for _, r := range []struct {
		task   string
		a, b   schemas.Outcome
		firedB []string
	}{{"t1", pass, pass, nil}, {"t2", fail, pass, nil}, {"t3", pass, fail, []string{"g1_protected_paths"}}, {"t4", fail, fail, nil}} {
		f.add(runSpec{exp: "e1", arm: "A", task: r.task, outcome: r.a, sealed: true, metrics: map[string]float64{"cost_usd": 0.02, "tokens": 1000}})
		f.add(runSpec{exp: "e1", arm: "B", task: r.task, outcome: r.b, sealed: true, fired: r.firedB, metrics: map[string]float64{"cost_usd": 0.01, "tokens": 1500}})
	}
	return f
}

func TestDiffFlips(t *testing.T) {
	r, err := Diff(context.Background(), flipFixture(), nil, Query{A: "e1:A", B: "e1:B"})
	if err != nil {
		t.Fatal(err)
	}
	o := r.Outcome
	want := ClassCounts{FlippedToPass: 1, FlippedToFail: 1, UnchangedPass: 1, UnchangedFail: 1}
	if o.Counts != want || o.CommonTasks != 4 || o.Net != 0 {
		t.Fatalf("counts %+v common %d net %d", o.Counts, o.CommonTasks, o.Net)
	}
	if r.Suite != "s@1" || r.Condition != "clean" || r.A.ConfigHash != hashA || r.B.Label != "v2" || r.A.Runs != 4 {
		t.Fatalf("header %+v %+v", r.A, r.B)
	}
	if o.Tasks[0].Task != "t3" || o.Tasks[0].Class != ClassFlippedToFail || o.Tasks[1].Task != "t2" {
		t.Fatalf("task order %+v", o.Tasks)
	}
	p := o.Tasks[1].Pair
	if p == nil || p.PairID == "" || p.A.Outcome != "fail" || p.B.Outcome != "pass" || p.A.ExpID != "e1" {
		t.Fatalf("pair %+v", p)
	}
	if o.Test.Conclusion != ConclusionCannotTell || o.Test.NNonzero != 2 {
		t.Fatalf("test %+v", o.Test)
	}
	if len(o.Gates) != 1 || o.Gates[0].Gate != "g1_protected_paths" || !o.Gates[0].New || o.Gates[0].Tasks[0] != "t3" {
		t.Fatalf("gates %+v", o.Gates)
	}
	cost := o.Metrics[0]
	if cost.Metric != "cost_usd" || cost.Direction != DirectionBetter || cost.Tasks != 4 || *cost.Delta > -0.0099 {
		t.Fatalf("cost %+v", cost)
	}
	if o.Metrics[1].Metric != "tokens" || o.Metrics[1].Direction != DirectionWorse {
		t.Fatalf("tokens %+v", o.Metrics[1])
	}
	if o.Metrics[2].Direction != DirectionUnknown || o.Metrics[2].UnknownA != 4 {
		t.Fatalf("steps %+v", o.Metrics[2])
	}
	if r.Config.Identical || r.Config.Groups[0].ID != GroupModel {
		t.Fatalf("config %+v", r.Config)
	}
	for _, s := range []string{"B passes 2 of 4 tasks (50%) and A passes 2 of 4 tasks (50%)", "1 task flipped to pass and 1 flipped to fail (net +0)", "Cannot tell", "model"} {
		if !strings.Contains(r.Summary, s) {
			t.Errorf("summary %q lacks %q", r.Summary, s)
		}
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-v1") || strings.Contains(string(data), "sk-v2") {
		t.Fatalf("secret leaked: %s", data)
	}
}

func TestDiffSelections(t *testing.T) {
	f := flipFixture()
	t1 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	f.addExp("e2", "s@1", t1, map[string]schemas.ArmManifest{"X": botArm(hashC, "v3", "m3", "x\n")})
	f.add(runSpec{exp: "e2", arm: "X", task: "t1", outcome: schemas.OutcomeFail, sealed: true})
	cases := []struct {
		name   string
		q      Query
		a, b   string
		common int
		err    string
	}{
		{name: "hash prefix and full hash", q: Query{A: hashA[:8], B: hashB}, a: hashA, b: hashB, common: 4},
		{name: "latest two of an agent", q: Query{Agent: "bot", Suite: "s@1"}, a: hashB, b: hashC, common: 1},
		{name: "latest two without suite", q: Query{Agent: "bot"}, a: hashB, b: hashC, common: 1},
		{name: "unknown arm", q: Query{A: "e1:Z", B: "e1:B"}, err: `no experiment "e1" with arm "Z"`},
		{name: "short prefix", q: Query{A: "aaaa", B: "e1:B"}, err: "shorter than 8"},
		{name: "not a hash", q: Query{A: "zzzz", B: "e1:B"}, err: "neither a config hash"},
		{name: "one side only", q: Query{A: "e1:A"}, err: "both versions"},
		{name: "nothing", q: Query{}, err: "two versions"},
		{name: "unknown agent", q: Query{Agent: "nobody"}, err: "two are needed"},
		{name: "bad suite", q: Query{A: "e1:A", B: "e1:B", Suite: "s"}, err: "not id@version"},
		{name: "no common suite", q: Query{A: "e1:A", B: "e1:B", Suite: "other@2"}, err: "share no task"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Diff(context.Background(), f, nil, c.q)
			if c.err != "" {
				if err == nil || !errors.Is(err, ErrBadQuery) || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.A.ConfigHash != c.a || r.B.ConfigHash != c.b || r.Outcome.CommonTasks != c.common {
				t.Fatalf("got %s %s %d", r.A.ConfigHash[:4], r.B.ConfigHash[:4], r.Outcome.CommonTasks)
			}
		})
	}
}

func TestDiffExclusions(t *testing.T) {
	f := flipFixture()
	// A rerun of t2 on B under stale-notes has a seal mismatch; t9 is unsealed.
	bad := f.add(runSpec{exp: "e1", arm: "B", task: "t2", cond: "stale-notes", outcome: schemas.OutcomePass, sealed: true})
	f.mismatch[bad] = true
	f.add(runSpec{exp: "e1", arm: "A", task: "t2", cond: "stale-notes", outcome: schemas.OutcomeFail, sealed: true})
	f.add(runSpec{exp: "e1", arm: "B", task: "t9", cond: "stale-notes", outcome: schemas.OutcomePass})
	_, err := Diff(context.Background(), f, nil, Query{A: "e1:A", B: "e1:B", Condition: "stale-notes"})
	if err == nil || !strings.Contains(err.Error(), "1 left out: seal mismatch") || !strings.Contains(err.Error(), "1 left out: not sealed") {
		t.Fatalf("err %v", err)
	}

	t.Run("invalid verdict", func(t *testing.T) {
		f := flipFixture()
		f.decisions["e1"] = schemas.DecisionInvalid
		_, err := Diff(context.Background(), f, nil, Query{A: "e1:A", B: "e1:B"})
		if err == nil || !strings.Contains(err.Error(), ReasonVerdictInvalid) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("synthetic excluded unless asked", func(t *testing.T) {
		f := flipFixture()
		f.manifests["e1"].Experiment.Smoke = true
		_, err := Diff(context.Background(), f, nil, Query{A: "e1:A", B: "e1:B"})
		if err == nil || !strings.Contains(err.Error(), "synthetic") {
			t.Fatalf("err %v", err)
		}
		r, err := Diff(context.Background(), f, nil, Query{A: "e1:A", B: "e1:B", Synthetic: true})
		if err != nil || r.Outcome.CommonTasks != 4 || !r.Synthetic {
			t.Fatalf("synthetic diff: %v", err)
		}
	})
}

// indexSource reads a store index as a Source, the way the handler adapts
// the API's read index.
type indexSource struct{ ix *store.Index }

func (s indexSource) Experiments() []string { return s.ix.Experiments() }

func (s indexSource) Manifest(id string) (*schemas.Manifest, bool) { return s.ix.Manifest(id) }

func (s indexSource) Runs(id string) []Run {
	es, _ := s.ix.Runs(id)
	out := make([]Run, 0, len(es))
	for _, e := range es {
		out = append(out, Run{Slot: e.Slot, Run: e.Run, Sealed: e.Sealed})
	}
	return out
}

func (s indexSource) RunFile(runID, name string) ([]byte, error) { return s.ix.RunFile(runID, name) }

func (s indexSource) VerifySeal(ctx context.Context, runID string) error {
	return s.ix.VerifyRun(ctx, runID)
}

// recordedSource opens the read-only recorded bundle.
func recordedSource(t *testing.T) Source {
	t.Helper()
	st, err := store.NewFSStore("../testdata/recorded", nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := store.NewIndex(st)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	return indexSource{ix: ix}
}

// TestDiffRecorded runs the diff on the real recorded experiment: Codex (A)
// against the custom-python agent (B), one run per arm and condition.
func TestDiffRecorded(t *testing.T) {
	src := recordedSource(t)
	cases := []struct {
		condition, task, class string
	}{
		{"clean", "t02", ClassUnchangedPass},
		{"empty-test-run", "t05", ClassFlippedToPass},
		{"model-outage", "t01", ClassUnchangedFail},
	}
	for _, c := range cases {
		t.Run(c.condition, func(t *testing.T) {
			r, err := Diff(context.Background(), src, nil, Query{A: "e_pilot_mixed_nc93:A", B: "e_pilot_mixed_nc93:B", Condition: c.condition})
			if err != nil {
				t.Fatal(err)
			}
			if r.Suite != "coding-core@1.0.0" || r.Outcome.CommonTasks != 1 || r.Outcome.Tasks[0].Task != c.task || r.Outcome.Tasks[0].Class != c.class {
				t.Fatalf("outcome %+v", r.Outcome.Tasks)
			}
			if r.A.AgentID != "codex" || r.B.AgentID != "custom-python" || r.A.Runs != 1 || r.B.Runs != 1 {
				t.Fatalf("versions %+v %+v", r.A, r.B)
			}
			if c.class == ClassFlippedToPass {
				p := r.Outcome.Tasks[0].Pair
				if p == nil || p.PairID == "" || p.A.Outcome != "fail" || p.B.Outcome != "pass" {
					t.Fatalf("pair %+v", p)
				}
			}
			groups := map[string]ConfigGroup{}
			for _, g := range r.Config.Groups {
				groups[g.ID] = g
			}
			if len(groups[GroupImage].Fields) == 0 || len(groups[GroupBudgets].Fields) == 0 {
				t.Fatalf("config groups %+v", r.Config.Groups)
			}
			var sawKey bool
			for _, f := range groups[GroupOther].Fields {
				if f.Path == "env.BIFROST_API_KEY" {
					sawKey = f.Kind == KindRemoved && f.Old == redacted && f.Redacted
				}
			}
			if !sawKey {
				t.Fatalf("env.BIFROST_API_KEY not reported as a redacted removal: %+v", groups[GroupOther].Fields)
			}
			if r.Outcome.Metrics[0].Metric != "cost_usd" || r.Outcome.Metrics[0].Tasks != 1 {
				t.Fatalf("cost %+v", r.Outcome.Metrics[0])
			}
		})
	}
}

// TestDiffRecordedSynthetic checks the synthetic recorded experiment stays
// out unless asked for.
func TestDiffRecordedSynthetic(t *testing.T) {
	src := recordedSource(t)
	q := Query{A: "e_smoke_sdoj:A", B: "e_smoke_sdoj:B"}
	if _, err := Diff(context.Background(), src, nil, q); err == nil || !strings.Contains(err.Error(), "CLI -synthetic") {
		t.Fatalf("err %v", err)
	}
	q.Synthetic = true
	r, err := Diff(context.Background(), src, nil, q)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome.CommonTasks != 2 || r.Outcome.Counts.UnchangedPass != 2 || r.Config.Groups[0].ID != GroupPrompt {
		t.Fatalf("smoke diff %+v %+v", r.Outcome.Counts, r.Config.Groups)
	}
}
