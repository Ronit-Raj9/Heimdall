package forecast

import (
	"context"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeStore is a synthetic run store: manifests and sealed runs by
// experiment id. It records which experiments had their runs read.
type fakeStore struct {
	manifests map[string]*schemas.Manifest
	runs      map[string][]schemas.SealedRun
	opened    map[string]bool
}

func (s *fakeStore) ListExperiments(context.Context) ([]string, error) {
	var ids []string
	for id := range s.manifests {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *fakeStore) LoadManifest(_ context.Context, id string) (*schemas.Manifest, error) {
	m, ok := s.manifests[id]
	if !ok {
		return nil, schemas.ErrNotFound
	}
	return m, nil
}

func (s *fakeStore) SealedRuns(_ context.Context, id, source string) ([]schemas.SealedRun, error) {
	if source != schemas.GradeSourceOriginal {
		return nil, errors.New("history must read original grades")
	}
	s.opened[id] = true
	return s.runs[id], nil
}

// pastExperiment is a sealed experiment on suite with one arm A of config
// cfg; every run passes with the given cost.
func pastExperiment(id, suite string, cfg schemas.AgentConfig, hash string, costs ...float64) (*schemas.Manifest, []schemas.SealedRun) {
	m := &schemas.Manifest{ExpID: id, SuiteID: suite, SuiteVersion: "1",
		Arms: map[string]schemas.ArmManifest{"A": {ConfigHash: hash, Config: cfg}}}
	var runs []schemas.SealedRun
	for i, c := range costs {
		runs = append(runs, schemas.SealedRun{
			Slot: schemas.Slot{Arm: "A", Task: taskID(i%2 + 1), Counted: true},
			Run:  schemas.Run{RunID: id + "_" + taskID(i), Outcome: schemas.OutcomePass},
			Metrics: map[string]schemas.Value{
				leaderboard.MetricCostUSD: schemas.Observed(c, schemas.EvidenceGateway),
			},
		})
	}
	return m, runs
}

func TestLoadHistory(t *testing.T) {
	codexA := schemas.AgentConfig{ID: "codex", Model: "m-a", Adapter: "command"}
	st := &fakeStore{manifests: map[string]*schemas.Manifest{}, runs: map[string][]schemas.SealedRun{}, opened: map[string]bool{}}
	add := func(m *schemas.Manifest, runs []schemas.SealedRun) {
		st.manifests[m.ExpID], st.runs[m.ExpID] = m, runs
	}
	add(pastExperiment("e_live", "s", codexA, "hashA", 0.1, 0.2, 0.3))
	add(pastExperiment("e_other_suite", "q", codexA, "hashA", 9))
	add(pastExperiment("e_unrelated", "s", schemas.AgentConfig{ID: "claude", Model: "x"}, "hashZ", 9))
	smoke, smokeRuns := pastExperiment("e_smoke", "s", codexA, "hashA", 7, 7)
	smoke.Experiment.Smoke = true
	add(smoke, smokeRuns)
	scripted, scriptedRuns := pastExperiment("e_scripted_model", "s", codexA, "hashA", 8)
	add(scripted, scriptedRuns)
	bad, badRuns := pastExperiment("e_bad", "s", codexA, "hashA", 5, 5, 5)
	badRuns[0].Problems = []string{"seal does not verify"}
	badRuns[1].Run.Outcome = schemas.OutcomeInfraError
	add(bad, badRuns)
	read := func(sr *schemas.SealedRun, name string) ([]byte, bool, error) {
		if sr.Run.RunID == "e_scripted_model_t0" {
			return []byte(`{"kind":"message","action":"pass","upstream":"scripted","status":200}`), true, nil
		}
		return nil, false, nil
	}
	m := plannedManifest(2, 1, 0)
	h, err := LoadHistory(context.Background(), st, m, read)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Runs) != 4 || h.Synthetic != 3 || h.Problems != 1 || h.Ungraded != 1 {
		t.Fatalf("runs %d synthetic %d problems %d ungraded %d", len(h.Runs), h.Synthetic, h.Problems, h.Ungraded)
	}
	if st.opened["e_other_suite"] || st.opened["e_unrelated"] {
		t.Fatalf("opened runs of an experiment that cannot inform the forecast: %v", st.opened)
	}
	if len(h.Notes()) != 3 {
		t.Fatalf("notes %v", h.Notes())
	}
	f := Estimate(Input{Manifest: m, History: h.Runs, Notes: h.Notes()})
	if f.Arms[0].Basis != schemas.BasisConfigHash || f.Arms[0].HistoryRuns != 4 || f.Arms[1].Basis != schemas.BasisNone {
		t.Fatalf("arms %+v", f.Arms)
	}
	// A judged suite reads every experiment on it for judge records.
	m.Scenario = map[string]string{"suite.judge": "jh"}
	if _, err := LoadHistory(context.Background(), st, m, nil); err != nil || !st.opened["e_unrelated"] {
		t.Fatalf("judged suite did not read every experiment on it (err %v)", err)
	}
}
