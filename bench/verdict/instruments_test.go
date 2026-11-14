package verdict

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestUsageCompleteness(t *testing.T) {
	clean := schemas.ConditionClean
	base := concat(passes(clean, ArmBaseline, 1, 1, 1, 1, 1), passes(clean, ArmCandidate, 1, 1, 1, 1, 1))
	tally := func(req, usage int, complete bool) *schemas.GatewayTally {
		return &schemas.GatewayTally{Requests: req, WithUsage: usage, Complete: complete}
	}
	tests := []struct {
		name   string
		runs   []schemas.SealedRun
		status schemas.InstrumentStatus
		want   map[string]schemas.Ratio
		detail string
	}{
		{name: "every request carries usage", runs: base, status: schemas.InstrumentPass,
			want:   map[string]schemas.Ratio{ArmBaseline: {Num: 8, Den: 8}, ArmCandidate: {Num: 8, Den: 8}},
			detail: "usage completeness arm A 1.000, arm B 1.000"},
		{name: "one arm below 0.95", status: schemas.InstrumentFail,
			runs: withGateway(base, ArmCandidate, func(i int) *schemas.GatewayTally { return tally(10, 9, true) }),
			want: map[string]schemas.Ratio{ArmBaseline: {Num: 8, Den: 8}, ArmCandidate: {Num: 36, Den: 40}},
			detail: "usage completeness 0.900 (36 of 40 model requests carry usage, 0 in runs with a missing or incomplete log) in arm B is below 0.95; " +
				"usage completeness gap 10.0 pp between arm B (0.900) and arm A (1.000) is above 5 pp"},
		{name: "both arms above 0.95 but 5.5 pp apart", status: schemas.InstrumentFail,
			runs: withGateway(withGateway(base, ArmBaseline, func(int) *schemas.GatewayTally { return tally(200, 200, true) }),
				ArmCandidate, func(int) *schemas.GatewayTally { return tally(200, 189, true) }),
			detail: "usage completeness gap 5.5 pp between arm B (0.945) and arm A (1.000) is above 5 pp"},
		{name: "a run with no log counts as one request without usage", status: schemas.InstrumentFail,
			runs: started(withGateway(base, ArmCandidate, func(i int) *schemas.GatewayTally {
				if i == 0 {
					return nil
				}
				return tally(2, 2, true)
			})),
			want:   map[string]schemas.Ratio{ArmBaseline: {Num: 8, Den: 8}, ArmCandidate: {Num: 6, Den: 7, Unknown: 1}},
			detail: "usage completeness 0.857 (6 of 7 model requests carry usage, 1 in runs with a missing or incomplete log) in arm B"},
		{name: "an incomplete log counts every row as without usage", status: schemas.InstrumentFail,
			runs: withGateway(base, ArmBaseline, func(i int) *schemas.GatewayTally {
				if i == 0 {
					return tally(3, 3, false)
				}
				return tally(2, 2, true)
			}),
			want:   map[string]schemas.Ratio{ArmBaseline: {Num: 6, Den: 9, Unknown: 3}, ArmCandidate: {Num: 8, Den: 8}},
			detail: "in arm A is below 0.95"},
		{name: "an incomplete log with no rows still counts once", status: schemas.InstrumentFail,
			runs: withGateway(base, ArmBaseline, func(i int) *schemas.GatewayTally {
				if i == 0 {
					return tally(0, 0, false)
				}
				return tally(2, 2, true)
			}),
			want: map[string]schemas.Ratio{ArmBaseline: {Num: 6, Den: 7, Unknown: 1}, ArmCandidate: {Num: 8, Den: 8}}},
		{name: "a run that never reached its agent is left out", status: schemas.InstrumentPass,
			runs: withGateway(base, ArmCandidate, func(i int) *schemas.GatewayTally {
				if i == 0 {
					return nil
				}
				return tally(2, 2, true)
			}),
			want: map[string]schemas.Ratio{ArmBaseline: {Num: 8, Den: 8}, ArmCandidate: {Num: 6, Den: 6}}},
		{name: "no model requests in any arm", status: schemas.InstrumentNotApplicable,
			runs: withGateway(withGateway(base, ArmBaseline, func(int) *schemas.GatewayTally { return tally(0, 0, true) }),
				ArmCandidate, func(int) *schemas.GatewayTally { return tally(0, 0, true) }),
			detail: "no arm made a model request"},
		{name: "no gateway log in any run is unknown", status: schemas.InstrumentUnknown,
			runs: started(withGateway(withGateway(base, ArmBaseline, func(int) *schemas.GatewayTally { return nil }),
				ArmCandidate, func(int) *schemas.GatewayTally { return nil })),
			detail: "no counted run has a gateway request log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := usageCompleteness(newRunIndex(tt.runs))
			if got.Name != InstrumentUsageCompleteness || got.Status != tt.status {
				t.Fatalf("got %s %s (%s), want %s", got.Name, got.Status, got.Detail, tt.status)
			}
			if !strings.Contains(got.Detail, tt.detail) {
				t.Fatalf("detail %q, want it to contain %q", got.Detail, tt.detail)
			}
			for arm, want := range tt.want {
				if got.PerArm[arm] != want {
					t.Fatalf("arm %s = %+v, want %+v", arm, got.PerArm[arm], want)
				}
			}
		})
	}
}

// started marks every run as having started its agent.
func started(runs []schemas.SealedRun) []schemas.SealedRun {
	out := append([]schemas.SealedRun(nil), runs...)
	at := testFrozenAt
	for i := range out {
		out[i].Run.Timing.AgentStarted = &at
	}
	return out
}

func TestDroppedLogs(t *testing.T) {
	clean := schemas.ConditionClean
	base := concat(passes(clean, ArmBaseline, 1, 1, 1), passes(clean, ArmCandidate, 1, 1, 1))
	at := func(vals ...int64) func(int) *schemas.GatewayTally {
		return func(i int) *schemas.GatewayTally {
			if i >= len(vals) || vals[i] < 0 {
				t := cleanTally()
				t.Dropped = nil
				return t
			}
			return dropped(vals[i])
		}
	}
	baseline := func(n int64) *schemas.Manifest {
		m := testManifest(ArmBaseline, ArmCandidate)
		m.Gateway = &schemas.GatewayManifest{DroppedLogs: &n}
		return m
	}
	tests := []struct {
		name   string
		m      *schemas.Manifest
		runs   []schemas.SealedRun
		status schemas.InstrumentStatus
		detail string
	}{
		{name: "flat at zero", runs: base, status: schemas.InstrumentPass,
			detail: "no rise in the gateway drop counter (4 readings, counter 0 to 0)"},
		{name: "flat above zero passes", runs: withGateway(withGateway(base, ArmBaseline, at(7, 7)), ArmCandidate, at(7, 7)),
			status: schemas.InstrumentPass, detail: "counter 7 to 7"},
		{name: "a rise fails", runs: withGateway(withGateway(base, ArmBaseline, at(7, 7)), ArmCandidate, at(7, 9)),
			status: schemas.InstrumentFail, detail: "gateway dropped 2 log rows during the experiment (4 readings, counter 7 to 9)"},
		{name: "a rise from the preflight baseline fails", m: baseline(5), runs: withGateway(withGateway(base, ArmBaseline, at(7, 7)), ArmCandidate, at(7, 7)),
			status: schemas.InstrumentFail, detail: "gateway dropped 2 log rows during the experiment (5 readings, counter 5 to 7)"},
		{name: "a missing counter is unknown, not a pass", runs: withGateway(base, ArmCandidate, at(0, -1)),
			status: schemas.InstrumentUnknown, detail: "gateway drop counter not recorded in 1 of 4 runs; no rise in the 3 readings, counter 0 to 0"},
		{name: "a rise wins over a missing counter", runs: withGateway(withGateway(base, ArmBaseline, at(0, 1)), ArmCandidate, at(0, -1)),
			status: schemas.InstrumentFail, detail: "gateway dropped 1 log rows"},
		{name: "no counter anywhere is unknown", runs: withGateway(withGateway(base, ArmBaseline, at()), ArmCandidate, at()),
			status: schemas.InstrumentUnknown, detail: "gateway drop counter not recorded in any of 4 runs"},
		{name: "a run that never reached its agent is left out",
			runs:   withGateway(base, ArmCandidate, func(i int) *schemas.GatewayTally { return []*schemas.GatewayTally{nil, dropped(0)}[i] }),
			status: schemas.InstrumentPass, detail: "3 readings, counter 0 to 0"},
		{name: "a started run with no log is unknown",
			runs:   started(withGateway(base, ArmCandidate, func(i int) *schemas.GatewayTally { return []*schemas.GatewayTally{nil, dropped(0)}[i] })),
			status: schemas.InstrumentUnknown, detail: "not recorded in 1 of 4 runs"},
		{name: "a restart that resets the counter fails", runs: withGateway(withGateway(base, ArmBaseline, at(4, 4)), ArmCandidate, at(4, 0)),
			status: schemas.InstrumentFail, detail: "counter 0 to 4"},
		{name: "one reading above zero is unknown",
			runs:   withGateway(passes(clean, ArmBaseline, 1, 1), ArmBaseline, at(3)),
			status: schemas.InstrumentUnknown, detail: "one reading of the gateway drop counter (3)"},
		{name: "one reading of zero passes",
			runs:   passes(clean, ArmBaseline, 1, 1),
			status: schemas.InstrumentPass, detail: "1 readings, counter 0 to 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.m
			if m == nil {
				m = testManifest(ArmBaseline, ArmCandidate)
			}
			got := droppedLogs(m, newRunIndex(tt.runs))
			if got.Name != InstrumentDroppedLogs || got.Status != tt.status {
				t.Fatalf("got %s %s (%s), want %s", got.Name, got.Status, got.Detail, tt.status)
			}
			if !strings.Contains(got.Detail, tt.detail) {
				t.Fatalf("detail %q, want it to contain %q", got.Detail, tt.detail)
			}
		})
	}
}

func TestInstrumentReasons(t *testing.T) {
	checks := []schemas.InstrumentCheck{
		{Name: "a", Status: schemas.InstrumentPass, Detail: "fine"},
		{Name: "b", Status: schemas.InstrumentFail, Detail: "broken"},
		{Name: "c", Status: schemas.InstrumentUnknown, Detail: "unseen"},
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{"only failed checks become reasons", strings.Join(instrumentReasons(checks), ",") == "broken"},
		{"unknown is found", anyInstrument(checks, schemas.InstrumentUnknown)},
		{"not applicable is not found", !anyInstrument(checks, schemas.InstrumentNotApplicable)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Fatal("check failed")
			}
		})
	}
}

func TestParityChecks(t *testing.T) {
	clean := schemas.ConditionClean
	base := concat(passes(clean, ArmBaseline, 1, 1, 1, 1, 1), passes(clean, ArmCandidate, 1, 1, 1, 1, 1))
	budgets := func(a, b schemas.Budgets, set map[string]any) *schemas.Manifest {
		m := testManifest(ArmBaseline, ArmCandidate)
		m.Arms[ArmBaseline] = schemas.ArmManifest{Config: schemas.AgentConfig{Budgets: a}}
		m.Arms[ArmCandidate] = schemas.ArmManifest{Config: schemas.AgentConfig{Budgets: b}}
		m.Experiment.Arms = map[string]schemas.ArmSpec{ArmBaseline: {}, ArmCandidate: {Set: set}}
		return m
	}
	codex := schemas.Budgets{WallS: 900, CPUs: 2, MemGB: 4}
	custom := schemas.Budgets{WallS: 900, Turns: 40, CPUs: 1, MemGB: 2}
	tests := []struct {
		name   string
		check  schemas.InstrumentCheck
		status schemas.InstrumentStatus
		detail string
	}{
		{"equal timeouts pass", timeoutParityCheck(newRunIndex(base)), schemas.InstrumentPass, "gap +0.0 pp on every condition"},
		{"a timeout gap warns", timeoutParityCheck(newRunIndex(withTimeouts(base, ArmCandidate, 2))), schemas.InstrumentWarn, "more than 10 pp apart"},
		{"one arm without runs is not applicable", timeoutParityCheck(newRunIndex(passes(clean, ArmBaseline, 1, 1))), schemas.InstrumentNotApplicable, ""},
		{"equal budgets pass", budgetParity(budgets(codex, codex, nil)), schemas.InstrumentPass, ""},
		{"different agent budgets warn", budgetParity(budgets(codex, custom, nil)), schemas.InstrumentWarn,
			"budgets.turns A=unset, B=40; budgets.cpus A=2, B=1; budgets.mem_gb A=4, B=2: differ between arms with no set: override"},
		{"a set: budget is the variable", budgetParity(budgets(codex, schemas.Budgets{WallS: 600, CPUs: 2, MemGB: 4}, map[string]any{"budgets.wall_s": 600})),
			schemas.InstrumentPass, ""},
		{"no resolved arms", budgetParity(&schemas.Manifest{}), schemas.InstrumentNotApplicable, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.check.Status != tt.status || !strings.Contains(tt.check.Detail, tt.detail) {
				t.Fatalf("got %s %q, want %s %q", tt.check.Status, tt.check.Detail, tt.status, tt.detail)
			}
		})
	}
}

func TestUpstreamErrorsInstrument(t *testing.T) {
	clean := schemas.ConditionClean
	base := concat(passes(clean, ArmBaseline, 1, 1, 1), passes(clean, ArmCandidate, 1, 1, 1))
	// withUpstream gives every run the upstream_errors gate, fired on the
	// first n runs of arm.
	withUpstream := func(arm string, n int) []schemas.SealedRun {
		out := append([]schemas.SealedRun(nil), base...)
		i := 0
		for k := range out {
			fired := false
			if out[k].Slot.Arm == arm {
				fired = i < n
				i++
			}
			out[k].Gates = append(append([]schemas.GateResult(nil), out[k].Gates...),
				schemas.GateResult{Gate: GateUpstreamErrors, Effect: schemas.GateReport, Fired: fired})
		}
		return out
	}
	tests := []struct {
		name   string
		runs   []schemas.SealedRun
		status schemas.InstrumentStatus
		want   map[string]schemas.Ratio
		detail string
	}{
		{name: "runs sealed before the gate", runs: base, status: schemas.InstrumentNotApplicable,
			want: map[string]schemas.Ratio{}, detail: "no counted run carries"},
		{name: "every call answered", runs: withUpstream(ArmCandidate, 0), status: schemas.InstrumentPass,
			want:   map[string]schemas.Ratio{ArmBaseline: {Num: 0, Den: 2}, ArmCandidate: {Num: 0, Den: 2}},
			detail: "in 4 runs"},
		{name: "provider unreachable in one arm", runs: withUpstream(ArmCandidate, 1), status: schemas.InstrumentWarn,
			want:   map[string]schemas.Ratio{ArmBaseline: {Num: 0, Den: 2}, ArmCandidate: {Num: 1, Den: 2}},
			detail: "B 1 of 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := upstreamErrors(newRunIndex(tt.runs))
			if got.Name != InstrumentUpstreamErrors || got.Status != tt.status || !strings.Contains(got.Detail, tt.detail) {
				t.Fatalf("got %s %s %q, want %s containing %q", got.Name, got.Status, got.Detail, tt.status, tt.detail)
			}
			if !reflect.DeepEqual(got.PerArm, tt.want) {
				t.Fatalf("per arm %v, want %v", got.PerArm, tt.want)
			}
		})
	}
}
