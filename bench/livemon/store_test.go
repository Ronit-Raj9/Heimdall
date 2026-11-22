package livemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	s := Open(root)
	ms, err := s.List()
	if err != nil || len(ms) != 0 {
		t.Fatalf("empty list: %v %v", ms, err)
	}
	m := Monitor{ID: NewID(), Agent: "a", Benchmark: "b", BudgetUSDPerDay: 2, CreatedAt: time.Now()}
	unlock := s.Lock()
	if err := s.Save(m, map[string]any{"name": "x"}); err != nil {
		t.Fatal(err)
	}
	unlock()
	if _, err := os.Stat(filepath.Join(root, EvaluationRef(m.ID))); err != nil {
		t.Fatalf("evaluation spec not written: %v", err)
	}
	got, err := s.Get(m.ID)
	if err != nil || got.Agent != "a" {
		t.Fatalf("get: %+v %v", got, err)
	}
	st, err := s.State(m.ID)
	if err != nil || len(st.Samples) != 0 {
		t.Fatalf("state: %+v %v", st, err)
	}
	for i := 0; i < MaxSamples+5; i++ {
		st.Samples = append(st.Samples, Sample{EvaluationID: "ev_x", Status: SampleOK})
	}
	st.Samples[len(st.Samples)-1].EvaluationID = "ev_last"
	if err := s.SaveState(m.ID, st); err != nil {
		t.Fatal(err)
	}
	st2, _ := s.State(m.ID)
	if len(st2.Samples) != MaxSamples || !st2.HasEvaluation("ev_last") {
		t.Fatalf("samples kept: %d", len(st2.Samples))
	}
	if err := s.Delete(m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if _, err := s.Get("../x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad id: %v", err)
	}
}

func TestSaveStateKeepsOpenIncidents(t *testing.T) {
	s := Open(t.TempDir())
	id := NewID()
	st := &State{}
	st.Incidents = append(st.Incidents, Incident{ID: "inc_open", State: StateOpen})
	for i := 0; i < MaxIncidents+3; i++ {
		st.Incidents = append(st.Incidents, Incident{ID: "inc_old", State: StateResolved})
	}
	if err := s.SaveState(id, st); err != nil {
		t.Fatal(err)
	}
	got, _ := s.State(id)
	if len(got.Incidents) != MaxIncidents || got.Incidents[0].ID != "inc_open" {
		t.Fatalf("kept %d, first %s", len(got.Incidents), got.Incidents[0].ID)
	}
}
