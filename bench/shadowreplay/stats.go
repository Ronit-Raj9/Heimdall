package shadowreplay

import (
	"math"
	"sort"
)

// Stats are the run's numbers. Redacted and insufficient steps are left out
// of the denominator and counted beside it. There is no verdict.
type Stats struct {
	Compared int                `json:"compared"` // steps in the denominator
	LeftOut  int                `json:"left_out"` // redacted and insufficient steps
	Shares   map[string]float64 `json:"shares"`   // state -> share of compared steps
	// EquivalentLow and EquivalentHigh are the Wilson 95% interval of the
	// equivalent share.
	EquivalentLow  float64 `json:"equivalent_low"`
	EquivalentHigh float64 `json:"equivalent_high"`
	// Medians per step, new against recorded, over the steps that had both.
	Paired              int     `json:"paired"`
	MedianCostUSD       float64 `json:"median_cost_usd"`
	MedianRecordedCost  float64 `json:"median_recorded_cost_usd"`
	MedianLatencyMS     float64 `json:"median_latency_ms"`
	MedianRecordedLatMS float64 `json:"median_recorded_latency_ms"`
	PairedLatency       int     `json:"paired_latency"`
}

// Summarize computes the run's numbers from its sessions.
func Summarize(rep *RunReport) Stats {
	st := Stats{Shares: map[string]float64{}}
	counts := map[string]int{}
	var cost, recCost, lat, recLat []float64
	for _, s := range rep.Sessions {
		for _, step := range s.Steps {
			if step.State == StateRedacted || step.State == StateInsufficient {
				st.LeftOut++
				continue
			}
			st.Compared++
			counts[step.State]++
			if step.State == StateInfraFailure {
				continue
			}
			if step.RecordedCostUSD != nil {
				cost, recCost = append(cost, step.CostUSD), append(recCost, *step.RecordedCostUSD)
			}
			if step.RecordedLatencyMS != nil {
				lat, recLat = append(lat, float64(step.LatencyMS)), append(recLat, *step.RecordedLatencyMS)
			}
		}
	}
	for k, n := range counts {
		st.Shares[k] = float64(n) / float64(st.Compared)
	}
	st.EquivalentLow, st.EquivalentHigh = Wilson(counts[StateEquivalent], st.Compared)
	st.Paired, st.PairedLatency = len(cost), len(lat)
	st.MedianCostUSD, st.MedianRecordedCost = median(cost), median(recCost)
	st.MedianLatencyMS, st.MedianRecordedLatMS = median(lat), median(recLat)
	return st
}

// Wilson is the Wilson score 95% interval of k successes in n.
func Wilson(k, n int) (float64, float64) {
	if n <= 0 {
		return 0, 0
	}
	const z = 1.959964
	p := float64(k) / float64(n)
	nf := float64(n)
	den := 1 + z*z/nf
	center := (p + z*z/(2*nf)) / den
	half := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / den
	return math.Max(0, center-half), math.Min(1, center+half)
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}
