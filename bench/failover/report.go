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
}

// Report is the failover evaluation's result.
type Report struct {
	Clean      ArmStats `json:"clean"`
	Failover   ArmStats `json:"failover"`
	NoFailover ArmStats `json:"no_failover"`
	// FailoverWorked: Bifrost answered from a fallback during the outage
	// and the failover arm had no calls left failing.
	FailoverWorked bool `json:"failover_worked"`
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
	r.FailoverWorked = r.Failover.FallbackServed > 0 && r.Failover.Errors == 0
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
