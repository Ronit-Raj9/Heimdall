package schemas

import "time"

// Policy is a team's release policy, read from a policy YAML file. It can only
// add requirements: the baseline integrity gates always run whatever a policy
// says. Its hash is recorded in the manifest.
type Policy struct {
	PolicyVersion int           `json:"policy_version" yaml:"policy_version"`
	Name          string        `json:"name" yaml:"name"`
	Description   string        `json:"description,omitempty" yaml:"description"`
	OnUnknown     string        `json:"on_unknown" yaml:"on_unknown"` // block | warn; unset: a small-sample unknown warns, any other unknown blocks
	Checks        []PolicyCheck `json:"checks" yaml:"checks"`

	JudgeCalibration *JudgeCalibrationPolicy `json:"judge_calibration,omitempty" yaml:"judge_calibration"` // W8 judge calibration: thresholds of the judge_calibration instrument (judgecalibration.go)
}

// PolicyCheck is one rule. Exactly one of Rule, Max, Min or Equals applies.
type PolicyCheck struct {
	ID          string   `json:"id" yaml:"id"`
	Description string   `json:"description,omitempty" yaml:"description"`
	Metric      string   `json:"metric" yaml:"metric"`                 // a per-run metric name, or an aggregate: integrity_violations, cost_per_verified_success
	Condition   string   `json:"condition,omitempty" yaml:"condition"` // default: the primary condition; "all" for every condition
	Arm         string   `json:"arm,omitempty" yaml:"arm"`             // default B, the candidate
	Rule        string   `json:"rule,omitempty" yaml:"rule"`           // no_regression | improved
	Max         *float64 `json:"max,omitempty" yaml:"max"`
	Min         *float64 `json:"min,omitempty" yaml:"min"`
	Equals      *float64 `json:"equals,omitempty" yaml:"equals"`
	MinN        int      `json:"min_n,omitempty" yaml:"min_n"`       // fewer observed runs than this gives unknown
	Severity    string   `json:"severity,omitempty" yaml:"severity"` // block (default) | warn
}

// PolicyStatus is one check's result.
type PolicyStatus string

// Policy statuses.
const (
	PolicyPass    PolicyStatus = "pass"
	PolicyFail    PolicyStatus = "fail"
	PolicyUnknown PolicyStatus = "unknown"
)

// PolicyDecision is the policy's overall result.
type PolicyDecision string

// Policy decisions. block means the release must not ship.
const (
	PolicyAllow PolicyDecision = "allow"
	PolicyWarn  PolicyDecision = "warn"
	PolicyBlock PolicyDecision = "block"
)

// PolicyResult is the evaluation of a policy over one analysis.
type PolicyResult struct {
	PolicyName  string              `json:"policy_name"`
	PolicyHash  string              `json:"policy_hash"`
	Decision    PolicyDecision      `json:"decision"`
	Checks      []PolicyCheckResult `json:"checks"`
	EvaluatedAt time.Time           `json:"evaluated_at"`
}

// PolicyCheckResult is one check's result with its evidence.
type PolicyCheckResult struct {
	ID        string       `json:"id"`
	Metric    string       `json:"metric"`
	Condition string       `json:"condition"`
	Arm       string       `json:"arm"`
	Status    PolicyStatus `json:"status"`
	Blocking  bool         `json:"blocking"` // this result alone blocks the release
	Observed  Value        `json:"observed"`
	Threshold string       `json:"threshold"` // e.g. "<= 0.05", "no regression"
	N         int          `json:"n"`         // observed runs behind Observed
	Reason    string       `json:"reason,omitempty"`
	Evidence  []string     `json:"evidence,omitempty"` // run ids
}

// ReleaseRecord is one entry of the hash-chained release log. It is a record
// of a decision and the evidence behind it, not a cryptographic signature.
type ReleaseRecord struct {
	ReleaseID      string         `json:"release_id"`
	ExpID          string         `json:"exp_id"`
	AnalysisID     string         `json:"analysis_id"`
	Arm            string         `json:"arm"` // the arm released, normally B
	AgentConfigID  string         `json:"agent_config_id"`
	ConfigHash     string         `json:"config_hash"`
	Decision       Decision       `json:"decision"`
	Action         Action         `json:"action"`
	PolicyName     string         `json:"policy_name,omitempty"`
	PolicyHash     string         `json:"policy_hash,omitempty"`
	PolicyDecision PolicyDecision `json:"policy_decision,omitempty"`
	ManifestHash   string         `json:"manifest_hash"`
	SealsDigest    string         `json:"seals_digest"` // sha256 over every counted run's seal hash, sorted
	Approver       string         `json:"approver"`
	Note           string         `json:"note,omitempty"`
	Override       string         `json:"override,omitempty"` // set only when released against a block, with the reason
	EvidencePath   string         `json:"evidence_path,omitempty"`
	EvidenceSHA256 string         `json:"evidence_sha256,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	PrevHash       string         `json:"prev_hash"`
	Hash           string         `json:"hash"`

	// Agent version fields: which version of which agent this release puts
	// live and which version it replaces (0: none was live). Empty on a
	// record written before agent versions existed.
	AgentID         string `json:"agent_id,omitempty"`
	Version         int    `json:"version,omitempty"`
	ReplacesVersion int    `json:"replaces_version,omitempty"`
	// EvaluationID is set when the release covers a whole evaluation (every
	// benchmark it ran); ExpID and AnalysisID then name its first child and
	// Children lists them all.
	EvaluationID string          `json:"evaluation_id,omitempty"`
	Children     []ReleaseChild  `json:"children,omitempty"`
	Summary      *ReleaseSummary `json:"summary,omitempty"`
	Gate         string          `json:"gate,omitempty"` // the evaluation's gate state at release time: pass, fail, no_baseline
}

// ReleaseChild is one benchmark of a release that covers an evaluation.
type ReleaseChild struct {
	Benchmark      string         `json:"benchmark"`
	ExpID          string         `json:"exp_id"`
	AnalysisID     string         `json:"analysis_id"`
	Decision       Decision       `json:"decision"`
	Action         Action         `json:"action"`
	PolicyName     string         `json:"policy_name,omitempty"`
	PolicyDecision PolicyDecision `json:"policy_decision,omitempty"`
	ManifestHash   string         `json:"manifest_hash"`
	SealsDigest    string         `json:"seals_digest"`
	Runs           int            `json:"runs"`
}

// ReleaseSide is one agent version's numbers in a release summary. Score
// is the verified success rate over clean runs with its 95% interval.
// CostPerSolved, FalseCompletion and FaultSurvival are nil when unknown.
type ReleaseSide struct {
	Label           string   `json:"label"` // the evaluation arm
	Agent           string   `json:"agent"`
	Version         int      `json:"version,omitempty"`
	Model           string   `json:"model,omitempty"`
	EvaluationID    string   `json:"evaluation_id,omitempty"`
	Score           float64  `json:"score"`
	CILow           float64  `json:"ci_low"`
	CIHigh          float64  `json:"ci_high"`
	Runs            int      `json:"runs"`
	CostPerSolved   *float64 `json:"cost_per_solved_usd,omitempty"`
	FalseCompletion *float64 `json:"false_completion,omitempty"` // share of failed runs the agent claimed as solved
	FaultSurvival   *float64 `json:"fault_survival,omitempty"`   // success rate on runs with a fault injected
}

// ReleaseCapabilityScore is one capability's score, before and after.
type ReleaseCapabilityScore struct {
	Capability string   `json:"capability"`
	Candidate  ScoreCI  `json:"candidate"`
	Baseline   *ScoreCI `json:"baseline,omitempty"`
}

// ScoreCI is a score with its interval and run count.
type ScoreCI struct {
	Score  float64 `json:"score"`
	CILow  float64 `json:"ci_low"`
	CIHigh float64 `json:"ci_high"`
	Runs   int     `json:"runs"`
}

// ReleaseSummary is the before and after of a release: the candidate (the
// version released) and the baseline (the version it replaces).
type ReleaseSummary struct {
	Candidate    ReleaseSide              `json:"candidate"`
	Baseline     *ReleaseSide             `json:"baseline,omitempty"`
	Capabilities []ReleaseCapabilityScore `json:"capabilities,omitempty"`
	Note         string                   `json:"note,omitempty"` // e.g. the baseline is not the live version
}

// ReleaseRequest is POST /releases.
type ReleaseRequest struct {
	ExpID      string `json:"exp_id"`
	AnalysisID string `json:"analysis_id,omitempty"` // default: the latest analysis
	Approver   string `json:"approver"`
	Note       string `json:"note,omitempty"`
	Override   string `json:"override,omitempty"` // required to release against a block

	// EvaluationID asks for a release of a whole evaluation: POST
	// /evaluations/{id}/release fills it and Evaluation, never the body of
	// POST /releases.
	EvaluationID string             `json:"evaluation_id,omitempty"`
	Evaluation   *ReleaseEvaluation `json:"-"`
}

// ReleaseEvaluation is what the server resolved for a release of an
// evaluation: the agent version, the version it replaces, the children and
// the numbers. It is never read from a request body.
type ReleaseEvaluation struct {
	EvaluationID    string
	AgentID         string
	Version         int
	ReplacesVersion int
	CandidateArm    string // the evaluation arm label released, also the experiment arm id
	Children        []ReleaseChildRef
	Decision        Decision
	Blocked         []string // reasons from the evaluation as a whole (gate, overall verdict)
	Gate            string
	Summary         *ReleaseSummary
}

// ReleaseChildRef names one child experiment of a release of an evaluation.
type ReleaseChildRef struct {
	Benchmark string
	ExpID     string
}

// ReleaseList is GET /releases.
type ReleaseList struct {
	Releases []ReleaseRecord `json:"releases"`
	ChainOK  bool            `json:"chain_ok"`
}

// TriageGroup is one observed failure pattern across an experiment: how often
// it appeared per arm and in which runs. It is an observed pattern, never a
// root cause.
type TriageGroup struct {
	Pattern     string              `json:"pattern"` // metric name, e.g. pattern_loop
	Label       string              `json:"label"`
	Description string              `json:"description"`
	PerArm      map[string]Ratio    `json:"per_arm"` // runs showing the pattern over runs with an observed value
	Runs        map[string][]string `json:"runs"`    // arm -> run ids showing the pattern
}

// Triage is GET /experiments/{id}/triage.
type Triage struct {
	ExpID  string        `json:"exp_id"`
	Groups []TriageGroup `json:"groups"`
}
