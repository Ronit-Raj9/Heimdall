// Package gwfeature reports what one Bifrost gateway setting did to a
// benchmark: the W4 Bifrost feature tests (audit/W4_DESIGN_3_BIFROST.md).
// Each feature registers a Draft (build the comparison) and a Compute (read
// its runs and gateway rows into an Effect) in its init().
package gwfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Effect states.
const (
	StateMeasured      = "measured"        // enough data: the numbers are the result
	StateNotEnoughData = "not_enough_data" // too few tasks, hits or rows; Reason says how many and how many are needed
	StateNotApplied    = "not_applied"     // Bifrost did not apply the changed setting on that setup's calls: the test is broken
	StateUnknown       = "unknown"         // no report can be built; Reason says why
	StateRunning       = "running"         // runs still going: Headline says how many are graded, no verdict
)

// Interval is a point estimate with an optional 95% range.
type Interval struct {
	Point float64  `json:"point"`
	Low   *float64 `json:"low,omitempty"`
	High  *float64 `json:"high,omitempty"`
}

// Source says where an Effect's numbers come from.
type Source struct {
	Kind    string `json:"kind"` // experiment | gateway_config | schedule
	ExpID   string `json:"exp_id,omitempty"`
	Runs    int    `json:"runs"`
	Rows    int    `json:"rows"`              // gateway rows read
	RawRows int    `json:"raw_rows"`          // rows whose full Bifrost detail was sealed
	Gateway string `json:"gateway,omitempty"` // Bifrost version, when known
	Text    string `json:"text"`              // plain words, e.g. "comparison e_ab12: 24 runs, 380 gateway calls"
}

// BifrostSetting is what to copy into Bifrost to use the winning setting
// (feature 14). Heimdall never applies it.
type BifrostSetting struct {
	Kind  string `json:"kind"`  // routing_rule | request_headers | plugin_config | virtual_key | none
	Title string `json:"title"` // e.g. "Routing rule: complex requests to claude-sonnet-4-5"
	Where string `json:"where"` // plain words: where in Bifrost it goes
	Body  string `json:"body"`  // the JSON or header lines to copy, pretty printed
	Note  string `json:"note,omitempty"`
}

// ArmResult is one setup's numbers.
type ArmResult struct {
	Arm        string   `json:"arm"`
	Label      string   `json:"label"`
	Runs       int      `json:"runs"`
	Tasks      int      `json:"tasks"`
	SolveRate  *float64 `json:"solve_rate,omitempty"`   // mean over tasks of the per-task verified success rate
	CostPerRun *float64 `json:"cost_per_run,omitempty"` // mean USD over runs with a known cost
	LatencyMS  *float64 `json:"latency_ms,omitempty"`   // median model call latency over the setup's rows
}

// Effect is one Bifrost feature test's result (the feature effect record).
type Effect struct {
	Feature      string          `json:"feature"`
	ExpID        string          `json:"exp_id"`
	Control      string          `json:"control"`
	Variant      string          `json:"variant,omitempty"` // the recommended, or only, changed setup
	State        string          `json:"state"`
	Reason       string          `json:"reason,omitempty"`
	Headline     string          `json:"headline"`                // one plain sentence
	Result       string          `json:"result,omitempty"`        // saved | lost | worked | failed: the one-word summary (section 3.13)
	Tasks        int             `json:"tasks"`                   // tasks both setups ran
	SolveDelta   *Interval       `json:"solve_delta,omitempty"`   // variant minus control, as a fraction (-0.05 is 5 points lower)
	CostDelta    *Interval       `json:"cost_delta,omitempty"`    // USD per run, variant minus control
	LatencyDelta *Interval       `json:"latency_delta,omitempty"` // ms per model call, variant minus control
	Arms         []ArmResult     `json:"arms"`
	Details      json.RawMessage `json:"details,omitempty"` // the feature's own report
	Setting      *BifrostSetting `json:"setting,omitempty"`
	Source       Source          `json:"source"`
	Fingerprint  string          `json:"fingerprint"`
	ComputedAt   time.Time       `json:"computed_at"`
}

// Bucket is one overhead_breakdown entry of a row.
type Bucket struct {
	Name string  // e.g. plugin.governance, key.selection
	US   float64 // microseconds
}

// Row is one gateway row with every field a feature reads. The fields after
// RawKnown come from the sealed raw detail (gateway.raw.jsonl) and are zero
// when that row's detail was not sealed.
type Row struct {
	ID            string
	Kind          string // message | count_tokens | models | audio | other
	Status        string // success | error
	Provider      string
	ModelAsked    string
	ModelServed   string // "" when unknown, never a copy of ModelAsked
	FallbackIndex int
	LatencyMS     float64
	OverheadMS    *float64
	CostUSD       *float64
	ErrorStatus   int
	CacheHit      bool
	CacheID       string
	BudgetRefused bool

	RawKnown            bool
	ComplexityTier      string
	ComplexityMechanism string
	ComplexityScore     *float64
	PromptName          string
	PromptVersion       string
	CacheSimilarity     *float64
	CacheThreshold      *float64
	Overhead            []Bucket
}

// Run is one counted run: the newest attempt of a slot, sealed, not
// superseded, not aborted by infrastructure.
type Run struct {
	RunID, Task, Arm, Condition string
	Trial                       int
	Success                     *bool    // verified_success; nil when unknown
	CostUSD                     *float64 // cost_usd; nil when unknown
	Rows                        []Row
	RowsComplete                bool                 // the gateway log of the run was complete
	Gates                       []schemas.GateResult // gates.json
	Evidence                    *schemas.Evidence    // evidence.json; nil when unreadable
}

// DraftRequest is POST /gateway/tests/draft.
type DraftRequest struct {
	Feature   string            `json:"feature"`
	Suite     string            `json:"suite"` // id@version
	Base      string            `json:"base"`  // agent config path, as the New comparison form sends it
	Model     string            `json:"model,omitempty"`
	Provider  string            `json:"provider,omitempty"`
	Tasks     []string          `json:"tasks,omitempty"`      // empty: all admitted tasks
	TaskCount int               `json:"task_count,omitempty"` // with Tasks empty: the first this many ready tasks; 0: every ready task
	Name      string            `json:"name,omitempty"`       // the comparison's name; empty: "Bifrost test: <feature>"
	Trials    int               `json:"trials,omitempty"`
	BudgetUSD float64           `json:"budget_usd"`
	Options   map[string]string `json:"options,omitempty"`
}

// DraftEnv is what a Draft may read.
type DraftEnv struct {
	Root string // the org project root
	// SourceGet GETs a path of the org's own Bifrost (the saved source); nil
	// when no source is connected.
	SourceGet func(ctx context.Context, path string, out any) error
	// GatewayGet GETs a management path of the gateway runs go through; nil
	// when none is configured.
	GatewayGet func(ctx context.Context, path string, out any) error
}

// Input is what a Compute reads.
type Input struct {
	ExpID    string
	Manifest *schemas.Manifest
	Test     schemas.GatewayTest
	Runs     []Run
	Src      leaderboard.Source
}

// Feature is one Bifrost feature test.
type Feature struct {
	ID            string
	Name          string // plain words, e.g. "Complexity router"
	AllConditions bool   // Compute needs probe conditions too; otherwise only clean runs are loaded
	Draft         func(ctx context.Context, env DraftEnv, req DraftRequest) (*schemas.Experiment, []string, error)
	Compute       func(ctx context.Context, in Input) Effect
}

var (
	mu       sync.Mutex
	features = map[string]Feature{}
)

// Register adds a feature; a duplicate id panics. Features call it from init().
func Register(f Feature) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := features[f.ID]; dup {
		panic(fmt.Sprintf("gwfeature: feature %s registered twice", f.ID))
	}
	features[f.ID] = f
}

// Lookup returns the feature with id.
func Lookup(id string) (Feature, bool) {
	mu.Lock()
	defer mu.Unlock()
	f, ok := features[id]
	return f, ok
}

// Features returns every registered feature sorted by id.
func Features() []Feature {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Feature, 0, len(features))
	for _, f := range features {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Replace swaps the registered feature with f.ID for f and returns the
// function that puts the old one back. Tests of other packages use it to
// give a real feature id a fixed result.
func Replace(f Feature) (restore func()) {
	mu.Lock()
	defer mu.Unlock()
	old, had := features[f.ID]
	features[f.ID] = f
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if had {
			features[f.ID] = old
		} else {
			delete(features, f.ID)
		}
	}
}
