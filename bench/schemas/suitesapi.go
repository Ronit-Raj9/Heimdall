package schemas

import "time"

// This file holds the payloads of GET /suites, GET /suites/{id}/tasks/{task}
// and GET /agents: what the UI shows about the benchmark packs and agent
// configs on disk. They are read-only views built from suite.yaml, task.yaml,
// suite.lock and agents/*.yaml. By design they never carry a hidden verifier
// test, a reference solution, an admission control, a hidden test name, an
// admission check's raw detail, a probe's markers or an environment value.

// Suite lock states (SuiteSummary.LockState).
const (
	SuiteLocked   = "locked"   // suite.lock exists and every hash matches the files
	SuiteUnlocked = "unlocked" // no suite.lock yet
	SuiteStale    = "stale"    // suite.lock exists but files changed since it was written
	SuiteLockErr  = "unknown"  // suite.lock exists but the files could not be hashed
)

// Admission controls (SuiteAdmissionCheck.Control): the family an admission
// check belongs to, so the UI can show one row per control.
const (
	ControlReference  = "reference"   // the reference solution passes, every time
	ControlEmpty      = "empty"       // the empty patch fails
	ControlMutant     = "mutant"      // each mutant fails
	ControlExploit    = "exploit"     // the exploit control fails
	ControlNullAgent  = "null_agent"  // the noop and refuse agents fail
	ControlStaleNotes = "stale_notes" // the reference still passes with the stale notes overlaid
	ControlSecrets    = "secrets"     // no file matches the secret patterns
	ControlNetwork    = "network"     // the verifier ran with no network
	ControlOther      = "other"       // a check this version of the UI does not know
)

// Task admission states beyond TaskStatus (SuiteTask.Status).
const (
	TaskNotLocked = "not_locked" // the task is not in suite.lock
)

// SuiteList is GET /suites: every pack under the benchmarks root, sorted by
// id. A pack that fails to load is listed with Error set and its other fields
// empty, so one broken pack never hides the others.
type SuiteList struct {
	Suites []SuiteSummary `json:"suites"`
}

// SuiteSummary is one pack: its suite.yaml, its lock state and its tasks.
type SuiteSummary struct {
	ID               string       `json:"id"`
	Version          string       `json:"version"`
	Description      string       `json:"description"`
	Environment      string       `json:"environment"`     // environment part name
	Grader           string       `json:"grader"`          // grader part name
	VerifierResult   string       `json:"verifier_result"` // junit | reward | exit_code (default junit)
	VerifierTimeoutS int          `json:"verifier_timeout_s"`
	PassThreshold    *float64     `json:"pass_threshold"` // lowest passing reward; null unless reward
	BaseImage        SuiteImage   `json:"base_image"`
	LockHash         string       `json:"lock_hash"`            // sha256 of suite.lock; empty when unlocked
	LockState        string       `json:"lock_state"`           // locked | unlocked | stale | unknown
	LockedAt         *time.Time   `json:"locked_at"`            // suite.lock created_at; null when unlocked
	Drift            []SuiteDrift `json:"drift"`                // what no longer matches suite.lock
	LockError        string       `json:"lock_error,omitempty"` // why the lock could not be checked (lock_state unknown)
	TaskCount        int          `json:"task_count"`
	AdmittedCount    int          `json:"admitted_count"` // tasks admitted whose files still match the lock
	Probes           []SuiteProbe `json:"probes"`
	Tasks            []SuiteTask  `json:"tasks"`
	Error            string       `json:"error,omitempty"` // why the pack failed to load
	// Built-in library (suite/library.go): Builtin is true and Source
	// "library" when the pack loads from the server's read-only library.
	Builtin bool   `json:"builtin,omitempty"`
	Source  string `json:"source,omitempty"`

	// Scenario fields (scenario.go).
	Track    string        `json:"track,omitempty"`    // coding when the suite sets none
	Grading  string        `json:"grading,omitempty"`  // verified | judged
	Output   string        `json:"output,omitempty"`   // diff | answer | state
	Services []string      `json:"services,omitempty"` // ids of the suite's services
	Imported *ImportRecord `json:"imported,omitempty"` // where an imported suite came from
	// Artifacts are the suite's declared output artifacts (output kind
	// artifact); a task without its own list uses them.
	Artifacts []ArtifactSpec `json:"artifacts,omitempty"`

	// Suite health fields (suitehealth.go). Held-out tasks are listed by id
	// with their counts only, unless the server config sets
	// held_out.visible.
	HeldOutCount     int  `json:"held_out_count,omitempty"`    // tasks with split held_out
	QuarantinedCount int  `json:"quarantined_count,omitempty"` // tasks in suite.health.json
	HasCanary        bool `json:"has_canary,omitempty"`        // suite.yaml sets a canary

	// Lock fingerprints (docs/lock-fingerprints.md). LockKind is
	// fingerprinted when suite.lock binds evaluator settings, legacy when it
	// has no fingerprints section, empty when unlocked. FingerprintDrift
	// lists what no longer matches the recorded fingerprints; planning
	// refuses while it is not empty.
	LockKind         string             `json:"lock_kind,omitempty"`
	FingerprintDrift []FingerprintDrift `json:"fingerprint_drift,omitempty"`

	// Judge is the pinned judge of a judged suite, with what its readiness
	// check costs in judge calls (Y07: the Build tab asks for consent with
	// that number); nil for a verified suite.
	Judge *SuiteJudge `json:"judge,omitempty"`

	// U3: the suite's tiers (docs/suite-tiers.md), pr, nightly and headroom
	// first, then the others by name. nightly is listed even when suite.yaml
	// does not define it, as every task.
	Tiers []SuiteTier `json:"tiers,omitempty"`
}

// SuiteJudge is a judged suite's pinned judge on the suites API.
type SuiteJudge struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Votes    int    `json:"votes"` // calls per judged grade (votes, doubled with an auditor)
	// ReadinessCalls is about how many judge calls the readiness check of
	// every task makes: three reference runs plus every near miss and wrong
	// answer that reaches the judge, times Votes.
	ReadinessCalls int `json:"readiness_calls"`
}

// SuiteTier is one tier of a suite: a named task subset an experiment can
// pick with `tier:`. Tasks are ids in suite order; a held-out task is listed
// by id like everywhere else in the suites API.
type SuiteTier struct {
	Name    string   `json:"name"`
	Tasks   []string `json:"tasks"`
	Defined bool     `json:"defined"`         // false for nightly when suite.yaml leaves it out
	Error   string   `json:"error,omitempty"` // why planning would refuse the tier, e.g. an unknown task
}

// Lock kinds (SuiteSummary.LockKind).
const (
	LockKindFingerprinted = "fingerprinted"
	LockKindLegacy        = "legacy"
)

// SuiteImage is a suite's base image: the toolchain every task and verifier
// runs on.
type SuiteImage struct {
	Tag    string `json:"tag"`
	Digest string `json:"digest,omitempty"` // filled by `suite lock` once the image is built
}

// SuiteDrift is one difference between suite.lock and the files on disk, as
// re-locking would record it (suite.Diff of the lock and a fresh lock).
type SuiteDrift struct {
	Scope  string   `json:"scope"` // suite | task | probe
	ID     string   `json:"id"`
	Level  string   `json:"level"`  // PATCH | MINOR | MAJOR
	Action string   `json:"action"` // reuse | regrade | rerun | added | removed
	Fields []string `json:"fields"` // lock fields that differ, e.g. repo_tree_sha256
}

// SuiteProbe is one probe of a suite. The apply block (fault plans, canary
// markers, overlay sources) is left out: it is how the probe works, not what
// it covers.
type SuiteProbe struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Layer          string `json:"layer"`      // fixture | gateway | tool_shim
	PS2Family      string `json:"ps2_family"` // unavailable_model | broken_tool | misleading_instruction
	Axis           string `json:"axis"`       // diagnostics row label
	GoalPreserving bool   `json:"goal_preserving"`
	SafeBehavior   string `json:"safe_behavior"`
}

// SuiteTask is one task as the suite page lists it. Hidden tests are counted,
// never named.
type SuiteTask struct {
	ID              string                `json:"id"`
	Version         string                `json:"version"`
	Title           string                `json:"title"`
	Project         string                `json:"project"`
	Kind            string                `json:"kind"`
	Difficulty      string                `json:"difficulty"` // easy | medium | hard; empty when unset
	Capabilities    []string              `json:"capabilities"`
	Tags            []string              `json:"tags"`
	Provenance      *TaskProvenance       `json:"provenance"`        // null when the task records none
	Status          string                `json:"status"`            // lock status, or not_locked
	Stale           bool                  `json:"stale"`             // the task's files no longer match its lock entry
	Admitted        bool                  `json:"admitted"`          // admitted, every check passed, and not stale
	AdmittedAt      *time.Time            `json:"admitted_at"`       // when admission last ran; null when never
	Checks          []SuiteAdmissionCheck `json:"checks"`            // the last admission's checks, in run order
	HiddenTestFiles int                   `json:"hidden_test_files"` // files in the hidden verifier folder
	RequiredTests   int                   `json:"required_tests"`    // hidden test ids that must run and pass
	ProtectedGlobs  int                   `json:"protected_globs"`   // globs of visible files that must not change
	Mutants         int                   `json:"mutants"`           // mutant controls declared
	AgentTimeoutS   int                   `json:"agent_timeout_s"`

	// Scenario fields (scenario.go); each is empty for a repository task.
	// Services carry no environment, seed files or commands.
	Environment *TaskEnvironment `json:"environment,omitempty"` // the task's own image
	Services    []ServiceSpec    `json:"services,omitempty"`    // started beside the agent, after the suite's
	Sessions    []SessionSpec    `json:"sessions,omitempty"`    // in run order; empty means one session
	Output      *OutputSpec      `json:"output,omitempty"`      // the task's own output; empty uses the suite's
	Skills      []string         `json:"skills,omitempty"`      // skill folder names, each holding a SKILL.md
	Impossible  bool             `json:"impossible,omitempty"`  // no honest solution exists: a pass is an integrity violation

	// Suite health fields (suitehealth.go).
	Split      string          `json:"split,omitempty"`      // held_out; empty for public
	Redacted   bool            `json:"redacted,omitempty"`   // held-out task served by id only: title, files and checks withheld
	Quarantine *TaskQuarantine `json:"quarantine,omitempty"` // set while the task is out of new plans
}

// SuiteAdmissionCheck is one recorded admission check. Summary keeps only the
// grader's counts (tests_ran, failures, errors, skipped, exit); the raw detail
// is never served because it can name hidden tests.
type SuiteAdmissionCheck struct {
	Name    string `json:"name"`    // e.g. reference_1, empty_patch, mutant_2, exploit
	Control string `json:"control"` // reference | empty | mutant | exploit | null_agent | stale_notes | secrets | network | other
	Want    string `json:"want"`    // pass | fail
	Got     string `json:"got"`     // pass | fail | invalid_evaluator | error
	Passed  bool   `json:"passed"`
	Summary string `json:"summary,omitempty"`
}

// SuiteTaskDetail is GET /suites/{id}/tasks/{task}: the list entry plus what
// the agent itself sees. Instruction is the instruction file as text (it is
// not secret: every agent receives it). Files lists the visible repo/ tree.
type SuiteTaskDetail struct {
	Suite                string          `json:"suite"`
	SuiteVersion         string          `json:"suite_version"`
	VerifierResult       string          `json:"verifier_result"`
	Task                 SuiteTask       `json:"task"`
	Instruction          string          `json:"instruction"`
	InstructionTruncated bool            `json:"instruction_truncated"`
	Protected            []string        `json:"protected"`       // protected globs, relative to the workspace
	Files                []SuiteRepoFile `json:"files"`           // sorted by path, at most FilesLimit
	FilesTotal           int             `json:"files_total"`     // visible files in repo/, all of them
	FilesTruncated       bool            `json:"files_truncated"` // Files holds the first FilesLimit only
}

// SuiteRepoFile is one visible file of a task's repo/ folder.
type SuiteRepoFile struct {
	Path string `json:"path"` // slash-separated, relative to repo/
	Size int64  `json:"size"` // bytes
}

// AgentList is GET /agents: every agents/*.yaml config, sorted by file name.
// A config that fails to decode is listed with Error set.
type AgentList struct {
	Agents []AgentSummary `json:"agents"`
}

// AgentSummary is one agent config as the agents page shows it. Env lists
// variable names only: values can hold keys and are never served. Files
// lists the container paths the adapter writes, never their content.
type AgentSummary struct {
	ID               string        `json:"id"`
	Path             string        `json:"path"`    // relative to the bench root, e.g. agents/codex.yaml
	Adapter          string        `json:"adapter"` // command | scripted
	Image            string        `json:"image"`
	Dockerfile       string        `json:"dockerfile"`      // build.dockerfile; empty when the image is pulled
	BaseArg          string        `json:"base_arg"`        // build.base_arg; empty for one image across suites
	PerSuiteBuild    bool          `json:"per_suite_build"` // base_arg set: built once per suite on its base image
	Parser           string        `json:"parser"`          // empty: gateway-level evidence only
	EvidenceLevel    string        `json:"evidence_level"`  // native (a parser) | gateway
	Model            string        `json:"model"`           // the config's default model; experiments usually set it
	ScriptedKind     string        `json:"scripted_kind"`   // oracle | noop | exploit | refuse | toy; empty unless scripted
	NotesProjectFile string        `json:"notes_project_file"`
	NotesUserFile    string        `json:"notes_user_file"`
	Budgets          Budgets       `json:"budgets"`
	Gateway          GatewayPolicy `json:"gateway"`
	EnvNames         []string      `json:"env_names"`   // sorted; values are never served
	FilePaths        []string      `json:"file_paths"`  // container paths the adapter writes, sorted
	SealedRuns       *int          `json:"sealed_runs"` // sealed runs of this config id; null without a run index
	// RunsTotal counts every run of this config id in the run index, any
	// outcome (aborted and cancelled runs too); omitted without runs or an
	// index. SealedRuns counts only the scored ones.
	RunsTotal *int `json:"runs_total,omitempty"`
	// LastRunAt is when the latest run of this config id started; omitted
	// when it never ran or there is no index.
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	// Capabilities is what the agent can do: its own capabilities: list, else
	// the defaults of its adapter (EffectiveCapabilities). Empty when the
	// config failed to decode.
	Capabilities CapabilitySet `json:"capabilities,omitempty"`
	Error        string        `json:"error,omitempty"` // why the config failed to decode
	// ReportsEvents says how the agent reports its own steps when its config
	// shows it: "native" for the generic_jsonl parser. Empty means not
	// known from the config; agent check shows what a run actually reported
	// (docs/agent-events.md).
	ReportsEvents string `json:"reports_events,omitempty"`
	// OwnsModel: the agent brings its own model (an HTTP endpoint, an A2A
	// agent, an OpenAI-compatible endpoint that names its model), so an
	// evaluation may leave its model blank (utils.OwnsModel).
	OwnsModel bool `json:"owns_model,omitempty"`
}
