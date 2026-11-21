package schemas

// Trajectory (process) scoring: an optional task block that names the
// reference tool path an agent is expected to take. The process metrics
// (graders/process.go) compare a run's tool calls with it. They are report
// only: they never enter verified_success, a gate or a verdict.

// Process match modes, as agentevals names them
// (https://github.com/langchain-ai/agentevals): strict is the same calls in
// the same order; unordered the same calls in any order; subset means the
// agent made no call outside the reference; superset means the agent made
// every reference call (extra calls allowed).
const (
	ProcessMatchStrict    = "strict"
	ProcessMatchUnordered = "unordered"
	ProcessMatchSubset    = "subset"
	ProcessMatchSuperset  = "superset"
)

// Argument match modes of one expected tool call: ignore compares names
// only; exact needs equal arguments (as JSON, key order ignored); subset
// needs every expected argument present with an equal value.
const (
	ProcessArgsIgnore = "ignore"
	ProcessArgsExact  = "exact"
	ProcessArgsSubset = "subset"
)

// ProcessSpec is a task's process block.
type ProcessSpec struct {
	// ExpectedTools is the reference tool path, in order.
	ExpectedTools []ExpectedTool `json:"expected_tools,omitempty" yaml:"expected_tools"`
	// Reference is a JSON-lines file of a reference run's tool calls,
	// relative to the task folder: a tools.jsonl copied from a sealed run
	// (rows with tool and arguments) or rows of {name, args}. It is used
	// when ExpectedTools is empty.
	Reference string `json:"reference,omitempty" yaml:"reference"`
	// Match is strict (default), unordered, subset or superset.
	Match string `json:"match,omitempty" yaml:"match"`
	// ArgsMatch is the default argument match of expected calls: subset
	// when the call names arguments, ignore otherwise.
	ArgsMatch string `json:"args_match,omitempty" yaml:"args_match"`
	// ReferenceSteps is the number of tool calls the reference run made;
	// 0 uses the length of the reference path.
	ReferenceSteps int `json:"reference_steps,omitempty" yaml:"reference_steps"`
	// MaxStepsRatio flags a run whose tool calls exceed this multiple of the
	// reference (0: no flag).
	MaxStepsRatio float64 `json:"max_steps_ratio,omitempty" yaml:"max_steps_ratio"`
}

// ExpectedTool is one call of the reference path.
type ExpectedTool struct {
	Name      string         `json:"name" yaml:"name"`
	Args      map[string]any `json:"args,omitempty" yaml:"args"`
	ArgsMatch string         `json:"args_match,omitempty" yaml:"args_match"`
}
