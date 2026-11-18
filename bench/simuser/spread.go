package simuser

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Simulator validity (docs/customer-support.md): the same agents run twice
// on the same suite, once with each of two simulator models. If the success
// of an agent moves more between the two simulated users than the effect an
// experiment claims between agents, the claim rests on the simulator, not
// on the agents.

// SpreadSide is one experiment of a spread check: its id, its effective
// simulator, and the verified success of each arm.
type SpreadSide struct {
	ExpID     string
	Suite     string
	Simulator *schemas.SimulatorSpec
	Success   map[string]schemas.Ratio // arm -> verified passes over runs with a known outcome (Unknown: undecided runs)
}

// SpreadArm is one arm's success under each simulator.
type SpreadArm struct {
	Arm      string  `json:"arm"`
	SuccessA float64 `json:"success_a"`
	SuccessB float64 `json:"success_b"`
	RunsA    int     `json:"runs_a"`
	RunsB    int     `json:"runs_b"`
	Spread   float64 `json:"spread"` // |success_b - success_a|
}

// SpreadReport is the outcome of a spread check. Effect is the largest
// difference in success between two arms inside experiment A (the effect a
// report of A would claim); Warn is set when the largest spread is not
// smaller than it.
type SpreadReport struct {
	ExpA       string      `json:"exp_a"`
	ExpB       string      `json:"exp_b"`
	Suite      string      `json:"suite"`
	SimulatorA string      `json:"simulator_a"`
	SimulatorB string      `json:"simulator_b"`
	Arms       []SpreadArm `json:"arms"`
	MaxSpread  float64     `json:"max_spread"`
	Effect     float64     `json:"effect"`
	EffectArms [2]string   `json:"effect_arms"`
	Warn       bool        `json:"warn"`
	Message    string      `json:"message"`
}

// Spread compares two experiments that differ only in the simulator model.
// Both must use an llm simulator with different models on the same suite,
// and share at least one arm.
func Spread(a, b SpreadSide) (*SpreadReport, error) {
	switch {
	case !IsLLM(a.Simulator) || !IsLLM(b.Simulator):
		return nil, errors.New("a spread check needs two experiments with an llm user simulator (a scripted user has no model to vary)")
	case a.Simulator.Provider == b.Simulator.Provider && a.Simulator.Model == b.Simulator.Model:
		return nil, fmt.Errorf("both experiments use the same simulator model %s/%s", a.Simulator.Provider, a.Simulator.Model)
	case a.Suite != b.Suite:
		return nil, fmt.Errorf("the experiments ran different suites (%s, %s)", a.Suite, b.Suite)
	}
	rep := &SpreadReport{ExpA: a.ExpID, ExpB: b.ExpID, Suite: a.Suite, SimulatorA: Label(a.Simulator), SimulatorB: Label(b.Simulator)}
	arms := make([]string, 0, len(a.Success))
	for arm := range a.Success {
		if _, ok := b.Success[arm]; ok {
			arms = append(arms, arm)
		}
	}
	sort.Strings(arms)
	if len(arms) == 0 {
		return nil, errors.New("the experiments share no arm")
	}
	rate := func(r schemas.Ratio) float64 {
		if r.Den == 0 {
			return math.NaN()
		}
		return float64(r.Num) / float64(r.Den)
	}
	for _, arm := range arms {
		ra, rb := a.Success[arm], b.Success[arm]
		sa, sb := rate(ra), rate(rb)
		if math.IsNaN(sa) || math.IsNaN(sb) {
			return nil, fmt.Errorf("arm %s has no decided run in one of the experiments", arm)
		}
		row := SpreadArm{Arm: arm, SuccessA: sa, SuccessB: sb, RunsA: ra.Den, RunsB: rb.Den, Spread: math.Abs(sb - sa)}
		rep.Arms = append(rep.Arms, row)
		rep.MaxSpread = math.Max(rep.MaxSpread, row.Spread)
	}
	for i := range rep.Arms {
		for j := i + 1; j < len(rep.Arms); j++ {
			if d := math.Abs(rep.Arms[j].SuccessA - rep.Arms[i].SuccessA); d > rep.Effect || rep.EffectArms[0] == "" {
				rep.Effect, rep.EffectArms = d, [2]string{rep.Arms[i].Arm, rep.Arms[j].Arm}
			}
		}
	}
	switch {
	case len(rep.Arms) < 2:
		rep.Warn = rep.MaxSpread > 0
		rep.Message = fmt.Sprintf("one shared arm: success moved by %.0f points between the two simulated users; no effect between arms to compare it with", 100*rep.MaxSpread)
	case rep.MaxSpread >= rep.Effect:
		rep.Warn = true
		rep.Message = fmt.Sprintf("WARNING: success moves by up to %.0f points when only the simulator model changes, at least the %.0f-point effect between %s and %s under %s; that effect may come from the simulated user, not the agents",
			100*rep.MaxSpread, 100*rep.Effect, rep.EffectArms[0], rep.EffectArms[1], rep.SimulatorA)
	default:
		rep.Message = fmt.Sprintf("the simulator model moves success by up to %.0f points, less than the %.0f-point effect between %s and %s",
			100*rep.MaxSpread, 100*rep.Effect, rep.EffectArms[0], rep.EffectArms[1])
	}
	return rep, nil
}

// SuccessFromRuns counts verified passes per arm from the counted sealed
// runs of one condition, by their verified_success metric: observed values
// only; unknown ones are counted apart.
func SuccessFromRuns(runs []schemas.SealedRun, condition string) map[string]schemas.Ratio {
	out := map[string]schemas.Ratio{}
	for _, r := range runs {
		if !r.Slot.Counted || r.Slot.Condition != condition {
			continue
		}
		arm := r.Slot.Arm
		ratio := out[arm]
		v, ok := r.Metrics["verified_success"]
		switch {
		case !ok || v.State != schemas.ValueObserved || v.V == nil:
			ratio.Unknown++
		default:
			ratio.Den++
			if *v.V >= 1 {
				ratio.Num++
			}
		}
		out[arm] = ratio
	}
	return out
}
