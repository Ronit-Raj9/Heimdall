package gwfeature

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"testing"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeSource is a leaderboard.Source over in-memory runs and run files.
type fakeSource struct {
	manifests map[string]*schemas.Manifest
	runs      map[string][]leaderboard.Run
	files     map[string]map[string][]byte // run id, file name
	reads     int
	decided   map[string]bool     // experiments with an analysis
	invalid   map[string][]string // experiments whose newest analysis is invalid, with its reasons
}

func newFakeSource() *fakeSource {
	return &fakeSource{manifests: map[string]*schemas.Manifest{}, runs: map[string][]leaderboard.Run{}, files: map[string]map[string][]byte{}}
}

func (f *fakeSource) Experiments() []string {
	var out []string
	for id := range f.manifests {
		out = append(out, id)
	}
	return out
}

func (f *fakeSource) Manifest(expID string) (*schemas.Manifest, bool) {
	m, ok := f.manifests[expID]
	return m, ok
}

func (f *fakeSource) Runs(expID string) []leaderboard.Run { return f.runs[expID] }

func (f *fakeSource) RunFile(runID, name string) ([]byte, error) {
	f.reads++
	if b, ok := f.files[runID][name]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("%s/%s: %w", runID, name, fs.ErrNotExist)
}

func (f *fakeSource) LatestDecision(expID string) (schemas.Decision, bool) {
	if f.invalid[expID] != nil {
		return schemas.DecisionInvalid, true
	}
	return "", f.decided[expID]
}

func (f *fakeSource) LatestInvalid(expID string) (string, []string, bool) {
	r, ok := f.invalid[expID]
	return "experiment", r, ok
}

func (f *fakeSource) setFile(runID, name string, data []byte) {
	if f.files[runID] == nil {
		f.files[runID] = map[string][]byte{}
	}
	f.files[runID][name] = data
}

// addRun adds one sealed run with its metrics.
func (f *fakeSource) addRun(t *testing.T, expID, runID, task, arm, cond string, trial, attempt int, success bool, cost float64) *leaderboard.Run {
	t.Helper()
	r := leaderboard.Run{
		Slot:   schemas.Slot{SlotID: fmt.Sprintf("%s-%s-%s-%d", task, arm, cond, trial), ExpID: expID, Task: task, Arm: arm, Condition: cond, Trial: trial},
		Run:    schemas.Run{RunID: runID, Attempt: attempt},
		Sealed: true,
	}
	f.runs[expID] = append(f.runs[expID], r)
	s := 0.0
	if success {
		s = 1
	}
	m, _ := json.Marshal(map[string]schemas.Value{"verified_success": {V: &s}, "cost_usd": {V: &cost}})
	f.setFile(runID, "metrics.json", m)
	return &f.runs[expID][len(f.runs[expID])-1]
}
