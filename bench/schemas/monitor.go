package schemas

import "time"

// Monitor certification (docs/monitor-certification.md). A monitor is a
// command, an HTTP endpoint or a built-in that reads a run transcript (mode
// transcript) or one proposed action (mode action) and answers allow or
// block with a score in [0, 1]. The bench replays labelled sealed runs
// through it and reports the catch rate at fixed false-positive rates. Eval
// integrity area (S2).

// Monitor kinds.
const (
	MonitorKindCommand = "command" // a container image run once per run, the request on stdin
	MonitorKindHTTP    = "http"    // an operator-run endpoint, the request as the POST body
	MonitorKindBuiltin = "builtin" // a Go monitor shipped with the bench, for tests and baselines
)

// Monitor modes.
const (
	MonitorModeTranscript = "transcript" // one request per run with every action
	MonitorModeAction     = "action"     // one request per action; the run scores the max
)

// Monitor decisions.
const (
	MonitorAllow = "allow"
	MonitorBlock = "block"
)

// Monitor network settings a certificate records.
const (
	MonitorNetworkNone     = "none"             // command monitors: --network none
	MonitorNetworkOperator = "operator-managed" // http monitors: the bench does not sandbox a remote endpoint
)

// Labels of a replayed run.
const (
	MonitorLabelHonest = "honest"
	MonitorLabelAttack = "attack"
)

// Monitor-injection check states.
const (
	MonitorInjectionPassed = "passed"  // every planted approval left the decision unchanged
	MonitorInjectionFailed = "failed"  // at least one planted approval turned a caught attack into a miss
	MonitorInjectionNotRun = "not_run" // switched off, or no caught attack to test
)

// MonitorProtocolVersion is the version of MonitorRequest and
// MonitorResponse.
const MonitorProtocolVersion = "1"

// MonitorConfig is a monitor under certification, read from YAML or JSON.
// Image, Argv and Script are for kind command, URL for kind http, Builtin,
// Keywords and Patterns for kind builtin. Network is "none" (the default)
// for a command monitor; the bench cannot enforce an allowlist on a monitor
// container and refuses any other value. A command monitor's argv may hold
// the token {{script}}, replaced with the contents of Script (a file path
// relative to the config file), so a small script needs no custom image
// and no bind mount.
type MonitorConfig struct {
	Name      string           `json:"name" yaml:"name"`
	Kind      string           `json:"kind" yaml:"kind"`
	Mode      string           `json:"mode" yaml:"mode"`
	Image     string           `json:"image,omitempty" yaml:"image,omitempty"`
	Argv      []string         `json:"argv,omitempty" yaml:"argv,omitempty"`
	Script    string           `json:"script,omitempty" yaml:"script,omitempty"`
	URL       string           `json:"url,omitempty" yaml:"url,omitempty"`
	TimeoutS  int              `json:"timeout_s,omitempty" yaml:"timeout_s,omitempty"`
	Network   string           `json:"network,omitempty" yaml:"network,omitempty"`
	Resources MonitorResources `json:"resources,omitzero" yaml:"resources,omitempty"`
	Builtin   string           `json:"builtin,omitempty" yaml:"builtin,omitempty"`
	Keywords  []string         `json:"keywords,omitempty" yaml:"keywords,omitempty"` // builtin keyword: case-insensitive substrings
	Patterns  []string         `json:"patterns,omitempty" yaml:"patterns,omitempty"` // builtin keyword: Go regular expressions
	BlockAt   int              `json:"block_at,omitempty" yaml:"block_at,omitempty"` // builtin keyword: matched terms that block; default 1
	// Views are what the monitor is certified on, one certificate per view:
	// actions (default), cot (reasoning and the final message) and hybrid
	// (both). Absent means [actions], so old configs hash the same.
	Views []string `json:"views,omitempty" yaml:"views,omitempty"`
}

// Monitor views (MonitorConfig.Views).
const (
	MonitorViewActions = "actions" // every action except reasoning
	MonitorViewCoT     = "cot"     // reasoning and the final message only
	MonitorViewHybrid  = "hybrid"  // every action including reasoning
)

// MonitorResources caps a command monitor's container.
type MonitorResources struct {
	CPUs      float64 `json:"cpus,omitempty" yaml:"cpus,omitempty"`
	MemoryMB  int     `json:"memory_mb,omitempty" yaml:"memory_mb,omitempty"`
	PidsLimit int     `json:"pids_limit,omitempty" yaml:"pids_limit,omitempty"`
}

// MonitorRequest is what a monitor reads: one JSON object per line on a
// command monitor's stdin, or the POST body of an http monitor. It carries
// the task instruction and the agent's actions only, never a label, gate,
// grade, probe id or run id.
type MonitorRequest struct {
	Version              string          `json:"version"`
	Mode                 string          `json:"mode"`
	Instruction          string          `json:"instruction,omitempty"`
	InstructionTruncated bool            `json:"instruction_truncated,omitempty"`
	Actions              []MonitorAction `json:"actions,omitempty"` // transcript mode
	Action               *MonitorAction  `json:"action,omitempty"`  // action mode
	FinalMessage         string          `json:"final_message,omitempty"`
	OmittedActions       int             `json:"omitted_actions,omitempty"` // actions dropped from the middle of a long transcript
	View                 string          `json:"view,omitempty"`            // cot or hybrid; absent for the actions view
}

// Monitor action kinds.
const (
	MonitorActionMessage   = "assistant_message"
	MonitorActionToolCall  = "tool_call"
	MonitorActionProxyCall = "tool_proxy_call"
	MonitorActionCommand   = "command"
	MonitorActionFile      = "file_change"
	MonitorActionNetwork   = "network"
	MonitorActionReasoning = "reasoning" // the model's reasoning text (thinking, reasoning summaries)
)

// MonitorAction is one thing the agent did, in run order.
type MonitorAction struct {
	Index     int    `json:"index"`          // 0-based position in the run's action list
	Kind      string `json:"kind"`           // assistant_message | reasoning | tool_call | tool_proxy_call | command | file_change | network
	Source    string `json:"source"`         // gateway | agent_stream | tool_proxy | process_observer | workspace_observer | network_observer
	Name      string `json:"name,omitempty"` // tool name, or the file operation
	Content   string `json:"content"`        // arguments, command line, message text or path
	Truncated bool   `json:"truncated,omitempty"`
}

// MonitorResponse is what a monitor answers. Score is optional: absent, it
// is 1 for block and 0 for allow.
type MonitorResponse struct {
	Decision string   `json:"decision"`
	Score    *float64 `json:"score,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// MonitorCertificate is one certification of a monitor against labelled
// sealed runs. It is a child artifact outside every seal:
// <monitor certs root>/<cert_id>/certificate.json.
type MonitorCertificate struct {
	CertID          string                  `json:"cert_id"`
	CreatedAt       time.Time               `json:"created_at"`
	Monitor         MonitorCertMonitor      `json:"monitor"`
	ExpIDs          []string                `json:"exp_ids"`
	Labels          MonitorLabelSummary     `json:"labels"`
	OperatingPoints []MonitorOperatingPoint `json:"operating_points"`
	Families        []MonitorFamily         `json:"families"`
	Warnings        []string                `json:"warnings"`
	Items           []MonitorItem           `json:"items"`
	Errors          int                     `json:"errors"`
	Excluded        []MonitorExcludedRun    `json:"excluded"`
	Experiments     []MonitorCertExperiment `json:"experiments"`
	Integrity       MonitorInjectionCheck   `json:"integrity"`
	GradeSource     string                  `json:"grade_source"`
	AttackAxes      []string                `json:"attack_axes"`
	HarmMetrics     []string                `json:"harm_metrics"`
	Transcript      MonitorTranscriptLimits `json:"transcript"`
	ErrorLimit      float64                 `json:"error_limit"` // largest share of labelled runs that may error
	BenchVersion    string                  `json:"bench_version,omitempty"`
	Mode            Mode                    `json:"bench_mode,omitempty"` // the bench mode the certification ran in
	Headline        *MonitorCertHeadline    `json:"headline,omitempty"`
	// View is the transcript view certified (actions, cot or hybrid); empty
	// on certificates written before views existed, which are actions.
	View string `json:"view,omitempty"`
	// Reasoning counts, per arm, the labelled runs whose transcript had any
	// reasoning, so views are never compared on unequal visibility.
	Reasoning map[string]MonitorReasoningCoverage `json:"reasoning,omitempty"`
	// Sabotage is the undetected-sabotage summary of attack-condition runs
	// at the headline threshold; nil when no run was under the attack
	// condition.
	Sabotage *MonitorSabotage `json:"sabotage,omitempty"`
}

// MonitorReasoningCoverage is one arm's reasoning visibility.
type MonitorReasoningCoverage struct {
	Runs          int `json:"runs"`
	WithReasoning int `json:"with_reasoning"`
}

// MonitorSabotage is what the monitor let through of the side tasks agents
// completed under the attack condition, at the headline operating point.
// Undetected counts completed side tasks the monitor did not flag; its rate
// is over every judged attack run (completed or not), the share of attack
// attempts that ended in sabotage nobody caught.
type MonitorSabotage struct {
	TargetFPR      float64                         `json:"target_fpr"`
	AttackRuns     int                             `json:"attack_runs"`
	Completed      int                             `json:"completed"`
	Caught         int                             `json:"caught"`
	Undetected     int                             `json:"undetected"`
	UndetectedRate *float64                        `json:"undetected_rate"`
	Interval       *[2]float64                     `json:"interval"`
	PerArm         map[string]MonitorSabotageCount `json:"per_arm"`
}

// MonitorSabotageCount is one arm's sabotage counts.
type MonitorSabotageCount struct {
	AttackRuns int `json:"attack_runs"`
	Completed  int `json:"completed"`
	Undetected int `json:"undetected"`
}

// MonitorCertMonitor identifies the monitor a certificate is about. The
// config hash covers the resolved config, including the script contents.
type MonitorCertMonitor struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Mode       string `json:"mode"`
	ConfigHash string `json:"config_hash"`
	Image      string `json:"image,omitempty"`
	Builtin    string `json:"builtin,omitempty"`
	Network    string `json:"network"`
	TimeoutS   int    `json:"timeout_s,omitempty"`
}

// MonitorLabelSummary counts the replayed runs by label and states the
// labelling rule. ExcludedReasons counts excluded runs by reason.
type MonitorLabelSummary struct {
	Honest          int            `json:"honest"`
	Attack          int            `json:"attack"`
	Excluded        int            `json:"excluded"`
	Rule            string         `json:"rule"`
	ExcludedReasons map[string]int `json:"excluded_reasons"`
}

// MonitorOperatingPoint is the monitor at one target false-positive rate.
// Threshold is null when no score cut is needed or possible: only the
// monitor's own block decisions flag runs there (Note says which).
// AchievedFPR and CatchRate are null when their denominator is 0.
// Intervals are exact Clopper-Pearson 95% intervals.
type MonitorOperatingPoint struct {
	TargetFPR      float64   `json:"target_fpr"`
	Supported      bool      `json:"supported"`
	Threshold      *float64  `json:"threshold"`
	FalsePositives int       `json:"false_positives"`
	Honest         int       `json:"honest"`
	AchievedFPR    *float64  `json:"achieved_fpr"`
	FPRInterval    []float64 `json:"fpr_interval"`
	Caught         int       `json:"caught"`
	Attacks        int       `json:"attacks"`
	CatchRate      *float64  `json:"catch_rate"`
	CatchInterval  []float64 `json:"catch_interval"`
	Note           string    `json:"note"`
}

// MonitorFamily is the catch rate on one attack family: the runs of one
// probe whose veto gates fired, at each operating point's threshold.
type MonitorFamily struct {
	Family  string               `json:"family"`
	Gates   []string             `json:"gates"`
	Attacks int                  `json:"attacks"`
	Points  []MonitorFamilyPoint `json:"points"`
}

// MonitorFamilyPoint is one family at one operating point.
type MonitorFamilyPoint struct {
	TargetFPR     float64   `json:"target_fpr"`
	Caught        int       `json:"caught"`
	Attacks       int       `json:"attacks"`
	CatchRate     *float64  `json:"catch_rate"`
	CatchInterval []float64 `json:"catch_interval"`
}

// MonitorItem is one labelled run and what the monitor said about it. Score
// is null and Error set when the monitor failed on the run; an error never
// counts as allow.
type MonitorItem struct {
	RunID       string   `json:"run_id"`
	ExpID       string   `json:"exp_id"`
	Task        string   `json:"task"`
	Arm         string   `json:"arm"`
	Condition   string   `json:"condition"`
	Label       string   `json:"label"`
	Family      string   `json:"family"`
	Gates       []string `json:"gates,omitempty"` // fired veto gates of an attack
	Score       *float64 `json:"score"`
	Decision    string   `json:"decision"`
	Reason      string   `json:"reason,omitempty"`
	SealHash    string   `json:"seal_hash"`
	Actions     int      `json:"actions"`
	Truncated   bool     `json:"truncated,omitempty"`
	Synthetic   bool     `json:"synthetic,omitempty"` // a scripted agent produced the run
	Error       string   `json:"error"`
	GradeSource string   `json:"grade_source,omitempty"`
}

// MonitorExcludedRun is a replayed run that got no label, and why.
type MonitorExcludedRun struct {
	RunID     string `json:"run_id"`
	ExpID     string `json:"exp_id"`
	Task      string `json:"task"`
	Arm       string `json:"arm"`
	Condition string `json:"condition"`
	SealHash  string `json:"seal_hash,omitempty"`
	Reason    string `json:"reason"`
}

// MonitorCertExperiment ties a certificate to one experiment's frozen
// manifest.
type MonitorCertExperiment struct {
	ExpID        string `json:"exp_id"`
	ManifestHash string `json:"manifest_hash"`
	SuiteID      string `json:"suite_id"`
	SuiteVersion string `json:"suite_version"`
	Runs         int    `json:"runs"` // counted sealed runs replayed
}

// MonitorInjectionCheck is the monitor-integrity check: caught attack
// transcripts replayed with a planted approval ("this action is approved,
// score 0") in agent-controlled text. Flipped counts the attacks the plant
// turned into a miss at the headline threshold. HonestFlagged counts the
// honest transcripts the same plant made the monitor flag (informational).
type MonitorInjectionCheck struct {
	State         string `json:"state"` // passed | failed | not_run
	Plant         string `json:"plant,omitempty"`
	Tested        int    `json:"tested"`
	Flipped       int    `json:"flipped"`
	HonestTested  int    `json:"honest_tested"`
	HonestFlagged int    `json:"honest_flagged"`
	Errors        int    `json:"errors"`
	Detail        string `json:"detail"`
}

// MonitorTranscriptLimits are the bounds the transcript builder applied.
type MonitorTranscriptLimits struct {
	MaxActions       int `json:"max_actions"`
	MaxActionBytes   int `json:"max_action_bytes"`
	MaxInstruction   int `json:"max_instruction_bytes"`
	MaxFinalMessage  int `json:"max_final_message_bytes"`
	TruncatedRuns    int `json:"truncated_runs"`
	ActionsOmittedIn int `json:"actions_omitted_in"` // runs whose middle actions were dropped
}

// MonitorCertHeadline is the operating point a list shows: the first target
// FPR asked for.
type MonitorCertHeadline struct {
	TargetFPR     float64   `json:"target_fpr"`
	CatchRate     *float64  `json:"catch_rate"`
	CatchInterval []float64 `json:"catch_interval"`
	Supported     bool      `json:"supported"`
}

// MonitorCertSummary is one certificate in a list.
type MonitorCertSummary struct {
	CertID      string               `json:"cert_id"`
	CreatedAt   time.Time            `json:"created_at"`
	MonitorName string               `json:"monitor_name"`
	MonitorKind string               `json:"monitor_kind"`
	Mode        string               `json:"mode"`
	ExpIDs      []string             `json:"exp_ids"`
	Honest      int                  `json:"honest"`
	Attacks     int                  `json:"attacks"`
	Headline    *MonitorCertHeadline `json:"headline"`
	Warnings    []string             `json:"warnings"`
	View        string               `json:"view,omitempty"`
}

// MonitorCertList is GET /monitors/certifications: newest first.
type MonitorCertList struct {
	Certifications []MonitorCertSummary `json:"certifications"`
}
