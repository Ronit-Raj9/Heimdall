package verdict

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Sizing at 199 and exactly 200 tasks is in range; beyond the cap the
// follow-up is "more than 200", never 0 tasks, 0 runs or 0 USD (audit 1-B-06).
func TestMoreEvidenceBeyondSizingCap(t *testing.T) {
	const sd = 0.30
	cfg := settings{alpha: defaultAlpha, assumedSD: sd}
	m := testManifest(ArmBaseline, ArmCandidate)
	cost := schemas.SealedRun{Metrics: map[string]schemas.Value{MetricCostUSD: schemas.Observed(0.5, schemas.EvidenceGateway)}}
	idx := runIndex{"t01": {schemas.ConditionClean: {ArmBaseline: {cost}}}}
	size := func(target float64) *schemas.MoreEvidence {
		v := &schemas.Verdict{SD: sd, Effect: &schemas.Effect{DBar: target}}
		return moreEvidence(m, cfg, idx, v)
	}
	for _, n := range []int{199, 200} {
		me := size(stats.MDE(n, sd, defaultAlpha, mdePower))
		if me.Over || me.TasksTotal != n || me.Tasks == 0 || me.CostUSD == nil {
			t.Fatalf("n=%d: %+v", n, me)
		}
	}
	me := size(0.02) // MDE at 200 is about 5.97 pp
	if !me.Over || me.TasksTotal != maxTasksNeeded || me.Tasks != 0 || me.Runs != 0 || me.CostUSD != nil || me.Command != "" {
		t.Fatalf("beyond cap: %+v", me)
	}
}
