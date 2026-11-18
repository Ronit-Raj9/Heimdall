// Package costexplain explains why one arm of an experiment cost more than
// another (docs/cost-explain.md). It splits the difference in cost per run
// and per verified success into additive parts read from the sealed cost
// rows: price, input tokens, output tokens, cache read share, cache write
// share, model turns, wasted attempts, rows it cannot split, cost it cannot
// see, and, per success, the success rate. The parts sum to the total
// difference exactly (an exact Shapley split over the factors of the cost),
// and a cost the rows do not know stays unknown with its bounds, never zero.
// It also flags a silent cache break (the cache read share drops between
// arms or over time) and names the likely cause from the evidence, and it
// places configs on the cost against success plane with the Pareto frontier
// marked.
//
// The package is pure compute over verified sealed runs and the files of
// their folders, read through a RunFileReader. It writes nothing.
package costexplain

import (
	"errors"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Run folder files the explanation reads.
const (
	FileCost     = "cost.json"              // the cost ledger, one row per physical attempt
	FileEvidence = "evidence.json"          // fallback for the cost rows and the gateway log
	FileRequests = "gateway.requests.jsonl" // Bifrost's rows with their raw detail (prompt, tools)
)

// Part ids of a waterfall, in waterfall order.
const (
	PartPrice        = "price"
	PartInputTokens  = "input_tokens"
	PartOutputTokens = "output_tokens"
	PartCacheRead    = "cache_read_share"
	PartCacheWrite   = "cache_write_share"
	PartTurns        = "turns"
	PartWasted       = "wasted_attempts"
	PartUnattributed = "unattributed"
	PartUnpriced     = "unpriced"
	PartSuccessRate  = "success_rate"
)

// Waterfall bases.
const (
	BasisPerRun     = "per_run"
	BasisPerSuccess = "per_success"
)

// Cache finding kinds.
const (
	FindingBetweenArms = "between_arms"
	FindingOverTime    = "over_time"
)

// ErrBadQuery marks a query that cannot be answered as asked; the API
// answers 400 and the CLI a usage error.
var ErrBadQuery = errors.New("bad cost explain query")

// RunFileReader reads one file of a sealed run's folder. A missing file
// returns found false and no error. It has the shape of
// leaderboard.RunFileReader.
type RunFileReader func(sr *schemas.SealedRun, name string) (data []byte, found bool, err error)

// Query selects the two arms and the condition. Empty arms take the
// experiment's first two arms in id order; an empty condition is the
// experiment's primary condition.
type Query struct {
	A         string `json:"a"`
	B         string `json:"b"`
	Condition string `json:"condition"`
}

// Explanation is why arm B cost more (or less) than arm A
// (GET /api/bench/experiments/{id}/cost-explain).
type Explanation struct {
	ExpID      string    `json:"exp_id"`
	Suite      string    `json:"suite"`
	Condition  string    `json:"condition"`
	Conditions []string  `json:"conditions"` // conditions with a graded run
	PriceRef   string    `json:"price_ref"`  // the price snapshot rows were split with; empty without one
	Synthetic  bool      `json:"synthetic"`  // scripted agent, scripted model or smoke: the numbers are not findings
	A          ArmCost   `json:"a"`
	B          ArmCost   `json:"b"`
	PerRun     Waterfall `json:"per_run"`
	PerSuccess Waterfall `json:"per_success"`
	// Apart holds what measuring cost (judge, user simulator): reported
	// beside the agent's cost and never inside it.
	Apart []ApartCost    `json:"apart"`
	Cache []CacheFinding `json:"cache"` // silent cache breaks, empty when none
	Notes []string       `json:"notes"`
}

// ArmCost is one arm's cost picture over its graded runs on the condition.
// Means are per run over every graded run, failures included.
type ArmCost struct {
	Arm          string   `json:"arm"`
	Label        string   `json:"label"`
	Model        string   `json:"model"` // the config's model
	ServedModels []string `json:"served_models"`
	Runs         int      `json:"runs"`
	Successes    int      `json:"successes"`

	CostPerRun             schemas.Value `json:"cost_per_run"`              // mean; unknown with bounds when any row is unpriced
	CostPerSuccess         schemas.Value `json:"cost_per_success"`          // total over verified successes; not applicable with none
	CacheNeutralPerRun     schemas.Value `json:"cache_neutral_per_run"`     // every input token priced as fresh input
	CacheNeutralPerSuccess schemas.Value `json:"cache_neutral_per_success"` // the same over verified successes
	FailedRunCostPerRun    schemas.Value `json:"failed_run_cost_per_run"`   // cost of failed runs, spread over all runs
	CacheReadShare         schemas.Value `json:"cache_read_share"`          // cache read over all input tokens
	CacheWriteShare        schemas.Value `json:"cache_write_share"`         // cache write over all input tokens

	TurnsPerRun         float64        `json:"turns_per_run"`          // priced model turns (rows with usage)
	InputTokensPerTurn  float64        `json:"input_tokens_per_turn"`  // fresh + cache read + cache write
	OutputTokensPerTurn float64        `json:"output_tokens_per_turn"` // output, reasoning included
	Tokens              schemas.Tokens `json:"tokens"`                 // every row's tokens, summed over runs

	// Effective prices in USD per million tokens of each bucket: the cost of
	// the bucket over its tokens, so a mix of served models is one price.
	PriceInput      *float64 `json:"price_input,omitempty"`
	PriceCacheRead  *float64 `json:"price_cache_read,omitempty"`
	PriceCacheWrite *float64 `json:"price_cache_write,omitempty"`
	PriceOutput     *float64 `json:"price_output,omitempty"`

	AttemptsPerRun       float64       `json:"attempts_per_run"`        // physical attempts (cost rows)
	WastedAttemptsPerRun float64       `json:"wasted_attempts_per_run"` // attempts that returned no usage: retries, rejected and failed requests
	RetriesPerRun        float64       `json:"retries_per_run"`         // attempts after the first of a request
	FallbacksPerRun      float64       `json:"fallbacks_per_run"`       // requests served by a fallback (gateway rows)
	RejectedPerRun       float64       `json:"rejected_per_run"`        // requests the provider rejected with a 4xx (malformed request or tool call, refused)
	RefusalsPerRun       float64       `json:"refusals_per_run"`        // responses that carry a refusal (gateway raw rows)
	LatencyMSPerTurn     schemas.Value `json:"latency_ms_per_turn"`     // mean gateway latency of answered rows

	UnpricedRows     int      `json:"unpriced_rows"`     // rows whose cost is unknown
	UnattributedRows int      `json:"unattributed_rows"` // priced rows the price snapshot cannot split into buckets
	RunsWithoutRows  int      `json:"runs_without_rows"` // runs with no cost rows, read from cost_usd only
	PromptHashes     []string `json:"prompt_hashes"`     // system prompt sha256 prefixes seen in the raw rows
	ToolHashes       []string `json:"tool_hashes"`       // tool list sha256 prefixes seen in the raw rows
	Keys             []string `json:"keys"`              // provider key names that served the rows
}

// Waterfall is one difference (B minus A) split into additive parts.
type Waterfall struct {
	Basis  string        `json:"basis"` // per_run | per_success
	From   schemas.Value `json:"from"`  // arm A's value
	To     schemas.Value `json:"to"`    // arm B's value
	Total  schemas.Value `json:"total"` // B minus A; unknown with bounds when a part is
	Parts  []Part        `json:"parts"`
	Known  float64       `json:"known"`  // sum of the observed parts
	Reason string        `json:"reason"` // why the waterfall is not applicable, or empty
}

// Part is one additive part of a waterfall.
type Part struct {
	ID     string        `json:"id"`
	Label  string        `json:"label"`
	USD    schemas.Value `json:"usd"`
	Detail string        `json:"detail"` // plain words: what moved
}

// ApartCost is a measuring cost kept out of the agent's cost.
type ApartCost struct {
	ID    string        `json:"id"` // judge | simulator
	Label string        `json:"label"`
	A     schemas.Value `json:"a"` // mean per run
	B     schemas.Value `json:"b"`
	Diff  schemas.Value `json:"diff"`
}

// CacheFinding is a silent cache break: the cache read share of Arm
// dropped against Baseline, with the likely cause named from evidence.
type CacheFinding struct {
	Kind        string   `json:"kind"` // between_arms | over_time
	Arm         string   `json:"arm"`
	Baseline    string   `json:"baseline"` // the other arm, or "earlier runs of <arm>"
	ShareBefore float64  `json:"share_before"`
	ShareAfter  float64  `json:"share_after"`
	Cause       string   `json:"cause"`
	Evidence    []string `json:"evidence"`
}
