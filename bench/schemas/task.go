package schemas

// TaskKind classifies a task for reporting.
type TaskKind string

// Task kinds.
const (
	TaskBugfix      TaskKind = "bugfix"
	TaskFeature     TaskKind = "feature"
	TaskRefactor    TaskKind = "refactor"
	TaskPerformance TaskKind = "performance"
)

// Task is one job with a hidden verifier and admission controls, read from
// benchmarks/<suite>/tasks/<id>/task.yaml. The format is documented in
// benchmarks/coding-core/README.md.
type Task struct {
	ID          string       `json:"id" yaml:"id"`
	Version     string       `json:"version" yaml:"version"`
	Title       string       `json:"title" yaml:"title"`
	Project     string       `json:"project" yaml:"project"`
	Kind        TaskKind     `json:"kind" yaml:"kind"`
	Instruction string       `json:"instruction" yaml:"instruction"` // file name, relative to Dir
	RepoDir     string       `json:"repo_dir" yaml:"repo_dir"`       // folder copied into the workspace
	Notes       TaskNotes    `json:"notes" yaml:"notes"`
	Verifier    TaskVerifier `json:"verifier" yaml:"verifier"`
	Protected   []string     `json:"protected" yaml:"protected"` // globs of existing files whose hashes must not change (G1)
	Controls    TaskControls `json:"controls" yaml:"controls"`
	Timeouts    TaskTimeouts `json:"timeouts" yaml:"timeouts"`
	Tags        []string     `json:"tags,omitempty" yaml:"tags"`
	// Capabilities names what the task exercises (planning, tool_use,
	// test_discipline, error_recovery, safety, honesty, efficiency, ...), so
	// results can be read per capability. Difficulty is easy, medium or hard.
	Capabilities []string        `json:"capabilities,omitempty" yaml:"capabilities"`
	Difficulty   string          `json:"difficulty,omitempty" yaml:"difficulty"`
	Provenance   *TaskProvenance `json:"provenance,omitempty" yaml:"provenance"`

	// Scenario fields (scenario.go, docs/scenarios-design.md). All optional;
	// a task without them is a repository task graded on its diff.
	Environment *TaskEnvironment `json:"environment,omitempty" yaml:"environment"` // the task's own image
	Services    []ServiceSpec    `json:"services,omitempty" yaml:"services"`       // started beside the agent, after the suite's
	Sessions    []SessionSpec    `json:"sessions,omitempty" yaml:"sessions"`       // several agent sessions; empty means one, from Instruction
	Output      *OutputSpec      `json:"output,omitempty" yaml:"output"`           // overrides the suite's output
	Skills      string           `json:"skills,omitempty" yaml:"skills"`           // folder of agent skills, relative to Dir
	// Impossible marks a task that has no honest solution (its tests
	// contradict its instruction). A pass can only come from cheating: the
	// impossible_pass gate turns it into an integrity violation.
	Impossible bool `json:"impossible,omitempty" yaml:"impossible"`

	Dir string `json:"-" yaml:"-"` // absolute task folder
}

// TaskNotes names the notes fixtures. The clean file is installed as the
// agent's project notes file in every condition except stale-notes.
type TaskNotes struct {
	Clean       string   `json:"clean" yaml:"clean"`
	Stale       string   `json:"stale" yaml:"stale"`
	StaleClaims []string `json:"stale_claims,omitempty" yaml:"stale_claims"`
}

// TaskVerifier names the hidden tests. The command and restore list come from
// the suite.
type TaskVerifier struct {
	Tests    string   `json:"tests" yaml:"tests"`       // folder mounted at /grader/tests
	Required []string `json:"required" yaml:"required"` // test ids that must run and pass
	// Answer is the hidden expected-answer file (an AnswerSpec in YAML),
	// relative to the task folder, for tasks with output kind answer.
	Answer string `json:"answer,omitempty" yaml:"answer"`
	// OriginalTests is the folder of the unmutated tests of an impossible
	// task, against which its honest reference (TaskControls.Honest) must
	// pass at admission.
	OriginalTests string `json:"original_tests,omitempty" yaml:"original_tests"`
}

// TaskControls are the admission controls, relative to the task folder. What a
// control file is follows the task's output kind: a patch (.diff) for diff
// tasks; a script (.sh) run in the agent's place, with the services up, for
// state tasks and snapshot-graded tasks; a text file holding the answer for
// answer tasks.
type TaskControls struct {
	Reference string   `json:"reference" yaml:"reference"`
	Empty     string   `json:"empty" yaml:"empty"`
	Mutants   []string `json:"mutants" yaml:"mutants"`
	Exploit   string   `json:"exploit" yaml:"exploit"`
	// Honest is an impossible task's honest solution (it may be the
	// reference itself): at admission it must pass the original tests
	// (TaskVerifier.OriginalTests) three times and fail the task's tests.
	Honest string `json:"honest,omitempty" yaml:"honest"`
}

// TaskTimeouts are per-task limits. The agent wall time actually used is the
// stricter of this and the agent config budget, identical across arms.
type TaskTimeouts struct {
	AgentS int `json:"agent_s" yaml:"agent_s"`
}

// TaskProvenance records where a task came from, so an imported or
// company-authored task can be traced and its license checked.
type TaskProvenance struct {
	Source          string `json:"source,omitempty" yaml:"source"`                     // repository URL or path the task was cut from
	BaseCommit      string `json:"base_commit,omitempty" yaml:"base_commit"`           // commit the workspace snapshot was taken at
	ReferenceCommit string `json:"reference_commit,omitempty" yaml:"reference_commit"` // commit holding the reference fix
	License         string `json:"license,omitempty" yaml:"license"`                   // SPDX id, or "proprietary"
	ImportedFrom    string `json:"imported_from,omitempty" yaml:"imported_from"`       // benchmark and version, for imported tasks
	CreatedBy       string `json:"created_by,omitempty" yaml:"created_by"`             // tool that scaffolded it, e.g. "task init"
}
