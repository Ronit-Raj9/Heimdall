package metrics

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestWallTime(t *testing.T) {
	outcome := schemas.EvidenceOutcome
	start := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	exec := func(d time.Duration, timedOut bool) *schemas.ExecResult {
		return &schemas.ExecResult{Started: start, Ended: start.Add(d), TimedOut: timedOut}
	}
	tests := []struct {
		name string
		rc   *schemas.RunContext
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "observed", rc: &schemas.RunContext{Exec: exec(90*time.Second+500*time.Millisecond, false)}, want: schemas.Observed(90.5, outcome)},
		{name: "timed out still has a duration", rc: &schemas.RunContext{Exec: exec(30*time.Minute, true)}, want: schemas.Observed(1800, outcome)},
		{name: "zero length run", rc: &schemas.RunContext{Exec: exec(0, false)}, want: schemas.Observed(0, outcome)},
		{name: "context wins over evidence", rc: &schemas.RunContext{Exec: exec(10*time.Second, false)}, ev: &schemas.Evidence{Exec: exec(20*time.Second, false)}, want: schemas.Observed(10, outcome)},
		{name: "evidence copy when context has none", rc: &schemas.RunContext{}, ev: &schemas.Evidence{Exec: exec(20*time.Second, false)}, want: schemas.Observed(20, outcome)},
		{name: "no exec", rc: &schemas.RunContext{}, ev: &schemas.Evidence{}, want: schemas.Unknown("no exec result", outcome)},
		{name: "nil context and evidence", want: schemas.Unknown("no exec result", outcome)},
		{name: "missing end", rc: &schemas.RunContext{Exec: &schemas.ExecResult{Started: start}}, want: schemas.Unknown("start or end time missing", outcome)},
		{name: "end before start", rc: &schemas.RunContext{Exec: exec(-time.Second, false)}, want: schemas.Unknown("ended before it started", outcome)},
	}
	m := newWallTimeMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, tt.rc, tt.ev), tt.want)
		})
	}
}
