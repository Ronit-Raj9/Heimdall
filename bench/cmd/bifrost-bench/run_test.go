package main

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/reporters"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/verdict"
)

func TestCheckExitCode(t *testing.T) {
	failOn := []schemas.Decision{schemas.DecisionRegressed, schemas.DecisionInvalid}
	withPolicy := func(d schemas.PolicyDecision) *schemas.PolicyResult {
		return &schemas.PolicyResult{PolicyName: "p", Decision: d}
	}
	tests := []struct {
		name     string
		decision schemas.Decision
		policy   *schemas.PolicyResult
		want     int
	}{
		{"improved, no policy", schemas.DecisionImproved, nil, 0},
		{"improved, policy allows", schemas.DecisionImproved, withPolicy(schemas.PolicyAllow), 0},
		{"improved, policy warns", schemas.DecisionImproved, withPolicy(schemas.PolicyWarn), 0},
		{"improved, policy blocks", schemas.DecisionImproved, withPolicy(schemas.PolicyBlock), 5},
		{"inconclusive not in fail-on, policy blocks", schemas.DecisionInconclusive, withPolicy(schemas.PolicyBlock), 5},
		{"regressed, policy blocks: policy first", schemas.DecisionRegressed, withPolicy(schemas.PolicyBlock), 5},
		{"invalid, policy blocks: policy first", schemas.DecisionInvalid, withPolicy(schemas.PolicyBlock), 5},
		{"regressed, policy allows", schemas.DecisionRegressed, withPolicy(schemas.PolicyAllow), 1},
		{"invalid, no policy", schemas.DecisionInvalid, nil, 3},
		{"inconclusive not in fail-on", schemas.DecisionInconclusive, withPolicy(schemas.PolicyWarn), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			an := &schemas.Analysis{Verdict: schemas.Verdict{Decision: tt.decision}, Policy: tt.policy}
			if got := checkExitCode(an, failOn); got != tt.want {
				t.Fatalf("exit = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestArmCounts(t *testing.T) {
	tests := []struct {
		counts map[string]int
		want   string
	}{
		{map[string]int{"B": 2, "A": 3}, "arm A 3, arm B 2"},
		{map[string]int{"A": 1}, "arm A 1"},
		{nil, ""},
	}
	for _, tt := range tests {
		if got := armCounts(tt.counts); got != tt.want {
			t.Errorf("armCounts(%v) = %q, want %q", tt.counts, got, tt.want)
		}
	}
}

func TestProgressLine(t *testing.T) {
	at := time.Date(2026, 11, 15, 0, 49, 41, 0, time.UTC)
	tests := []struct {
		name string
		ev   engine.Event
		want string
	}{
		{"stage names the stage", engine.Event{Kind: engine.EventStage, SlotID: "s_1", Stage: schemas.Stage("prepare"), Message: "started", At: at},
			"00:49:41 stage          s_1 prepare started"},
		{"sealed run", engine.Event{Kind: engine.EventRunSealed, SlotID: "s_1", RunID: "r_2", Outcome: schemas.Outcome("pass"), At: at},
			"00:49:41 run_sealed     s_1 r_2 pass"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := progressLine(tt.ev); got != tt.want {
				t.Fatalf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestPartDrift(t *testing.T) {
	frozen := map[string]string{"gate/g1": "1.0.0", "metric/steps": "1.0.0", "grader/old": "1.0.0"}
	now := map[string]string{"gate/g1": "1.1.0", "metric/steps": "1.0.0", "grader/new": "1.0.0"}
	changed, added := partDrift(frozen, now)
	wantChanged := []string{"gate/g1: frozen at 1.0.0, now 1.1.0", "grader/old: frozen at 1.0.0, no longer registered"}
	if !slices.Equal(changed, wantChanged) || !slices.Equal(added, []string{"grader/new"}) {
		t.Fatalf("changed %q added %q", changed, added)
	}
	if c, a := partDrift(now, now); len(c) != 0 || len(a) != 0 {
		t.Fatalf("no drift: changed %q added %q", c, a)
	}
}

// driftApp returns an app over a store holding experiment e_drift frozen
// with parts, and the current parts this app would run with.
func driftApp(t *testing.T, parts func(now map[string]string) map[string]string) (*app, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "runs")
	st, err := store.NewFSStore(root, schemas.NoopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := scaffold.ProjectDefault(pricingDefault)
	if err != nil {
		t.Fatal(err)
	}
	prices, err := evidence.ParsePrices(raw, "embedded")
	if err != nil {
		t.Fatal(err)
	}
	a := &app{cfg: &lib.Config{Root: t.TempDir()}, logger: schemas.NoopLogger{}, store: st, prices: prices,
		reg: &schemas.Registry{Policies: verdict.Builtin(), Reporters: reporters.Builtin()}}
	m := &schemas.Manifest{ExpID: "e_drift", Hash: "sha256:m", Parts: parts(engine.PartVersions(a.reg)),
		Slots: []schemas.Slot{{SlotID: "s_1", PairID: "p_1", Task: "t01", Arm: "A", Condition: schemas.ConditionClean, Trial: 1, Counted: true}}}
	if err := st.CreateExperiment(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return a, m.ExpID
}

// writeSealedCost lays out a run folder with a seal.json and cost rows
// carrying ref, as a sealed run's cost.json does.
func writeSealedCost(t *testing.T, a *app, expID, runID, ref string) {
	t.Helper()
	dir := filepath.Join(a.store.Root(), expID, "slots", "s_1", "runs", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"seal.json": "{}", "cost.json": `[{"request_id":"q1","price_ref":"` + ref + `"}]`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResumeVersionDrift(t *testing.T) {
	same := func(now map[string]string) map[string]string { return now }
	older := func(now map[string]string) map[string]string {
		out := maps.Clone(now)
		out["verdict_policy/paired_signflip"] = "0.9.0"
		return out
	}
	tests := []struct {
		name     string
		parts    func(map[string]string) map[string]string
		priceRef string // a sealed run costed with this ref; "" for none, "now" for the current snapshot
		allow    bool
		resumes  bool
		recorded bool
	}{
		{name: "nothing changed", parts: same, resumes: true},
		{name: "same price snapshot", parts: same, priceRef: "now", resumes: true},
		{name: "a frozen part changed", parts: older, resumes: false},
		{name: "a frozen part changed, allowed", parts: older, allow: true, resumes: true, recorded: true},
		{name: "another price snapshot", parts: same, priceRef: "old-snapshot@sha256:00", resumes: false},
		{name: "another price snapshot, allowed", parts: same, priceRef: "old-snapshot@sha256:00", allow: true, resumes: true, recorded: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, expID := driftApp(t, tt.parts)
			switch tt.priceRef {
			case "":
			case "now":
				writeSealedCost(t, a, expID, "r_1", a.prices.Ref())
			default:
				writeSealedCost(t, a, expID, "r_1", tt.priceRef)
			}
			m, code := a.manifestFor(context.Background(), "", expID, tt.allow)
			if (m != nil) != tt.resumes {
				t.Fatalf("resumed %v (exit %d), want %v", m != nil, code, tt.resumes)
			}
			if !tt.resumes && code != exitInfra {
				t.Fatalf("exit %d, want %d", code, exitInfra)
			}
			b, err := os.ReadFile(filepath.Join(a.store.Root(), expID, resumesFile))
			if tt.recorded != (err == nil) {
				t.Fatalf("resumes.jsonl recorded %v (%v), want %v", err == nil, err, tt.recorded)
			}
			if tt.recorded && !strings.Contains(string(b), `"allow_version_drift":true`) {
				t.Fatalf("resumes.jsonl = %s", b)
			}
		})
	}
}
