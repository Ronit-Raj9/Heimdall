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

	// Scenario fields (scenario.go).
	Track    string        `json:"track,omitempty"`    // coding when the suite sets none
	Grading  string        `json:"grading,omitempty"`  // verified | judged
	Output   string        `json:"output,omitempty"`   // diff | answer | state
	Services []string      `json:"services,omitempty"` // ids of the suite's services
	Imported *ImportRecord `json:"imported,omitempty"` // where an imported suite came from
}

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
	EnvNames         []string      `json:"env_names"`       // sorted; values are never served
	FilePaths        []string      `json:"file_paths"`      // container paths the adapter writes, sorted
	SealedRuns       *int          `json:"sealed_runs"`     // sealed runs of this config id; null without a run index
	Error            string        `json:"error,omitempty"` // why the config failed to decode
}
