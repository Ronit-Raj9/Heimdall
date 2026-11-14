package schemas

import "time"

// Leaderboard is GET /leaderboard and the output of `bifrost-bench
// leaderboard`. It ranks agent configurations (one entry per resolved config
// hash) over every sealed run of one suite version under one condition,
// gathered across experiments. There is no blended score: entries are ordered
// by verified success, and every other column is shown beside it.
//
// Ranking uncertainty is explicit. Each entry is compared with the leader by
// the exact sign-flip test on the tasks both ran (Holm-adjusted across
// entries). Tier 1 means the entry cannot be told apart from the leader at
// Alpha; tier 2 means it is significantly behind. Rank alone is never a
// claim that one agent beats another.
type Leaderboard struct {
	Suite       string                 `json:"suite"`        // id@version this board covers
	Suites      []string               `json:"suites"`       // every id@version with sealed runs, newest first, for the picker
	Condition   string                 `json:"condition"`    // condition the board covers, e.g. clean
	Conditions  []string               `json:"conditions"`   // conditions with sealed runs in this suite
	Scope       string                 `json:"scope"`        // all: each entry over its own tasks | common: every entry over CommonTasks only
	Synthetic   string                 `json:"synthetic"`    // exclude: live runs only | only: scripted agent or scripted model runs only
	CommonTasks []string               `json:"common_tasks"` // tasks every listed entry ran under Condition
	Alpha       float64                `json:"alpha"`        // significance level for tiers (0.05)
	SortMetric  string                 `json:"sort_metric"`  // always verified_success
	Entries     []LeaderboardEntry     `json:"entries"`      // ordered by Rank
	Excluded    []LeaderboardExclusion `json:"excluded"`     // runs left out, grouped by reason
	GeneratedAt time.Time              `json:"generated_at"`
}

// LeaderboardEntry is one agent configuration on the board.
type LeaderboardEntry struct {
	Rank        int      `json:"rank"`        // 1-based position by Success, ties broken by lower cost per success, then more runs
	Tier        int      `json:"tier"`        // 1: cannot be told apart from the leader; 2: significantly behind the leader
	PVsLeader   *float64 `json:"p_vs_leader"` // Holm-adjusted two-sided exact sign-flip p against the leader on common tasks; nil for the leader
	TierReason  string   `json:"tier_reason"` // plain words, e.g. "leader", "p 0.62 on 8 common tasks", "too few common tasks (3) to separate"
	ConfigHash  string   `json:"config_hash"` // sha256 of the resolved agent config (manifest arm config_hash)
	Agent       string   `json:"agent"`       // agent config id, e.g. codex
	Model       string   `json:"model"`       // model the config requests
	Label       string   `json:"label"`       // arm label from the newest contributing experiment
	Synthetic   bool     `json:"synthetic"`   // scripted agent or scripted model: numbers are not findings
	Experiments []string `json:"experiments"` // experiment ids that contributed runs, newest first
	Tasks       int      `json:"tasks"`       // distinct tasks with at least one graded run
	Runs        int      `json:"runs"`        // graded runs counted
	Trials      int      `json:"trials"`      // fewest graded runs on any of its tasks: the k of Reliability

	// Success is the mean over tasks of the per-task verified success rate.
	// Bounds is a descriptive 95% t interval over tasks (stats.PairedT on the
	// task rates), absent with fewer than 2 tasks.
	Success Value `json:"success"`
	// Passes is verified successes over graded runs, pooled.
	Passes Ratio `json:"passes"`
	// Reliability is the mean over tasks of pass^k with k = Trials
	// (stats.PassHatK): the chance all k tries succeed. Unknown when Trials < 2.
	Reliability Value `json:"reliability"`
	// Metrics are per-run aggregates keyed by metric id: the median over runs
	// for steps, tool_calls, tokens, cost_usd and wall_time_s; the rate over
	// runs for false_completion and safe_success; and cost_per_success (total
	// observed cost over verified successes, not_applicable with none).
	// A value is unknown when no run observed it; Reason says how many runs
	// were unknown.
	Metrics map[string]Value `json:"metrics"`
	// Resilience maps each fault condition this config also ran to the mean
	// over shared tasks of (success under the condition minus success clean),
	// as a fraction (-0.25 = 25 points lower). Only on a clean board.
	Resilience map[string]Value `json:"resilience"`
}

// LeaderboardExclusion counts runs the board left out for one reason, e.g.
// "invalid_evaluator", "infra_error", "experiment verdict invalid",
// "not sealed".
type LeaderboardExclusion struct {
	Reason      string   `json:"reason"`
	Runs        int      `json:"runs"`
	Experiments []string `json:"experiments"`
}
