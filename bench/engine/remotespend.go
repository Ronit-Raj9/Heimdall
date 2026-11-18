package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// DefaultUnknownRunUSD is what the runner spend cap charges a run whose cost
// was not observed and whose experiment sets no budget.reserve_usd.
const DefaultUnknownRunUSD = 0.5

// runnerSpend is a runner's own spend cap per UTC day (Runners area,
// docs/runners.md), the backstop of a runner that serves strangers' suites,
// such as the demo runner. It is checked before each lease, so up to Slots
// runs already in flight may pass it. The running total is kept in a file so
// a restart does not reset it.
type runnerSpend struct {
	capUSD float64
	path   string
	now    func() time.Time

	mu    sync.Mutex
	state spendState
}

// spendState is the spend file.
type spendState struct {
	Day      string  `json:"day"` // UTC, 2006-01-02
	SpentUSD float64 `json:"spent_usd"`
	Runs     int     `json:"runs"`
	Unknown  int     `json:"unknown_cost_runs"`
}

func newRunnerSpend(capUSD float64, path string) (*runnerSpend, error) {
	if capUSD <= 0 {
		return nil, nil
	}
	s := &runnerSpend{capUSD: capUSD, path: path, now: time.Now}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("spend file: %w", err)
	default:
		if err := json.Unmarshal(b, &s.state); err != nil {
			return nil, fmt.Errorf("spend file %s: %w", path, err)
		}
	}
	return s, nil
}

// rollLocked starts a new day's total. The caller holds mu.
func (s *runnerSpend) rollLocked() {
	if day := s.now().UTC().Format(time.DateOnly); s.state.Day != day {
		s.state = spendState{Day: day}
	}
}

// reached reports whether today's spend is at or past the cap, and the
// total.
func (s *runnerSpend) reached() (bool, float64) {
	if s == nil {
		return false, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollLocked()
	return s.state.SpentUSD >= s.capUSD, s.state.SpentUSD
}

// add charges one finished run: its observed cost, else the experiment's
// reserve, else DefaultUnknownRunUSD. A run whose agent never started costs
// nothing.
func (s *runnerSpend) add(res *SlotResult, m *schemas.Manifest) error {
	if s == nil || res == nil || res.Run == nil {
		return nil
	}
	c, ok := runCost(res.Metrics, res.Run)
	unknown := !ok
	if unknown {
		c = DefaultUnknownRunUSD
		if m != nil && m.Experiment.Budget.ReserveUSD > 0 {
			c = m.Experiment.Budget.ReserveUSD
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollLocked()
	s.state.SpentUSD += c
	s.state.Runs++
	if unknown {
		s.state.Unknown++
	}
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
