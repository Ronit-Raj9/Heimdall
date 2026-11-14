package schemas

import "time"

// Policy is a team's release policy, read from a policy YAML file. It can only
// add requirements: the baseline integrity gates always run whatever a policy
// says. Its hash is recorded in the manifest.
type Policy struct {
	PolicyVersion int           `json:"policy_version" yaml:"policy_version"`
	Name          string        `json:"name" yaml:"name"`
	Description   string        `json:"description,omitempty" yaml:"description"`
	OnUnknown     string        `json:"on_unknown" yaml:"on_unknown"` // block (default) | warn
	Checks        []PolicyCheck `json:"checks" yaml:"checks"`
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
}

// ReleaseRequest is POST /releases.
type ReleaseRequest struct {
	ExpID      string `json:"exp_id"`
	AnalysisID string `json:"analysis_id,omitempty"` // default: the latest analysis
	Approver   string `json:"approver"`
	Note       string `json:"note,omitempty"`
	Override   string `json:"override,omitempty"` // required to release against a block
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
