package schemas

import "time"

// Forecast is the expected cost and time of a planned experiment, from the
// sealed history of the same agent configs on the same suite
// (docs/forecast.md). Nothing in it is invented: an arm with no history and
// no per-run budget is unknown, and says so.
type Forecast struct {
	ExpID       string         `json:"exp_id"`
	Suite       string         `json:"suite"` // id@version
	GeneratedAt time.Time      `json:"generated_at"`
	PriceRef    string         `json:"price_ref,omitempty"` // price snapshot the per-run bounds were checked against
	Arms        []ArmForecast  `json:"arms"`
	CostUSD     ForecastRange  `json:"cost_usd"`     // agent cost of every slot; judge cost is separate
	AgentTimeS  ForecastRange  `json:"agent_time_s"` // sum of run wall times
	WallClockS  ForecastRange  `json:"wall_clock_s"` // agent time over concurrency, no queueing assumed
	Concurrency int            `json:"concurrency"`
	Judge       *JudgeForecast `json:"judge,omitempty"` // judged suites only
	BudgetUSD   float64        `json:"budget_usd"`      // budget.total_usd; 0 when none is set
	Fit         string         `json:"fit"`             // ForecastFit values
	FitReason   string         `json:"fit_reason"`
	Basis       string         `json:"basis"` // one sentence: where the numbers come from
	Notes       []string       `json:"notes"`
}

// Forecast fit against budget.total_usd.
const (
	FitFits          = "fits"           // even the high end is within the budget
	FitMayExceed     = "may_exceed"     // the median fits, the high end does not
	FitLikelyExceeds = "likely_exceeds" // the low end fits, the median does not
	FitExceeds       = "exceeds"        // even the low end is over the budget
	FitUnknown       = "unknown"        // some arm has no estimate
	FitNoBudget      = "no_budget"      // budget.total_usd is not set
)

// Forecast bases, in order of preference.
const (
	BasisConfigHash  = "config_hash"  // sealed runs of the same resolved agent config
	BasisAgentModel  = "agent_model"  // sealed runs of the same agent id and model
	BasisBudgetBound = "budget_bound" // no history: per-run usd budget times runs, an upper bound only
	BasisNone        = "none"         // no history, cannot estimate
)

// ArmForecast is one arm's forecast.
type ArmForecast struct {
	Arm         string        `json:"arm"`
	Label       string        `json:"label"`
	Agent       string        `json:"agent"`
	Model       string        `json:"model"`
	ConfigHash  string        `json:"config_hash"`
	Runs        int           `json:"runs"` // slots of this arm, counted and compatibility
	Basis       string        `json:"basis"`
	BasisText   string        `json:"basis_text"`   // plain words, e.g. "14 sealed runs of config 3f2a91c0 in 2 experiments"
	HistoryRuns int           `json:"history_runs"` // sealed runs the estimate rests on
	Experiments []string      `json:"experiments"`  // experiments those runs came from
	TaskLevel   int           `json:"task_level"`   // planned tasks estimated from their own history; the rest from the suite-wide history
	PerRun      PerRunRanges  `json:"per_run"`
	CostUSD     ForecastRange `json:"cost_usd"`
	Tokens      ForecastRange `json:"tokens"`
	AgentTimeS  ForecastRange `json:"agent_time_s"`
}

// PerRunRanges are the 10th, 50th and 90th percentiles of one run over the
// arm's history, pooled across tasks.
type PerRunRanges struct {
	CostUSD ForecastRange `json:"cost_usd"`
	Tokens  ForecastRange `json:"tokens"`
	WallS   ForecastRange `json:"wall_s"`
}

// JudgeForecast is the judge model's expected cost for a judged suite, from
// past judge records on that suite. It is never part of the agent cost.
type JudgeForecast struct {
	Runs        int           `json:"runs"`         // runs that will be judged
	HistoryRuns int           `json:"history_runs"` // past judge records it rests on
	CostUSD     ForecastRange `json:"cost_usd"`
	BasisText   string        `json:"basis_text"`
}

// ForecastRange is a low, median and high estimate. Kind says what it is: a
// range from history (all three set), an upper bound only (High set), or
// unknown (none set, Reason says why). Low and High are the sums of the
// per-run 10th and 90th percentiles: a planning range, not a confidence
// interval.
type ForecastRange struct {
	Kind   string   `json:"kind"` // history | upper_bound | unknown
	Low    *float64 `json:"low"`
	Median *float64 `json:"median"`
	High   *float64 `json:"high"`
	Reason string   `json:"reason,omitempty"`
}

// Forecast range kinds.
const (
	RangeHistory    = "history"
	RangeUpperBound = "upper_bound"
	RangeUnknown    = "unknown"
)
