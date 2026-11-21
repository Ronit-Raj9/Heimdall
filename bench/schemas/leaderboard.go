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

	// GradeSource is what the board read each run's outcome and metrics
	// from: original (as sealed) or latest (each experiment's newest
	// regrade, see LeaderboardGradeSource).
	GradeSource string `json:"grade_source"`
	// GradeSources says, for every experiment with runs on the board
	// (graded or excluded), which source its runs were read from, newest
	// experiment first.
	GradeSources []LeaderboardGradeSource `json:"grade_sources"`
	// ReviewedRuns counts runs on the board whose outcome came from a human
	// review (grade source reviewed), and ReviewChangedRuns those whose
	// outcome the review changed. Absent under any other grade source.
	ReviewedRuns      int `json:"reviewed_runs,omitempty"`
	ReviewChangedRuns int `json:"review_changed_runs,omitempty"`
	// Split is held_out on a held-out board (runs of held-out tasks only,
	// from experiments that selected them); empty on the public board, which
	// never counts a held-out task (docs/held-out-and-canaries.md).
	Split string `json:"split,omitempty"`
	// Grading is how the board's runs were graded, from each experiment's
	// frozen plan and the runs' sealed grade.json: verified (a deterministic
	// check), judged (a judge model decided, docs/answer-grading.md),
	// mixed, or unknown when no graded run carries grade evidence. Judges
	// lists the judge models of judged runs.
	Grading string   `json:"grading"`
	Judges  []string `json:"judges,omitempty"`
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
	Trials      int      `json:"trials"`      // the k of Reliability: min(3, trials per task its experiments ran) unless the query sets k

	// Success is the mean over tasks of the per-task verified success rate.
	// Bounds is a descriptive 95% t interval over tasks (stats.PairedT on the
	// task rates), absent with fewer than 2 tasks.
	Success Value `json:"success"`
	// Passes is verified successes over graded runs, pooled.
	Passes Ratio `json:"passes"`
	// Reliability is the mean over tasks of pass^k with k = Trials
	// (stats.PassHatK): the chance all k tries succeed. Unknown when Trials < 2
	// or a task has fewer than Trials graded runs.
	Reliability Value `json:"reliability"`
	// Metrics are per-run aggregates keyed by metric id: the median over runs
	// for steps, tool_calls, tokens, cost_usd and wall_time_s; the share of
	// runs for false_completion (over runs whose outcome and claim are known,
	// a passing run counting as 0) and safe_success; and cost_per_success (total
	// observed cost over verified successes, not_applicable with none).
	// A value is unknown when no run observed it; Reason says how many runs
	// were unknown.
	Metrics map[string]Value `json:"metrics"`
	// Resilience maps each fault condition this config also ran to the mean
	// over shared tasks of (success under the condition minus success clean),
	// as a fraction (-0.25 = 25 points lower). Only on a clean board.
	Resilience map[string]Value `json:"resilience"`

	// TaskPasses is verified successes over graded runs per task in scope,
	// for callers that need a task-clustered interval (the agent
	// leaderboard). In process only: never serialized.
	TaskPasses map[string]Ratio `json:"-"`
}

// LeaderboardExclusion counts runs the board left out for one reason, e.g.
// "invalid_evaluator", "infra_error", "experiment verdict invalid",
// "not sealed".
type LeaderboardExclusion struct {
	Reason      string   `json:"reason"`
	Runs        int      `json:"runs"`
	Experiments []string `json:"experiments"`
}

// LeaderboardGradeSource is the grade source one experiment's runs on the
// board were read from. Under grade source latest it is the experiment's
// newest regrade (regrade.json created_at, ties by the larger regrade id),
// read as analysis reads it; a run that regrade has no result for (a
// compatibility run, or one sealed after the regrade) keeps its original
// grade, so RegradedRuns can be below Runs.
type LeaderboardGradeSource struct {
	Experiment   string `json:"experiment"`
	Source       string `json:"source"`        // "original" or the regrade id read
	Runs         int    `json:"runs"`          // the experiment's runs on the board, graded or excluded
	RegradedRuns int    `json:"regraded_runs"` // of Runs, those read from the regrade
	// ReviewedRuns is, of Runs, those whose outcome came from a human
	// review (grade source reviewed).
	ReviewedRuns int `json:"reviewed_runs,omitempty"`
}
