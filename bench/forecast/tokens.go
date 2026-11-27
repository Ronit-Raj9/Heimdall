package forecast

import (
	"fmt"
	"math"

	"github.com/maximhq/bifrost/bench/schemas"
)

// BasisAgentTokens is the basis of a rough list-price estimate: no history
// of this arm on the suite and no usable library run, so the cost is the
// agent's typical tokens per run (from its sealed runs on any suite and
// model) times the arm model's list price. It belongs beside the other
// bases in schemas/forecast.go; it lives here until that file moves it.
const BasisAgentTokens = "agent_tokens"

// agentTokensArm estimates an arm from in.AgentRuns and in.TokenPrice. It
// uses the runs of the arm's agent id; when the agent has none with
// observed tokens it answers ok false and the caller falls back to the
// budget bound. Cost and tokens are history ranges widened by
// LibraryWiden, because other suites say only roughly what these tasks
// take. Agent time is the per-run wall time of the same runs, widened.
func agentTokensArm(a schemas.ArmForecast, arm schemas.ArmManifest, in Input) (schemas.ArmForecast, bool) {
	if in.TokenPrice == nil || arm.Config.ID == "" || arm.Config.Model == "" || a.Runs == 0 {
		return a, false
	}
	pl, pm, ph, ok := in.TokenPrice(arm.Config.Model)
	if !ok || pm <= 0 {
		return a, false
	}
	var runs []HistoryRun
	var toks, walls []float64
	for _, h := range in.AgentRuns {
		if h.Agent != arm.Config.ID {
			continue
		}
		x, ok := h.Tokens.Float()
		if !ok || x <= 0 || math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		runs = append(runs, h)
		toks = append(toks, x)
		if w, ok := h.WallS.Float(); ok && w > 0 {
			walls = append(walls, w)
		}
	}
	if len(toks) == 0 {
		return a, false
	}
	n := float64(a.Runs)
	tl, tm, th := Quantile(toks, lowQ), Quantile(toks, midQ), Quantile(toks, highQ)
	a.Basis, a.HistoryRuns, a.Experiments = BasisAgentTokens, len(runs), experimentsOf(runs)
	a.PerRun.Tokens = widen(historyRange(tl, tm, th))
	a.Tokens = widen(historyRange(tl*n, tm*n, th*n))
	a.PerRun.CostUSD = widen(historyRange(tl*pl, tm*pm, th*ph))
	a.CostUSD = widen(historyRange(tl*pl*n, tm*pm*n, th*ph*n))
	none := unknownRange("no wall time in the agent's runs")
	a.PerRun.WallS, a.AgentTimeS = none, none
	if len(walls) > 0 {
		wl, wm, wh := Quantile(walls, lowQ), Quantile(walls, midQ), Quantile(walls, highQ)
		a.PerRun.WallS = widen(historyRange(wl, wm, wh))
		a.AgentTimeS = widen(historyRange(wl*n, wm*n, wh*n))
	}
	a.BasisText = fmt.Sprintf("rough: no past runs of %s on this suite; %s usually uses about %.0f tokens per run (%d run%s on other suites or models), priced at the list price of %s",
		arm.Config.ID, arm.Config.ID, tm, len(runs), plural(len(runs)), arm.Config.Model)
	return a, true
}
