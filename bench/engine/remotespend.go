package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
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
	if math.IsNaN(capUSD) || math.IsInf(capUSD, 0) { // QC2-07-11: a non-finite cap never trips
		return nil, fmt.Errorf("spend cap %v is not a finite number", capUSD)
	}
	if capUSD <= 0 {
		return nil, nil
	}
	s := &runnerSpend{capUSD: capUSD, path: path, now: time.Now}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// load reads the spend file into state; a missing file is an empty total.
// The caller holds mu or owns s alone.
func (s *runnerSpend) load() error {
	if s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("spend file: %w", err)
	}
	var st spendState
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("spend file %s: %w", s.path, err)
	}
	if math.IsNaN(st.SpentUSD) || math.IsInf(st.SpentUSD, 0) || st.SpentUSD < 0 {
		return fmt.Errorf("spend file %s: spent_usd %v is not a finite, non-negative number", s.path, st.SpentUSD)
	}
	s.state = st
	return nil
}

// rollLocked starts a new day's total. The caller holds mu.
func (s *runnerSpend) rollLocked() {
	if day := s.now().UTC().Format(time.DateOnly); s.state.Day != day {
		s.state = spendState{Day: day}
	}
}

// reached reports whether today's spend is at or past the cap, and the
// total. It reads the file again, so a total another runner process on the
// same runs root added is seen; a file that cannot be read counts as
// reached (fail closed).
func (s *runnerSpend) reached() (bool, float64) {
	if s == nil {
		return false, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return true, s.state.SpentUSD
	}
	s.rollLocked()
	return s.state.SpentUSD >= s.capUSD, s.state.SpentUSD
}

// add charges one finished run: its observed cost, else its observed part
// plus the experiment's reserve, else plus DefaultUnknownRunUSD. A run whose
// agent never started costs nothing. Under an interprocess lock it reads the
// file again and adds to that total, so runner processes sharing one runs
// root never overwrite each other's spend (QC2-07-09).
func (s *runnerSpend) add(res *SlotResult, m *schemas.Manifest) error {
	if s == nil || res == nil || res.Run == nil {
		return nil
	}
	c, ok := spendCost(res.Dir, res.Metrics, res.Run)
	unknown := !ok
	if unknown { // the observed subtotal stays; the estimate covers only the unknown rest (QC2-07-01)
		est := DefaultUnknownRunUSD
		if m != nil && m.Experiment.Budget.ReserveUSD > 0 {
			est = m.Experiment.Budget.ReserveUSD
		}
		c += est
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
			return err
		}
		unlock, err := lockSpendFile(s.path)
		if err != nil {
			return err
		}
		defer unlock()
		if err := s.load(); err != nil {
			return err
		}
	}
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
	tmp, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(b)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
