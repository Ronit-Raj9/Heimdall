package reportcard

// Through Bifrost (lane C09): a short report card section that says, per
// arm, what the Bifrost gateway did across the evaluation's runs. It reads
// only metrics the runs already carry (metrics/routing.go,
// metrics/bifrostfeatures.go, metrics/modelturns.go), so it adds no file
// reads. A nil count means no run observed that metric.
const (
	metricFallbackUsed      = "fallback_used"
	metricSemanticCacheHits = "semantic_cache_hits"
	metricModelRetries      = "model_retries"
	metricInputTokens       = "input_tokens"
)

// BifrostRow is one arm's Bifrost numbers over every benchmark.
type BifrostRow struct {
	Arm  string `json:"arm"`
	Runs int    `json:"runs"`
	// FallbackRuns counts runs where Bifrost served a call from a fallback;
	// FallbackKnown the runs that observed fallback_used.
	FallbackRuns  *int     `json:"fallback_runs,omitempty"`
	FallbackKnown int      `json:"fallback_known"`
	CacheHits     *int     `json:"cache_hits,omitempty"`   // Bifrost semantic cache hits, summed
	Retries       *int     `json:"retries,omitempty"`      // model retries Bifrost logged, summed
	InputTokens   *int64   `json:"input_tokens,omitempty"` // input tokens through Bifrost, summed
	CostUSD       *float64 `json:"cost_usd,omitempty"`     // cost over the runs whose cost is known
	CostRuns      int      `json:"cost_runs"`
}

// bifrostRows builds the section; nil when no arm has a run.
func bifrostRows(children []childRuns, labels []string) []BifrostRow {
	var out []BifrostRow
	for _, l := range labels {
		var runs []graded
		for _, cr := range children {
			runs = append(runs, cr.arms[l]...)
		}
		if len(runs) == 0 {
			continue
		}
		row := BifrostRow{Arm: l, Runs: len(runs)}
		fb := 0
		for _, r := range runs {
			if v, ok := r.metrics[metricFallbackUsed].Float(); ok {
				row.FallbackKnown++
				if v > 0 {
					fb++
				}
			}
		}
		if row.FallbackKnown > 0 {
			row.FallbackRuns = &fb
		}
		sumInt := func(id string) *int {
			total, known, _ := sumKnown(runs, id)
			if known == 0 {
				return nil
			}
			n := int(total)
			return &n
		}
		row.CacheHits = sumInt(metricSemanticCacheHits)
		row.Retries = sumInt(metricModelRetries)
		if total, known, _ := sumKnown(runs, metricInputTokens); known > 0 {
			n := int64(total)
			row.InputTokens = &n
		}
		if total, known, _ := sumKnown(runs, metricCostUSD); known > 0 {
			c := round(total)
			row.CostUSD, row.CostRuns = &c, known
		}
		out = append(out, row)
	}
	return out
}
