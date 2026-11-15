package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// cleanupTimeout bounds cleanup and seal when they run on a fresh context
// after the run's own context was cancelled.
const cleanupTimeout = 2 * time.Minute

// upstreamPart names the provider's API in the part failure the upstream rule
// records.
const upstreamPart = "upstream"

// Pipeline runs one slot through the fixed stage order of schemas.Stages.
// It is safe for concurrent use: all per-run state lives in runState.
type Pipeline struct {
	reg       *schemas.Registry
	benchRoot string
	logger    schemas.Logger
	now       func() time.Time
	faultMode string // plan mode bound to every run's key slot at prepare
}

// PipelineOption configures a Pipeline.
type PipelineOption func(*Pipeline)

// WithFaultMode sets the fault plan mode bound to each run's key slot when
// no probe binds its own: "none" (the default) tags the run's calls and
// passes them on; "scripted" makes the fault proxy answer every call of the
// run from its scripted model, whatever the provider and whether or not it
// holds a real key. `agent check` uses scripted so a conformance run costs
// nothing. Under scripted, a run without a fault controller or with a
// gateway-layer probe (which would bind its own plan and lift the scripted
// one) is refused before its agent starts.
func WithFaultMode(mode string) PipelineOption {
	return func(p *Pipeline) {
		if mode != "" {
			p.faultMode = mode
		}
	}
}

// SlotInput is one slot to run.
type SlotInput struct {
	Manifest *schemas.Manifest
	Suite    *schemas.Suite
	Slot     *schemas.Slot
	Attempt  int         // 1-based
	Emit     func(Event) // optional progress sink
}

// SlotResult is a finished run.
type SlotResult struct {
	Run     *schemas.Run
	Dir     string
	Metrics map[string]schemas.Value
	Seal    *schemas.SealRecord // nil when sealing failed
}

// runState is everything one run accumulates on its way through the stages.
type runState struct {
	in           SlotInput
	rc           *schemas.RunContext
	env          schemas.Environment
	adapter      schemas.AgentAdapter
	layer        schemas.ProbeLayer // nil for clean
	grader       schemas.Grader
	ev           *schemas.Evidence
	plan         *schemas.RunPlan
	armReceipt   *schemas.Receipt
	receipts     []schemas.Receipt
	metrics      map[string]schemas.Value
	agentStarted bool
	aborted      bool   // a stage failed before the agent started
	broken       bool   // a stage failed after the agent started
	taskImageID  string // id the manifest froze for the per-task agent image; empty when the run has none
}

// NewPipeline returns a pipeline. benchRoot resolves the relative
// notes.user_file of an arm into the absolute host path parts read; the
// manifest keeps the relative path so config hashes do not depend on the
// machine.
func NewPipeline(reg *schemas.Registry, benchRoot string, logger schemas.Logger, opts ...PipelineOption) *Pipeline {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	p := &Pipeline{reg: reg, benchRoot: benchRoot, logger: logger, now: time.Now, faultMode: faultModeNone}
	for _, o := range opts {
		o(p)
	}
	return p
}

// RunSlot runs one attempt of a slot to its seal.
//
// Every stage appends a started and a completed or failed transition before
// the next stage begins. A failure before the agent starts skips straight to
// cleanup and seals the run as aborted_infra; the scheduler then gives the
// slot a new run. A failure after the agent starts never reruns the agent:
// the later stages still run, so as much evidence as possible is kept, and
// the run is sealed as infra_error. Cleanup always runs, on a fresh context
// if ctx was cancelled. When ctx was cancelled because the slot's lease was
// lost (an ErrLeaseLost cause), the run is sealed as aborted_infra whether or
// not its agent started: another owner may already run the slot, and two
// counted runs of one slot would bias the ledger. Stage failures are
// recorded in the run, not returned; the error is non-nil only when the run
// folder could not be created or the run could not be sealed.
func (p *Pipeline) RunSlot(ctx context.Context, in SlotInput) (*SlotResult, error) {
	st, err := p.begin(ctx, in)
	if err != nil {
		return nil, err
	}
	for _, stage := range schemas.Stages {
		if stage == schemas.StageCleanup || stage == schemas.StageSeal {
			continue
		}
		if st.aborted {
			break
		}
		p.runStage(ctx, st, stage)
	}
	cctx, cancel := afterContext(ctx)
	defer cancel()
	p.runStage(cctx, st, schemas.StageCleanup)
	up := utils.TallyUpstream(st.ev.Ledger, st.ev.Requests)
	st.rc.Run.Outcome = decideOutcome(st, up)
	if cause := context.Cause(ctx); errors.Is(cause, ErrLeaseLost) {
		st.rc.Run.Outcome = schemas.OutcomeAbortedInfra
		st.rc.Run.Failures = append(st.rc.Run.Failures, schemas.PartFailure{Stage: schemas.StageLeased, Part: "engine",
			Error: cause.Error() + "; the run was stopped and is not counted, since another owner may run the slot"})
	}
	st.rc.Run.Y = scoreOf(st.rc.Run.Outcome)
	if pf, ok := upstreamFailure(up, st.rc.Run.Outcome); ok {
		st.rc.Run.Failures = append(st.rc.Run.Failures, pf)
		p.logger.Warn("provider unreachable during run", "run", st.rc.Run.RunID, "slot", st.rc.Slot.SlotID,
			"outcome", string(st.rc.Run.Outcome), "upstream_errors", up.Errors, "answered", up.Answered)
	}
	return p.seal(cctx, st)
}

// SealInterrupted seals a run that a crashed process left unsealed. A run
// whose agent started is sealed as infra_error and never rerun; any other run
// is sealed as aborted_infra so the scheduler may start a new one. Its
// workspace and key were removed by the start-up janitor.
func (p *Pipeline) SealInterrupted(ctx context.Context, in SlotInput, rec schemas.RunRecord) (*SlotResult, error) {
	run := rec.Run
	if run.RunID == "" {
		return nil, fmt.Errorf("slot %s: unsealed run in %s has no run id", in.Slot.SlotID, rec.Dir)
	}
	st := &runState{
		in: in,
		rc: &schemas.RunContext{Experiment: &in.Manifest.Experiment, Manifest: in.Manifest, Suite: in.Suite,
			Slot: in.Slot, Run: &run, Dir: rec.Dir, Logger: p.logger},
		ev:           &schemas.Evidence{},
		agentStarted: run.Timing.AgentStarted != nil || stageCompleted(rec.Transitions, schemas.StageRun),
	}
	open := openStage(rec.Transitions)
	if open != "" {
		p.appendTransition(ctx, st, open, schemas.TransitionFailed, "interrupted: the process stopped before the stage finished")
	} else {
		open = run.Stage
	}
	run.Failures = append(run.Failures, schemas.PartFailure{Stage: open, Part: "engine",
		Error: "run interrupted before seal; sealed on resume after the start-up janitor removed its workspace and key"})
	p.appendTransition(ctx, st, schemas.StageCleanup, schemas.TransitionStarted, "")
	p.appendTransition(ctx, st, schemas.StageCleanup, schemas.TransitionCompleted, "")
	run.Stage = schemas.StageCleanup
	if st.agentStarted {
		run.Outcome = schemas.OutcomeInfraError
	} else {
		run.Outcome = schemas.OutcomeAbortedInfra
	}
	run.Y = scoreOf(run.Outcome)
	return p.seal(ctx, st)
}

// begin creates the run record and folder.
func (p *Pipeline) begin(ctx context.Context, in SlotInput) (*runState, error) {
	if p.reg == nil || p.reg.Store == nil {
		return nil, errors.New("pipeline: no store registered")
	}
	if in.Manifest == nil || in.Suite == nil || in.Slot == nil {
		return nil, errors.New("pipeline: manifest, suite and slot are required")
	}
	slot := in.Slot
	arm, err := armConfig(in.Manifest, slot)
	if err != nil {
		return nil, err
	}
	cfg := arm.Config
	taskTag, taskImageID := taskAgentImage(arm, slot.Task)
	if taskTag != "" {
		cfg.Image = taskTag
	}
	if cfg.Notes.UserFile != "" && !filepath.IsAbs(cfg.Notes.UserFile) {
		cfg.Notes.UserFile = filepath.Join(p.benchRoot, cfg.Notes.UserFile)
	}
	runID := utils.NewID("r", 10)
	run := &schemas.Run{
		RunID: runID, SlotID: slot.SlotID, PairID: slot.PairID, ExpID: slot.ExpID,
		Attempt: max(in.Attempt, 1), Stage: schemas.StageLeased,
		ManifestHash: in.Manifest.Hash, SuiteLockHash: in.Manifest.SuiteLockHash,
		TaskHash: slot.TaskHash, RunInputHash: slot.RunInputHash, ArmIdentity: arm.Identity, BudgetSnapshot: arm.BudgetSnapshot,
		Outcome: schemas.OutcomeUnknown, Y: schemas.Unknown("not_finished", schemas.EvidenceOutcome),
		Timing: schemas.RunTiming{Started: p.now().UTC()},
		Agent: schemas.RunAgentInfo{ConfigID: cfg.ID, Adapter: cfg.Adapter, Parser: cfg.Parser, ConfigHash: arm.ConfigHash,
			AgentVersion: cfg.AgentVersion, ImageDigest: cfg.ImageDigest, Capabilities: cfg.Capabilities, CapabilityGaps: arm.CapabilityGaps,
			ParserVersion: p.parserVersion(cfg.Parser)},
		Gateway: schemas.RunGatewayInfo{SessionID: runID},
	}
	dir, err := p.reg.Store.NewRun(ctx, slot, run)
	if err != nil {
		return nil, fmt.Errorf("create run folder for slot %s: %w", slot.SlotID, err)
	}
	st := &runState{
		in: in,
		rc: &schemas.RunContext{
			Experiment: &in.Manifest.Experiment, Manifest: in.Manifest, Suite: in.Suite,
			Task: in.Suite.TaskByID(slot.Task), Slot: slot, Run: run, Agent: &cfg, Dir: dir, Logger: p.logger,
		},
		ev:          &schemas.Evidence{},
		taskImageID: taskImageID,
	}
	if slot.Condition != schemas.ConditionClean {
		st.rc.Probe = in.Suite.ProbeByID(slot.Condition)
	}
	p.emit(st, Event{Kind: EventRunStarted, Message: fmt.Sprintf("attempt %d", run.Attempt)})
	if err := p.writeJSON(ctx, st, "run.json", run); err != nil {
		p.fail(ctx, st, schemas.StageLeased, err)
	}
	return st, nil
}

func (p *Pipeline) parserVersion(name string) string {
	if name == "" {
		return ""
	}
	for _, parser := range p.reg.Parsers {
		if parser != nil && parser.GetName() == name {
			return parser.GetVersion()
		}
	}
	return ""
}

// runStage runs one stage between its started and completed or failed
// transitions. run.json is checkpointed before the completed transition so a
// resumed run never claims more than it did.
func (p *Pipeline) runStage(ctx context.Context, st *runState, stage schemas.Stage) {
	if err := p.appendTransition(ctx, st, stage, schemas.TransitionStarted, ""); err != nil {
		p.fail(ctx, st, stage, err)
		return
	}
	p.emit(st, Event{Kind: EventStage, Stage: stage, Message: string(schemas.TransitionStarted)})
	err := ctx.Err()
	if err == nil {
		err = p.do(ctx, st, stage)
	}
	if err != nil {
		p.fail(ctx, st, stage, err)
		return
	}
	prev := st.rc.Run.Stage
	st.rc.Run.Stage = stage
	if err := p.writeJSON(ctx, st, "run.json", st.rc.Run); err != nil {
		st.rc.Run.Stage = prev
		p.fail(ctx, st, stage, err)
		return
	}
	if err := p.appendTransition(ctx, st, stage, schemas.TransitionCompleted, ""); err != nil {
		p.fail(ctx, st, stage, err)
		return
	}
	p.emit(st, Event{Kind: EventStage, Stage: stage, Message: string(schemas.TransitionCompleted)})
}

// fail records a stage failure: one PartFailure per failing part, a failed
// transition, and the aborted or broken flag depending on whether the agent
// had started.
func (p *Pipeline) fail(ctx context.Context, st *runState, stage schemas.Stage, err error) {
	for _, e := range flatten(err) {
		pf := schemas.PartFailure{Stage: stage, Part: "engine", Error: e.Error()}
		var pe *partError
		if errors.As(e, &pe) {
			pf.Part, pf.Error = pe.part, pe.err.Error()
		}
		st.rc.Run.Failures = append(st.rc.Run.Failures, pf)
	}
	if st.agentStarted {
		st.broken = true
	} else {
		st.aborted = true
	}
	p.logger.Warn("run stage failed", "run", st.rc.Run.RunID, "slot", st.rc.Slot.SlotID, "stage", string(stage), "error", err.Error())
	if terr := p.appendTransition(ctx, st, stage, schemas.TransitionFailed, err.Error()); terr != nil {
		p.logger.Error("append failed transition", "run", st.rc.Run.RunID, "stage", string(stage), "error", terr.Error())
	}
	p.emit(st, Event{Kind: EventStage, Stage: stage, Message: "failed: " + err.Error()})
}

// seal writes the remaining artifacts, appends the seal transitions and asks
// the store to seal the folder. The completed transition is appended before
// Store.Seal because a sealed folder is read-only.
func (p *Pipeline) seal(ctx context.Context, st *runState) (*SlotResult, error) {
	run := st.rc.Run
	res := &SlotResult{Run: run, Dir: st.rc.Dir, Metrics: st.metrics}
	sealFailed := func(err error) (*SlotResult, error) {
		run.Stage = schemas.StageCleanup
		p.fail(ctx, st, schemas.StageSeal, err)
		if st.agentStarted {
			run.Outcome, run.Y = schemas.OutcomeInfraError, scoreOf(schemas.OutcomeInfraError)
		}
		return res, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	if err := p.appendTransition(ctx, st, schemas.StageSeal, schemas.TransitionStarted, ""); err != nil {
		return sealFailed(err)
	}
	now := p.now().UTC()
	run.Timing.Sealed = &now
	run.Stage = schemas.StageSeal
	if err := p.writeArtifacts(ctx, st); err != nil {
		return sealFailed(err)
	}
	if err := p.appendTransition(ctx, st, schemas.StageSeal, schemas.TransitionCompleted, ""); err != nil {
		return sealFailed(err)
	}
	rec, err := p.reg.Store.Seal(ctx, run)
	if err != nil {
		return sealFailed(wrapPart("store", p.reg.Store, err))
	}
	res.Seal = rec
	p.emit(st, Event{Kind: EventRunSealed, Stage: schemas.StageSeal, Outcome: run.Outcome})
	return res, nil
}

// writeArtifacts writes canonical.json first, then run.json, evidence.json,
// gates.json, grade.json and plan.json, and for a scenario run sessions.json
// and services.json. The canonical document is hashed into run.json before
// the store seals the run. Secrets in the plan's env are redacted.
func (p *Pipeline) writeArtifacts(ctx context.Context, st *runState) error {
	gates := st.ev.Gates
	if gates == nil {
		gates = []schemas.GateResult{}
	}
	canonical, err := p.canonicalRun(st, gates)
	if err != nil {
		return err
	}
	canonicalBytes, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return fmt.Errorf("encode canonical run %s: %w", st.rc.Run.RunID, err)
	}
	if err := p.reg.Store.WriteRunFile(ctx, st.rc.Run, "canonical.json", canonicalBytes); err != nil {
		return wrapPart("store", p.reg.Store, fmt.Errorf("write canonical.json: %w", err))
	}
	st.rc.Run.CanonicalHash = "sha256:" + utils.SHA256Bytes(canonicalBytes)
	files := []struct {
		name string
		v    any
	}{
		{"evidence.json", trimEvidence(st.ev)},
		{"gates.json", gates},
		{"grade.json", st.ev.Grade},
		{"plan.json", redactPlan(st.plan)},
		{"run.json", st.rc.Run},
	}
	if len(st.ev.Sessions) > 0 {
		files = append(files, struct {
			name string
			v    any
		}{FileSessions, st.ev.Sessions})
	}
	if len(st.ev.Services) > 0 {
		files = append(files, struct {
			name string
			v    any
		}{FileServices, st.ev.Services})
	}
	for _, f := range files {
		if err := p.writeJSON(ctx, st, f.name, f.v); err != nil {
			return err
		}
	}
	return nil
}

func (p *Pipeline) canonicalRun(st *runState, gates []schemas.GateResult) (*schemas.CanonicalRun, error) {
	run := st.rc.Run
	arm, err := armConfig(st.in.Manifest, st.rc.Slot)
	if err != nil {
		return nil, err
	}
	identity := schemas.CanonicalTaskIdentity{SuiteID: st.rc.Suite.ID, SuiteVersion: st.rc.Suite.Version,
		TaskID: st.rc.Slot.Task, TaskVersion: st.rc.Slot.TaskVersion, TaskHash: st.rc.Slot.TaskHash,
		Condition: st.rc.Slot.Condition, Trial: st.rc.Slot.Trial}
	for _, candidate := range st.in.Manifest.TaskIdentities {
		if candidate.TaskID == identity.TaskID && candidate.Condition == identity.Condition && candidate.Trial == identity.Trial {
			identity = candidate
			break
		}
	}
	input := st.in.Manifest.RunInputs[st.rc.Slot.SlotID]
	if input.RunInputHash == "" {
		input = schemas.RunInputIdentity{TaskHash: run.TaskHash, RunInputHash: run.RunInputHash, Condition: runSlotCondition(st), ArmConfigHash: arm.ConfigHash, ModelRequested: arm.Config.Model, GatewayProvider: arm.Config.Gateway.Provider}
	}
	armIdentity := schemas.CanonicalArmIdentity{}
	if arm.Identity != nil {
		armIdentity = *arm.Identity
	}
	budget := schemas.BudgetSnapshot{}
	if arm.BudgetSnapshot != nil {
		budget = *arm.BudgetSnapshot
	}
	status := schemas.EvidenceStatus{Level: schemas.EvidenceUnknown}
	if st.ev.Status != nil {
		status = *st.ev.Status
	}
	generatedAt := time.Time{}
	if run.Timing.Sealed != nil {
		generatedAt = *run.Timing.Sealed
	}
	return &schemas.CanonicalRun{
		CanonicalVersion: schemas.CanonicalVersion, SchemaVersion: schemas.CanonicalSchemaVersion,
		GeneratedAt: generatedAt, RunID: run.RunID, Experiment: run.ExpID, Task: identity,
		RunInput: input, Arm: st.rc.Slot.Arm, Agent: armIdentity, Budgets: budget,
		ModelRequested: runModelRequested(st), ModelServed: runModelServed(st), Events: append([]schemas.Event(nil), st.ev.Events...),
		Grade: st.ev.Grade, Gates: append([]schemas.GateResult(nil), gates...), Metrics: st.metrics,
		EvidenceStatus: status, LogicalSteps: st.ev.LogicalSteps, Attempts: st.ev.Attempts,
		Retries: st.ev.Retries, Fallbacks: st.ev.Fallbacks, ToolCalls: st.ev.RequestedTools,
	}, nil
}

func runSlotCondition(st *runState) string { return st.rc.Slot.Condition }

func runModelRequested(st *runState) string {
	if st.rc.Agent == nil {
		return ""
	}
	return st.rc.Agent.Model
}

func runModelServed(st *runState) string {
	for _, event := range st.ev.Events {
		if event.ModelServed != "" {
			return event.ModelServed
		}
	}
	return ""
}

func (p *Pipeline) writeJSON(ctx context.Context, st *runState, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	if err := p.reg.Store.WriteRunFile(ctx, st.rc.Run, name, b); err != nil {
		return wrapPart("store", p.reg.Store, fmt.Errorf("write %s: %w", name, err))
	}
	return nil
}

func (p *Pipeline) appendTransition(ctx context.Context, st *runState, stage schemas.Stage, status schemas.TransitionStatus, msg string) error {
	t := schemas.Transition{Stage: stage, Status: status, At: p.now().UTC(), Error: msg}
	if err := p.reg.Store.AppendTransition(ctx, st.rc.Run, t); err != nil {
		return wrapPart("store", p.reg.Store, fmt.Errorf("append %s %s transition: %w", stage, status, err))
	}
	return nil
}

func (p *Pipeline) emit(st *runState, ev Event) {
	if st.in.Emit == nil {
		return
	}
	ev.SlotID, ev.RunID = st.rc.Slot.SlotID, st.rc.Run.RunID
	if ev.At.IsZero() {
		ev.At = p.now()
	}
	st.in.Emit(ev)
}

// decideOutcome maps what the run observed to its outcome.
func decideOutcome(st *runState, up utils.UpstreamTally) schemas.Outcome {
	var ended schemas.Outcome
	if st.rc.Task != nil {
		ended = sessionsEnded(st.ev.Sessions, len(st.rc.Task.Sessions))
	}
	return outcomeFrom(st.aborted, st.broken, ended, st.ev.Gates, st.ev.Grade, st.rc.Exec, up)
}

// outcomeFrom maps what a run observed to its outcome. A fired veto gate is an
// observed fact and wins over a later infrastructure failure. Regate uses it
// too, so a recomputed gate list decides an outcome the same way.
//
// up is the run's upstream tally from the fault proxy ledger. Model calls the
// proxy passed on with no fault planned but could not deliver to the provider
// are an infrastructure failure, never the agent's:
//   - no model call was answered at all: aborted_infra. The agent never got a
//     model response, so nothing it did depends on the model, and the
//     scheduler gives the slot a new run as it does for any aborted run.
//   - some calls were answered and the last model call failed upstream: the
//     agent stopped because the provider went away. infra_error (unknown,
//     never rerun, since the agent did real work), unless the grade passed.
//
// Upstream errors the agent recovered from (a later call was answered and the
// run ended on it) change nothing; the upstream_errors gate reports them.
//
// ended is the outcome a session of a multi-session run ended it with before
// its last session (sessionsEnded), empty otherwise. Like a veto it is an
// observed fact about the agent, so it wins over a later infrastructure
// failure and over the grade of a run whose last session never ran.
func outcomeFrom(aborted, broken bool, ended schemas.Outcome, gates []schemas.GateResult, grade *schemas.Grade, exec *schemas.ExecResult, up utils.UpstreamTally) schemas.Outcome {
	switch {
	case aborted:
		return schemas.OutcomeAbortedInfra
	case vetoFired(gates):
		return schemas.OutcomeIntegrityViolation
	case up.NeverAnswered():
		return schemas.OutcomeAbortedInfra
	case ended != "":
		return ended
	case broken, grade == nil:
		return schemas.OutcomeInfraError
	case grade.InvalidEvaluator:
		return schemas.OutcomeInvalidEvaluator
	case grade.Passed:
		return schemas.OutcomePass
	case up.EndedOnError():
		return schemas.OutcomeInfraError
	case exec != nil && exec.TimedOut:
		return schemas.OutcomeTimeout
	default:
		return schemas.OutcomeFail
	}
}

// upstreamFailure is the part failure that records why the upstream rule of
// outcomeFrom made a run infrastructure: ok is false when it did not.
func upstreamFailure(up utils.UpstreamTally, o schemas.Outcome) (schemas.PartFailure, bool) {
	if !up.NeverAnswered() && !up.EndedOnError() {
		return schemas.PartFailure{}, false
	}
	if o != schemas.OutcomeAbortedInfra && o != schemas.OutcomeInfraError {
		return schemas.PartFailure{}, false
	}
	msg := fmt.Sprintf("provider unreachable: %d model calls with no planned fault got no response from the provider, %d answered, last call failed: %t",
		up.Errors, up.Answered, up.LastFailed)
	switch {
	case up.FirstError != "":
		msg += "; first error: " + up.FirstError
	case up.Inferred:
		msg += "; inferred from the gateway's \"" + utils.ProxyUnreachable + "\" errors (proxy predates upstream_error)"
	}
	return schemas.PartFailure{Stage: schemas.StageRun, Part: upstreamPart, Error: msg}, true
}

func vetoFired(gates []schemas.GateResult) bool {
	for _, g := range gates {
		if g.Fired && g.Effect == schemas.GateVeto {
			return true
		}
	}
	return false
}

// scoreOf returns the run's Y: observed 1 or 0 when the outcome scores,
// unknown otherwise.
func scoreOf(o schemas.Outcome) schemas.Value {
	if y, ok := o.Score(); ok {
		return schemas.Observed(y, schemas.EvidenceOutcome)
	}
	return schemas.Unknown(string(o), schemas.EvidenceOutcome)
}

// afterContext returns ctx while it is live, or a fresh bounded context when
// it was cancelled, so cleanup and seal still run after an interrupt.
func afterContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}

// trimEvidence drops the large raw gateway rows; gateway.requests.jsonl keeps
// them.
func trimEvidence(ev *schemas.Evidence) schemas.Evidence {
	out := *ev
	if ev.Requests != nil {
		rl := *ev.Requests
		rl.Requests = make([]schemas.GatewayRequest, len(ev.Requests.Requests))
		for i, r := range ev.Requests.Requests {
			r.Raw = nil
			rl.Requests[i] = r
		}
		out.Requests = &rl
	}
	return out
}

// redactPlan copies the plan with env values blanked for every key that
// names a credential.
func redactPlan(plan *schemas.RunPlan) *schemas.RunPlan {
	if plan == nil {
		return nil
	}
	out := *plan
	out.Env = make(map[string]string, len(plan.Env))
	for k, v := range plan.Env {
		if isSecretKey(k) {
			v = "[redacted]"
		}
		out.Env[k] = v
	}
	return &out
}

func isSecretKey(k string) bool {
	u := strings.ToUpper(k)
	for _, s := range []string{"KEY", "TOKEN", "SECRET", "AUTH"} {
		if strings.Contains(u, s) {
			return true
		}
	}
	return false
}

func stageCompleted(ts []schemas.Transition, stage schemas.Stage) bool {
	for _, t := range ts {
		if t.Stage == stage && t.Status == schemas.TransitionCompleted {
			return true
		}
	}
	return false
}

// openStage returns the last stage that started and never finished.
func openStage(ts []schemas.Transition) schemas.Stage {
	open := map[schemas.Stage]bool{}
	var last schemas.Stage
	for _, t := range ts {
		switch t.Status {
		case schemas.TransitionStarted:
			open[t.Stage] = true
			last = t.Stage
		default:
			delete(open, t.Stage)
		}
	}
	if last != "" && open[last] {
		return last
	}
	return ""
}

// flatten splits a joined error into its parts.
func flatten(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, e := range j.Unwrap() {
			out = append(out, flatten(e)...)
		}
		return out
	}
	return []error{err}
}
