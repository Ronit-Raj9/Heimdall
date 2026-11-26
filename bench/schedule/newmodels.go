package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// NewModelWatch turns an evaluation schedule into a new model watch: on each
// trigger Heimdall reads Bifrost's newest models of Providers and starts the
// schedule's evaluation once per model it has not seen, comparing it with
// the evaluation's arm Current.
type NewModelWatch struct {
	Providers   []string `json:"providers" yaml:"providers"`                         // e.g. [anthropic, openai]; 1 to 5
	Current     string   `json:"current" yaml:"current"`                             // label of the saved evaluation's arm that is today's model
	Match       string   `json:"match,omitempty" yaml:"match,omitempty"`             // optional: a new model id must contain this text
	MaxPerDay   int      `json:"max_per_day,omitempty" yaml:"max_per_day,omitempty"` // evaluations started per day; default 1, at most 3
	USDPerModel float64  `json:"usd_per_model" yaml:"usd_per_model"`                 // the hard budget of each started evaluation (a Bifrost customer budget)
}

// PerDay is MaxPerDay with its default: 0 means 1.
func (w NewModelWatch) PerDay() int {
	if w.MaxPerDay < 1 {
		return 1
	}
	return w.MaxPerDay
}

// Watch item states.
const (
	WatchClaimed = "claimed"
	WatchStarted = "started"
	WatchFailed  = "failed"
	WatchSkipped = "skipped"
)

// WatchState is the state file of one new model watch. The Gateway report
// card reads it with its own copy of these types, so the JSON is fixed.
type WatchState struct {
	Schedule  string               `json:"schedule"`
	Baseline  map[string][]string  `json:"baseline"` // provider -> model ids listed at the first check; never evaluated
	Seen      map[string]WatchItem `json:"seen"`     // "<provider>/<model>" -> what happened
	CheckedAt time.Time            `json:"checked_at"`
	LastError string               `json:"last_error,omitempty"`
}

// WatchItem is what happened to one new model.
type WatchItem struct {
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	FirstSeen    time.Time `json:"first_seen"`
	State        string    `json:"state"` // claimed | started | failed | skipped
	EvaluationID string    `json:"evaluation_id,omitempty"`
	Reason       string    `json:"reason,omitempty"` // plain words
	ClaimedAt    time.Time `json:"claimed_at"`
}

// validateNewModels checks the new model watch of an evaluation schedule
// against its saved evaluation spec.
func (s Schedule) validateNewModels(spec *schemas.EvaluationSpec) error {
	w := s.NewModels
	if w == nil {
		return nil
	}
	if n := len(w.Providers); n < 1 || n > 5 {
		return fmt.Errorf("new_models.providers needs 1 to 5 providers, got %d", n)
	}
	known := map[string]bool{}
	for _, p := range schemas.BifrostProviders() {
		known[p.ID] = true
	}
	for _, p := range w.Providers {
		if !known[p] {
			return fmt.Errorf("new_models.providers: %q is not a provider Bifrost knows", p)
		}
	}
	found := false
	if spec != nil {
		for _, a := range spec.Arms {
			if a.Label == w.Current {
				found = true
			}
		}
	}
	if !found {
		return fmt.Errorf("new_models.current: the saved evaluation has no setup named %q", w.Current)
	}
	if !finitePositive(w.USDPerModel) {
		return errors.New("new_models.usd_per_model must be a positive number of USD")
	}
	if w.MaxPerDay < 0 || w.MaxPerDay > 3 {
		return fmt.Errorf("new_models.max_per_day must be 0 to 3, got %d", w.MaxPerDay)
	}
	if n := w.PerDay(); w.USDPerModel*float64(n) > s.MaxUSDPerDay {
		return fmt.Errorf("the watch could start %d evaluations of %.2f USD a day, above max_usd_per_day %.2f USD", n, w.USDPerModel, s.MaxUSDPerDay)
	}
	if c, err := ParseCron(s.Cron); err == nil && cronMoreThanHourly(c) {
		return errors.New("a new model watch checks at most once an hour")
	}
	return nil
}

// cronMoreThanHourly reports whether two firings of c over the next eight
// days come less than an hour apart (UTC, so no clock change interferes).
func cronMoreThanHourly(c *Cron) bool {
	t := c.Next(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC))
	for i := 0; i < 200 && !t.IsZero(); i++ {
		n := c.Next(t)
		if n.IsZero() {
			return false
		}
		if n.Sub(t) < time.Hour {
			return true
		}
		t = n
	}
	return false
}

// WatchStatePath is the state file of the watch of schedule name.
func WatchStatePath(root, name string) string {
	return filepath.Join(root, ".heimdall", "gateway", "model-watch", name+".json")
}

// LoadWatchState reads the state of the watch of schedule name; nil and no
// error when the watch has not checked yet.
func LoadWatchState(root, name string) (*WatchState, error) {
	b, err := os.ReadFile(WatchStatePath(root, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st WatchState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("model watch state %s: %w", name, err)
	}
	if st.Seen == nil {
		st.Seen = map[string]WatchItem{}
	}
	if st.Baseline == nil {
		st.Baseline = map[string][]string{}
	}
	return &st, nil
}

// SaveWatchState writes st atomically (0600, synced before the rename), so
// a claim is on disk before the evaluation it guards starts.
func SaveWatchState(root string, st *WatchState) error {
	p := WatchStatePath(root, st.Schedule)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".watch-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
