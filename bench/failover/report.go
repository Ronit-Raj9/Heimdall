package failover

import (
	"github.com/maximhq/bifrost/bench/schemas"
)

// ArmResult is what one arm produced: its Bifrost log rows and whether the
// task passed.
type ArmResult struct {
	Requests    []schemas.GatewayRequest
	TaskSuccess bool
}

// ArmStats is one arm summarised from its Bifrost log rows.
type ArmStats struct {
	Calls          int     `json:"calls"`
	Errors         int     `json:"errors"`
	FallbackServed int     `json:"fallback_served"` // calls answered by a fallback (fallback_index > 0)
	MeanLatencyMS  float64 `json:"mean_latency_ms"`
	CostUSD        float64 `json:"cost_usd"`
	CostKnown      bool    `json:"cost_known"`
	TaskSuccess    bool    `json:"task_success"`
	SuccessRate    float64 `json:"success_rate,omitempty"` // run reports only
}

// Report is the failover evaluation's result.
type Report struct {
	Clean      ArmStats `json:"clean"`
	Failover   ArmStats `json:"failover"`
	NoFailover ArmStats `json:"no_failover"`
	// FailoverWorked: Bifrost answered from a fallback during the outage
	// and the failover arm had no calls left failing (call reports), or a
	// fallback answered in the outage arm (run reports). It says nothing
	// about the task: SuccessWith does.
	FailoverWorked bool `json:"failover_worked"`
	// FallbackReached: at least one call or run was answered by a fallback.
	FallbackReached bool `json:"fallback_reached"`
	// ExtraLatencyMS is the failover arm's mean call latency minus the
	// clean arm's: the price of trying the dead primary first.
	ExtraLatencyMS float64 `json:"extra_latency_ms"`
	// CostChangeUSD is failover cost minus clean cost; set only when both
	// costs are known.
	CostChangeUSD *float64 `json:"cost_change_usd,omitempty"`
	// SuccessWith and SuccessWithout are the task results with and
	// without fallbacks under the same outage.
	SuccessWith    bool   `json:"success_with_failover"`
	SuccessWithout bool   `json:"success_without_failover"`
	Summary        string `json:"summary"`
	// PerRun is set when the stats count runs (BuildRunReport), not calls.
	PerRun bool `json:"per_run,omitempty"`
}

// Summarise turns an arm's rows into stats. Only message rows count.
func Summarise(r ArmResult) ArmStats {
	s := ArmStats{TaskSuccess: r.TaskSuccess, CostKnown: true}
	var lat float64
	for _, q := range r.Requests {
		if q.Kind != "" && q.Kind != "message" {
			continue
		}
		s.Calls++
		lat += q.LatencyMS
		if q.Status == "error" {
			s.Errors++
		} else if q.FallbackIndex > 0 {
			s.FallbackServed++
		}
		if q.BifrostUSD != nil {
			s.CostUSD += *q.BifrostUSD
		} else {
			s.CostKnown = false
		}
	}
	if s.Calls > 0 {
		s.MeanLatencyMS = lat / float64(s.Calls)
	} else {
		s.CostKnown = false
	}
	return s
}

// BuildReport compares the three arms.
func BuildReport(clean, with, without ArmResult) Report {
	r := Report{Clean: Summarise(clean), Failover: Summarise(with), NoFailover: Summarise(without)}
	r.FallbackReached = r.Failover.FallbackServed > 0
	r.FailoverWorked = r.FallbackReached && r.Failover.Errors == 0
	r.ExtraLatencyMS = r.Failover.MeanLatencyMS - r.Clean.MeanLatencyMS
	if r.Clean.CostKnown && r.Failover.CostKnown {
		d := r.Failover.CostUSD - r.Clean.CostUSD
		r.CostChangeUSD = &d
	}
	r.SuccessWith, r.SuccessWithout = r.Failover.TaskSuccess, r.NoFailover.TaskSuccess
	r.Summary = summary(r)
	return r
}

func summary(r Report) string {
	switch {
	case r.Failover.Calls == 0:
		return "Failover not tested: the failover arm made no model calls."
	case r.FailoverWorked && r.SuccessWith && !r.SuccessWithout:
		return "Failover worked: Bifrost moved calls to the fallback model and the task passed, which it did not without fallbacks."
	case r.FailoverWorked && r.SuccessWith:
		return "Failover worked: Bifrost moved calls to the fallback model and the task passed."
	case r.FailoverWorked:
		return "Failover worked at the gateway, but the task still failed on the fallback model."
	case r.Failover.FallbackServed > 0:
		return "Failover was partial: some calls reached the fallback model, others still failed."
	default:
		return "Failover did not work: no call was answered by a fallback model during the outage."
	}
}

// RunCell is one arm of a finished failover evaluation counted from its
// graded runs (the report card's view, reportcard/failover.go): the run
// metrics fallback_used, wall_time_s and cost_usd stand in for log rows.
type RunCell struct {
	Runs, Successes int
	FallbackRuns    int // runs whose fallback_used metric is above 0
	WallS           float64
	WallN           int
	CostUSD         float64
	CostN           int
}

func (c RunCell) stats() ArmStats {
	s := ArmStats{Calls: c.Runs, FallbackServed: c.FallbackRuns, Errors: c.Runs - c.Successes}
	if c.Runs > 0 {
		s.SuccessRate = float64(c.Successes) / float64(c.Runs)
		s.TaskSuccess = 2*c.Successes >= c.Runs
	}
	if c.WallN > 0 {
		s.MeanLatencyMS = 1000 * c.WallS / float64(c.WallN)
	}
	if c.CostN > 0 && c.CostN == c.Runs {
		s.CostUSD, s.CostKnown = c.CostUSD/float64(c.CostN), true
	}
	return s
}

// BuildRunReport compares the three arms from their graded runs. Latency is
// the mean wall time per run and cost the mean cost per run; failover worked
// when a fallback answered in the outage arm; the task result is separate.
func BuildRunReport(clean, with, without RunCell) Report {
	r := Report{Clean: clean.stats(), Failover: with.stats(), NoFailover: without.stats(), PerRun: true}
	// A failed task is not a failed failover: a run's errors are task
	// failures, so reaching the fallback is the gateway's whole job here.
	r.FallbackReached = r.Failover.FallbackServed > 0
	r.FailoverWorked = r.FallbackReached
	if r.Clean.MeanLatencyMS > 0 && r.Failover.MeanLatencyMS > 0 {
		r.ExtraLatencyMS = r.Failover.MeanLatencyMS - r.Clean.MeanLatencyMS
	}
	if r.Clean.CostKnown && r.Failover.CostKnown {
		d := r.Failover.CostUSD - r.Clean.CostUSD
		r.CostChangeUSD = &d
	}
	r.SuccessWith, r.SuccessWithout = r.Failover.TaskSuccess, r.NoFailover.TaskSuccess
	r.Summary = summary(r)
	return r
}
