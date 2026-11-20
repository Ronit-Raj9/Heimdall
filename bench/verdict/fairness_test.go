package verdict

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fairManifest is testManifest under fairness: equal_budget with arms A
// (single), B (team) and, when bestOfN, N (best_of_n), each capped at caps
// in that order.
func fairManifest(bestOfN bool, caps ...float64) *schemas.Manifest {
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Fairness = schemas.FairnessEqualBudget
	arm := func(label string, limit float64, topology string, team bool) schemas.ArmManifest {
		cfg := schemas.AgentConfig{Budgets: schemas.Budgets{USD: limit}, Topology: topology}
		if team {
			cfg.Team = []schemas.TeamMember{{ID: "lead", Role: schemas.AgentRoleLead}, {ID: "w1", Role: schemas.AgentRoleWorker}}
		}
		return schemas.ArmManifest{Label: label, Config: cfg}
	}
	m.Arms[ArmBaseline] = arm("single", caps[0], "", false)
	m.Arms[ArmCandidate] = arm("team", caps[1], "", true)
	if bestOfN {
		m.Arms["N"] = arm("best of n", caps[2], schemas.TopologyBestOfN, false)
	}
	return m
}

// fairRuns are 8 tasks x arms A and B x 1 trial, every run passing and
// spending 1 USD under its cap.
func fairRuns() []schemas.SealedRun {
	runs := concat(passes(schemas.ConditionClean, ArmBaseline, 1, repeatInts(1, 8)...), passes(schemas.ConditionClean, ArmCandidate, 1, repeatInts(1, 8)...))
	for i := range runs {
		runs[i].Metrics = map[string]schemas.Value{MetricVerifiedSuccess: runs[i].Metrics[MetricVerifiedSuccess],
			MetricCostUSD: usd(1), MetricBudgetExceeded: schemas.Observed(0, schemas.EvidenceGateway)}
	}
	return runs
}

// repeatInts returns n copies of v.
func repeatInts(v, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// editRun changes the metrics of the run with id.
func editRun(runs []schemas.SealedRun, id string, f func(r *schemas.SealedRun)) []schemas.SealedRun {
	out := append([]schemas.SealedRun(nil), runs...)
	for i := range out {
		if out[i].Run.RunID != id {
			continue
		}
		ms := map[string]schemas.Value{}
		for k, v := range out[i].Metrics {
			ms[k] = v
		}
		out[i].Metrics = ms
		f(&out[i])
	}
	return out
}

func TestFairnessFor(t *testing.T) {
	const runB1, runA2 = "r_t01_B_clean_1", "r_t02_A_clean_1"
	tests := []struct {
		name       string
		m          *schemas.Manifest
		runs       []schemas.SealedRun
		status     schemas.InstrumentStatus
		detail     string
		exceeded   []string
		claim      bool
		single     bool
		bestOfN    bool
		note       string
		topologies map[string]string
	}{
		{name: "both baselines and nothing over: claim allowed", m: fairManifest(true, 2, 2, 2), runs: fairRuns(),
			status: schemas.InstrumentPass, detail: "no run exceeded its arm's cap (16 runs)", claim: true, single: true, bestOfN: true,
			topologies: map[string]string{ArmBaseline: schemas.TopologySingle, ArmCandidate: schemas.TopologyTeam, "N": schemas.TopologyBestOfN}},
		{name: "no best-of-N arm: no claim", m: fairManifest(false, 2, 2), runs: fairRuns(),
			status: schemas.InstrumentPass, single: true, note: "no best-of-N single-agent baseline arm"},
		{name: "budget_exceeded flag", m: fairManifest(true, 2, 2, 2),
			runs: editRun(fairRuns(), runB1, func(r *schemas.SealedRun) {
				r.Metrics[MetricBudgetExceeded] = schemas.Observed(1, schemas.EvidenceGateway)
			}),
			status: schemas.InstrumentFail, detail: "fairness equal_budget: run r_t01_B_clean_1 exceeded its arm's cap", exceeded: []string{runB1}, single: true, bestOfN: true},
		{name: "cost over the cap without the flag", m: fairManifest(true, 2, 2, 2),
			runs: editRun(fairRuns(), runA2, func(r *schemas.SealedRun) {
				delete(r.Metrics, MetricBudgetExceeded)
				r.Metrics[MetricCostUSD] = usd(2.5)
			}),
			status: schemas.InstrumentFail, detail: "fairness equal_budget: run r_t02_A_clean_1 exceeded its arm's cap", exceeded: []string{runA2}, single: true, bestOfN: true},
		{name: "cost bounds above the cap", m: fairManifest(true, 2, 2, 2),
			runs: editRun(fairRuns(), runA2, func(r *schemas.SealedRun) {
				delete(r.Metrics, MetricBudgetExceeded)
				r.Metrics[MetricCostUSD] = schemas.UnknownWithin("unpriced", schemas.EvidenceGateway, 3, 4)
			}),
			status: schemas.InstrumentFail, exceeded: []string{runA2}, single: true, bestOfN: true},
		{name: "spend unknown: unknown, not pass", m: fairManifest(true, 2, 2, 2),
			runs: editRun(fairRuns(), runA2, func(r *schemas.SealedRun) {
				delete(r.Metrics, MetricBudgetExceeded)
				r.Metrics[MetricCostUSD] = schemas.UnknownWithin("unpriced", schemas.EvidenceGateway, 1, 3)
			}),
			status: schemas.InstrumentUnknown, detail: "spend against the cap unknown in 1 of 16 runs", single: true, bestOfN: true, note: "unknown in 1 of 16 runs"},
		{name: "a run that never started is left out", m: fairManifest(true, 2, 2, 2),
			runs: editRun(fairRuns(), runA2, func(r *schemas.SealedRun) {
				r.Gateway, r.Metrics = nil, map[string]schemas.Value{}
			}),
			status: schemas.InstrumentPass, detail: "(15 runs)", claim: true, single: true, bestOfN: true},
		{name: "caps differ", m: fairManifest(true, 1, 2, 2), runs: fairRuns(),
			status: schemas.InstrumentFail, detail: "fairness equal_budget: arms differ in total USD cap: A=1.00 B=2.00 N=2.00", single: true, bestOfN: true},
		{name: "a missing cap is a problem, its runs are not over it", m: fairManifest(false, 0, 0), runs: fairRuns(),
			status: schemas.InstrumentFail, detail: "fairness equal_budget: arm A has no total USD cap (budgets.usd)", single: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, checks := fairnessFor(tt.m, newRunIndex(tt.runs))
			if rep == nil || len(checks) != 1 {
				t.Fatalf("report %+v, checks %+v", rep, checks)
			}
			c := checks[0]
			if c.Name != InstrumentFairnessEqualBudget || c.Status != tt.status || !strings.Contains(c.Detail, tt.detail) {
				t.Fatalf("check = %+v, want %s with %q", c, tt.status, tt.detail)
			}
			if !reflect.DeepEqual(rep.Exceeded, tt.exceeded) || rep.ClaimAllowed != tt.claim || rep.SingleAgentBaseline != tt.single || rep.BestOfNBaseline != tt.bestOfN {
				t.Fatalf("report = %+v", rep)
			}
			if tt.note != "" && !strings.Contains(strings.Join(rep.Notes, "\n"), tt.note) {
				t.Errorf("notes %q, want %q", rep.Notes, tt.note)
			}
			if tt.topologies != nil && !reflect.DeepEqual(rep.Topologies, tt.topologies) {
				t.Errorf("topologies %v, want %v", rep.Topologies, tt.topologies)
			}
			if rep.Rule != schemas.FairnessEqualBudget || len(rep.CapsUSD) != len(tt.m.Arms) {
				t.Errorf("rule %q caps %v", rep.Rule, rep.CapsUSD)
			}
		})
	}
}

func TestFairnessForNoRule(t *testing.T) {
	tests := []struct {
		name string
		m    *schemas.Manifest
		want string
	}{
		{name: "no rule", m: testManifest(ArmBaseline, ArmCandidate)},
		{name: "nil manifest"},
		{name: "unknown rule", m: func() *schemas.Manifest {
			m := testManifest(ArmBaseline, ArmCandidate)
			m.Experiment.Fairness = "equal_tokens"
			return m
		}(), want: `fairness "equal_tokens" is not a known rule`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, checks := fairnessFor(tt.m, newRunIndex(fairRuns()))
			if tt.want == "" {
				if rep != nil || checks != nil {
					t.Fatalf("report %+v, checks %+v, want none", rep, checks)
				}
				return
			}
			if rep == nil || len(checks) != 1 || checks[0].Status != schemas.InstrumentFail || checks[0].Detail != tt.want {
				t.Fatalf("report %+v, checks %+v", rep, checks)
			}
		})
	}
}

func TestDecideFairness(t *testing.T) {
	over := editRun(fairRuns(), "r_t03_B_clean_1", func(r *schemas.SealedRun) {
		r.Metrics[MetricBudgetExceeded] = schemas.Observed(1, schemas.EvidenceGateway)
	})
	tests := []struct {
		name     string
		m        *schemas.Manifest
		runs     []schemas.SealedRun
		invalid  bool
		fairness bool
	}{
		{name: "a run over its cap makes the verdict INVALID", m: fairManifest(true, 2, 2, 2), runs: over, invalid: true, fairness: true},
		{name: "every run under its cap", m: fairManifest(true, 2, 2, 2), runs: fairRuns(), fairness: true},
		{name: "no rule: no fairness, whatever the spend", m: testManifest(ArmBaseline, ArmCandidate), runs: over},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := decideOrFail(t, tt.m, tt.runs).Verdict
			reason := "fairness equal_budget: run r_t03_B_clean_1 exceeded its arm's cap"
			hasReason := strings.Contains(strings.Join(v.InvalidReasons, "\n"), reason)
			if (v.Decision == schemas.DecisionInvalid) != tt.invalid || hasReason != tt.invalid {
				t.Fatalf("decision %s, invalid reasons %q", v.Decision, v.InvalidReasons)
			}
			if (v.Fairness != nil) != tt.fairness || (checkNamed(v.Instruments, InstrumentFairnessEqualBudget) != nil) != tt.fairness {
				t.Fatalf("fairness %+v, instruments %+v", v.Fairness, v.Instruments)
			}
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"fairness"`) != tt.fairness {
				t.Errorf("verdict JSON fairness key present = %v, want %v", !tt.fairness, tt.fairness)
			}
			if tt.invalid && !reflect.DeepEqual(v.Fairness.Exceeded, []string{"r_t03_B_clean_1"}) {
				t.Errorf("exceeded %v", v.Fairness.Exceeded)
			}
		})
	}
}

// TestFairnessA2ATeamIsNotSingle covers 3-A-11: an A2A arm with a2a.team
// and no topology is a team, so no claim is allowed without a real
// single-agent baseline.
func TestFairnessA2ATeamIsNotSingle(t *testing.T) {
	m := fairManifest(true, 1, 1, 1)
	m.Arms[ArmBaseline] = schemas.ArmManifest{Label: "a2a", Config: schemas.AgentConfig{Adapter: "a2a", Budgets: schemas.Budgets{USD: 1},
		A2A: &schemas.A2AAgent{CardURL: "http://e/card", Team: []schemas.A2AMember{{ID: "w"}}}}}
	single, bestOfN := fairnessBaselines(m.Arms)
	if single || !bestOfN {
		t.Fatalf("single=%v bestOfN=%v, want false true", single, bestOfN)
	}
}
