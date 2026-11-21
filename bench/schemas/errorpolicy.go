package schemas

import (
	"errors"
	"fmt"
)

// Error policy (run control, RESEARCH_R1 item 7): what the bench does with
// each failure class of a run, as one declarative table instead of rules
// spread over the scheduler and the pipeline. DefaultErrorPolicy is exactly
// the engine's behaviour; an experiment may override the rows marked
// Editable through error_policy: in its spec, and the override is frozen in
// the manifest with everything else.

// Failure classes of the error policy table.
const (
	FailureAbortedInfra     = "aborted_infra"     // stopped before the agent started, or no model call answered (provider unreachable)
	FailureOwnerStopped     = "stopped"           // an operator cancelled the run or the experiment, or the server stopped
	FailureLeaseLost        = "lease_lost"        // the worker lost the slot (crash, network); sealed aborted_infra
	FailureInfraError       = "infra_error"       // the bench or gateway failed after the agent started
	FailureInvalidEvaluator = "invalid_evaluator" // the grader could not decide
	FailureAgent            = "agent_failure"     // fail, timeout, budget, crash, refusal: the agent's own result
	FailureIntegrity        = "integrity_violation"
)

// Grade values of an error policy row.
const (
	GradeNone    = "not_graded" // no score: the run is not evidence about the agent
	GradeUnknown = "unknown"    // counted with no score; the verdict reads it as missing evidence
	GradeZero    = "zero"       // counted as a failure (score 0)
	GradeVeto    = "zero_veto"  // counted as 0 and a veto gate fired
)

// Retry values of an error policy row.
const (
	RetryAuto     = "auto"      // the scheduler runs the slot again at once
	RetryOnResume = "on_resume" // the slot runs again when the experiment resumes; no attempt is used
	RetryManual   = "manual"    // only an operator's requeue runs it again
	RetryNever    = "never"
)

// Limits of the editable rows.
const (
	MaxAbortedInfraAttempts = 5
	MaxInfraErrorAttempts   = 3
	defaultAbortedAttempts  = 3
)

// ErrorPolicyRow is one failure class: whether and how the slot runs again,
// how the run is graded and whether it counts in the verdict.
type ErrorPolicyRow struct {
	Class string `json:"class"`
	// Retry is auto, on_resume, manual or never.
	Retry string `json:"retry"`
	// MaxAttempts bounds the slot's runs of this class, the first included;
	// 0 when Retry is not auto.
	MaxAttempts int `json:"max_attempts"`
	// UsesAttempt reports whether such a run uses one of the slot's attempts.
	UsesAttempt bool   `json:"uses_attempt"`
	Grade       string `json:"grade"`
	// Counts reports whether the run enters the verdict.
	Counts   bool   `json:"counts"`
	Editable bool   `json:"editable"`
	Note     string `json:"note"`
}

// ErrorRule is one editable row's override in an experiment spec.
type ErrorRule struct {
	Retry       *bool `json:"retry,omitempty" yaml:"retry,omitempty"`
	MaxAttempts int   `json:"max_attempts,omitempty" yaml:"max_attempts,omitempty"`
}

// ErrorPolicySpec is error_policy: in an experiment spec. Only the rows a
// run may safely retry are editable: a graded pass or fail is never retried,
// so a policy can not buy a better score.
type ErrorPolicySpec struct {
	AbortedInfra *ErrorRule `json:"aborted_infra,omitempty" yaml:"aborted_infra,omitempty"`
	InfraError   *ErrorRule `json:"infra_error,omitempty" yaml:"infra_error,omitempty"`
}

// Validate checks the overrides against the limits.
func (p *ErrorPolicySpec) Validate() error {
	if p == nil {
		return nil
	}
	var errs []error
	if r := p.AbortedInfra; r != nil {
		if r.Retry != nil && !*r.Retry {
			errs = append(errs, errors.New("error_policy.aborted_infra.retry can not be false: such a run never reached the agent, so it is always run again"))
		}
		if r.MaxAttempts < 0 || r.MaxAttempts > MaxAbortedInfraAttempts {
			errs = append(errs, fmt.Errorf("error_policy.aborted_infra.max_attempts must be 1 to %d, got %d", MaxAbortedInfraAttempts, r.MaxAttempts))
		}
	}
	if r := p.InfraError; r != nil {
		if r.MaxAttempts < 0 || r.MaxAttempts > MaxInfraErrorAttempts {
			errs = append(errs, fmt.Errorf("error_policy.infra_error.max_attempts must be 1 to %d, got %d", MaxInfraErrorAttempts, r.MaxAttempts))
		}
		if r.MaxAttempts > 1 && (r.Retry == nil || !*r.Retry) {
			errs = append(errs, errors.New("error_policy.infra_error.max_attempts needs retry: true"))
		}
	}
	return errors.Join(errs...)
}

// AbortedInfraAttempts is the slot's attempt budget for aborted_infra runs.
func (p *ErrorPolicySpec) AbortedInfraAttempts() int {
	if p != nil && p.AbortedInfra != nil && p.AbortedInfra.MaxAttempts > 0 {
		return p.AbortedInfra.MaxAttempts
	}
	return defaultAbortedAttempts
}

// InfraErrorAttempts is how many infra_error runs a slot may have before the
// last one counts: 1 (no retry) unless the policy turns retry on, then
// max_attempts, default 2.
func (p *ErrorPolicySpec) InfraErrorAttempts() int {
	if p == nil || p.InfraError == nil || p.InfraError.Retry == nil || !*p.InfraError.Retry {
		return 1
	}
	if p.InfraError.MaxAttempts > 0 {
		return p.InfraError.MaxAttempts
	}
	return 2
}

// ErrorPolicyTable is the effective table for p (nil: the defaults).
func ErrorPolicyTable(p *ErrorPolicySpec) []ErrorPolicyRow {
	infraRetry, infraMax := RetryManual, 0
	infraNote := "Counted with no score. An operator may requeue it: the old run is kept, marked superseded, and uses no attempt."
	if n := p.InfraErrorAttempts(); n > 1 {
		infraRetry, infraMax = RetryAuto, n
		infraNote = fmt.Sprintf("Run again automatically up to %d runs in all; earlier runs are kept, marked superseded. The last one counts with no score.", n)
	}
	return []ErrorPolicyRow{
		{Class: FailureAbortedInfra, Retry: RetryAuto, MaxAttempts: p.AbortedInfraAttempts(), UsesAttempt: true, Grade: GradeNone, Counts: false, Editable: true,
			Note: "The agent never ran, so the run says nothing about it. When every attempt aborts, the slot counts as missing."},
		{Class: FailureOwnerStopped, Retry: RetryOnResume, UsesAttempt: false, Grade: GradeNone, Counts: false,
			Note: "Cancel, pause with cancel, or a server restart. The slot runs again on resume or requeue."},
		{Class: FailureLeaseLost, Retry: RetryAuto, MaxAttempts: p.AbortedInfraAttempts(), UsesAttempt: true, Grade: GradeNone, Counts: false,
			Note: "The worker lost the run (crash, network). Recorded as aborted_infra and shares its attempts."},
		{Class: FailureInfraError, Retry: infraRetry, MaxAttempts: infraMax, UsesAttempt: false, Grade: GradeUnknown, Counts: true, Editable: true, Note: infraNote},
		{Class: FailureInvalidEvaluator, Retry: RetryNever, Grade: GradeUnknown, Counts: true,
			Note: "Counted with no score and sent to review; regrade instead of rerunning the agent."},
		{Class: FailureAgent, Retry: RetryNever, Grade: GradeZero, Counts: true,
			Note: "fail, timeout, budget, crash and refusal are the agent's result and are never retried."},
		{Class: FailureIntegrity, Retry: RetryNever, Grade: GradeVeto, Counts: true,
			Note: "Counted as 0 and blocks a ship decision."},
	}
}
