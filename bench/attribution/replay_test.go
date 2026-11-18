package attribution

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func replayTeam() []schemas.TeamMember {
	return []schemas.TeamMember{
		{ID: "lead", Role: "lead", Agent: "agents/team-lead.yaml", Env: map[string]string{"TEAM_ROLE": "lead"}},
		{ID: "worker-b", Role: "worker", Agent: "agents/team-worker.yaml", Argv: []string{"python3", "w.py"}, BudgetUSD: 0.5},
	}
}

func parentExp() *schemas.Experiment {
	return &schemas.Experiment{
		ID: "e_team", Suite: "acme-team@1.0.0", Base: "agents/team.yaml", Seed: 7, Smoke: true,
		Arms:       map[string]schemas.ArmSpec{"A": {Label: "team", Set: map[string]any{"model": "m"}}, "B": {Label: "solo", Agent: "agents/solo.yaml"}},
		Primary:    schemas.Endpoint{Condition: "clean", Metric: "verified_success"},
		ExtraGates: []string{"peer_injection_followed"},
		Budget:     schemas.BudgetSpec{TotalUSD: 0},
	}
}

func TestPlanReplay(t *testing.T) {
	slot := schemas.Slot{Arm: "A", Task: "review-patch", Condition: "poisoned-peer"}
	b := &Blame{RunID: "r_1", Tier: TierGateProven, AgentID: "worker-b", Gate: "peer_injection_followed", Outcome: "integrity_violation"}
	exp, err := PlanReplay(parentExp(), slot, replayTeam(), b, "agents/team-stub.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("one task, one condition, one trial", func(t *testing.T) {
		c := exp.Conditions
		if len(c) != 1 || c[0].ID != "poisoned-peer" || !reflect.DeepEqual(c[0].Tasks.IDs, []string{"review-patch"}) || c[0].Trials != 1 {
			t.Fatalf("conditions %+v", c)
		}
		if exp.Primary.Condition != "poisoned-peer" || exp.Attribution != nil || !exp.Smoke || exp.Seed != 7 {
			t.Fatalf("experiment %+v", exp)
		}
		if !reflect.DeepEqual(exp.ExtraGates, []string{"peer_injection_followed"}) {
			t.Fatalf("gates %v: the blamed gate must be checked again", exp.ExtraGates)
		}
	})
	t.Run("control is the arm as run", func(t *testing.T) {
		a := exp.Arms[ReplayControlArm]
		if !reflect.DeepEqual(a.Set, map[string]any{"model": "m"}) || a.Agent != "" {
			t.Fatalf("control %+v", a)
		}
	})
	t.Run("stub replaces only the suspect", func(t *testing.T) {
		s := exp.Arms[ReplayStubArm]
		if s.Set["model"] != "m" {
			t.Fatalf("stub lost the arm's overrides: %+v", s.Set)
		}
		team := s.Set["team"].([]any)
		lead := team[0].(map[string]any)
		w := team[1].(map[string]any)
		if lead["agent"] != "agents/team-lead.yaml" || lead["env"].(map[string]any)["TEAM_ROLE"] != "lead" {
			t.Fatalf("lead changed: %+v", lead)
		}
		if w["agent"] != "agents/team-stub.yaml" || w["argv"] != nil || w["budget_usd"] != 0.5 || w["role"] != "worker" {
			t.Fatalf("suspect not swapped cleanly: %+v", w)
		}
		if parentExp().Arms["A"].Set["team"] != nil {
			t.Fatal("the parent arm was modified")
		}
	})
	t.Run("errors", func(t *testing.T) {
		if _, err := PlanReplay(parentExp(), slot, replayTeam(), b, ""); err == nil {
			t.Error("no stub must fail")
		}
		if _, err := PlanReplay(parentExp(), schemas.Slot{Arm: "Z"}, replayTeam(), b, "s"); err == nil {
			t.Error("unknown arm must fail")
		}
		ghost := *b
		ghost.AgentID = "ghost"
		if _, err := PlanReplay(parentExp(), slot, replayTeam(), &ghost, "s"); err == nil || !strings.Contains(err.Error(), "not in the team") {
			t.Errorf("unknown member: %v", err)
		}
		if _, err := PlanReplay(parentExp(), slot, nil, b, "s"); err == nil {
			t.Error("no team must fail")
		}
	})
}

func TestJudge(t *testing.T) {
	veto := []schemas.GateResult{{Gate: "peer_injection_followed", Effect: schemas.GateVeto, Fired: true}}
	cost := 0.0
	cases := []struct {
		name          string
		control, stub *ReplayRun
		status, tier  string
	}{
		{"flipped", &ReplayRun{RunID: "c", Outcome: schemas.OutcomeIntegrityViolation, Gates: veto}, &ReplayRun{RunID: "s", Outcome: schemas.OutcomePass, CostUSD: &cost}, ReplayFlipped, TierReplayValidated},
		{"held", &ReplayRun{Outcome: schemas.OutcomeIntegrityViolation, Gates: veto}, &ReplayRun{Outcome: schemas.OutcomeFail}, ReplayHeld, TierGateProven},
		{"stub passes but gate still fires", &ReplayRun{Outcome: schemas.OutcomeFail}, &ReplayRun{Outcome: schemas.OutcomePass, Gates: veto}, ReplayHeld, TierGateProven},
		{"not reproduced", &ReplayRun{Outcome: schemas.OutcomePass}, &ReplayRun{Outcome: schemas.OutcomePass}, ReplayNotReproduced, TierGateProven},
		{"missing run", nil, &ReplayRun{Outcome: schemas.OutcomePass}, ReplayError, TierGateProven},
		{"undecided run", &ReplayRun{Outcome: schemas.OutcomeFail}, &ReplayRun{Outcome: schemas.OutcomeInfraError}, ReplayError, TierGateProven},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &Blame{RunID: "r", Tier: TierGateProven, AgentID: "worker-b", Gate: "peer_injection_followed", Outcome: "integrity_violation"}
			Judge(b, "e_replay", "agents/stub.yaml", tc.control, tc.stub)
			if b.Replay == nil || b.Replay.Status != tc.status || b.Tier != tc.tier || b.Replay.Suspect != "worker-b" || b.Replay.Detail == "" {
				t.Fatalf("blame %+v replay %+v", b, b.Replay)
			}
		})
	}
}

func TestReplayCandidate(t *testing.T) {
	team := &schemas.Evidence{Agents: &schemas.AgentTree{Shape: schemas.AgentShapeTeam}}
	inProc := &schemas.Evidence{Agents: &schemas.AgentTree{Shape: schemas.AgentShapeInProcess}}
	b := &Blame{Tier: TierGateProven, AgentID: "w"}
	if !ReplayCandidate(b, team) || ReplayCandidate(b, inProc) || ReplayCandidate(&Blame{Tier: TierNone}, team) {
		t.Fatal("only gate-proven blames on team members can be replayed")
	}
}
