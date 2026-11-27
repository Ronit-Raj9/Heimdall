package forecast

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// LoadAgentRuns reads the sealed runs, on any suite and model, of the past
// experiments that have an arm with the same agent id as one of m's arms.
// It is Input.AgentRuns, the base of the rough list-price estimate
// (tokens.go). The exclusions are LoadHistory's (synthetic arms, runs with a
// problem, runs whose outcome does not score). An experiment with no such
// agent is never opened beyond its manifest, so the read grows with the
// agent's own history, not with the store.
func LoadAgentRuns(ctx context.Context, st Store, m *schemas.Manifest, read leaderboard.RunFileReader) (*History, error) {
	agents := map[string]bool{}
	for _, a := range planned(m) {
		if a.Config.ID != "" {
			agents[a.Config.ID] = true
		}
	}
	h := &History{}
	if len(agents) == 0 {
		return h, nil
	}
	ids, err := st.ListExperiments(ctx)
	if err != nil {
		return nil, fmt.Errorf("forecast agent runs: %w", err)
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("forecast agent runs: %w", err)
		}
		past, err := st.LoadManifest(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("forecast agent runs: %s: %w", id, err)
		}
		hit := false
		for _, p := range past.Arms {
			hit = hit || agents[p.Config.ID]
		}
		if !hit {
			continue
		}
		if err := h.load(ctx, st, id, past, past.SuiteID+"@"+past.SuiteVersion, read); err != nil {
			return nil, err
		}
	}
	return h, nil
}
