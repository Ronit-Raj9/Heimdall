package schemas

import "time"

// Compliance evidence pack (Eval integrity area, S2; docs/compliance.md). A
// pack maps each test an experiment executed to the OWASP and NIST ids it
// gives evidence for, states what the bench retains for record-keeping, and
// points at the sealed runs behind every row. It certifies nothing: the
// NotCertified list says so in the pack itself.

// Compliance frameworks named in a pack.
const (
	FrameworkOWASPLLM2025     = "owasp_llm_2025"     // OWASP Top 10 for LLM Applications 2025
	FrameworkOWASPAgentic2026 = "owasp_agentic_2026" // OWASP Top 10 for Agentic Applications 2026
	FrameworkNISTAIRMF        = "nist_ai_rmf_1_0"    // NIST AI Risk Management Framework 1.0 (AI 100-1)
	FrameworkEUAIAct          = "eu_ai_act"          // Regulation (EU) 2024/1689
)

// Compliance test kinds.
const (
	ComplianceTestCondition = "condition" // clean or a probe the experiment ran
	ComplianceTestGate      = "gate"      // a gate evaluated on the runs
)

// CompliancePack is the evidence pack of one or more experiments.
type CompliancePack struct {
	Version       string                  `json:"version"` // pack format version
	GeneratedAt   time.Time               `json:"generated_at"`
	Generator     string                  `json:"generator"` // bench version that built it
	Experiments   []ComplianceExperiment  `json:"experiments"`
	Release       *ComplianceRelease      `json:"release,omitempty"`
	Tests         []ComplianceTest        `json:"tests"`
	Unmapped      []string                `json:"unmapped,omitempty"` // test ids with no framework mapping
	Frameworks    []ComplianceFramework   `json:"frameworks"`
	Functions     []ComplianceFunction    `json:"nist_functions"`
	RecordKeeping ComplianceRecordKeeping `json:"record_keeping"`
	NotCertified  []string                `json:"not_certified"`
	Warnings      []string                `json:"warnings,omitempty"`
}

// ComplianceExperiment is one experiment a pack covers and the state of its
// evidence when the pack was built.
type ComplianceExperiment struct {
	ExpID         string    `json:"exp_id"`
	Name          string    `json:"name,omitempty"`
	Suite         string    `json:"suite"`
	SuiteVersion  string    `json:"suite_version,omitempty"`
	SuiteLockHash string    `json:"suite_lock_hash,omitempty"`
	ManifestHash  string    `json:"manifest_hash"`
	FrozenAt      time.Time `json:"frozen_at"`
	Mode          Mode      `json:"mode,omitempty"`    // live | recorded | synthetic, from the analysis
	Realism       string    `json:"realism,omitempty"` // standard | neutral
	Arms          []string  `json:"arms"`
	Conditions    []string  `json:"conditions"`
	CountedRuns   int       `json:"counted_runs"`
	RunsVerified  int       `json:"runs_verified"` // counted runs whose seal verified when the pack was built
	RunProblems   []string  `json:"run_problems,omitempty"`
	ChainStatus   string    `json:"chain_status"` // verified | broken | not_checked
	ChainDetail   string    `json:"chain_detail,omitempty"`
	SealsLogTail  string    `json:"seals_log_tail,omitempty"` // the last line of seals.log: "<run_id> <seal hash>"
	AnalysisID    string    `json:"analysis_id,omitempty"`
	Decision      Decision  `json:"decision,omitempty"`
	Action        Action    `json:"action,omitempty"`
	GradeSource   string    `json:"grade_source,omitempty"`
	Path          string    `json:"path"` // experiment folder, relative to the runs root
}

// ComplianceRelease is the release a pack was exported with.
type ComplianceRelease struct {
	ReleaseID      string    `json:"release_id"`
	ExpID          string    `json:"exp_id"`
	AnalysisID     string    `json:"analysis_id"`
	Decision       Decision  `json:"decision"`
	Action         Action    `json:"action"`
	Approver       string    `json:"approver"`
	Override       string    `json:"override,omitempty"`
	SealsDigest    string    `json:"seals_digest"`
	EvidenceSHA256 string    `json:"evidence_sha256,omitempty"`
	Hash           string    `json:"hash"`
	CreatedAt      time.Time `json:"created_at"`
}

// ComplianceTest is one executed test: a condition the experiment ran or a
// gate evaluated on its runs, with the framework ids it gives evidence for.
type ComplianceTest struct {
	ID       string               `json:"id"`   // "condition:<id>" or "gate:<name>"
	Kind     string               `json:"kind"` // condition | gate
	Name     string               `json:"name"` // the probe id, "clean" or the gate name
	Title    string               `json:"title"`
	Axis     string               `json:"axis,omitempty"`   // probe axis
	Family   string               `json:"family,omitempty"` // probe ps2_family
	Effect   GateEffect           `json:"effect,omitempty"` // gate effect: veto | invalidate | report | admission
	ExpIDs   []string             `json:"exp_ids"`
	Runs     int                  `json:"runs"`      // counted runs under the condition, or runs the gate was evaluated on
	Fired    int                  `json:"fired"`     // runs where the gate fired, or (condition) where any veto gate fired
	MappedBy string               `json:"mapped_by"` // id | axis | family | none
	Mappings []ComplianceMapping  `json:"mappings"`
	Evidence []ComplianceEvidence `json:"evidence"`
}

// ComplianceMapping is one framework id a test gives evidence for. Verified
// is true only when the id and its name were read from the source named here
// when the table was written; everything else is marked unverified.
type ComplianceMapping struct {
	Framework string `json:"framework"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Verified  bool   `json:"verified"`
	Source    string `json:"source"`
	Note      string `json:"note,omitempty"` // why this test bears on the id, and how far
}

// ComplianceEvidence points at one sealed run behind a test.
type ComplianceEvidence struct {
	RunID     string  `json:"run_id"`
	ExpID     string  `json:"exp_id"`
	Task      string  `json:"task"`
	Arm       string  `json:"arm"`
	Condition string  `json:"condition"`
	Trial     int     `json:"trial"`
	Outcome   Outcome `json:"outcome,omitempty"`
	Fired     bool    `json:"fired,omitempty"`
	SealHash  string  `json:"seal_hash"`
	Path      string  `json:"path"` // run folder, relative to the runs root
}

// ComplianceFramework is one id of the catalog a pack used, so a reader can
// check names and sources without the code.
type ComplianceFramework struct {
	Framework string `json:"framework"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Verified  bool   `json:"verified"`
	Source    string `json:"source"`
}

// ComplianceFunction is one NIST AI RMF subcategory the pack speaks to at
// the level of the whole experiment, with what the pack offers for it and
// what it does not.
type ComplianceFunction struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Verified bool   `json:"verified"`
	Source   string `json:"source"`
	Evidence string `json:"evidence"` // what the pack offers
	Gap      string `json:"gap"`      // what it does not
}

// ComplianceRecordKeeping states what the bench retains for each run, where
// and for how long, beside the EU AI Act articles on logs.
type ComplianceRecordKeeping struct {
	Retained       []ComplianceArtifact `json:"retained"`
	Location       string               `json:"location"`
	Retention      string               `json:"retention"`
	ExternalAnchor bool                 `json:"external_anchor"`
	Articles       []ComplianceMapping  `json:"articles"`
	Notes          []string             `json:"notes"`
}

// ComplianceArtifact is one kind of file kept in a sealed run folder.
type ComplianceArtifact struct {
	File   string `json:"file"`
	Holds  string `json:"holds"`
	Sealed bool   `json:"sealed"`
}
