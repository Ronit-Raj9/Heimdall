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
}

// TaskControls are the admission controls, relative to the task folder.
type TaskControls struct {
	Reference string   `json:"reference" yaml:"reference"`
	Empty     string   `json:"empty" yaml:"empty"`
	Mutants   []string `json:"mutants" yaml:"mutants"`
	Exploit   string   `json:"exploit" yaml:"exploit"`
}

// TaskTimeouts are per-task limits. The agent wall time actually used is the
// stricter of this and the agent config budget, identical across arms.
type TaskTimeouts struct {
	AgentS int `json:"agent_s" yaml:"agent_s"`
}
