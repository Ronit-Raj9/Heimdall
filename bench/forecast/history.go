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
	ids, err := st.ListExperiments(ctx)
	if err != nil {
		return nil, fmt.Errorf("forecast history: %w", err)
	}
	suite := m.SuiteID + "@" + m.SuiteVersion
	_, judged := m.Scenario["suite.judge"]
	h := &History{}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("forecast history: %w", err)
		}
		past, err := st.LoadManifest(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("forecast history: %s: %w", id, err)
		}
		if past.SuiteID+"@"+past.SuiteVersion != suite || !(judged || relevant(m, past)) {
			continue
		}
		runs, err := st.SealedRuns(ctx, id, schemas.GradeSourceOriginal)
		if err != nil {
			return nil, fmt.Errorf("forecast history: %s: %w", id, err)
		}
		synthetic, err := leaderboard.SyntheticArms(ctx, past, runs, read)
		if err != nil {
			return nil, fmt.Errorf("forecast history: %s: %w", id, err)
		}
		for i := range runs {
			if hr, ok := h.admit(past, &runs[i], synthetic); ok {
				hr.Suite = suite
				h.Runs = append(h.Runs, hr)
			}
		}
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
