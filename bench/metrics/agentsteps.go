package metrics

import (
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Names of the agent step metrics, read from the unified step timeline
// (Evidence.Steps), the trusted observers and the gateway log
// (docs/steps-and-evidence.md).
const (
	AgentSteps           = "agent_steps"
	CommandsExecuted     = "commands_executed"
	FilesChangedEvents   = "files_changed_events"
	ModelAttempts        = "model_attempts"
	ToolCallsUnwitnessed = "tool_calls_unwitnessed"
)

// Reasons an agent step metric has no number.
const (
	reasonNoTimeline     = "no step timeline (run recorded before it)"
	reasonOnlyModelTurns = "only model turns were observed: no process or workspace observer ran and there is no tool ledger, shim log or agent stream"
	reasonNoWitness      = "no execution witness available: observers off or missing, no tool ledger, shim log or agent stream"
)

var (
	_ schemas.Metric = (*agentStepsMetric)(nil)
	_ schemas.Metric = (*commandsExecutedMetric)(nil)
	_ schemas.Metric = (*filesChangedMetric)(nil)
	_ schemas.Metric = (*modelAttemptsMetric)(nil)
	_ schemas.Metric = (*toolCallsUnwitnessedMetric)(nil)
)

// agentStepsMetric counts every agent action a trusted or native witness
// saw, each once: the timeline rows marked counted (evidence.BuildTimeline
// holds the rule).
type agentStepsMetric struct{}

// GetName implements schemas.Part.
func (m *agentStepsMetric) GetName() string { return AgentSteps }

// GetVersion implements schemas.Part.
func (m *agentStepsMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *agentStepsMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: AgentSteps, Version: version, Label: "Agent steps (all witnessed actions)", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Agent actions per run, each counted once across witnesses (answered model turns, tool calls with an execution witness, command trees no tool call explains, and file changes per path and turn that no tool call or command explains) at the evidence level of the weakest witness used, averaged over runs with a complete gateway log and at least one witness beyond it.",
	}
}

// Compute implements schemas.Metric.
func (m *agentStepsMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	if ev.Steps == nil {
		return schemas.Unknown(reasonNoTimeline, schemas.EvidenceGateway), nil
	}
	n, native, beyond := 0, false, false
	for _, s := range ev.Steps {
		if (s.Kind == schemas.StepModelTurn && s.IsError) || s.Source == schemas.StepSourceReported { // Agent events area: reported rows are never a witness here
			continue
		}
		switch {
		case s.Source == schemas.StepSourceAgent:
			if s.Counted {
				native = true
			}
		case s.Kind == schemas.StepToolRequest && s.Execution == schemas.ExecutedAgentReported:
			native = true
		case s.Source != schemas.StepSourceGateway:
			beyond = true
		}
		if s.Counted {
			n++
		}
	}
	if !beyond && !native && !observed(ev.Observers) {
		return schemas.Unknown(reasonOnlyModelTurns, schemas.EvidenceGateway), nil
	}
	v := schemas.Observed(float64(n), schemas.EvidenceBoundary)
	v.Source, v.EvidenceLevel = schemas.EvidenceSourceInferred, schemas.EvidenceStrong
	if native {
		v.Evidence, v.Source, v.EvidenceLevel = schemas.EvidenceNative, schemas.EvidenceSourceAgentStream, schemas.EvidencePartial
	}
	return v, nil
}

func newAgentStepsMetric() *agentStepsMetric { return &agentStepsMetric{} }

// commandsExecutedMetric counts the command trees the process observer saw.
type commandsExecutedMetric struct{}

// GetName implements schemas.Part.
func (m *commandsExecutedMetric) GetName() string { return CommandsExecuted }

// GetVersion implements schemas.Part.
func (m *commandsExecutedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *commandsExecutedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: CommandsExecuted, Version: version, Label: "Commands executed", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
		Description: "Commands the agent started per run, seen by the process observer in the agent's PID namespace, each process whose parent is the agent's runtime counted once with all its descendants (processes shorter than the poll interval can be missed, bounded per run in observers.json), averaged over runs where the process observer ran for every session.",
	}
}

// Compute implements schemas.Metric.
func (m *commandsExecutedMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, ok := observerProblem(ev.Observers, schemas.ObserverProc); !ok {
		return v, nil
	}
	n := 0
	for _, s := range ev.Steps {
		if s.Source == schemas.StepSourceProcess && s.Op != "runtime" && (s.Counted || (s.WitnessOf > 0 && isRoot(ev.Steps, s))) {
			n++
		}
	}
	v := schemas.Observed(float64(n), schemas.EvidenceBoundary)
	v.Source, v.EvidenceLevel = schemas.EvidenceSourceProcessResult, schemas.EvidenceVerified
	return v, nil
}

// isRoot reports whether a process row linked to a tool call is the root of
// its command tree: its parent is not another non-runtime process row.
func isRoot(steps []schemas.StepRecord, s schemas.StepRecord) bool {
	for _, p := range steps {
		if p.Source == schemas.StepSourceProcess && p.Op != "runtime" && p.PID == s.PPID && p.Session == s.Session && p.TS.Before(s.TS) {
			return false
		}
	}
	return true
}

func newCommandsExecutedMetric() *commandsExecutedMetric { return &commandsExecutedMetric{} }

// filesChangedMetric counts the workspace observer's file events.
type filesChangedMetric struct{}

// GetName implements schemas.Part.
func (m *filesChangedMetric) GetName() string { return FilesChangedEvents }

// GetVersion implements schemas.Part.
func (m *filesChangedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *filesChangedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: FilesChangedEvents, Version: version, Label: "File change events", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
		Description: "File creates, modifications, deletions and renames in the workspace per run, seen by the workspace observer with git internals and caches skipped and events over the cap still counted, averaged over runs where the workspace observer ran for every session.",
	}
}

// Compute implements schemas.Metric.
func (m *filesChangedMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, ok := observerProblem(ev.Observers, schemas.ObserverFS); !ok {
		return v, nil
	}
	n := len(ev.FileEvents)
	for _, s := range ev.Observers {
		if s.Observer == schemas.ObserverFS {
			n += s.Dropped
		}
	}
	v := schemas.Observed(float64(n), schemas.EvidenceBoundary)
	v.Source, v.EvidenceLevel = schemas.EvidenceSourceWorkspaceDiff, schemas.EvidenceVerified
	return v, nil
}

func newFilesChangedMetric() *filesChangedMetric { return &filesChangedMetric{} }

// modelAttemptsMetric counts physical upstream attempts, so the retries and
// fallbacks that steps folds into one turn stay visible.
type modelAttemptsMetric struct{}

// GetName implements schemas.Part.
func (m *modelAttemptsMetric) GetName() string { return ModelAttempts }

// GetVersion implements schemas.Part.
func (m *modelAttemptsMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *modelAttemptsMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ModelAttempts, Version: version, Label: "Model attempts (incl. retries, fallbacks)", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Physical upstream model attempts per run: every attempt in Bifrost's retry trail plus every fallback row (a row without a trail is one attempt, a cache hit none), averaged over runs with a complete gateway log.",
	}
}

// Compute implements schemas.Metric.
func (m *modelAttemptsMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	n := 0
	for _, r := range messageRows(ev.Requests.Requests) {
		n += upstreamAttempts(r)
	}
	return schemas.Observed(float64(n), schemas.EvidenceGateway), nil
}

func newModelAttemptsMetric() *modelAttemptsMetric { return &modelAttemptsMetric{} }

// toolCallsUnwitnessedMetric counts requested tool calls nothing showed ran.
type toolCallsUnwitnessedMetric struct{}

// GetName implements schemas.Part.
func (m *toolCallsUnwitnessedMetric) GetName() string { return ToolCallsUnwitnessed }

// GetVersion implements schemas.Part.
func (m *toolCallsUnwitnessedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *toolCallsUnwitnessedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ToolCallsUnwitnessed, Version: version, Label: "Tool calls without execution witness", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Tool calls the model requested per run that no witness (tool proxy, shim, process or workspace observer, or the agent's own tool result) shows ran, averaged over runs with a complete gateway log and at least one execution witness available.",
	}
}

// Compute implements schemas.Metric.
func (m *toolCallsUnwitnessedMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	if ev.Steps == nil {
		return schemas.Unknown(reasonNoTimeline, schemas.EvidenceGateway), nil
	}
	if !witnessAvailable(ev) {
		return schemas.Unknown(reasonNoWitness, schemas.EvidenceGateway), nil
	}
	n := 0
	for _, s := range ev.Steps {
		if s.Kind == schemas.StepToolRequest && s.Execution == schemas.ExecutionNoWitness {
			n++
		}
	}
	return schemas.Observed(float64(n), schemas.EvidenceGateway), nil
}

func newToolCallsUnwitnessedMetric() *toolCallsUnwitnessedMetric {
	return &toolCallsUnwitnessedMetric{}
}

// observerProblem returns the value to report and false when an observer's
// records cannot support a count: not applicable when it was off for a
// remote agent, unknown when it was off, missing for any session, or never
// recorded.
func observerProblem(statuses []schemas.ObserverStatus, observer string) (schemas.Value, bool) {
	var mine []schemas.ObserverStatus
	for _, s := range statuses {
		if s.Observer == observer {
			mine = append(mine, s)
		}
	}
	if len(mine) == 0 {
		return schemas.Unknown(fmt.Sprintf("no %s observer record (switched off, or run recorded before observers)", observer), schemas.EvidenceBoundary), false
	}
	var off []string
	for _, s := range mine {
		switch s.State {
		case schemas.ObserverOn:
		case schemas.ObserverOff:
			off = append(off, s.Reason)
		default:
			return schemas.Unknown(fmt.Sprintf("%s observer missing: %s", observer, s.Reason), schemas.EvidenceBoundary), false
		}
	}
	if len(off) == len(mine) && strings.HasPrefix(off[0], "remote agent") {
		return schemas.NotApplicable(), false
	}
	if len(off) > 0 {
		return schemas.Unknown(fmt.Sprintf("%s observer off: %s", observer, off[0]), schemas.EvidenceBoundary), false
	}
	return schemas.Value{}, true
}

// observed reports whether any observer ran for a session.
func observed(statuses []schemas.ObserverStatus) bool {
	for _, s := range statuses {
		if s.State == schemas.ObserverOn {
			return true
		}
	}
	return false
}

// witnessAvailable reports whether anything could have witnessed a tool
// call's execution.
func witnessAvailable(ev *schemas.Evidence) bool {
	if observed(ev.Observers) || ev.ToolCalls != nil || ev.ShimLog != nil {
		return true
	}
	for _, e := range ev.Events {
		if e.Source == schemas.SourceAgentStream {
			return true
		}
	}
	return false
}
