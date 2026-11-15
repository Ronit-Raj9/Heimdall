package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/schemas"
)

// faultModeNone is the fault proxy's pass-through plan mode.
const faultModeNone = "none"

// faultPart names the fault proxy in a run's failure list. FaultController
// is not a Part, so it has no registered name.
const faultPart = "fault_proxy"

// do dispatches one stage. The order lives in schemas.Stages only.
func (p *Pipeline) do(ctx context.Context, st *runState, stage schemas.Stage) error {
	switch stage {
	case schemas.StagePrepare:
		return p.prepare(ctx, st)
	case schemas.StageArm:
		return p.arm(ctx, st)
	case schemas.StageRun:
		return p.runAgent(ctx, st)
	case schemas.StageDisarm:
		return p.disarm(ctx, st)
	case schemas.StageExport:
		return p.export(ctx, st)
	case schemas.StageCollect:
		return p.collect(ctx, st)
	case schemas.StageGrade:
		return p.grade(ctx, st)
	case schemas.StageGate:
		return p.gate(ctx, st)
	case schemas.StageMeasure:
		return p.measure(ctx, st)
	case schemas.StageReceipt:
		return p.receipt(ctx, st)
	case schemas.StageCleanup:
		return p.cleanup(ctx, st)
	default:
		return fmt.Errorf("no handler for stage %s", stage)
	}
}

// prepare resolves the run's parts, creates the workspace and mints the
// run's virtual key.
func (p *Pipeline) prepare(ctx context.Context, st *runState) error {
	if err := p.resolveParts(st); err != nil {
		return err
	}
	h, err := st.env.Create(ctx, st.rc)
	if err != nil {
		return wrapPart("environment", st.env, fmt.Errorf("create workspace: %w", err))
	}
	st.rc.Handle = h
	key, err := p.reg.Gateway.MintKey(ctx, st.rc)
	if err != nil {
		return wrapPart("gateway", p.reg.Gateway, fmt.Errorf("mint key: %w", err))
	}
	if key == nil {
		return wrapPart("gateway", p.reg.Gateway, errors.New("mint key returned no key"))
	}
	if st.rc.Agent != nil && st.rc.Agent.Gateway.Provider != "" && key.Provider != "" && key.Provider != st.rc.Agent.Gateway.Provider {
		return &partError{part: "gateway", err: fmt.Errorf("gateway provider mismatch before execution: expected %s, got %s", st.rc.Agent.Gateway.Provider, key.Provider)}
	}
	if st.rc.Agent != nil && st.rc.Agent.Model != "" && key.Model != "" && key.Model != st.rc.Agent.Model && !strings.HasPrefix(key.Model, st.rc.Agent.Model+"/") {
		return &partError{part: "gateway", err: fmt.Errorf("gateway model mismatch before execution: expected %s, got %s", st.rc.Agent.Model, key.Model)}
	}
	st.rc.Key = key
	st.rc.Run.Gateway.VirtualKeyID = key.ID
	st.rc.Run.Gateway.KeySlot = key.KeySlot
	return p.bindFaultPlan(ctx, st)
}

// bindFaultPlan binds an empty fault plan carrying the run id to the run's
// key slot, so the fault proxy tags every ledger line of the run even when
// nothing is injected. Without it a clean run has an empty ledger and the
// gateway completeness join fails. A gateway-layer probe binds its own plan
// when it arms, so it is skipped here.
func (p *Pipeline) bindFaultPlan(ctx context.Context, st *runState) error {
	if p.reg.Fault == nil || (st.rc.Probe != nil && st.rc.Probe.Layer == schemas.LayerGateway) {
		return nil
	}
	plan := schemas.FaultPlan{Mode: faultModeNone, RunID: st.rc.Run.RunID}
	if err := p.reg.Fault.SetPlan(ctx, st.rc.Key.KeySlot, plan); err != nil {
		return &partError{part: faultPart, err: fmt.Errorf("bind empty plan to key slot %s: %w", st.rc.Key.KeySlot, err)}
	}
	return nil
}

func (p *Pipeline) resolveParts(st *runState) error {
	rc := st.rc
	var errs []error
	var ok bool
	if rc.Task == nil {
		errs = append(errs, fmt.Errorf("task %s is not loaded", rc.Slot.Task))
	}
	if st.env, ok = findPart(p.reg.Environments, rc.Suite.Environment); !ok {
		errs = append(errs, fmt.Errorf("environment %q is not registered", rc.Suite.Environment))
	}
	if st.adapter, ok = findPart(p.reg.Adapters, rc.Agent.Adapter); !ok {
		errs = append(errs, fmt.Errorf("adapter %q is not registered", rc.Agent.Adapter))
	}
	if st.grader, ok = findPart(p.reg.Graders, rc.Suite.Grader); !ok {
		errs = append(errs, fmt.Errorf("grader %q is not registered", rc.Suite.Grader))
	}
	if rc.Slot.Condition != schemas.ConditionClean {
		if rc.Probe == nil {
			errs = append(errs, fmt.Errorf("probe %s is not in the suite", rc.Slot.Condition))
		} else if st.layer, ok = findPart(p.reg.ProbeLayers, string(rc.Probe.Layer)); !ok {
			errs = append(errs, fmt.Errorf("probe layer %q is not registered", rc.Probe.Layer))
		}
	}
	if p.reg.Gateway == nil {
		errs = append(errs, errors.New("no gateway registered"))
	}
	return errors.Join(errs...)
}

// arm arms the probe's layer and keeps its receipt. Clean runs arm nothing.
func (p *Pipeline) arm(ctx context.Context, st *runState) error {
	if st.layer == nil {
		return nil
	}
	r, err := st.layer.Arm(ctx, st.rc)
	if err != nil {
		return wrapPart("probe_layer", st.layer, fmt.Errorf("arm %s: %w", st.rc.Probe.ID, err))
	}
	st.armReceipt = r
	return nil
}

// runAgent plans the container and executes it. run.json is checkpointed
// with AgentStarted before Exec is called, so a resumed run can always tell
// whether its agent may have started and must never be rerun.
func (p *Pipeline) runAgent(ctx context.Context, st *runState) error {
	var plan *schemas.RunPlan
	var err error
	if validator, ok := st.adapter.(schemas.AdapterValidator); ok {
		if err = validator.Validate(ctx, st.rc); err != nil {
			return wrapPart("adapter", st.adapter, fmt.Errorf("validate: %w", err))
		}
	}
	if runner, ok := st.adapter.(schemas.AdapterRunner); ok {
		plan, err = runner.BuildExecutionPlan(ctx, st.rc)
	} else {
		plan, err = st.adapter.Plan(ctx, st.rc)
	}
	if err != nil {
		return wrapPart("adapter", st.adapter, fmt.Errorf("plan: %w", err))
	}
	if plan == nil {
		return wrapPart("adapter", st.adapter, errors.New("plan returned no plan"))
	}
	st.plan = plan
	run := st.rc.Run
	started := p.now().UTC()
	run.Timing.AgentStarted = &started
	if err := p.writeJSON(ctx, st, "run.json", run); err != nil {
		run.Timing.AgentStarted = nil
		return fmt.Errorf("checkpoint before agent start: %w", err)
	}
	st.agentStarted = true
	res, err := st.env.Exec(ctx, st.rc, plan)
	exited := p.now().UTC()
	run.Timing.AgentExited = &exited
	if res != nil {
		st.rc.Exec, st.ev.Exec = res, res
		code := res.ExitCode
		run.Agent.ExitCode = &code
		run.Agent.TimedOut = res.TimedOut
		if !res.Started.IsZero() {
			s := res.Started.UTC()
			run.Timing.AgentStarted = &s
		}
		if !res.Ended.IsZero() {
			e := res.Ended.UTC()
			run.Timing.AgentExited = &e
		}
	}
	if err != nil {
		return wrapPart("environment", st.env, fmt.Errorf("exec agent: %w", err))
	}
	if res == nil {
		return wrapPart("environment", st.env, errors.New("exec returned no result"))
	}
	return nil
}

// disarm disarms the probe's layer. P0 arms one layer per run, so reverse
// order is that one layer.
func (p *Pipeline) disarm(ctx context.Context, st *runState) error {
	if st.layer == nil {
		return nil
	}
	if err := st.layer.Disarm(ctx, st.rc); err != nil {
		return wrapPart("probe_layer", st.layer, fmt.Errorf("disarm %s: %w", st.rc.Probe.ID, err))
	}
	return nil
}

func (p *Pipeline) export(ctx context.Context, st *runState) error {
	d, err := st.env.ExportDiff(ctx, st.rc)
	if err != nil {
		return wrapPart("environment", st.env, fmt.Errorf("export diff: %w", err))
	}
	if d == nil {
		return wrapPart("environment", st.env, errors.New("export diff returned no diff"))
	}
	st.ev.Diff = d
	return nil
}

// collect runs every evidence source in registry order into one Evidence. A
// failing source does not stop the others.
func (p *Pipeline) collect(ctx context.Context, st *runState) error {
	var errs []error
	st.ev.Contract = &schemas.RunEvidenceContract{Required: []string{"gateway_requests"}, Quality: schemas.EvidenceQualityUnknown}
	if st.rc.Agent != nil && st.rc.Agent.Capabilities != nil && st.rc.Agent.Capabilities.SupportsToolEvents {
		st.ev.Contract.Required = append(st.ev.Contract.Required, "tool_execution")
	}
	if st.rc.Run != nil && len(st.rc.Run.Agent.CapabilityGaps) > 0 {
		st.ev.Contract.OptionalMissing = append([]string(nil), st.rc.Run.Agent.CapabilityGaps...)
	}
	for _, src := range p.reg.Evidence {
		if err := src.Collect(ctx, st.rc, st.ev); err != nil {
			errs = append(errs, wrapPart("evidence", src, fmt.Errorf("collect: %w", err)))
		}
	}
	if st.ev.Claim != nil {
		st.rc.Run.Claim = st.ev.Claim
	}
	st.ev.Contract.Available = availableEvidence(st.ev)
	st.ev.Events = append(st.ev.Events, gatewayIntegrityEvents(st.rc, st.ev.Requests)...)
	joined := evidence.JoinEvents(st.ev.Events)
	st.ev.Events = joined.Events
	st.ev.Status = &schemas.EvidenceStatus{Level: schemas.EvidenceStrong, Conflicts: joined.Conflicts}
	for _, event := range joined.Events {
		if event.EvidenceSource != "" && !slices.Contains(st.ev.Contract.Sources, event.EvidenceSource) {
			st.ev.Contract.Sources = append(st.ev.Contract.Sources, event.EvidenceSource)
		}
	}
	st.ev.Contract.Conflicts = append(st.ev.Contract.Conflicts, joined.Conflicts...)
	if len(joined.Conflicts) > 0 {
		st.ev.Status.Level = schemas.EvidencePartial
	}
	for _, required := range st.ev.Contract.Required {
		if !slices.Contains(st.ev.Contract.Available, required) {
			st.ev.Contract.Missing = append(st.ev.Contract.Missing, required)
		}
	}
	if len(st.ev.Contract.Missing) == 0 && len(st.ev.Contract.OptionalMissing) == 0 {
		st.ev.Contract.Quality = schemas.EvidenceQualityComplete
	} else if len(st.ev.Contract.Available) > 0 || len(st.ev.Contract.OptionalMissing) > 0 || len(joined.Conflicts) > 0 {
		st.ev.Contract.Quality = schemas.EvidenceQualityPartial
	}
	return errors.Join(errs...)
}

func gatewayIntegrityEvents(rc *schemas.RunContext, log *schemas.RequestLog) []schemas.Event {
	if rc == nil || rc.Run == nil || rc.Agent == nil || log == nil {
		return nil
	}
	seq := 1
	var events []schemas.Event
	for _, req := range log.Requests {
		if rc.Agent.Gateway.Provider != "" && req.Provider != "" && req.Provider != rc.Agent.Gateway.Provider {
			e := schemas.Event{EventVersion: schemas.EventSchemaVersion, EventID: fmt.Sprintf("%s:%s:%d", rc.Run.RunID, schemas.SourceGateway, seq), RunID: rc.Run.RunID, SlotID: rc.Run.SlotID, PairID: rc.Run.PairID, Seq: seq, Sequence: seq, TS: req.Timestamp, Timestamp: req.Timestamp, Type: schemas.EventIntegrity, Source: schemas.SourceGateway, Evidence: schemas.EvidenceGateway, EvidenceLevel: schemas.EvidenceGateway}
			seq++
			e.Status = schemas.EventStatusError
			e.RequestID, e.Provider, e.ModelReq, e.ModelServed = req.ID, req.Provider, req.ModelRequested, req.ModelServed
			e.Summary = fmt.Sprintf("gateway provider mismatch: expected %s, got %s", rc.Agent.Gateway.Provider, req.Provider)
			events = append(events, e)
		}
		if rc.Agent.Model != "" && req.ModelRequested != "" && req.ModelRequested != rc.Agent.Model && !strings.HasSuffix(req.ModelRequested, "/"+rc.Agent.Model) {
			e := schemas.Event{EventVersion: schemas.EventSchemaVersion, EventID: fmt.Sprintf("%s:%s:%d", rc.Run.RunID, schemas.SourceGateway, seq), RunID: rc.Run.RunID, SlotID: rc.Run.SlotID, PairID: rc.Run.PairID, Seq: seq, Sequence: seq, TS: req.Timestamp, Timestamp: req.Timestamp, Type: schemas.EventIntegrity, Source: schemas.SourceGateway, Evidence: schemas.EvidenceGateway, EvidenceLevel: schemas.EvidenceGateway}
			seq++
			e.Status = schemas.EventStatusError
			e.RequestID, e.Provider, e.ModelReq, e.ModelServed = req.ID, req.Provider, req.ModelRequested, req.ModelServed
			e.Summary = fmt.Sprintf("gateway model mismatch: expected %s, got %s", rc.Agent.Model, req.ModelRequested)
			events = append(events, e)
		}
		if req.FallbackIndex > 0 {
			e := schemas.Event{EventVersion: schemas.EventSchemaVersion, EventID: fmt.Sprintf("%s:%s:%d", rc.Run.RunID, schemas.SourceGateway, seq), RunID: rc.Run.RunID, SlotID: rc.Run.SlotID, PairID: rc.Run.PairID, Seq: seq, Sequence: seq, TS: req.Timestamp, Timestamp: req.Timestamp, Type: schemas.EventIntegrity, Source: schemas.SourceGateway, Evidence: schemas.EvidenceGateway, EvidenceLevel: schemas.EvidenceGateway}
			seq++
			e.Status = schemas.EventStatusError
			e.RequestID = req.ID
			e.Summary = "unplanned gateway fallback"
			events = append(events, e)
		}
	}
	return events
}

func availableEvidence(ev *schemas.Evidence) []string {
	var out []string
	if ev.Requests != nil {
		out = append(out, "gateway_requests")
	}
	if len(ev.ShimLog) > 0 {
		out = append(out, "tool_execution")
	}
	if len(ev.Events) > 0 {
		out = append(out, "agent_events")
	}
	return out
}

func (p *Pipeline) grade(ctx context.Context, st *runState) error {
	if st.ev.Diff == nil {
		return errors.New("no exported diff to grade")
	}
	g, err := st.grader.Grade(ctx, st.rc, st.ev.Diff)
	if err != nil {
		return wrapPart("grader", st.grader, fmt.Errorf("grade: %w", err))
	}
	if g == nil {
		return wrapPart("grader", st.grader, errors.New("grade returned no grade"))
	}
	st.ev.Grade = g
	return nil
}

// gate runs the baseline gates and the experiment's extra gates. A failing
// gate does not stop the others.
func (p *Pipeline) gate(ctx context.Context, st *runState) error {
	var errs []error
	for _, g := range selectGates(p.reg, st.rc.Experiment.ExtraGates) {
		res, err := g.Check(ctx, st.rc, st.ev)
		if err != nil {
			errs = append(errs, wrapPart("gate", g, fmt.Errorf("check: %w", err)))
			continue
		}
		if res == nil {
			continue
		}
		r := *res
		if r.Gate == "" {
			r.Gate = g.GetName()
		}
		if r.Version == "" {
			r.Version = g.GetVersion()
		}
		if r.Effect == "" {
			r.Effect = g.Effect()
		}
		st.ev.Gates = append(st.ev.Gates, r)
	}
	return errors.Join(errs...)
}

// measure computes every selected metric and writes metrics.json. A metric
// that fails is recorded as unknown, never zero.
func (p *Pipeline) measure(ctx context.Context, st *runState) error {
	var errs []error
	st.metrics = map[string]schemas.Value{}
	for _, m := range selectMetrics(p.reg, st.rc.Experiment.Metrics) {
		v, err := m.Compute(ctx, st.rc, st.ev)
		if err != nil {
			errs = append(errs, wrapPart("metric", m, fmt.Errorf("compute: %w", err)))
			v = schemas.Unknown("metric_error", m.Descriptor().MinEvidence)
		}
		st.metrics[metricName(m)] = v
	}
	if err := p.writeJSON(ctx, st, "metrics.json", st.metrics); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// receipt completes the probe's receipt from the evidence, merges it with
// the arm-time receipt and writes receipts.json.
func (p *Pipeline) receipt(ctx context.Context, st *runState) error {
	var errs []error
	st.receipts = []schemas.Receipt{}
	if st.layer != nil {
		later, err := st.layer.Receipts(ctx, st.rc, st.ev)
		if err != nil {
			errs = append(errs, wrapPart("probe_layer", st.layer, fmt.Errorf("receipts %s: %w", st.rc.Probe.ID, err)))
		}
		st.receipts = append(st.receipts, mergeReceipt(st, later))
	}
	if err := p.writeJSON(ctx, st, "receipts.json", st.receipts); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// cleanup destroys the workspace, clears the key slot's fault plan and
// revokes the key. It runs after every run, failed or not; Destroy is safe
// after a failed Create.
func (p *Pipeline) cleanup(ctx context.Context, st *runState) error {
	var errs []error
	if cleanup, ok := st.adapter.(schemas.AdapterCleanup); ok {
		if err := cleanup.Cleanup(ctx, st.rc); err != nil {
			errs = append(errs, wrapPart("adapter", st.adapter, fmt.Errorf("cleanup: %w", err)))
		}
	}
	if st.env != nil {
		if err := st.env.Destroy(ctx, st.rc); err != nil {
			errs = append(errs, wrapPart("environment", st.env, fmt.Errorf("destroy: %w", err)))
		}
	}
	if st.rc.Key != nil && p.reg.Fault != nil {
		if err := p.reg.Fault.ClearPlan(ctx, st.rc.Key.KeySlot); err != nil {
			errs = append(errs, &partError{part: faultPart, err: fmt.Errorf("clear plan of key slot %s: %w", st.rc.Key.KeySlot, err)})
		}
	}
	if st.rc.Key != nil && p.reg.Gateway != nil {
		if err := p.reg.Gateway.RevokeKey(ctx, st.rc.Key); err != nil {
			errs = append(errs, wrapPart("gateway", p.reg.Gateway, fmt.Errorf("revoke key: %w", err)))
		}
	}
	return errors.Join(errs...)
}

// mergeReceipt overlays the evidence-time receipt on the arm-time receipt and
// fills every missing step: assigned from the manifest, unknown for a step
// the layer declares, not_applicable for the rest. No step is left blank.
func mergeReceipt(st *runState, later *schemas.Receipt) schemas.Receipt {
	out := schemas.Receipt{ProbeID: st.rc.Probe.ID, Layer: st.rc.Probe.Layer, Steps: map[schemas.ReceiptStep]schemas.ReceiptEntry{}}
	for _, r := range []*schemas.Receipt{st.armReceipt, later} {
		if r == nil {
			continue
		}
		for k, v := range r.Steps {
			out.Steps[k] = v
		}
	}
	applies := map[schemas.ReceiptStep]bool{}
	for _, s := range st.layer.Steps() {
		applies[s] = true
	}
	for _, s := range schemas.AllReceiptSteps {
		if _, ok := out.Steps[s]; ok {
			continue
		}
		switch {
		case s == schemas.StepAssigned:
			out.Steps[s] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: "in manifest", Evidence: []string{st.rc.Manifest.Hash}}
		case applies[s]:
			out.Steps[s] = schemas.ReceiptEntry{State: schemas.StepUnknown}
		default:
			out.Steps[s] = schemas.ReceiptEntry{State: schemas.StepNotApplicable}
		}
	}
	return out
}
