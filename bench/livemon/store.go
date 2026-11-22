package livemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Store caps.
const (
	MaxMonitors   = 50
	MaxSamples    = 2000
	MaxIncidents  = 200
	evaluationDoc = "evaluation.json"
)

// State is what a monitor has observed.
type State struct {
	Samples   []Sample   `json:"samples"`
	Incidents []Incident `json:"incidents"`
	// LastRecordAt is the finish time of the newest schedule record read,
	// so a check that never started is recorded once.
	LastRecordAt time.Time `json:"last_record_at,omitzero"`
}

// HasEvaluation reports whether a sample of evaluation id exists.
func (s *State) HasEvaluation(id string) bool {
	return slices.ContainsFunc(s.Samples, func(x Sample) bool { return x.EvaluationID == id })
}

// Store keeps one project's monitors under <root>/.heimdall/monitors/<id>/:
// monitor.json (the definition), state.json (samples and incidents) and
// evaluation.json (the evaluation spec the monitor's schedule repeats).
// The folder is outside every area the project files API serves.
type Store struct{ dir string }

var locks sync.Map // dir -> *sync.Mutex

// Dir is the monitors folder of a project root.
func Dir(root string) string { return filepath.Join(root, ".heimdall", "monitors") }

// EvaluationRef is the project-relative path of a monitor's evaluation
// spec, as the schedule names it.
func EvaluationRef(id string) string {
	return filepath.ToSlash(filepath.Join(".heimdall", "monitors", id, evaluationDoc))
}

// Open returns the store of a project root. Nothing is created until a write.
func Open(root string) *Store { return &Store{dir: Dir(root)} }

// Lock serializes every write of the store's project in this process.
func (s *Store) Lock() func() {
	v, _ := locks.LoadOrStore(s.dir, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Store) path(id, name string) string { return filepath.Join(s.dir, id, name) }

// List returns every monitor, oldest first. A broken file is skipped.
func (s *Store) List() ([]Monitor, error) {
	ents, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Monitor{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	out := []Monitor{}
	for _, e := range ents {
		if !e.IsDir() || !ValidID(e.Name()) {
			continue
		}
		m, err := s.Get(e.Name())
		if err != nil {
			continue
		}
		out = append(out, *m)
	}
	slices.SortFunc(out, func(a, b Monitor) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// Get reads one monitor.
func (s *Store) Get(id string) (*Monitor, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	var m Monitor
	if err := readJSON(s.path(id, "monitor.json"), &m); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, err
	}
	return &m, nil
}

// Save writes the monitor and the evaluation spec its schedule repeats.
// The caller holds Lock.
func (s *Store) Save(m Monitor, spec any) error {
	if !ValidID(m.ID) {
		return fmt.Errorf("%w: id %q", ErrInvalid, m.ID)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, m.ID), 0o700); err != nil {
		return fmt.Errorf("create monitor folder: %w", err)
	}
	if err := writeJSON(s.path(m.ID, evaluationDoc), spec); err != nil {
		return fmt.Errorf("save monitor evaluation: %w", err)
	}
	if err := writeJSON(s.path(m.ID, "monitor.json"), m); err != nil {
		return fmt.Errorf("save monitor: %w", err)
	}
	return nil
}

// Delete removes a monitor with its samples and incidents. The evaluations
// and runs it started stay. The caller holds Lock.
func (s *Store) Delete(id string) error {
	if _, err := s.Get(id); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.dir, id))
}

// State reads a monitor's samples and incidents; none yet is empty.
func (s *Store) State(id string) (*State, error) {
	st := &State{Samples: []Sample{}, Incidents: []Incident{}}
	if err := readJSON(s.path(id, "state.json"), st); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if st.Samples == nil {
		st.Samples = []Sample{}
	}
	if st.Incidents == nil {
		st.Incidents = []Incident{}
	}
	return st, nil
}

// SaveState writes a monitor's state, keeping the newest MaxSamples
// samples and MaxIncidents incidents (open ones always stay). The caller
// holds Lock.
func (s *Store) SaveState(id string, st *State) error {
	if len(st.Samples) > MaxSamples {
		st.Samples = st.Samples[len(st.Samples)-MaxSamples:]
	}
	if len(st.Incidents) > MaxIncidents {
		var keep []Incident
		drop := len(st.Incidents) - MaxIncidents
		for _, inc := range st.Incidents {
			if drop > 0 && inc.State != StateOpen {
				drop--
				continue
			}
			keep = append(keep, inc)
		}
		st.Incidents = keep
	}
	return writeJSON(s.path(id, "state.json"), st)
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", filepath.Base(p), err)
	}
	return nil
}

func writeJSON(p string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
