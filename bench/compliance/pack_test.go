package compliance

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fixture builds one experiment with clean and prompt-injection runs, one of
// which fired the injection veto, and one unknown probe.
func fixture(root string) ExperimentInput {
	m := &schemas.Manifest{
		ExpID: "e_test", Hash: "sha256:abcdef0123456789", SuiteID: "coding-core", SuiteVersion: "1.0.0",
		CreatedAt:  time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC),
		Experiment: schemas.Experiment{Name: "test", Conditions: []schemas.ConditionSpec{{ID: "clean"}, {ID: "prompt-injection"}, {ID: "new-probe"}}},
		Arms:       map[string]schemas.ArmManifest{"A": {}, "B": {}},
	}
	run := func(id, cond, arm string, gates ...schemas.GateResult) schemas.SealedRun {
		return schemas.SealedRun{
			Slot:     schemas.Slot{SlotID: "s_" + id, Task: "t01", Arm: arm, Condition: cond, Trial: 1, Counted: true},
			Run:      schemas.Run{RunID: "r_" + id, Outcome: schemas.Outcome("pass")},
			Dir:      filepath.Join(root, "e_test", "slots", "s_"+id, "runs", "r_"+id),
			SealHash: "seal" + id,
			Gates:    gates,
		}
	}
	inj := func(fired bool) schemas.GateResult {
		return schemas.GateResult{Gate: "g12_injection_followed", Effect: schemas.GateVeto, Fired: fired}
	}
	uncounted := run("9", "clean", "compat")
	uncounted.Slot.Counted = false
	return ExperimentInput{
		Manifest: m,
		Runs: []schemas.SealedRun{
			run("1", "clean", "A"), run("2", "clean", "B"),
			run("3", "prompt-injection", "A", inj(false)), run("4", "prompt-injection", "B", inj(true)),
			run("5", "new-probe", "A", schemas.GateResult{Gate: "brand_new_gate", Effect: schemas.GateReport}),
			uncounted,
		},
		Analysis:     &schemas.Analysis{AnalysisID: "a_1", Mode: schemas.ModeLive, Verdict: schemas.Verdict{Decision: schemas.DecisionInconclusive, Action: schemas.ActionNoDecision}},
		Probes:       map[string]*schemas.ProbeSpec{"prompt-injection": {ID: "prompt-injection", Axis: "safety"}, "new-probe": {ID: "new-probe", Axis: "safety"}},
		ChainChecked: true,
		SealsTail:    "r_5 seal5",
	}
}

func TestBuild(t *testing.T) {
	root := "/runs"
	p, err := Build(Input{Experiments: []ExperimentInput{fixture(root)}, RunsRoot: root, BenchVersion: "test", Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]schemas.ComplianceTest{}
	for _, tt := range p.Tests {
		byID[tt.ID] = tt
	}
	cases := []struct {
		id              string
		runs, fired     int
		mappedBy        string
		wantID, evPaths int
	}{
		{"condition:clean", 2, 0, "id", 0, 2},
		{"condition:prompt-injection", 2, 1, "id", 0, 2},
		{"condition:new-probe", 1, 0, "axis", 0, 1},
		{"gate:g12_injection_followed", 2, 1, "id", 0, 1},
		{"gate:brand_new_gate", 1, 0, "none", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			tt, ok := byID[c.id]
			if !ok {
				t.Fatalf("test %s missing; have %v", c.id, p.Tests)
			}
			if tt.Runs != c.runs || tt.Fired != c.fired || tt.MappedBy != c.mappedBy || len(tt.Evidence) != c.evPaths {
				t.Fatalf("got runs %d fired %d mapped_by %s evidence %d", tt.Runs, tt.Fired, tt.MappedBy, len(tt.Evidence))
			}
		})
	}
	if got := byID["condition:prompt-injection"].Evidence[0]; got.Path != "e_test/slots/s_4/runs/r_4" || got.SealHash != "seal4" || !got.Fired {
		t.Fatalf("evidence row %+v", got)
	}
	if p.Tests[0].ID != "condition:clean" {
		t.Fatalf("clean must come first, got %s", p.Tests[0].ID)
	}
	if len(p.Unmapped) != 1 || p.Unmapped[0] != "gate:brand_new_gate" {
		t.Fatalf("unmapped %v", p.Unmapped)
	}
	e := p.Experiments[0]
	if e.CountedRuns != 5 || e.RunsVerified != 5 || e.ChainStatus != ChainVerified || e.Decision != schemas.DecisionInconclusive {
		t.Fatalf("experiment summary %+v", e)
	}
	if len(p.NotCertified) == 0 || p.RecordKeeping.ExternalAnchor {
		t.Fatal("pack must state what it does not certify and that there is no external anchor")
	}
}

func TestBuildWarnings(t *testing.T) {
	cases := []struct {
		name string
		edit func(*ExperimentInput)
		want string
	}{
		{"broken chain", func(e *ExperimentInput) { e.ChainErr = errors.New("line 3 does not match") }, "does not verify"},
		{"unchecked chain", func(e *ExperimentInput) { e.ChainChecked = false }, "not checked"},
		{"no analysis", func(e *ExperimentInput) { e.Analysis = nil }, "no analysis"},
		{"no suite", func(e *ExperimentInput) { e.Probes = nil }, "could not be loaded"},
		{"run problem", func(e *ExperimentInput) { e.Runs[0].Problems = []string{"seal mismatch"} }, "evidence problems"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ex := fixture("/runs")
			c.edit(&ex)
			p, err := Build(Input{Experiments: []ExperimentInput{ex}, RunsRoot: "/runs"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(p.Warnings, "\n"), c.want) {
				t.Fatalf("warnings %v lack %q", p.Warnings, c.want)
			}
		})
	}
	if _, err := Build(Input{}); err == nil {
		t.Fatal("an empty input must be refused")
	}
	if _, err := Build(Input{Experiments: []ExperimentInput{{}}}); err == nil {
		t.Fatal("an experiment without a manifest must be refused")
	}
}

func TestBuildRelease(t *testing.T) {
	rec := &schemas.ReleaseRecord{ReleaseID: "rel_1", ExpID: "e_test", AnalysisID: "a_1", Decision: schemas.DecisionImproved, Action: schemas.ActionShip, Hash: "h", SealsDigest: "d"}
	p, err := Build(Input{Experiments: []ExperimentInput{fixture("/runs")}, Release: rec})
	if err != nil {
		t.Fatal(err)
	}
	if p.Release == nil || p.Release.ReleaseID != "rel_1" || p.Release.Hash != "h" {
		t.Fatalf("release %+v", p.Release)
	}
}

func TestPackJSONShape(t *testing.T) {
	p, err := Build(Input{Experiments: []ExperimentInput{fixture("/runs")}, RunsRoot: "/runs"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "generated_at", "experiments", "tests", "frameworks", "nist_functions", "record_keeping", "not_certified"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("pack JSON lacks %q", k)
		}
	}
}

func TestRelPath(t *testing.T) {
	cases := []struct{ root, dir, want string }{
		{"/runs", "/runs/e/slots/s/runs/r", "e/slots/s/runs/r"},
		{"/runs", "/elsewhere/r", "/elsewhere/r"},
		{"", "/x/r", "/x/r"},
	}
	for _, c := range cases {
		if got := relPath(c.root, c.dir); got != c.want {
			t.Errorf("relPath(%q, %q) = %q, want %q", c.root, c.dir, got, c.want)
		}
	}
}
