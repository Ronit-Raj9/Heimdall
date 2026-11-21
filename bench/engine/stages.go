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

// Fault proxy plan modes the pipeline binds at prepare.
const (
	faultModeNone     = "none"     // pass-through, tagged with the run id
	faultModeScripted = "scripted" // every call answered by the proxy's scripted model
)

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

// prepare resolves the run's parts, creates the workspace, starts the run's
// services and mints the run's virtual key.
func (p *Pipeline) prepare(ctx context.Context, st *runState) error {
	if err := p.resolveParts(st); err != nil {
		return err
	}
	if err := p.checkTaskAgentImage(ctx, st); err != nil {
		return err
	}
	h, err := st.env.Create(ctx, st.rc)
	if err != nil {
		return wrapPart("environment", st.env, fmt.Errorf("create workspace: %w", err))
	}
	st.rc.Handle = h
	if err := p.prepareTeam(st); err != nil { // Multi-agent area: team route tokens before the tool proxy starts (team.go)
		return err
	}
	if err := p.startServices(ctx, st); err != nil {
		return err
	}
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
	if err := p.bindFaultPlan(ctx, st); err != nil {
		return err
	}
	if err := p.serveBifrostMCP(ctx, st); err != nil { // B2 MCP through Bifrost: the run's MCP clients, granted to its key (bifrostfeatures.go)
		return err
	}
	if err := p.mintTeam(ctx, st); err != nil { // Multi-agent area: one key per team member (team.go)
		return err
	}
	return p.prepareA2A(ctx, st) // B1: register the run's A2A agents in the agent gateway (a2a.go)
}

// bindFaultPlan binds an empty fault plan carrying the run id to the run's
// key slot, so the fault proxy tags every ledger line of the run even when
// nothing is injected. Without it a clean run has an empty ledger and the
// gateway completeness join fails. A gateway-layer probe binds its own plan
// when it arms, so it is skipped here. Under the scripted fault mode the
// plan is what keeps the run off every real provider, so a run that cannot
// bind it is refused rather than run.
func (p *Pipeline) bindFaultPlan(ctx context.Context, st *runState) error {
	gatewayProbe := st.rc.Probe != nil && st.rc.Probe.Layer == schemas.LayerGateway && st.rc.Probe.Apply.Agent == "" // Multi-agent area: a member-targeted probe binds only its member's slot when it arms
	mode := p.runFaultMode(st.rc)
	if mode == faultModeScripted {
		switch {
		case p.reg.Fault == nil:
			return &partError{part: faultPart, err: errors.New("scripted fault mode needs the fault proxy; refusing to run against a real provider")}
		case gatewayProbe && p.faultMode == faultModeScripted:
			return &partError{part: faultPart, err: fmt.Errorf("scripted fault mode cannot run gateway-layer probe %s", st.rc.Probe.ID)}
		}
		if err := directSlot(st.rc.Key); err != nil {
			return &partError{part: faultPart, err: err}
		}
	}
	if p.reg.Fault == nil || gatewayProbe {
		// A gateway-layer probe on a no-paid run binds its own plan, with
		// Scripted set, when it arms (probes/gateway.go).
		return nil
	}
	plan := schemas.FaultPlan{Mode: mode, RunID: st.rc.Run.RunID}
	if err := p.reg.Fault.SetPlan(ctx, st.rc.Key.KeySlot, plan); err != nil {
		return &partError{part: faultPart, err: fmt.Errorf("bind %s plan to key slot %s: %w", mode, st.rc.Key.KeySlot, err)}
	}
	return nil
}

// directSlot refuses a key on a direct slot under scripted mode: a direct
// slot holds the real provider key in the gateway and never passes the fault
// proxy, so no plan could keep its calls off the provider.
func directSlot(k *schemas.RunKey) error {
	if k != nil && k.KeySlot != "" && k.SlotAuth == "" {
		return fmt.Errorf("key slot %s is direct, not proxied: a scripted (no-paid-traffic) run cannot be answered from the scripted model; refusing to run", k.KeySlot)
	}
	return nil
}

// runFaultMode is the plan mode bound to rc's key slots: the pipeline's
// mode, or scripted for a run of a smoke or scripted_upstream experiment, so
// a run promised to cost nothing never forwards a call to a real provider.
func (p *Pipeline) runFaultMode(rc *schemas.RunContext) string {
	if p.faultMode == faultModeNone && rc != nil && (rc.Experiment.NoPaidTraffic() || (rc.Manifest != nil && rc.Manifest.Experiment.NoPaidTraffic())) {
		return faultModeScripted
	}
	return p.faultMode
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
	return p.exportScenario(ctx, st)
}

// collect runs every evidence source in registry order into one Evidence. A
// failing source does not stop the others.
func (p *Pipeline) collect(ctx context.Context, st *runState) error {
	var errs []error
	st.ev.Contract = &schemas.RunEvidenceContract{Required: []string{"gateway_requests"}, Quality: schemas.EvidenceQualityUnknown}
	if st.rc.Agent != nil && st.rc.Agent.Capabilities != nil && st.rc.Agent.Capabilities.SupportsToolEvents {
		st.ev.Contract.Required = append(st.ev.Contract.Required, "tool_execution")
	}
	if st.rc.Probe != nil && st.rc.Probe.Layer == schemas.LayerAdapter {
		st.ev.Contract.Required = append(st.ev.Contract.Required, "adapter_fault_controller")
	}
	if st.rc.Run != nil && len(st.rc.Run.Agent.CapabilityGaps) > 0 {
		st.ev.Contract.OptionalMissing = append([]string(nil), st.rc.Run.Agent.CapabilityGaps...)
	}
	for _, src := range p.reg.Evidence {
		if err := src.Collect(ctx, st.rc, st.ev); err != nil {
			errs = append(errs, wrapPart("evidence", src, fmt.Errorf("collect: %w", err)))
		}
	}
	p.collectBifrostMCP(ctx, st)                  // B2 MCP through Bifrost: Bifrost's mcp_tool_logs rows, the second witness (bifrostfeatures.go)
	if err := p.collectA2A(ctx, st); err != nil { // B1: the agent gateway's witness (a2a.go)
		errs = append(errs, err)
	}
	if st.ev.Claim != nil {
		st.rc.Run.Claim = st.ev.Claim
	}
	if err := p.finishAnswer(ctx, st); err != nil {
		errs = append(errs, err)
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
	if trace := st.ev.AgentTrace; trace != nil && trace.Quality != schemas.TraceQualityComplete {
		st.ev.Status.UnknownReasons = append(st.ev.Status.UnknownReasons, "agent trace: "+string(trace.Quality))
		st.ev.Contract.OptionalMissing = append(st.ev.Contract.OptionalMissing, "complete_agent_trace")
		st.ev.Status.Level = schemas.EvidencePartial
	}
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
			e.RequestID, e.Provider, e.ModelReq, e.ModelServed = req.ID, req.Provider, req.ModelRequested, req.ModelServed
			if declaredFallback(rc.Agent, req) {
				// A declared routing fallback chain served this row with one of
				// its models: an expected fallback, as model_fidelity treats it
				// (audit 8-B-08), not an integrity error.
				e.Type, e.Status = schemas.EventFallback, schemas.EventStatusSuccess
				e.Summary = fmt.Sprintf("declared gateway fallback %d: served %s", req.FallbackIndex, req.ModelServed)
			} else {
				e.Status = schemas.EventStatusError
				e.Summary = "unplanned gateway fallback"
			}
			events = append(events, e)
		}
	}
	return events
}

// declaredFallback reports whether a fallback row is one the agent's routing
// policy declares: the policy has a fallback chain and the row was served by
// a model of the policy (or the served model is unknown). It is the same
// rule the routed model_fidelity gate applies (gates/modelfidelityrouting.go).
//
// Without a routing policy the older gateway.fallbacks list decides, as in
// the unrouted model_fidelity rule (gates/modelfidelity.go, QC2-12-06): the
// row was served by one of the declared fallback models.
func declaredFallback(agent *schemas.AgentConfig, req schemas.GatewayRequest) bool {
	same := func(m string) bool {
		return m == req.ModelServed || strings.HasSuffix(req.ModelServed, "/"+m) || strings.HasSuffix(m, "/"+req.ModelServed)
	}
	pol := agent.Gateway.Routing
	if pol == nil {
		if req.ModelServed == "" {
			return false
		}
		return slices.ContainsFunc(agent.Gateway.Fallbacks, same)
	}
	if len(pol.Fallbacks) == 0 {
		return false
	}
	if req.ModelServed == "" {
		return true
	}
	return slices.ContainsFunc(pol.EffectiveModels(agent.Model), same)
}

func availableEvidence(ev *schemas.Evidence) []string {
	var out []string
	if ev.Requests != nil {
		out = append(out, "gateway_requests")
	}
	if len(ev.ShimLog) > 0 {
		out = append(out, "tool_execution")
	}
	for _, event := range ev.Events {
		if event.Source == schemas.SourceAgentStream && event.Type != schemas.EventClaim {
			out = append(out, "agent_events")
			break
		}
	}
	if ev.AgentTrace != nil && ev.AgentTrace.Quality != schemas.TraceQualityUnavailable {
		out = append(out, "agent_trace")
	}
	if ev.AdapterFault != nil && ev.AdapterFault.Complete {
		out = append(out, "adapter_fault_controller")
	}
	return out
}

// grade runs the grader on the exported diff. A scenario run whose state,
// answer or artifact files were not exported is not graded: its grade is
// invalid_evaluator with the reason (scenarioGradeProblem). An artifact task
// whose required artifacts are missing fails without running the verifier
// (missingArtifactsReason). A grade that is not a clean-room diff grade
// carries its mode.
func (p *Pipeline) grade(ctx context.Context, st *runState) error {
	if st.ev.Diff == nil {
		return errors.New("no exported diff to grade")
	}
	mode := gradeMode(st.rc)
	if why := scenarioGradeProblem(st.rc, st.ev, st.artifactErr); why != "" {
		st.ev.Grade = &schemas.Grade{InvalidEvaluator: true, Reason: why, Mode: mode, SuiteVersion: st.rc.Suite.Version,
			GraderVersion: st.grader.GetVersion(), DiffSHA256: st.ev.Diff.SHA256}
		return nil
	}
	if why := missingArtifactsReason(st.rc, st.ev); why != "" {
		st.ev.Grade = &schemas.Grade{Passed: false, Reason: why, Mode: mode, SuiteVersion: st.rc.Suite.Version,
			GraderVersion: st.grader.GetVersion(), DiffSHA256: st.ev.Diff.SHA256}
		return nil
	}
	g, err := st.grader.Grade(ctx, st.rc, st.ev.Diff)
	if err != nil {
		return wrapPart("grader", st.grader, fmt.Errorf("grade: %w", err))
	}
	if g == nil {
		return wrapPart("grader", st.grader, errors.New("grade returned no grade"))
	}
	if g.Mode == "" {
		g.Mode = mode
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
			// Recorded so a metric over gate results can tell a gate that
			// never ran from one that ran clean (audit 1-C-09).
			st.ev.GateErrors = append(st.ev.GateErrors, schemas.GateError{Gate: g.GetName(), Effect: g.Effect(), Error: err.Error()})
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
// that fails is recorded as unknown, never zero. Then every selected metric
// that is a schemas.DerivedMetric adds its run-time named values (reward
// dimensions), each only under a name no registered metric uses and no
// earlier value took, so a derived value never replaces a registered one.
func (p *Pipeline) measure(ctx context.Context, st *runState) error {
	var errs []error
	st.metrics = map[string]schemas.Value{}
	selected := selectMetrics(p.reg, st.rc.Experiment.Metrics)
	for _, m := range append(selected, accountingMetrics(p.reg, st.rc.Experiment, selected)...) {
		v, err := m.Compute(ctx, st.rc, st.ev)
		if err != nil {
			errs = append(errs, wrapPart("metric", m, fmt.Errorf("compute: %w", err)))
			v = schemas.Unknown("metric_error", m.Descriptor().MinEvidence)
		}
		st.metrics[metricName(m)] = v
	}
	errs = append(errs, p.measureDerived(ctx, st, selected)...)
	if err := p.writeJSON(ctx, st, "metrics.json", st.metrics); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// accountingMetrics returns the registered cost metrics the spend caps read
// that the experiment's display selection left out. Every cap (the
// experiment's budget.total_usd, a runner's own daily cap, a schedule's daily
// cap) needs the measured cost, whatever the experiment's budget, so choosing
// a metric subset never turns a measurable run into an unknown one charged at
// the per-run estimate (QC2-07-06). The values land in metrics.json, so
// resume sees them.
func accountingMetrics(reg *schemas.Registry, e *schemas.Experiment, selected []schemas.Metric) []schemas.Metric {
	if reg == nil || e == nil {
		return nil
	}
	have := map[string]bool{}
	for _, m := range selected {
		have[metricName(m)] = true
	}
	var out []schemas.Metric
	for _, m := range reg.Metrics {
		n := metricName(m)
		if (n == costMetric || n == simulatorCostMetric) && !have[n] {
			have[n] = true
			out = append(out, m)
		}
	}
	return out
}

// measureDerived adds the values of every selected DerivedMetric to
// st.metrics, in sorted name order, skipping a name that is taken: by a
// registered metric (selected or not) or by an earlier value.
func (p *Pipeline) measureDerived(ctx context.Context, st *runState, selected []schemas.Metric) []error {
	reserved := map[string]bool{}
	for _, m := range p.reg.Metrics {
		reserved[m.GetName()], reserved[metricName(m)] = true, true
	}
	var errs []error
	for _, m := range selected {
		d, ok := m.(schemas.DerivedMetric)
		if !ok {
			continue
		}
		vals, err := d.ComputeDerived(ctx, st.rc, st.ev)
		if err != nil {
			errs = append(errs, wrapPart("metric", m, fmt.Errorf("compute derived: %w", err)))
			continue
		}
		for _, name := range sortedKeys(vals) {
			if _, taken := st.metrics[name]; taken || reserved[name] {
				p.logger.Warn("derived metric name is taken; value dropped", "run", st.rc.Run.RunID, "metric", m.GetName(), "name", name)
				continue
			}
			st.metrics[name] = vals[name]
		}
	}
	return errs
}

// receipt completes the probe's receipt from the evidence, merges it with
// the arm-time receipt and writes receipts.json.
func (p *Pipeline) receipt(ctx context.Context, st *runState) error {
	defer clearGatewayRequestBodies(st.ev)
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

// clearGatewayRequestBodies releases transient gateway payloads after probe
// receipts consume them. trimEvidence remains the persistence boundary.
func clearGatewayRequestBodies(ev *schemas.Evidence) {
	if ev == nil || ev.Requests == nil {
		return
	}
	for i := range ev.Requests.Requests {
		ev.Requests.Requests[i].Raw = nil
	}
}

// cleanup destroys the workspace, revokes the key and then clears the key
// slots' fault plans. It runs after every run, failed or not; Destroy is safe
// after a failed Create.
func (p *Pipeline) cleanup(ctx context.Context, st *runState) error {
	var errs []error
	if err := p.removeBifrostMCP(ctx, st); err != nil { // B2 MCP through Bifrost: delete the run's MCP clients first (bifrostfeatures.go)
		errs = append(errs, err)
	}
	if p.reg.AdapterFaults != nil {
		if err := p.reg.AdapterFaults.Cleanup(ctx, st.rc); err != nil {
			errs = append(errs, &partError{part: "adapter_faults", err: fmt.Errorf("cleanup: %w", err)})
		}
	}
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
	errs = append(errs, p.cleanupA2A(ctx, st)) // B1: the run's agent gateway registrations (a2a.go)
	revoked := true
	if st.rc.Key != nil && p.reg.Gateway != nil {
		if err := p.reg.Gateway.RevokeKey(ctx, st.rc.Key); err != nil {
			revoked = false
			errs = append(errs, wrapPart("gateway", p.reg.Gateway, fmt.Errorf("revoke key: %w", err)))
		}
	}
	// QC2-13-06: the key is revoked before its slots' plans are cleared, so
	// an agent call still in flight never meets a slot without its plan. A
	// no-paid run whose revocation failed keeps its scripted plans bound:
	// the live key must never reach the real upstream.
	if !revoked && st.rc.NoPaidTraffic {
		p.logger.Warn("key revocation failed; keeping the no-paid scripted plans bound", "run_id", st.rc.Run.RunID)
		return errors.Join(errs...)
	}
	errs = append(errs, p.clearTeamPlans(ctx, st)) // Multi-agent area: team member slots (team.go)
	if st.rc.Key != nil && p.reg.Fault != nil {
		if err := p.reg.Fault.ClearPlan(ctx, st.rc.Key.KeySlot); err != nil {
			errs = append(errs, &partError{part: faultPart, err: fmt.Errorf("clear plan of key slot %s: %w", st.rc.Key.KeySlot, err)})
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
