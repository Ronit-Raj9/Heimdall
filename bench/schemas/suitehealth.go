package schemas

import "time"

// This file holds suite health (docs/suite-health.md) and the held-out split
// (docs/held-out-and-canaries.md): the quarantine sidecar written next to
// suite.lock, and the payload of `suite health -json` and
// GET /suites/{id}/health. Health is computed from sealed runs across
// experiments; it is a reading of the record, never part of a lock, a
// manifest or a seal.

// Task splits (Task.Split, ConditionSpec.Split). An empty split is public.
const (
	SplitPublic  = "public"
	SplitHeldOut = "held_out"
)

// SuiteHealthFileName is the quarantine sidecar beside suite.lock. It is
// hashed into nothing: quarantining a task never changes the lock hash, so
// manifests and seals written before stay valid.
const SuiteHealthFileName = "suite.health.json"

// SuiteHealthFile is the content of suite.health.json.
type SuiteHealthFile struct {
	Suite       string                    `json:"suite"`
	Quarantined map[string]TaskQuarantine `json:"quarantined"` // task id -> why it is out of new plans
}

// TaskQuarantine records why a task is kept out of new plans. Source is
// manual (`suite quarantine`) or auto (`suite health -auto-quarantine`, only
// ever on verifier flakiness evidence).
type TaskQuarantine struct {
	Reason      string    `json:"reason"`
	At          time.Time `json:"at"`
	Source      string    `json:"source"`                 // manual | auto
	TaskVersion string    `json:"task_version,omitempty"` // the version quarantined
	Evidence    []string  `json:"evidence,omitempty"`     // verifier evidence lines behind an auto quarantine
}

// Quarantine sources (TaskQuarantine.Source).
const (
	QuarantineManual = "manual"
	QuarantineAuto   = "auto"
)

// Task health flags (TaskHealth.Flags).
const (
	HealthFlakyVerifier      = "flaky_verifier"      // the same output was graded differently, or the reference did not always pass
	HealthSaturated          = "saturated"           // every scored run of every agent config passed
	HealthDead               = "dead"                // every scored run of every agent config failed
	HealthNoDiscrimination   = "no_discrimination"   // configs do not differ: saturated or dead
	HealthDifficultyMismatch = "difficulty_mismatch" // observed difficulty differs from the declared one
	HealthInfraTrouble       = "infra_trouble"       // infra failures above the threshold
	HealthTimeouts           = "timeouts"            // timeouts at the cap above the threshold
	HealthInsufficientData   = "insufficient_data"   // too few scored runs or configs to label difficulty
)

// Verifier evidence kinds (VerifierEvidence.Kind).
const (
	EvidenceRegradeDisagrees = "regrade_disagrees"   // a regrade at the same suite and grader version flipped the verdict
	EvidenceSameDiffDiffers  = "same_output_differs" // runs with an identical output hash got different verdicts
	EvidenceAdmissionRef     = "admission_reference" // admission reference_results were not all pass
	EvidenceOracleFailed     = "oracle_failed"       // a scripted oracle run (the reference) failed
)

// SuiteHealth is `suite health -json` and GET /suites/{id}/health.
type SuiteHealth struct {
	Suite       string           `json:"suite"`
	Version     string           `json:"version"`
	ComputedAt  time.Time        `json:"computed_at"`
	Experiments int              `json:"experiments"` // experiments of this suite id that were read
	Runs        int              `json:"runs"`        // sealed attempts read, synthetic included
	Thresholds  HealthThresholds `json:"thresholds"`
	Tasks       []TaskHealth     `json:"tasks"`
	Counts      map[string]int   `json:"counts"` // flag -> tasks carrying it
	Notes       []string         `json:"notes"`
}

// HealthThresholds are the cut-offs health applied, so a reader can see what
// "hard" or "infra trouble" meant for this report.
type HealthThresholds struct {
	MinRuns       int     `json:"min_runs"`       // scored non-synthetic runs before difficulty is labelled
	MinConfigs    int     `json:"min_configs"`    // distinct agent configs before difficulty is labelled
	EasyAbove     float64 `json:"easy_above"`     // pass rate at or above: easy
	HardBelow     float64 `json:"hard_below"`     // pass rate at or below: hard
	InfraRate     float64 `json:"infra_rate"`     // infra share above: infra_trouble
	TimeoutRate   float64 `json:"timeout_rate"`   // timeout share above: timeouts
	IntervalLevel float64 `json:"interval_level"` // confidence of the Wilson interval, e.g. 0.95
}

// TaskHealth is one task's health. Verifier is about the grader; Variance is
// about the agents. They are kept apart: a task where one agent config
// sometimes passes and sometimes fails is ordinary agent variance, while a
// verifier that grades the same output differently corrupts verdicts.
type TaskHealth struct {
	Task       string           `json:"task"`
	Split      string           `json:"split"` // public | held_out
	Quarantine *TaskQuarantine  `json:"quarantine"`
	Verifier   VerifierHealth   `json:"verifier"`
	Variance   OutcomeVariance  `json:"variance"`
	Infra      InfraHealth      `json:"infra"`
	Difficulty DifficultyHealth `json:"difficulty"`
	Flags      []string         `json:"flags"`
}

// VerifierHealth is the evidence that the verifier, not the agent, is
// unstable. Checks counts the comparisons that could have shown it: regrade
// pairs, groups of runs sharing an output hash, oracle runs and admission
// reference repeats.
type VerifierHealth struct {
	Flaky    bool               `json:"flaky"`
	Checks   int                `json:"checks"`
	Evidence []VerifierEvidence `json:"evidence"`
}

// VerifierEvidence is one observation of the verifier disagreeing with itself
// or rejecting the reference.
type VerifierEvidence struct {
	Kind   string   `json:"kind"`
	Detail string   `json:"detail"`
	ExpID  string   `json:"exp_id,omitempty"`
	RunIDs []string `json:"run_ids,omitempty"`
}

// OutcomeVariance is agent variance per (task, agent config): how often the
// same config both passed and failed across trials. Unstable counts configs
// with a pass rate strictly between 0 and 1; MeanVariance is the mean
// Bernoulli variance p(1-p) over configs with at least two scored trials.
type OutcomeVariance struct {
	Configs      int             `json:"configs"`
	Unstable     int             `json:"unstable"`
	MeanVariance float64         `json:"mean_variance"`
	PerConfig    []ConfigOutcome `json:"per_config"`
}

// ConfigOutcome is one agent config's record on one task, non-synthetic runs
// only.
type ConfigOutcome struct {
	Config     string  `json:"config"`      // agent config id
	ConfigHash string  `json:"config_hash"` // the identity runs are grouped by
	Trials     int     `json:"trials"`      // scored runs
	Passes     int     `json:"passes"`
	PassRate   float64 `json:"pass_rate"`
}

// InfraHealth counts runs that ended for reasons outside the agent. Attempts
// includes aborted_infra retries and synthetic runs: infra trouble is a
// property of the task's environment whoever runs it.
type InfraHealth struct {
	Attempts         int     `json:"attempts"`
	AbortedInfra     int     `json:"aborted_infra"`
	InfraError       int     `json:"infra_error"`
	InvalidEvaluator int     `json:"invalid_evaluator"`
	Timeouts         int     `json:"timeouts"` // the agent hit its wall-time cap
	Rate             float64 `json:"rate"`     // (aborted_infra + infra_error + invalid_evaluator) / attempts
	TimeoutRate      float64 `json:"timeout_rate"`
}

// DifficultyHealth is the empirical difficulty from non-synthetic runs. The
// pass rate pools every scored run; Interval is its Wilson score interval.
// Observed is unknown below the run or config minimum. Discrimination says
// whether the task separates agents at all: a task every config always
// passes, or always fails, carries no information about which is better.
type DifficultyHealth struct {
	Declared       string      `json:"declared"` // task.yaml difficulty; empty when unset
	Observed       string      `json:"observed"` // easy | medium | hard | unknown
	Match          string      `json:"match"`    // match | mismatch | undeclared | unknown
	PassRate       *float64    `json:"pass_rate"`
	Interval       *[2]float64 `json:"interval"`
	Runs           int         `json:"runs"`
	Configs        int         `json:"configs"`
	Discrimination string      `json:"discrimination"` // discriminating | always_pass | always_fail | single_config | unknown
	Spread         *float64    `json:"spread"`         // best minus worst config pass rate
}
