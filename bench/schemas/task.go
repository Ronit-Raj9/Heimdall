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
	// W3 customer support: whether handing the customer to a human is the
	// right outcome (expected | not_expected); empty leaves the
	// escalation_correct metric not applicable.
	Escalation string `json:"escalation,omitempty" yaml:"escalation"`

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
	// AllowedIrreversible names the classes of irreversible action the task
	// asks for (data_destroy, history_rewrite, infra_destroy, cloud_destroy,
	// service_delete), so irreversible_without_confirmation does not fire
	// on them.
	AllowedIrreversible []string `json:"allowed_irreversible,omitempty" yaml:"allowed_irreversible"`
	// Approver starts the approval channel beside the agent (approver.go):
	// an MCP service with request_approval and ask_user, answered by this
	// policy or by the user simulator. An agent config can also ask for it.
	Approver *ApproverSpec `json:"approver,omitempty" yaml:"approver"`
	// External lists real HTTP APIs the agent may call through the tool
	// proxy, after the suite's (scenario.go ExternalAPI).
	External []ExternalAPI `json:"external,omitempty" yaml:"external"`
	// Split is public (default when empty) or held_out (suitehealth.go).
	// `tasks: all` selects public tasks only; a held-out task runs only when
	// a condition sets split: held_out or names it, and the API never serves
	// its instruction or files unless the server config allows it.
	Split string `json:"split,omitempty" yaml:"split"`

	Voice *VoiceSpec `json:"voice,omitempty" yaml:"voice"` // X8 voice agents: the task is a phone call with a voice caller (voice.go)
	// Team runs the task with a multi-container team (agents.go) when the
	// agent config declares none: one container per member, each on its own
	// gateway key. Empty for every other task.
	Team []TeamMember `json:"team,omitempty" yaml:"team"`

	// Resources is what the task needs beyond the agent's budgets: whole
	// GPUs (X7, schemas/gpu.go). Nil for every task without a GPU.
	Resources *Resources `json:"resources,omitempty" yaml:"resources"`

	// X2: Compose is a docker compose file relative to the task folder
	// (docs/multi-container.md). The suite loader maps it onto Environment
	// (the workspace service) and Services (the rest) before anything else
	// reads the task.
	Compose string `json:"compose,omitempty" yaml:"compose"`

	// Process is the reference tool path the report-only process metrics
	// compare a run with (process.go, graders/process.go). Nil for a task
	// without one.
	Process *ProcessSpec `json:"process,omitempty" yaml:"process"`

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
	// First workflow: false when a tool drafted the instruction and no human
	// has reviewed it yet (`tasks mine`); `task review` sets it true. Absent
	// on every hand-written or older task.
	InstructionReviewed *bool `json:"instruction_reviewed,omitempty" yaml:"instruction_reviewed,omitempty"`
	// Failure inbox: the production incident the task was drafted from
	// (`inbox accept`, docs/failure-inbox.md). Absent on every other task.
	Incident *TaskIncident `json:"incident,omitempty" yaml:"incident,omitempty"`
}

// TaskIncident links a task to the failure inbox item it came from, so a
// report can name the incident when the task fails (Failure inbox area).
type TaskIncident struct {
	ID        string `json:"id" yaml:"id"`                                     // inbox item id, inc-<hex>
	Source    string `json:"source,omitempty" yaml:"source,omitempty"`         // inbox source name
	TraceID   string `json:"trace_id,omitempty" yaml:"trace_id,omitempty"`     // first occurrence's trace id, redacted
	Link      string `json:"link,omitempty" yaml:"link,omitempty"`             // back to the trace in the source tool
	FirstSeen string `json:"first_seen,omitempty" yaml:"first_seen,omitempty"` // RFC 3339
	Signature string `json:"signature,omitempty" yaml:"signature,omitempty"`   // failure signature, f_<hex>
}

// ApproverSpec is the answer policy of the approval channel. Rules are read
// in order; the first whose action and target patterns both match decides.
// A request no rule matches gets Default (approve when empty). ask_user
// answers come from Answers (the first whose words all appear in the
// question), else from the user simulator in llm mode, else "I don't
// know".
type ApproverSpec struct {
	Default string           `json:"default,omitempty" yaml:"default"` // approve (default) | deny
	Rules   []ApproverRule   `json:"rules,omitempty" yaml:"rules"`
	Answers []ApproverAnswer `json:"answers,omitempty" yaml:"answers"`
}

// ApproverRule decides the requests whose action and target match. Each
// pattern is a case-insensitive regular expression searched in the text;
// empty matches anything.
type ApproverRule struct {
	Action   string `json:"action,omitempty" yaml:"action"`
	Target   string `json:"target,omitempty" yaml:"target"`
	Decision string `json:"decision" yaml:"decision"` // approve | deny
}

// ApproverAnswer is a scripted reply to ask_user.
type ApproverAnswer struct {
	Words  []string `json:"words" yaml:"words"`
	Answer string   `json:"answer" yaml:"answer"`
}
