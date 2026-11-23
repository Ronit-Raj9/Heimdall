package schedule

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestScheduleFailOnLeader: a schedule may fail on the setups decision
// "leader" (fix wave 3, N244), like any other verdict decision.
func TestScheduleFailOnLeader(t *testing.T) {
	root, load := project(t, map[string]schemas.Experiment{"live.yaml": {Budget: schemas.BudgetSpec{TotalUSD: 5}}})
	s := Schedule{Name: "nightly", Experiment: "experiments/live.yaml", Cron: "0 2 * * *", MaxUSDPerDay: 10,
		FailOn: []string{string(schemas.DecisionLeader)}}
	if err := s.Validate(root, load); err != nil {
		t.Fatalf("fail_on leader: %v", err)
	}
}
