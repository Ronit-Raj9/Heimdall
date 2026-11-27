package handlers

import (
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// agentScoreAcc gathers, in the one pass of sealedRuns, the config hashes
// an agent ran with and the pass count per evaluation of its sealed scored
// runs (fix wave M2-21, M2-26). Memory is bounded by the index itself.
type agentScoreAcc struct {
	hashes  map[string]bool
	perExp  map[string]*[2]int // exp -> passed, scored
	lastExp string
	lastAt  time.Time
}

func (a *agentScoreAcc) add(exp string, rv *RunView) {
	if h := rv.Run.Agent.ConfigHash; h != "" {
		if a.hashes == nil {
			a.hashes = map[string]bool{}
		}
		a.hashes[h] = true
	}
	if !rv.Sealed || rv.Superseded || rv.Run.Practice {
		return
	}
	y, ok := rv.Run.Outcome.Score()
	if !ok {
		return
	}
	if a.perExp == nil {
		a.perExp = map[string]*[2]int{}
	}
	c := a.perExp[exp]
	if c == nil {
		c = &[2]int{}
		a.perExp[exp] = c
	}
	if y >= 1 {
		c[0]++
	}
	c[1]++
	if at := rv.Run.Timing.Started; a.lastExp == "" || at.After(a.lastAt) {
		a.lastExp, a.lastAt = exp, at
	}
}

func (a *agentScoreAcc) fill(out *schemas.AgentSummary) {
	if n := len(a.hashes); n > 0 {
		out.Versions = &n
	}
	if c := a.perExp[a.lastExp]; c != nil && c[1] > 0 {
		out.LastScore = &schemas.AgentLastScore{Score: float64(c[0]) / float64(c[1]), Runs: c[1], ExpID: a.lastExp, At: a.lastAt.UTC()}
	}
}
