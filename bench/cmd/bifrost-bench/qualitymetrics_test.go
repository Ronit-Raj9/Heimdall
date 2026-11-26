package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/selfmetrics"
)

// qualityFakeSource is a leaderboard.Source over in-memory experiments.
type qualityFakeSource struct {
	manifests map[string]*schemas.Manifest
	runs      map[string][]leaderboard.Run
	files     map[string][]byte
	decisions map[string]schemas.Decision
}

func newQualityFakeSource() *qualityFakeSource {
	return &qualityFakeSource{manifests: map[string]*schemas.Manifest{}, runs: map[string][]leaderboard.Run{}, files: map[string][]byte{}, decisions: map[string]schemas.Decision{}}
}

func (s *qualityFakeSource) Experiments() []string {
	var out []string
	for id := range s.manifests {
		out = append(out, id)
	}
	return out
}
func (s *qualityFakeSource) Manifest(id string) (*schemas.Manifest, bool) {
	m, ok := s.manifests[id]
	return m, ok
}
func (s *qualityFakeSource) Runs(id string) []leaderboard.Run { return s.runs[id] }
func (s *qualityFakeSource) RunFile(runID, name string) ([]byte, error) {
	if b, ok := s.files[runID+"/"+name]; ok {
		return b, nil
	}
	return nil, fs.ErrNotExist
}
func (s *qualityFakeSource) LatestDecision(id string) (schemas.Decision, bool) {
	d, ok := s.decisions[id]
	return d, ok
}

// add records one experiment: each arm label maps to provider and model; every
// arm runs tasks t0..t(n-1) once, passing the first pass tasks, at 0.1 USD.
func (s *qualityFakeSource) add(exp string, created time.Time, arms map[string][3]string, tasks, pass int) *schemas.Manifest {
	m := &schemas.Manifest{ExpID: exp, SuiteID: "coding-core", SuiteVersion: "1.0.0", CreatedAt: created, Arms: map[string]schemas.ArmManifest{}}
	for id, a := range arms {
		cfg := schemas.AgentConfig{ID: "agent-x", Model: a[2]}
		cfg.Gateway.Provider = a[1]
		m.Arms[id] = schemas.ArmManifest{Label: a[0], Config: cfg}
		for i := 0; i < tasks; i++ {
			rid := fmt.Sprintf("%s-%s-%d", exp, id, i)
			out := schemas.OutcomeFail
			if i < pass {
				out = schemas.OutcomePass
			}
			slot := schemas.Slot{SlotID: rid, ExpID: exp, Task: fmt.Sprintf("t%d", i), Arm: id, Condition: schemas.ConditionClean, Trial: 1}
			s.runs[exp] = append(s.runs[exp], leaderboard.Run{Slot: slot, Run: schemas.Run{RunID: rid, Outcome: out, Attempt: 1}, Sealed: true})
			cost := 0.1
			b, _ := json.Marshal(map[string]schemas.Value{"cost_usd": schemas.Observed(cost, schemas.EvidenceGateway)})
			s.files[rid+"/metrics.json"] = b
		}
	}
	s.manifests[exp] = m
	return m
}

var qualityNow = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

func TestQualitySamplesLabelsBounded(t *testing.T) {
	src := newQualityFakeSource()
	src.add("e_1", qualityNow.Add(-time.Hour), map[string][3]string{"A": {"agent on sonnet", "anthropic", "anthropic/claude-sonnet-4-5"}}, 4, 3)
	snap := computeQualitySnapshot(context.Background(), []qualityOrg{{slug: "acme", src: src}}, qualityNow, false)
	for _, fam := range [][]selfmetrics.Sample{snap.solve, snap.graded, snap.costPerSolved} {
		if len(fam) != 1 {
			t.Fatalf("samples %+v", fam)
		}
		l := fam[0].Labels
		if len(l) != 3 || l["provider"] != "anthropic" || l["model"] != "claude-sonnet-4-5" || l["benchmark"] != "coding-core" {
			t.Fatalf("labels %v", l)
		}
	}
	if snap.solve[0].Value != 0.75 || snap.graded[0].Value != 4 || fmt.Sprintf("%.4f", snap.costPerSolved[0].Value) != "0.1333" {
		t.Fatalf("values %v %v %v", snap.solve, snap.graded, snap.costPerSolved)
	}
	snap = computeQualitySnapshot(context.Background(), []qualityOrg{{slug: "acme", src: src}}, qualityNow, true)
	if l := snap.solve[0].Labels; len(l) != 4 || l["org"] != "acme" {
		t.Fatalf("hosted labels %v", l)
	}
}

func TestQualityExcludesSyntheticInvalidAndVariants(t *testing.T) {
	src := newQualityFakeSource()
	good := map[string][3]string{"A": {"agent", "anthropic", "claude-sonnet-4-5"}}
	src.add("e_good", qualityNow.Add(-time.Hour), good, 2, 1)
	src.add("e_old", qualityNow.Add(-8*24*time.Hour), map[string][3]string{"A": {"agent", "openai", "old-model"}}, 2, 1)
	src.add("e_smoke", qualityNow.Add(-time.Hour), map[string][3]string{"A": {"agent", "openai", "smoke-model"}}, 2, 1).Experiment.Smoke = true
	src.add("e_scripted", qualityNow.Add(-time.Hour), map[string][3]string{"A": {"agent", "openai", "scripted-model"}}, 2, 1).Experiment.ScriptedUpstream = true
	src.add("e_invalid", qualityNow.Add(-time.Hour), map[string][3]string{"A": {"agent", "openai", "invalid-model"}}, 2, 1)
	src.decisions["e_invalid"] = schemas.DecisionInvalid
	src.add("e_rerun", qualityNow.Add(-time.Hour), map[string][3]string{"A": {"agent", "openai", "rerun-model"}}, 2, 1).Experiment.RerunOf = "e_good"
	src.add("e_debug", qualityNow.Add(-time.Hour), map[string][3]string{"A": {"agent", "openai", "debug-model"}}, 2, 1).Experiment.Debug = &schemas.DebugSpec{}
	src.add("e_cache", qualityNow.Add(-time.Hour), map[string][3]string{"B": {"agent" + schemas.EvaluationCacheSuffix, "openai", "cache-model"}}, 2, 1)
	m := src.add("e_adapter", qualityNow.Add(-time.Hour), map[string][3]string{"A": {"agent", "openai", "adapter-model"}}, 2, 1)
	arm := m.Arms["A"]
	arm.Config.Adapter = "scripted"
	m.Arms["A"] = arm
	src.add("e_practice", qualityNow.Add(-time.Hour), map[string][3]string{"A": {"agent", "openai", "practice-model"}}, 2, 1)
	for i := range src.runs["e_practice"] {
		src.runs["e_practice"][i].Run.Practice = true
	}
	snap := computeQualitySnapshot(context.Background(), []qualityOrg{{slug: "acme", src: src}}, qualityNow, false)
	if len(snap.solve) != 1 || snap.solve[0].Labels["model"] != "claude-sonnet-4-5" {
		t.Fatalf("samples %+v", snap.solve)
	}
}

func TestQualityCapsSeries(t *testing.T) {
	src := newQualityFakeSource()
	for i := 0; i < 120; i++ {
		src.add(fmt.Sprintf("e_%03d", i), qualityNow.Add(-time.Duration(i)*time.Minute), map[string][3]string{"A": {"agent", "openai", fmt.Sprintf("m%03d", i)}}, 1+i%3, 1)
	}
	snap := computeQualitySnapshot(context.Background(), []qualityOrg{{slug: "acme", src: src}}, qualityNow, false)
	if len(snap.solve) != qualityMaxSeries || snap.dropped != 20 {
		t.Fatalf("series %d dropped %v", len(snap.solve), snap.dropped)
	}
	ones := 0
	for _, s := range snap.graded {
		if s.Value == 1 {
			ones++
		}
	}
	if ones != 20 {
		t.Fatalf("kept %d series with one graded run, want 20", ones)
	}
}

func familyNames(fams []selfmetrics.Family) map[string]selfmetrics.Family {
	out := map[string]selfmetrics.Family{}
	for _, f := range fams {
		out[f.Name] = f
	}
	return out
}

func TestQualityHostedOffByDefault(t *testing.T) {
	t.Setenv("HEIMDALL_QUALITY_METRICS", "")
	reg := selfmetrics.New()
	registerQualityMetrics(reg, &jobsHub{hosted: true, orgs: map[string]*launcher{}})
	if _, ok := familyNames(reg.Gather(context.Background()))["heimdall_solve_rate"]; ok {
		t.Fatal("hosted server publishes quality metrics without HEIMDALL_QUALITY_METRICS=on")
	}
	t.Setenv("HEIMDALL_QUALITY_METRICS", "on")
	registerQualityMetrics(reg, &jobsHub{hosted: true, orgs: map[string]*launcher{}})
	if _, ok := familyNames(reg.Gather(context.Background()))["heimdall_solve_rate"]; !ok {
		t.Fatal("HEIMDALL_QUALITY_METRICS=on did not register the gauges")
	}
	reg = selfmetrics.New()
	t.Setenv("HEIMDALL_QUALITY_METRICS", "")
	registerQualityMetrics(reg, &jobsHub{orgs: map[string]*launcher{}})
	fams := familyNames(reg.Gather(context.Background()))
	for _, name := range []string{"heimdall_solve_rate", "heimdall_graded_runs", "heimdall_cost_per_solved_task_usd", "heimdall_quality_series_dropped"} {
		if _, ok := fams[name]; !ok {
			t.Fatalf("local server misses %s", name)
		}
	}
}

func TestQualityCollectNeverComputes(t *testing.T) {
	q := &qualityMetrics{orgs: func(context.Context) []qualityOrg { panic("a scrape read the run store") }}
	reg := selfmetrics.New()
	q.register(reg)
	q.store(qualitySnapshot{solve: []selfmetrics.Sample{{Labels: map[string]string{"provider": "p", "model": "m", "benchmark": "b"}, Value: 0.5}}, dropped: 2})
	fams := familyNames(reg.Gather(context.Background()))
	if f := fams["heimdall_solve_rate"]; len(f.Samples) != 1 || f.Samples[0].Value != 0.5 {
		t.Fatalf("solve rate %+v", f)
	}
	if f := fams["heimdall_quality_series_dropped"]; len(f.Samples) != 1 || f.Samples[0].Value != 2 {
		t.Fatalf("dropped %+v", f)
	}
}
