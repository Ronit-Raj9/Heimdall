package forecast

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Run metrics the forecast reads from each run's metrics.json, as the
// leaderboard names them.
const (
	metricCost   = leaderboard.MetricCostUSD
	metricTokens = leaderboard.MetricTokens
	metricWall   = leaderboard.MetricWallTime
)

// Store is the read side of the run store history comes from.
// store.FSStore implements it; SealedRuns is its verified sealed-run index.
type Store interface {
	ListExperiments(ctx context.Context) ([]string, error)
	LoadManifest(ctx context.Context, expID string) (*schemas.Manifest, error)
	SealedRuns(ctx context.Context, expID, gradeSource string) ([]schemas.SealedRun, error)
}

// History is the sealed history of a planned experiment's suite, with what
// was left out.
type History struct {
	Runs      []HistoryRun
	Synthetic int // runs of synthetic arms (scripted agent, scripted model, smoke experiment)
	Problems  int // runs whose seal or slot did not verify
	Ungraded  int // runs whose outcome does not score (infra_error, invalid_evaluator, unknown)
}

// Notes says in plain words what the history left out.
func (h *History) Notes() []string {
	var out []string
	if h.Synthetic > 0 {
		out = append(out, fmt.Sprintf("left out %d synthetic run%s (scripted agent, scripted model or smoke experiment)", h.Synthetic, plural(h.Synthetic)))
	}
	if h.Problems > 0 {
		out = append(out, fmt.Sprintf("left out %d run%s whose seal did not verify", h.Problems, plural(h.Problems)))
	}
	if h.Ungraded > 0 {
		out = append(out, fmt.Sprintf("left out %d run%s whose outcome does not score", h.Ungraded, plural(h.Ungraded)))
	}
	return out
}

// LoadHistory reads every sealed run on the planned manifest's suite
// version that a forecast of it could rest on, through the store's
// SealedRuns with the original grades: runs of arms with the same config
// hash or the same agent id and model as a planned arm, and, for a judged
// suite, every run for its judge record. Synthetic arms (leaderboard
// SyntheticArms, reading each run's proxy ledger with read) are left out, as
// on the live leaderboard; so are runs SealedRuns flags with a problem and
// runs whose outcome does not score. An experiment on another suite is
// never opened beyond its manifest.
func LoadHistory(ctx context.Context, st Store, m *schemas.Manifest, read leaderboard.RunFileReader) (*History, error) {
	return loadHistory(ctx, st, m, read, false)
}

// LoadLibrary reads every sealed run on the planned manifest's suite
// version, whatever agent or model made it, with the same exclusions as
// LoadHistory. It is the library a first estimate rests on when no arm has
// history of its own (Input.Library): real runs of other agents on the same
// tasks, never a guess.
func LoadLibrary(ctx context.Context, st Store, m *schemas.Manifest, read leaderboard.RunFileReader) (*History, error) {
	return loadHistory(ctx, st, m, read, true)
}

func loadHistory(ctx context.Context, st Store, m *schemas.Manifest, read leaderboard.RunFileReader, all bool) (*History, error) {
	s, err := readSuite(ctx, st, m.SuiteID, m.SuiteVersion, read, func(past *schemas.Manifest) bool { return wanted(m, past, all) })
	if err != nil {
		return nil, err
	}
	return s.pick(m, all)
}

// SuiteRuns is the sealed history of one suite version in a store, kept per
// experiment, so a LoadHistory or LoadLibrary answer for any planned
// manifest of that suite can be cut from it without opening the store again
// (HistoryCache).
type SuiteRuns struct {
	exps []suiteExp
}

// suiteExp is one past experiment on the suite: its runs and what was left
// out of them, or the error reading them gave. The error is reported only
// when a load picks the experiment, as a direct load would have.
type suiteExp struct {
	id   string // the store's experiment id
	past *schemas.Manifest
	read bool // its runs were read (want said yes)
	h    History
	err  error
}

// wanted reports whether a load for m reads a past experiment's runs (it is
// on m's suite, checked by the caller): every one for the library or a
// judged suite, else only experiments with a relevant arm.
func wanted(m, past *schemas.Manifest, all bool) bool {
	_, judged := m.Scenario["suite.judge"]
	return all || judged || relevant(m, past)
}

// readSuite reads every experiment of suiteID@version in st, opening the
// runs only of the experiments want accepts (nil: every one). An experiment
// on another suite is never opened beyond its manifest.
func readSuite(ctx context.Context, st Store, suiteID, version string, read leaderboard.RunFileReader, want func(*schemas.Manifest) bool) (*SuiteRuns, error) {
	ids, err := st.ListExperiments(ctx)
	if err != nil {
		return nil, fmt.Errorf("forecast history: %w", err)
	}
	suite := suiteID + "@" + version
	out := &SuiteRuns{}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("forecast history: %w", err)
		}
		past, err := st.LoadManifest(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("forecast history: %s: %w", id, err)
		}
		if past.SuiteID+"@"+past.SuiteVersion != suite {
			continue
		}
		e := suiteExp{id: id, past: past}
		if want == nil || want(past) {
			e.read = true
			e.err = e.h.load(ctx, st, id, past, suite, read)
			if e.err != nil && want != nil {
				return nil, e.err // a direct load stops at the first error
			}
		}
		out.exps = append(out.exps, e)
	}
	return out, nil
}

// load reads one past experiment's sealed runs into h.
func (h *History) load(ctx context.Context, st Store, id string, past *schemas.Manifest, suite string, read leaderboard.RunFileReader) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("forecast history: %w", err)
	}
	runs, err := st.SealedRuns(ctx, id, schemas.GradeSourceOriginal)
	if err != nil {
		return fmt.Errorf("forecast history: %s: %w", id, err)
	}
	synthetic, err := leaderboard.SyntheticArms(ctx, past, runs, read)
	if err != nil {
		return fmt.Errorf("forecast history: %s: %w", id, err)
	}
	for i := range runs {
		if hr, ok := h.admit(past, &runs[i], synthetic); ok {
			hr.Suite = suite
			h.Runs = append(h.Runs, hr)
		}
	}
	return nil
}

// History is what LoadHistory(m) reads, cut from the suite's runs.
func (s *SuiteRuns) History(m *schemas.Manifest) (*History, error) { return s.pick(m, false) }

// Library is what LoadLibrary(m) reads, cut from the suite's runs.
func (s *SuiteRuns) Library(m *schemas.Manifest) (*History, error) { return s.pick(m, true) }

// pick joins the experiments a load for m reads, in store order.
func (s *SuiteRuns) pick(m *schemas.Manifest, all bool) (*History, error) {
	h := &History{}
	for i := range s.exps {
		e := &s.exps[i]
		if !wanted(m, e.past, all) {
			continue
		}
		if !e.read {
			return nil, fmt.Errorf("forecast history: %s: runs not read", e.id) // a reader bug, never a silent gap
		}
		if e.err != nil {
			return nil, e.err
		}
		h.Runs = append(h.Runs, e.h.Runs...)
		h.Synthetic += e.h.Synthetic
		h.Problems += e.h.Problems
		h.Ungraded += e.h.Ungraded
	}
	return h, nil
}

// admit turns one sealed run into a history record, or counts why it is
// left out.
func (h *History) admit(m *schemas.Manifest, sr *schemas.SealedRun, synthetic map[string]bool) (HistoryRun, bool) {
	arm, ok := m.Arms[sr.Slot.Arm]
	_, scores := sr.Run.Outcome.Score()
	switch {
	case !ok:
		return HistoryRun{}, false
	case synthetic[sr.Slot.Arm]:
		h.Synthetic++
		return HistoryRun{}, false
	case len(sr.Problems) > 0:
		h.Problems++
		return HistoryRun{}, false
	case !scores:
		h.Ungraded++
		return HistoryRun{}, false
	}
	hr := HistoryRun{
		ExpID: m.ExpID, JudgeHash: m.Scenario["suite.judge"], ConfigHash: arm.ConfigHash, Agent: arm.Config.ID,
		Model: arm.Config.Model, Task: sr.Slot.Task,
		CostUSD: metric(sr, metricCost), Tokens: metric(sr, metricTokens), WallS: metric(sr, metricWall),
	}
	if sr.Grade != nil {
		usd := schemas.Observed(0, schemas.EvidenceGateway)
		if sr.Grade.Judge != nil {
			usd = sr.Grade.Judge.USD
		}
		hr.JudgeUSD = &usd
	}
	return hr, true
}

// relevant reports whether a past experiment has an arm whose runs a
// forecast of m could rest on.
func relevant(m, past *schemas.Manifest) bool {
	for _, p := range past.Arms {
		for _, a := range planned(m) {
			if p.ConfigHash == a.ConfigHash || (p.Config.ID == a.Config.ID && p.Config.Model == a.Config.Model) {
				return true
			}
		}
	}
	return false
}

// planned lists the manifest's arms and its compatibility arm.
func planned(m *schemas.Manifest) []schemas.ArmManifest {
	out := make([]schemas.ArmManifest, 0, len(m.Arms)+1)
	for _, a := range m.Arms {
		out = append(out, a)
	}
	if m.Compatibility != nil {
		out = append(out, *m.Compatibility)
	}
	return out
}

// metric is one metric of a run, unknown when its metrics.json lacks it.
func metric(sr *schemas.SealedRun, id string) schemas.Value {
	if v, ok := sr.Metrics[id]; ok {
		return v
	}
	return schemas.Unknown("not in metrics.json", schemas.EvidenceGateway)
}
