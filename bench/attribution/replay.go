package attribution

import (
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Tier 2: counterfactual replay (DoVer, arXiv 2512.06749; AgenTracer, arXiv
// 2509.03312). A blamed run's slot is run again in a small child
// experiment with two arms on the run's task and condition, one trial each:
//
//   - A, control: the run's arm exactly as it ran, to check the failure
//     reproduces at all;
//   - B, stub: the same arm with the blamed team member's agent replaced by
//     a scripted stub that does that member's part correctly.
//
// The blame is validated (replay_validated) only when the control fails
// again and the stub arm passes with the blamed gate quiet. A control that
// passes makes the replay inconclusive (not_reproduced): the failure was
// not reliable enough to test. A stub arm that still fails means the
// suspect alone does not explain the failure (held); several interventions
// can fix the same failure, so held is not proof of innocence either.
//
// Replay runs agents again and can cost money, so it is opt-in
// (Experiment.Attribution.Replay) and capped (MaxReplays). It replaces
// whole team members only: in-process subagents cannot be swapped from
// outside the agent.

// Replay arm ids of the child experiment.
const (
	ReplayControlArm = "A"
	ReplayStubArm    = "B"
)

// ReplayNotReproduced is the replay status of a control arm that did not
// fail again.
const ReplayNotReproduced = "not_reproduced"

// DefaultMaxReplays caps replays per experiment when the spec sets none.
const DefaultMaxReplays = 3

// ReplayCandidate reports whether b can be checked by replay: a failed run
// blamed at tier 1 on a member of a team.
func ReplayCandidate(b *Blame, ev *schemas.Evidence) bool {
	return b != nil && b.Tier == TierGateProven && b.AgentID != "" &&
		ev != nil && ev.Agents != nil && ev.Agents.Shape == schemas.AgentShapeTeam
}

// PlanReplay builds the child experiment that replays the blamed run of
// slot. team is the team the run ran with (the arm's Team, else the task's).
// stub is the agent config path that replaces the suspect. The child keeps
// the parent's suite, base, gates, seed, budget, verdict, policy and smoke
// flag; it never asks for a replay itself.
func PlanReplay(parent *schemas.Experiment, slot schemas.Slot, team []schemas.TeamMember, b *Blame, stub string) (*schemas.Experiment, error) {
	if parent == nil || b == nil {
		return nil, errors.New("plan replay: experiment and blame are required")
	}
	if stub == "" {
		return nil, errors.New("plan replay: no stub agent (attribution.stub)")
	}
	arm, ok := parent.Arms[slot.Arm]
	if !ok {
		return nil, fmt.Errorf("plan replay: arm %s is not in experiment %s", slot.Arm, parent.ID)
	}
	swapped, err := swapMember(team, b.AgentID, stub)
	if err != nil {
		return nil, fmt.Errorf("plan replay of run %s: %w", b.RunID, err)
	}
	stubArm := schemas.ArmSpec{Label: "replay: " + b.AgentID + " replaced by " + stub, Agent: arm.Agent, Set: map[string]any{}}
	for k, v := range arm.Set {
		stubArm.Set[k] = v
	}
	stubArm.Set["team"] = swapped
	control := schemas.ArmSpec{Label: "replay control: arm " + slot.Arm + " as run", Agent: arm.Agent, Set: arm.Set}
	exp := &schemas.Experiment{
		Name:         "replay " + b.RunID,
		Suite:        parent.Suite,
		Base:         parent.Base,
		Arms:         map[string]schemas.ArmSpec{ReplayControlArm: control, ReplayStubArm: stubArm},
		Primary:      schemas.Endpoint{Condition: slot.Condition, Metric: parent.Primary.Metric},
		Conditions:   []schemas.ConditionSpec{{ID: slot.Condition, Tasks: schemas.TaskSelector{IDs: []string{slot.Task}}, Trials: 1}},
		Budget:       parent.Budget,
		Verdict:      parent.Verdict,
		ExtraGates:   parent.ExtraGates,
		Seed:         parent.Seed,
		Concurrency:  1,
		ManifestMode: parent.ManifestMode,
		Policy:       parent.Policy,
		Smoke:        parent.Smoke,
		Requirements: parent.Requirements,
		External:     parent.External,
	}
	return exp, nil
}

// swapMember returns team as override values (yaml keys) with member id's
// agent replaced by stub and its argv dropped, so the stub runs as written.
func swapMember(team []schemas.TeamMember, id, stub string) ([]any, error) {
	if len(team) == 0 {
		return nil, errors.New("the run has no team to swap a member of")
	}
	found := false
	out := make([]any, 0, len(team))
	for _, m := range team {
		v := map[string]any{"id": m.ID}
		if m.Role != "" {
			v["role"] = m.Role
		}
		if m.Agent != "" {
			v["agent"] = m.Agent
		}
		if m.Model != "" {
			v["model"] = m.Model
		}
		if m.BudgetUSD != 0 {
			v["budget_usd"] = m.BudgetUSD
		}
		if len(m.Env) > 0 {
			env := map[string]any{}
			for k, e := range m.Env {
				env[k] = e
			}
			v["env"] = env
		}
		if len(m.Argv) > 0 {
			argv := make([]any, len(m.Argv))
			for i, a := range m.Argv {
				argv[i] = a
			}
			v["argv"] = argv
		}
		if m.ID == id {
			found = true
			v["agent"] = stub
			delete(v, "argv")
		}
		out = append(out, v)
	}
	if !found {
		return nil, fmt.Errorf("member %s is not in the team", id)
	}
	return out, nil
}

// ReplayRun is the part of a sealed replay run the judgement reads.
type ReplayRun struct {
	RunID   string
	Outcome schemas.Outcome
	Gates   []schemas.GateResult
	CostUSD *float64
}

// Judge fills b.Replay from the two replay runs and upgrades b to
// replay_validated when the failure reproduced in the control and went away
// in the stub arm. control or stub nil means that run is missing.
func Judge(b *Blame, expID, stub string, control, stubRun *ReplayRun) {
	r := &Replay{Suspect: b.AgentID, Stub: stub, ExpID: expID, OutcomeBefore: b.Outcome}
	b.Replay = r
	switch {
	case control == nil || stubRun == nil:
		r.Status, r.Detail = ReplayError, "a replay run is missing"
		return
	case unknownOutcome(control.Outcome) || unknownOutcome(stubRun.Outcome):
		r.Status = ReplayError
		r.Detail = fmt.Sprintf("a replay run ended without a decided outcome (control %s, stub %s)", control.Outcome, stubRun.Outcome)
		return
	}
	r.RunID, r.OutcomeAfter, r.CostUSD = stubRun.RunID, string(stubRun.Outcome), stubRun.CostUSD
	r.GatesAfter = firedNames(stubRun.Gates)
	reproduced := control.Outcome != schemas.OutcomePass || gateFired(control.Gates, b.Gate)
	fixed := stubRun.Outcome == schemas.OutcomePass && !gateFired(stubRun.Gates, b.Gate)
	switch {
	case !reproduced:
		r.Status = ReplayNotReproduced
		r.Detail = fmt.Sprintf("the control run %s passed with %s in place; the failure does not reproduce, so the replay cannot test the blame", control.RunID, b.AgentID)
	case fixed:
		r.Status = ReplayFlipped
		r.Detail = fmt.Sprintf("with %s replaced by the stub the run passed and %s stayed quiet, while the control failed again", b.AgentID, b.Gate)
		b.Tier = TierReplayValidated
	default:
		r.Status = ReplayHeld
		r.Detail = fmt.Sprintf("with %s replaced by the stub the run still ended %s; the suspect alone does not explain the failure", b.AgentID, stubRun.Outcome)
	}
}

func unknownOutcome(o schemas.Outcome) bool {
	switch o {
	case schemas.OutcomeInvalidEvaluator, schemas.OutcomeInfraError, schemas.OutcomeUnknown, schemas.OutcomeAbortedInfra, "":
		return true
	}
	return false
}

func gateFired(gates []schemas.GateResult, name string) bool {
	for _, g := range gates {
		if g.Gate == name && g.Fired {
			return true
		}
	}
	return false
}

func firedNames(gates []schemas.GateResult) []string {
	var out []string
	for _, g := range gates {
		if g.Fired && (g.Effect == schemas.GateVeto || g.Effect == schemas.GateReport) {
			out = append(out, g.Gate)
		}
	}
	return out
}
