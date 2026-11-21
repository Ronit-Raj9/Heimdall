package verdict

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Meter names: the always-on honesty, safety and efficiency aggregates
// (DESIGN.md 9.6).
const (
	MeterFalseCompletion        = "false_completion_rate"
	MeterClaimPrecision         = "claim_precision"
	MeterVetoGatesPerRun        = "veto_gates_per_run"
	MeterStepsMedian            = "steps_median"
	MeterToolCallsMedian        = "tool_calls_median"
	MeterTokensMedian           = "tokens_median"
	MeterWallTimeMedian         = "wall_time_median"
	MeterCostPerVerifiedSuccess = "cost_per_verified_success"
)

// meterDef defines one meter and how one arm's runs reduce to its value.
type meterDef struct {
	name, label, num, den string
	kind                  schemas.ValueKind
	compute               func(runs []schemas.SealedRun) schemas.Value
}

// meters computes every meter per arm over all counted runs of that arm, in
// every condition, because the meters describe every run (DESIGN.md 9.6).
func meters(idx runIndex) []schemas.Meter {
	defs := []meterDef{
		{MeterFalseCompletion, "False completion among failed runs", "failed runs that still claimed success", "failed runs whose claim is known",
			schemas.KindRate, meanOf(MetricFalseCompletion)},
		{MeterClaimPrecision, "Claim precision", "success claims the verifier confirmed", "runs with a success claim",
			schemas.KindRate, meanOf(MetricClaimPrecision)},
		{MeterVetoGatesPerRun, "Veto gates fired per run", "veto gate results that fired", "counted runs",
			schemas.KindCount, vetoGatesPerRun},
		{MeterStepsMedian, "Steps (model turns), median", "median of steps per run", "runs with observed steps",
			schemas.KindCount, medianOf(MetricSteps)},
		{MeterToolCallsMedian, "Tool calls, median", "median of tool calls per run", "runs with observed tool calls",
			schemas.KindCount, medianOf(MetricToolCalls)},
		{MeterTokensMedian, "Tokens, median", "median of tokens per run", "runs with observed tokens",
			schemas.KindTokens, medianOf(MetricTokens)},
		{MeterWallTimeMedian, "Wall time, median", "median of agent wall time per run", "runs with observed wall time",
			schemas.KindDuration, medianOf(MetricWallTime)},
		{MeterCostPerVerifiedSuccess, "Cost per verified success", "cost_usd summed over every counted run", "verified successes",
			schemas.KindUSD, costPerVerifiedSuccess},
		{MeterTimeoutRate, "Timeout rate", "runs that hit the wall-clock budget (outcome timeout or agent timed out)", "counted runs",
			schemas.KindRate, timeoutRate},
	}
	defs = withStepFamily(idx, defs)
	defs = withSafetyFamily(idx, defs) // Safety probes area (S1): outage behaviour and silent compromise (safetymeters.go)
	defs = withControlFamily(idx, defs) // Control evaluations: usefulness and side-task success (safetyevidence.go)
	// B2 Bifrost features: gateway overhead, MCP witness agreement (bifrostmeters.go).
	defs = withBifrostFamily(idx, defs)
	arms := armsIn(idx, "")
	out := make([]schemas.Meter, 0, len(defs))
	for _, d := range defs {
		mt := schemas.Meter{Name: d.name, Label: d.label, Kind: d.kind, Numerator: d.num, Denominator: d.den, PerArm: map[string]schemas.Value{}}
		for _, arm := range arms {
			mt.PerArm[arm] = d.compute(idx.armRuns("", arm))
		}
		out = append(out, mt)
	}
	return out
}

// Meters of the step family (docs/steps-and-evidence.md): shown beside the
// steps meter so steps, model turns that got an answer, is never read as
// everything the agent did.
const (
	MeterModelTurnsFailedMedian = "model_turns_failed_median"
	MeterModelAttemptsMedian    = "model_attempts_median"
	MeterAgentStepsMedian       = "agent_steps_median"
	MeterToolProofRate          = "tool_proof_rate"
	MeterToolTrafficUnparsed    = "tool_traffic_unparsed_median"
)

// stepFamilyGate is the metric whose presence in any counted run turns the
// step family meters on. Runs sealed before it existed never carry it, so an
// analysis of them encodes exactly as before.
const stepFamilyGate = "model_turns_failed"

// withStepFamily inserts the step family meters right after the steps meter
// when some counted run recorded them: failed turns, physical attempts and
// agent steps beside turns answered, then how the requested tool calls were
// proven and how much proxied traffic could not be parsed.
func withStepFamily(idx runIndex, defs []meterDef) []meterDef {
	recorded := func(metric string) bool {
		for _, byTask := range idx {
			for _, byArm := range byTask {
				for _, runs := range byArm {
					for _, r := range runs {
						if _, ok := r.Metrics[metric]; ok {
							return true
						}
					}
				}
			}
		}
		return false
	}
	var family []meterDef
	if recorded(stepFamilyGate) {
		family = append(family,
			meterDef{MeterModelTurnsFailedMedian, "Model turns failed, median", "median of model turns whose final attempt failed per run", "runs with observed failed turns",
				schemas.KindCount, medianOf("model_turns_failed")},
			meterDef{MeterModelAttemptsMedian, "Model attempts (incl. retries, fallbacks), median", "median of physical model attempts per run", "runs with observed attempts",
				schemas.KindCount, medianOf("model_attempts")},
			meterDef{MeterAgentStepsMedian, "Agent steps (all witnessed actions), median", "median of agent steps per run", "runs with observed agent steps",
				schemas.KindCount, medianOf("agent_steps")})
	}
	if recorded("tool_proof_rate") {
		family = append(family, meterDef{MeterToolProofRate, "Tool calls with trusted proof", "requested tool calls a bench-side witness saw run, mean of the per-run share", "runs with requested tool calls and a trusted witness available",
			schemas.KindRate, meanOf("tool_proof_rate")})
		for _, c := range []struct{ metric, label string }{
			{"tool_proof_proxy", "proxy witnessed"}, {"tool_proof_process", "process witnessed"}, {"tool_proof_file_effect", "file effect witnessed"},
			{"tool_proof_shim", "shim witnessed"}, {"tool_proof_result_returned", "result returned only"}, {"tool_proof_none", "no witness"},
		} {
			if recorded(c.metric) {
				family = append(family, meterDef{c.metric, "Tool call proof: " + c.label, "requested tool calls of this proof class, mean of the per-run share", "runs with requested tool calls",
					schemas.KindRate, meanOf(c.metric)})
			}
		}
	}
	if recorded("tool_traffic_unparsed") {
		family = append(family, meterDef{MeterToolTrafficUnparsed, "Tool traffic unparsed, median", "median of proxied requests recorded opaque per run", "runs with a tool ledger",
			schemas.KindCount, medianOf("tool_traffic_unparsed")})
	}
	if len(family) == 0 {
		return defs
	}
	out := make([]meterDef, 0, len(defs)+len(family))
	for _, d := range defs {
		out = append(out, d)
		if d.name == MeterStepsMedian {
			out = append(out, family...)
		}
	}
	return out
}

// armMetrics is arm -> metric -> median per run over the counted runs of one
// condition, for every metric any run recorded.
func armMetrics(idx runIndex, condition string) map[string]map[string]schemas.Value {
	out := map[string]map[string]schemas.Value{}
	for _, arm := range armsIn(idx, condition) {
		runs := idx.armRuns(condition, arm)
		names := map[string]bool{}
		for _, r := range runs {
			for name := range r.Metrics {
				names[name] = true
			}
		}
		perMetric := map[string]schemas.Value{}
		for _, name := range sortedKeys(names) {
			perMetric[name] = medianOf(name)(runs)
		}
		out[arm] = perMetric
	}
	return out
}

// meanOf returns a reducer that averages metric over the runs where it was
// observed.
func meanOf(metric string) func([]schemas.SealedRun) schemas.Value {
	return func(runs []schemas.SealedRun) schemas.Value {
		return aggregate(runs, metric, stats.Mean)
	}
}

// medianOf returns a reducer that takes the median of metric over the runs
// where it was observed.
func medianOf(metric string) func([]schemas.SealedRun) schemas.Value {
	return func(runs []schemas.SealedRun) schemas.Value {
		return aggregate(runs, metric, stats.Median)
	}
}

// aggregate reduces the observed values of metric with f. With no observed
// value the result is unknown, or not applicable when no run applies; it is
// never zero. A partial result says how many runs it rests on.
func aggregate(runs []schemas.SealedRun, metric string, f func([]float64) float64) schemas.Value {
	vals := make([]schemas.Value, len(runs))
	for i, r := range runs {
		vals[i] = metricValue(r, metric)
	}
	ev := evidenceOf(vals)
	obs, unknown := observedValues(vals)
	if len(obs) == 0 {
		if unknown == 0 {
			v := schemas.NotApplicable()
			v.Evidence = ev
			v.Reason = fmt.Sprintf("does not apply to any run (0 of %d)", len(runs))
			return v
		}
		return schemas.Unknown(fmt.Sprintf("not observed in any run (0 of %d)", len(runs)), ev)
	}
	x := f(obs)
	if !finite(x) {
		return schemas.Unknown(fmt.Sprintf("%s could not be aggregated", metric), ev)
	}
	v := schemas.Observed(x, ev)
	if unknown > 0 {
		v.Reason = fmt.Sprintf("%d observed of %d runs", len(obs), len(obs)+unknown)
	}
	return v
}

// evidenceOf is the evidence level of the first observed value, or of the
// first value when none is observed.
func evidenceOf(vals []schemas.Value) schemas.EvidenceLevel {
	for _, v := range vals {
		if v.IsObserved() {
			return v.Evidence
		}
	}
	if len(vals) > 0 {
		return vals[0].Evidence
	}
	return ""
}

// vetoGatesPerRun is the number of fired veto gate results per counted run.
func vetoGatesPerRun(runs []schemas.SealedRun) schemas.Value {
	if len(runs) == 0 {
		return schemas.NotApplicable()
	}
	fired := 0
	for _, r := range runs {
		for _, g := range r.Gates {
			if g.Fired && g.Effect == schemas.GateVeto {
				fired++
			}
		}
	}
	return schemas.Observed(float64(fired)/float64(len(runs)), schemas.EvidenceOutcome)
}

// costPerVerifiedSuccess is all cost over all assigned runs divided by the
// verified successes (DESIGN.md 11), so failed runs still count against it.
// With no success it is "undefined (0 of N)". Any unknown cost or unknown
// success makes it unknown, bounded when every unknown cost is bounded and at
// least one success is observed.
func costPerVerifiedSuccess(runs []schemas.SealedRun) schemas.Value {
	var cost, costLo, costHi float64
	costUnknown, bounded := 0, true
	succ, succUnknown := 0, 0
	for _, r := range runs {
		c := metricValue(r, MetricCostUSD)
		if x, ok := c.Float(); ok {
			cost += x
		} else {
			costUnknown++
			if c.Bounds != nil {
				costLo += c.Bounds[0]
				costHi += c.Bounds[1]
			} else {
				bounded = false
			}
		}
		if x, ok := metricValue(r, MetricVerifiedSuccess).Float(); !ok {
			succUnknown++
		} else if x >= 0.5 {
			succ++
		}
	}
	if succ == 0 && succUnknown == 0 {
		v := schemas.NotApplicable()
		v.Evidence = schemas.EvidenceGateway
		v.Reason = fmt.Sprintf("undefined (0 of %d)", len(runs))
		return v
	}
	if costUnknown == 0 && succUnknown == 0 {
		return schemas.Observed(cost/float64(succ), schemas.EvidenceGateway)
	}
	reason := fmt.Sprintf("cost unknown in %d of %d runs, verified success unknown in %d", costUnknown, len(runs), succUnknown)
	if bounded && succ > 0 {
		return schemas.UnknownWithin(reason, schemas.EvidenceGateway,
			(cost+costLo)/float64(succ+succUnknown), (cost+costHi)/float64(succ))
	}
	return schemas.Unknown(reason, schemas.EvidenceGateway)
}
