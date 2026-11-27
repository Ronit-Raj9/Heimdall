package schemas

import (
	"slices"
	"strings"
	"time"
)

// Evaluations (agent-first benchmarking, contract section 3): one agent, or
// two arms for an A/B, across many benchmarks at once. Each benchmark becomes
// one normal experiment (a child); the evaluation only tracks them.

// Evaluation modes.
const (
	EvaluationModeScore    = "score"    // one arm, a score per benchmark
	EvaluationModeVersions = "versions" // two versions of one agent
	EvaluationModeModels   = "models"   // one agent on two models
	EvaluationModeOpponent = "opponent" // the agent against another agent
	// EvaluationModeModelSwitch is one agent on two to
	// EvaluationModelSwitchMaxArms models, for the model switch report and
	// its Bifrost routing config (bench/modelswitch).
	EvaluationModeModelSwitch = "model_switch"
)

// EvaluationModelSwitchMaxArms is the most models a model_switch
// evaluation compares.
const EvaluationModelSwitchMaxArms = 6

// Evaluation states.
const (
	EvaluationQueued    = "queued"
	EvaluationRunning   = "running"
	EvaluationDone      = "done"
	EvaluationFailed    = "failed"
	EvaluationCancelled = "cancelled"
)

// EvaluationChildSkipped is the status of a benchmark that never got an
// experiment (incompatible agent, no admitted task, refused plan).
const EvaluationChildSkipped = "skipped"

// EvaluationArm is one agent (at a version) on one model.
type EvaluationArm struct {
	Label   string `json:"label"`
	Agent   string `json:"agent"`
	Version int    `json:"version,omitempty"`
	Model   string `json:"model"`
	// Provider is the gateway provider the model is served through.
	Provider string `json:"provider,omitempty"`
	// LatestAtLaunch is set by the server when the arm asked for the
	// latest version: Version is then the version that was latest when the
	// evaluation was created, recorded so the report card and the agent
	// leaderboard name the exact version that ran.
	LatestAtLaunch bool `json:"latest_at_launch,omitempty"`
}

// EvaluationSpec is the body of POST /api/bench/evaluations.
type EvaluationSpec struct {
	Name              string          `json:"name"`
	Mode              string          `json:"mode"`       // score | versions | models | opponent
	Arms              []EvaluationArm `json:"arms"`       // 1 arm for score, 2 for A/B
	Benchmarks        []string        `json:"benchmarks"` // suite ids; or Pack
	Pack              string          `json:"pack,omitempty"`
	TasksPerBenchmark int             `json:"tasks_per_benchmark"` // default 10 (the Quick look preset), admitted tasks only
	Trials            int             `json:"trials"`              // repeats per task, default 1 (more tasks beat more trials)
	Faults            []string        `json:"faults"`              // "clean" always; plus model-down, broken-tool, misleading-instruction
	// Failover is the "Provider failover" option (lane C07): a second arm
	// with Bifrost fallbacks and a primary model outage condition.
	Failover  *FailoverOption `json:"failover,omitempty"`
	BudgetUSD float64         `json:"budget_usd,omitempty"`
	// Split is the task split the evaluation runs: public (the default) or
	// held_out, an explicit opt-in for a held-out evaluation. Tasks of the
	// other split never run in it.
	Split string          `json:"split,omitempty"`
	Gate  *EvaluationGate `json:"gate,omitempty"` // checked automatically when the evaluation finishes
	// Worker is the connected worker (its pool) the evaluation runs on;
	// "" is the org's default worker, "shared" the shared worker. The saved
	// evaluation holds the resolved pool ("" for the shared worker).
	Worker string `json:"worker,omitempty"`
	// Stream carries the agent's memory from task to task inside each
	// benchmark (stream.go); in score mode the hidden repeat arm is the
	// fresh control.
	Stream *StreamSpec `json:"stream,omitempty"`
	// DiscloseBudget tells the agent its budget and deadline.
	DiscloseBudget bool `json:"disclose_budget,omitempty"`
	// Concurrency is how many runs of one benchmark go at once; 0 is the
	// default (handlers.EvaluationDefaultConcurrency). The org's worker
	// slots still bound what runs side by side.
	Concurrency int `json:"concurrency,omitempty"`
	// Preset names the size preset the wizard started from (quick, standard,
	// thorough); informational, the numbers above are what runs. first_look
	// (lane Z01, the Connect box): one benchmark, 5 tasks, 1 trial, clean only,
	// score mode, concurrency 5; its report card says it is too small for a
	// verdict.
	Preset string `json:"preset,omitempty"`
	// BifrostCache makes Bifrost's semantic cache the test variable
	// ("Bifrost cache on vs off"): score mode only, the one arm runs with
	// the cache off and the server adds a second arm, identical except that
	// every model request sends a semantic cache key (direct mode, scoped
	// to the task, 1 hour TTL), so the report can show the hit rate and the
	// latency, cost and quality change the cache brings
	// (bifrostfeatures.CacheAB).
	BifrostCache bool `json:"bifrost_cache,omitempty"`
	// MCPRoute is how every arm reaches the tasks' MCP tool servers:
	// "" or "direct" through the tool proxy only, "bifrost" through
	// Bifrost's MCP gateway (gateway.bifrost.mcp, classic tools),
	// "bifrost_code_mode" the same with Code Mode clients. Through Bifrost
	// the tool proxy still sits behind Bifrost, so tool faults keep working
	// (docs/bifrost-features.md, "MCP through Bifrost").
	MCPRoute string `json:"mcp_route,omitempty"`
}

// EvaluationPresetFirstLook is the first-look preset (EvaluationSpec.Preset).
const EvaluationPresetFirstLook = "first_look"

// EvaluationCacheArm is the arm id of the cache-on copy an evaluation with
// BifrostCache adds; the spec's own arm is the cache-off arm.
const EvaluationCacheArm = "cache"

// EvaluationCacheSuffix ends the label of that arm.
const EvaluationCacheSuffix = " (Bifrost cache on)"

// CacheVariantArm reports whether arm of m is the cache-on copy an
// evaluation with BifrostCache adds: the arm whose label ends in
// EvaluationCacheSuffix, whatever its id (B in new evaluations,
// EvaluationCacheArm in older ones). It is a variant of the spec's own arm,
// not a separate setup, so readers that rank setups (the leaderboards) leave
// its runs out; the cache on vs off report reads them (lane C08).
func CacheVariantArm(m *Manifest, arm string) bool {
	return m != nil && arm != "" && strings.HasSuffix(variantArmLabel(m, arm), EvaluationCacheSuffix)
}

// VariantArmID is the id of the arm of m whose label ends in suffix (an
// evaluation's cache-on copy, failover arm or no-memory control), read from
// m.Arms and m.Experiment.Arms; "" when none. Readers find a variant arm by
// its label, never by its id: the first variant of a score evaluation is arm
// B (so the paired verdict can read it), later ones keep their own ids.
func VariantArmID(m *Manifest, suffix string) string {
	if m == nil || suffix == "" {
		return ""
	}
	ids := make([]string, 0, len(m.Arms)+len(m.Experiment.Arms))
	for id := range m.Arms {
		ids = append(ids, id)
	}
	for id := range m.Experiment.Arms {
		if _, dup := m.Arms[id]; !dup {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		if strings.HasSuffix(variantArmLabel(m, id), suffix) {
			return id
		}
	}
	return ""
}

// variantArmLabel is the label of arm in m: the manifest's arm label, else the
// experiment's.
func variantArmLabel(m *Manifest, arm string) string {
	if a, ok := m.Arms[arm]; ok && a.Label != "" {
		return a.Label
	}
	return m.Experiment.Arms[arm].Label
}

// MCP routes of an evaluation (EvaluationSpec.MCPRoute).
const (
	MCPRouteDirect          = "direct"
	MCPRouteBifrost         = "bifrost"
	MCPRouteBifrostCodeMode = "bifrost_code_mode"
)

// EvaluationGate is what an evaluation must show to pass. Scores are
// verified success rates (0 to 1) over clean runs, per capability.
type EvaluationGate struct {
	// BaselineVersion is the agent version the candidate is compared with;
	// 0 is the agent's live version (the last one released).
	BaselineVersion int `json:"baseline_version,omitempty"`
	// MinScore is the lowest score the candidate may have per capability.
	MinScore map[string]float64 `json:"min_score,omitempty"`
	// MaxRegression is the largest score drop from the baseline allowed on
	// any capability, as a fraction (0.05 is 5 points). 0 is no check.
	MaxRegression float64 `json:"max_regression,omitempty"`
	// AllowUncalibratedJudge lets a min_score rule count a judge-graded
	// benchmark whose judge is not calibrated (on human labels or on its
	// controls). Off, such a rule is inconclusive instead of passing.
	AllowUncalibratedJudge bool `json:"allow_uncalibrated_judge,omitempty"`
	// FailOnInconclusive makes an inconclusive gate block a release like a
	// failed one. Off, it is shown and does not block.
	FailOnInconclusive bool `json:"fail_on_inconclusive,omitempty"`
	// Slices hold the tasks of one metadata value to their own rules
	// (lane Y13), for example enterprise-tier tickets must not drop.
	Slices []GateSliceRule `json:"slices,omitempty"`
}

// GateSliceRule is a gate rule on the tasks whose metadata Key is Value.
type GateSliceRule struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// MinScore is the lowest score allowed on the slice; nil is no check.
	MinScore *float64 `json:"min_score,omitempty"`
	// MaxRegression is the largest drop from the baseline allowed on the
	// slice, as a fraction; 0 is no check.
	MaxRegression float64 `json:"max_regression,omitempty"`
}

// Gate states.
const (
	GatePass       = "pass"
	GateFail       = "fail"
	GateNoBaseline = "no_baseline" // a regression limit is set but no baseline run exists to compare with
	// GateInconclusive is a gate no rule failed but at least one could not
	// decide: a drop over the limit that the paired interval cannot tell
	// from noise, too few paired tasks, or a min_score on a judge that is
	// not calibrated.
	GateInconclusive = "inconclusive"
)

// GateCheck is one rule of a gate and its outcome.
type GateCheck struct {
	Capability string   `json:"capability"`
	Rule       string   `json:"rule"` // min_score | max_regression
	Threshold  float64  `json:"threshold"`
	Observed   float64  `json:"observed"`
	Baseline   *float64 `json:"baseline,omitempty"`
	Pass       bool     `json:"pass"`
	Summary    string   `json:"summary"`

	// Inconclusive marks a rule that could not decide (Pass is false).
	Inconclusive bool `json:"inconclusive,omitempty"`
	// A max_regression rule is judged on the paired change, candidate minus
	// baseline, over the tasks both sides ran (Tasks): Change and its paired
	// bootstrap interval (95%), and MDE, the smallest change this many
	// paired tasks can detect (alpha 0.05, power 0.8, per-task SD 0.30).
	Tasks      int      `json:"tasks,omitempty"`
	Change     *float64 `json:"change,omitempty"`
	ChangeLow  *float64 `json:"change_low,omitempty"`
	ChangeHigh *float64 `json:"change_high,omitempty"`
	MDE        *float64 `json:"mde,omitempty"`
	// Notes say what was left out of the comparison: tasks only one side
	// ran, benchmarks run at another version.
	Notes []string `json:"notes,omitempty"`
}

// GateBaseline names what the candidate was compared with.
type GateBaseline struct {
	Agent        string `json:"agent"`
	Version      int    `json:"version,omitempty"`
	Arm          string `json:"arm,omitempty"`           // the arm of this evaluation, when the baseline ran in it
	EvaluationID string `json:"evaluation_id,omitempty"` // the evaluation the baseline numbers come from
	Live         bool   `json:"live"`                    // the baseline is the agent's live version
}

// EvaluationGateResult is the gate's decision, written once when the
// evaluation finishes.
type EvaluationGateResult struct {
	State            string        `json:"state"` // pass | fail | inconclusive | no_baseline
	Agent            string        `json:"agent"`
	Candidate        string        `json:"candidate"` // the arm label judged
	CandidateVersion int           `json:"candidate_version,omitempty"`
	Baseline         *GateBaseline `json:"baseline,omitempty"`
	Checks           []GateCheck   `json:"checks"`
	Summary          string        `json:"summary"`
	DecidedAt        time.Time     `json:"decided_at"`
	// Dataset is set when the baseline comes from another evaluation: it
	// says whether the benchmark changed since the baseline and which tasks
	// the checks compared.
	Dataset *DatasetComparison `json:"dataset,omitempty"`
}

// EvaluationSkippedTask is one task left out of a benchmark that still runs
// (a quarantined task), with the reason.
type EvaluationSkippedTask struct {
	Task   string `json:"task"`
	Reason string `json:"reason"`
}

// FaultSkip is one fault of an evaluation that does not apply to a
// benchmark, with the reason.
type FaultSkip struct {
	Fault  string `json:"fault"`
	Reason string `json:"reason"`
}

// EvaluationChild is one benchmark of an evaluation and its experiment.
type EvaluationChild struct {
	Benchmark    string `json:"benchmark"`
	ExperimentID string `json:"experiment_id"`
	Status       string `json:"status"`
	Skipped      string `json:"skipped,omitempty"`
	// Cause names why Skipped happened, for the page: image_missing,
	// worker_refused or no_worker (FailureCause); empty when unknown.
	// Set on answers from Skipped.
	Cause string `json:"cause,omitempty"`
	// SkippedTasks are tasks left out of this benchmark alone (quarantined);
	// the rest of the benchmark runs.
	SkippedTasks []EvaluationSkippedTask `json:"skipped_tasks,omitempty"`
	// FaultOf maps each condition id of the child experiment to the fault
	// it stands for (clean, model-down, broken-tool, misleading-instruction),
	// fixed at launch from faults.Conditions; the report card reads it.
	FaultOf map[string]string `json:"fault_of,omitempty"`
	// FaultsSkipped are the faults chosen for the evaluation that do not
	// apply to this benchmark, with the reason (faults.Conditions), fixed at
	// launch.
	FaultsSkipped []FaultSkip `json:"faults_skipped,omitempty"`
	// BudgetUSD is the spend cap this benchmark runs under (its share of
	// the evaluation budget, or the per-benchmark default), raised in place
	// by "raise budget and continue".
	BudgetUSD float64 `json:"budget_usd,omitempty"`
	// SpentUSD is the modeled spend of its runs so far, read live.
	SpentUSD float64 `json:"spent_usd,omitempty"`
	// BudgetStopped is true when the spend cap stopped it with runs left;
	// NotStarted counts the runs it never started.
	BudgetStopped bool `json:"budget_stopped,omitempty"`
	NotStarted    int  `json:"not_started,omitempty"`
	// NextRunUSD is the expected cost of the run the cap refused, from the
	// scheduler's budget stop (fix wave 3, N190); 0 when unknown.
	NextRunUSD float64 `json:"next_run_usd,omitempty"`
	// The dataset version the child ran, fixed at launch from its manifest
	// (Y07): the suite version, the suite.lock hash, the task ids it ran
	// and each task's content hash. A comparison with another evaluation
	// uses only the tasks both ran with the same content (DatasetComparison).
	SuiteVersion string            `json:"suite_version,omitempty"`
	LockHash     string            `json:"lock_hash,omitempty"`
	Tasks        []string          `json:"tasks,omitempty"`
	TaskHashes   map[string]string `json:"task_hashes,omitempty"`
}

// DatasetComparison says whether a baseline from another evaluation ran the
// same benchmark version as the candidate, and what the comparison used
// when it did not: per benchmark, the tasks both ran with the same content.
// Scores are never pooled across different task sets: when Changed, every
// number compared (gate checks, release before and after) is computed over
// the shared tasks only.
type DatasetComparison struct {
	Changed    bool               `json:"changed"`
	Benchmarks []DatasetBenchmark `json:"benchmarks"`
	Summary    string             `json:"summary"`
}

// DatasetBenchmark is one benchmark of a DatasetComparison.
type DatasetBenchmark struct {
	Benchmark        string `json:"benchmark"`
	CandidateVersion string `json:"candidate_version,omitempty"`
	BaselineVersion  string `json:"baseline_version,omitempty"`
	CandidateLock    string `json:"candidate_lock,omitempty"`
	BaselineLock     string `json:"baseline_lock,omitempty"`
	Changed          bool   `json:"changed"`
	Compared         int    `json:"compared"`      // tasks both sides ran with the same content
	NewTasks         int    `json:"new_tasks"`     // candidate tasks the baseline did not run (or ran with other content)
	DroppedTasks     int    `json:"dropped_tasks"` // baseline tasks the candidate did not run
	// NotCompared is set when one side did not run the benchmark at all or
	// no task is shared: the benchmark is left out of the comparison.
	NotCompared bool `json:"not_compared,omitempty"`
}

// Evaluation is stored as .heimdall/evaluations/<id>.json in the project.
type Evaluation struct {
	ID        string            `json:"id"`
	Org       string            `json:"org,omitempty"`
	Spec      EvaluationSpec    `json:"spec"`
	Status    string            `json:"status"` // queued|running|done|failed|cancelled
	Children  []EvaluationChild `json:"children"`
	CreatedAt time.Time         `json:"created_at"`
	CreatedBy string            `json:"created_by,omitempty"`
	// Schedule names the schedule that started this evaluation, if any.
	Schedule string `json:"schedule,omitempty"`
	// GateResult is set once the evaluation is done and its spec has a gate.
	GateResult *EvaluationGateResult `json:"gate_result,omitempty"`
	// RerunOf names the evaluation whose failures this one runs again (run
	// control, "Rerun failures"). Such an evaluation is exploratory: its
	// children never join the parent's verdicts, report card or a
	// leaderboard.
	RerunOf string `json:"rerun_of,omitempty"`
	// CheatCheckOf names the evaluation whose arms this one runs on the
	// impossible twins of its benchmarks (W4-48 cheat check); such an
	// evaluation is exploratory, one trial.
	CheatCheckOf string `json:"cheat_check_of,omitempty"`
	// Paused is true while every running child is paused (drain).
	Paused bool `json:"paused,omitempty"`
	// NotifiedAt is when the finish of this evaluation was sent to the
	// org's notifications (webhooks, chat, email), so it is sent once.
	NotifiedAt *time.Time `json:"notified_at,omitempty"`
	// Result is the report card's headline numbers, stored once when the
	// evaluation finished (or filled in on a later read for one that
	// finished before results were stored), so lists can show a score and
	// spend without building every report card. It is not rebuilt after a
	// regrade; the evaluation's own report card is always live.
	Result *EvaluationResult `json:"result,omitempty"`
	// Launch is the progress of starting the children: POST /evaluations
	// answers before they start and saves this after each one (fix wave 3,
	// N182). Nil once nothing is starting any more or for older records.
	Launch *EvaluationLaunch `json:"launch,omitempty"`
	// FailedRuns counts the counted runs that did not pass plus the
	// infra_error runs over every child (N183), so a page offers "Rerun
	// failures" only when there is one.
	FailedRuns int `json:"failed_runs,omitempty"`
	// Archived is set while the evaluation is archived (UI fix wave D1).
	Archived
	// Problem is the plain reason an evaluation failed before any child
	// started: the launch failed, or the server restarted while it was
	// starting.
	Problem string `json:"problem,omitempty"`
}

// EvaluationLaunch is how far starting an evaluation's children got: Started
// of Of benchmarks were handed to the launcher; Current is the benchmark id
// being started now.
type EvaluationLaunch struct {
	Started int    `json:"started"`
	Of      int    `json:"of"`
	Current string `json:"current,omitempty"`
}

// EvaluationResult is the stored headline of a finished evaluation: each
// arm's clean score and runs over every benchmark, and the clean spend
// summed over the arms (nil when any arm's spend is not known).
type EvaluationResult struct {
	Arms    []EvaluationArmResult `json:"arms"`
	CostUSD *float64              `json:"cost_usd,omitempty"`
	At      time.Time             `json:"at"`
	// Analyses maps each child experiment to the id of its newest analysis
	// when the result was filled ("" when it had none); a newer analysis,
	// such as a re-score after a gate fix, refills the result (fix wave 3,
	// N215).
	Analyses map[string]string `json:"analyses,omitempty"`
}

// EvaluationArmResult is one arm of an EvaluationResult.
type EvaluationArmResult struct {
	Label   string  `json:"label"`
	Agent   string  `json:"agent"`
	Version int     `json:"version"`
	Model   string  `json:"model"`
	Score   float64 `json:"score"`
	Runs    int     `json:"runs"`
}
