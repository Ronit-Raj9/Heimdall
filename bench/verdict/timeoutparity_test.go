package verdict

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestTimeoutParity(t *testing.T) {
	clean := schemas.ConditionClean
	// Ten runs per arm on clean, five per arm on stale-notes.
	ten := func(arm string) []schemas.SealedRun {
		return passes(clean, arm, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1)
	}
	stale := concat(passes(condStale, ArmBaseline, 1, 1, 1, 1, 1, 1), passes(condStale, ArmCandidate, 1, 1, 1, 1, 1, 1))
	outcome := func(runs []schemas.SealedRun, arm string, n int) []schemas.SealedRun {
		out := append([]schemas.SealedRun(nil), runs...)
		for i := range out {
			if n > 0 && out[i].Slot.Arm == arm {
				out[i].Run.Outcome = schemas.OutcomeTimeout
				n--
			}
		}
		return out
	}
	tests := []struct {
		name      string
		runs      []schemas.SealedRun
		condition string
		status    schemas.GuardStatus
		a, b      int // timed-out runs per arm
		detail    string
	}{
		{name: "no timeouts", runs: concat(ten(ArmBaseline), ten(ArmCandidate)), condition: clean, status: schemas.GuardPass,
			detail: "timeout rate arm A 0.0% (0 of 10), arm B 0.0% (0 of 10), gap +0.0 pp on clean"},
		{name: "30 pp gap above 20% fails", runs: concat(ten(ArmBaseline), withTimeouts(ten(ArmCandidate), ArmCandidate, 3)), condition: clean,
			status: schemas.GuardFail, b: 3, detail: "more than 10 pp apart with one arm above 20%"},
		{name: "a gap in favour of B fails too", runs: concat(outcome(ten(ArmBaseline), ArmBaseline, 4), ten(ArmCandidate)), condition: clean,
			status: schemas.GuardFail, a: 4, detail: "gap -40.0 pp"},
		{name: "a 20 pp gap with both at most 20% passes", runs: concat(ten(ArmBaseline), withTimeouts(ten(ArmCandidate), ArmCandidate, 2)), condition: clean,
			status: schemas.GuardPass, b: 2},
		{name: "both high but within 10 pp passes", runs: concat(withTimeouts(ten(ArmBaseline), ArmBaseline, 5), withTimeouts(ten(ArmCandidate), ArmCandidate, 6)),
			condition: clean, status: schemas.GuardPass, a: 5, b: 6},
		{name: "exactly 10 pp apart passes", runs: concat(withTimeouts(ten(ArmBaseline), ArmBaseline, 3), withTimeouts(ten(ArmCandidate), ArmCandidate, 4)),
			condition: clean, status: schemas.GuardPass, a: 3, b: 4},
		{name: "all pools every condition", runs: concat(ten(ArmBaseline), ten(ArmCandidate), withTimeouts(stale, ArmCandidate, 5)), condition: "all",
			status: schemas.GuardFail, b: 5, detail: "(5 of 15), gap +33.3 pp on every condition"},
		{name: "empty pools every condition", runs: concat(ten(ArmBaseline), ten(ArmCandidate), withTimeouts(stale, ArmCandidate, 5)),
			status: schemas.GuardFail, b: 5},
		{name: "a condition with no runs is undetermined", runs: concat(ten(ArmBaseline), ten(ArmCandidate)), condition: condStale,
			status: schemas.GuardUndetermined, detail: "timeout parity needs both"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, per, detail := TimeoutParity(tt.runs, tt.condition, ArmBaseline, ArmCandidate)
			if status != tt.status {
				t.Fatalf("status %s (%s), want %s", status, detail, tt.status)
			}
			if per[ArmBaseline].Num != tt.a || per[ArmCandidate].Num != tt.b {
				t.Fatalf("timed out %+v, want A %d B %d", per, tt.a, tt.b)
			}
			if !strings.Contains(detail, tt.detail) {
				t.Fatalf("detail %q, want it to contain %q", detail, tt.detail)
			}
		})
	}
}

func TestTimeoutRate(t *testing.T) {
	clean := schemas.ConditionClean
	tests := []struct {
		name  string
		runs  []schemas.SealedRun
		state schemas.ValueState
		v     float64
	}{
		{"no runs is not applicable", nil, schemas.ValueNotApplicable, 0},
		{"one of four timed out", withTimeouts(passes(clean, ArmBaseline, 1, 1, 0, 1, 0), ArmBaseline, 1), schemas.ValueObserved, 0.25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := timeoutRate(tt.runs)
			if got.State != tt.state {
				t.Fatalf("state %s, want %s", got.State, tt.state)
			}
			if x, ok := got.Float(); tt.state == schemas.ValueObserved && (!ok || !near(x, tt.v, 1e-12)) {
				t.Fatalf("value %v, want %v", x, tt.v)
			}
		})
	}
}
