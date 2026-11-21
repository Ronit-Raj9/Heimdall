// Package trends turns the derived results index (package resultsindex) into
// time series per agent config lineage, so a slow regression across
// experiments shows up even when no single release verdict caught it.
//
// Trends read the index only. The index is derived from sealed run folders
// and can be deleted and rebuilt at any time; no verdict, leaderboard or
// release decision reads it, and nothing here feeds one. A trend is a
// monitoring signal, never a release gate.
package trends

import "time"

// Metric ids a trend can chart. They match the leaderboard's metric ids
// (leaderboard/entry.go) so the two pages say the same thing with the same
// words; MetricSuccess is the verified success rate.
const (
	MetricSuccess         = "verified_success"
	MetricCostPerSuccess  = "cost_per_success"
	MetricTokens          = "tokens"
	MetricWallTime        = "wall_time_s"
	MetricFalseCompletion = "false_completion"
)

// Metrics lists the chartable metric ids in display order.
var Metrics = []string{MetricSuccess, MetricCostPerSuccess, MetricTokens, MetricWallTime, MetricFalseCompletion}

// Synthetic filter values.
const (
	SyntheticExclude = "exclude" // live runs only (the default)
	SyntheticInclude = "include" // live and synthetic runs
	SyntheticOnly    = "only"    // scripted agent or scripted model runs only
)

// Regression statuses of a point against the previous point of its lineage.
const (
	StatusRegressed  = "regressed"   // fewer verified successes, p < Alpha on common tasks
	StatusImproved   = "improved"    // more verified successes, p < Alpha on common tasks
	StatusNoChange   = "no_change"   // enough common tasks, no difference detected
	StatusCannotTell = "cannot_tell" // too few common or discordant tasks for the test to ever reach Alpha
	StatusFirst      = "first"       // the first point of its lineage: nothing to compare with
)

// Release flags of a point, from its experiment's newest analysis.
const (
	ReleaseRegressed = "regressed" // the release verdict was REGRESSED
	ReleaseBlocked   = "blocked"   // the release policy blocked the release
)

// Alpha is the significance level of the regression flag, the verdict's
// default.
const Alpha = 0.05

// Response is GET /api/bench/trends. Points are grouped into lineages: one
// lineage per (agent config id, model), and one point per (experiment,
// config hash) of that lineage, ordered by experiment date.
type Response struct {
	Suite       string    `json:"suite"`        // id@version filter, "" for every suite
	Agent       string    `json:"agent"`        // agent config id filter, "" for every agent
	Metric      string    `json:"metric"`       // metric charted as Point.Value
	Condition   string    `json:"condition"`    // condition the points cover, clean by default
	Synthetic   string    `json:"synthetic"`    // exclude | include | only
	Alpha       float64   `json:"alpha"`        // significance level of the regression flag
	Metrics     []string  `json:"metrics"`      // chartable metric ids, for the picker
	Suites      []string  `json:"suites"`       // suites with indexed runs, for the picker
	Agents      []string  `json:"agents"`       // agent config ids with indexed runs, for the picker
	Conditions  []string  `json:"conditions"`   // conditions with indexed runs, for the picker
	Lineages    []Lineage `json:"lineages"`     // ordered by agent then model
	Points      int       `json:"points"`       // points over every lineage; the UI shows an empty state below 2
	Rejected    int       `json:"rejected"`     // runs the index lists as rejected (seal failed); never charted
	Invalid     int       `json:"invalid"`      // indexed runs of an experiment whose newest verdict is INVALID, or with evidence problems (e.g. a slot with two counted runs); never charted
	IndexedAt   time.Time `json:"indexed_at"`   // when the index was last written
	GeneratedAt time.Time `json:"generated_at"` // when this response was computed
}

// Lineage is one agent config lineage: every config of one agent id and
// model, across experiments. Regression is the newest point's flag.
type Lineage struct {
	Key        string     `json:"key"`   // "<agent>|<model>"
	Agent      string     `json:"agent"` // agent config id
	Model      string     `json:"model"` // model the config requests; "" when unknown
	Synthetic  bool       `json:"synthetic"`
	Points     []Point    `json:"points"`
	Regression Regression `json:"regression"` // newest point against the one before it
}

// Point is one config hash of a lineage in one experiment. Numeric fields
// are null when no run observed them, never zero.
type Point struct {
	Experiment      string     `json:"experiment"`
	Date            time.Time  `json:"date"`  // the experiment's creation time
	Suite           string     `json:"suite"` // id@version
	Arm             string     `json:"arm"`
	ConfigHash      string     `json:"config_hash"`
	ConfigChanged   bool       `json:"config_changed"` // config hash differs from the point the regression compared against (Regression.Against); false without one
	Synthetic       bool       `json:"synthetic"`
	Tasks           int        `json:"tasks"` // distinct tasks with a scored run
	Runs            int        `json:"runs"`  // scored runs
	Value           *float64   `json:"value"` // the charted metric (Response.Metric)
	Success         *float64   `json:"success"`
	SuccessLow      *float64   `json:"success_low"`  // descriptive 95% t interval over task rates; null under 2 tasks
	SuccessHigh     *float64   `json:"success_high"` // as SuccessLow
	CostPerSuccess  *float64   `json:"cost_per_success"`
	Tokens          *float64   `json:"tokens"`           // median per run
	WallTime        *float64   `json:"wall_time_s"`      // median per run
	FalseCompletion *float64   `json:"false_completion"` // share of runs whose claim and outcome are known
	Decision        string     `json:"decision"`         // newest analysis verdict decision; "" with no analysis
	Policy          string     `json:"policy"`           // newest analysis policy decision; "" with none
	Release         string     `json:"release"`          // regressed | blocked | ""
	Regression      Regression `json:"regression"`       // against the previous point of the lineage
}

// Regression is the exact sign-flip test (stats.SignFlip) of a point's
// per-task success rates against the previous point's, on the tasks both
// ran. A difference no test could call at Alpha is StatusCannotTell, never a
// regression.
type Regression struct {
	Status      string   `json:"status"`       // one of the Status constants
	P           *float64 `json:"p"`            // two-sided p; null when not tested
	CommonTasks int      `json:"common_tasks"` // tasks both points ran
	Discordant  int      `json:"discordant"`   // common tasks whose rate changed
	Delta       *float64 `json:"delta"`        // mean per-task change in success (newer minus older); null when not tested
	Against     string   `json:"against"`      // experiment of the previous point; "" for the first
	Reason      string   `json:"reason"`       // plain words, e.g. "p 0.03 on 9 common tasks"
}
