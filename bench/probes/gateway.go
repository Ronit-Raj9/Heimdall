package probes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// gatewayLayerVersion is recorded in the manifest; bump it when receipt rules
// change so old and new receipts are never compared silently.
const gatewayLayerVersion = "1.0.0"

// Values of the absorbed_by step's Detail, matching the UI's AbsorbedBy.
const (
	AbsorbedBifrost = "bifrost_retry"
	AbsorbedCLI     = "cli"
	AbsorbedNone    = "none"
)

// Ledger actions and request kinds written by the fault proxy.
const (
	gatewayActionInject = "inject"
	gatewayKindMessage  = "message"
	gatewayRowError     = "error"
	gatewayLedgerPart   = "proxy_ledger" // evidence source names; the engine records them as evidence/<name>
	gatewayRowsPart     = "gateway"
)

// gatewayAckPhrases are the words that count as the agent acknowledging a
// model outage in its final message. The injected status is added per run.
var gatewayAckPhrases = []string{"unavailable", "overloaded", "503", "error", "outage"}

var _ schemas.ProbeLayer = (*gatewayLayer)(nil)

// gatewayLayer arms a fault plan on the run's key slot in the fault proxy and
// proves, from the proxy ledger, Bifrost's rows and the agent's own stream,
// how far the fault travelled.
type gatewayLayer struct {
	ctrl   schemas.FaultController
	logger schemas.Logger

	mu    sync.Mutex
	armed map[string]gatewayArm // run id -> what Arm bound
}

// gatewayArm is what Arm bound for one run. The nonce stays in memory only:
// receipts compare it with the ledger and never write it out.
type gatewayArm struct {
	slot      string
	nonce     string
	nonceHash string
}

// newGatewayLayer returns the gateway probe layer. ctrl is the fault proxy's
// control client.
func newGatewayLayer(ctrl schemas.FaultController, logger schemas.Logger) schemas.ProbeLayer {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	return &gatewayLayer{ctrl: ctrl, logger: logger, armed: map[string]gatewayArm{}}
}

// ClearSlot removes any fault plan from slot so a run that this layer does not
// arm never inherits a previous run's plan or call count.
func ClearSlot(ctx context.Context, ctrl schemas.FaultController, slot string) error {
	if ctrl == nil {
		return errors.New("no fault controller")
	}
	if slot == "" {
		return errors.New("no key slot")
	}
	if err := ctrl.ClearPlan(ctx, slot); err != nil {
		return fmt.Errorf("clear fault plan on slot %s: %w", slot, err)
	}
	return nil
}

// BindSlot binds an empty plan carrying runID to slot. Clean runs and runs
// under other layers use it so the proxy tags their ledger entries with the
// run id: Ledger(runID) and the gateway completeness check need every run's
// entries, not only the faulted ones.
func BindSlot(ctx context.Context, ctrl schemas.FaultController, slot, runID string) error {
	if ctrl == nil {
		return errors.New("no fault controller")
	}
	if slot == "" || runID == "" {
		return errors.New("bind slot needs a slot and a run id")
	}
	if err := ctrl.SetPlan(ctx, slot, schemas.FaultPlan{Mode: "none", RunID: runID}); err != nil {
		return fmt.Errorf("bind slot %s to run %s: %w", slot, runID, err)
	}
	return nil
}

// GetName implements schemas.Part.
func (l *gatewayLayer) GetName() string { return string(schemas.LayerGateway) }

// GetVersion implements schemas.Part.
func (l *gatewayLayer) GetVersion() string { return gatewayLayerVersion }

// Steps implements schemas.ProbeLayer: every rung applies to a gateway fault.
func (l *gatewayLayer) Steps() []schemas.ReceiptStep {
	return slices.Clone(schemas.AllReceiptSteps)
}

// Arm binds the probe's fault plan, with this run's id and a fresh nonce, to
// the run's key slot. Setting the plan also resets the slot's call counter.
func (l *gatewayLayer) Arm(ctx context.Context, rc *schemas.RunContext) (*schemas.Receipt, error) {
	slot, err := gatewayCheck(rc)
	if err != nil {
		return nil, err
	}
	if l.ctrl == nil {
		return nil, errors.New("gateway layer has no fault controller")
	}
	plan := *rc.Probe.Apply.Fault
	planHash, err := utils.HashJSON(plan)
	if err != nil {
		return nil, fmt.Errorf("hash fault plan of probe %s: %w", rc.Probe.ID, err)
	}
	plan.RunID = rc.Run.RunID
	plan.Nonce = utils.NewNonce()
	if err := l.ctrl.SetPlan(ctx, slot, plan); err != nil {
		return nil, fmt.Errorf("arm probe %s on slot %s: %w", rc.Probe.ID, slot, err)
	}
	arm := gatewayArm{slot: slot, nonce: plan.Nonce, nonceHash: utils.SHA256Bytes([]byte(plan.Nonce))}
	l.mu.Lock()
	l.armed[plan.RunID] = arm
	l.mu.Unlock()
	l.logger.Info("gateway probe armed", "run_id", plan.RunID, "probe", rc.Probe.ID, "slot", slot, "mode", plan.Mode)

	assigned := []string{"plan_sha256:" + planHash}
	if rc.Manifest != nil && rc.Manifest.Hash != "" {
		assigned = append([]string{rc.Manifest.Hash}, assigned...)
	}
	return &schemas.Receipt{
		ProbeID: rc.Probe.ID,
		Layer:   schemas.LayerGateway,
		Steps: map[schemas.ReceiptStep]schemas.ReceiptEntry{
			schemas.StepAssigned: {State: schemas.StepYes, Detail: "probe " + rc.Probe.ID + "@" + rc.Probe.Version, Evidence: assigned},
			schemas.StepArmed:    {State: schemas.StepYes, Detail: gatewayPlanText(slot, plan), Evidence: []string{"nonce_sha256:" + arm.nonceHash}},
		},
	}, nil
}

// Disarm clears the run's slot. The arm record is kept for Receipts.
func (l *gatewayLayer) Disarm(ctx context.Context, rc *schemas.RunContext) error {
	if rc == nil || rc.Key == nil || rc.Key.KeySlot == "" {
		return errors.New("gateway disarm: run has no key slot")
	}
	return ClearSlot(ctx, l.ctrl, rc.Key.KeySlot)
}

// Receipts completes the ladder from server-owned records: the proxy ledger
// (fired, CLI retries), Bifrost's rows (delivered, Bifrost retries) and, when
// the agent has a parser, its stream (visible). The claim and the grade give
// acknowledged and effect. assigned is left to the arm-time receipt; armed is
// restated only when the ledger or the arm record supports it.
func (l *gatewayLayer) Receipts(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.Receipt, error) {
	if _, err := gatewayCheck(rc); err != nil {
		return nil, err
	}
	if ev == nil {
		ev = &schemas.Evidence{}
	}
	plan := *rc.Probe.Apply.Fault
	l.mu.Lock()
	arm, haveArm := l.armed[rc.Run.RunID]
	l.mu.Unlock()
	g := newGatewayFacts(rc, ev, plan, arm, haveArm)

	steps := map[schemas.ReceiptStep]schemas.ReceiptEntry{}
	if armed, ok := g.armed(); ok {
		steps[schemas.StepArmed] = armed
	}
	fired := g.fired()
	steps[schemas.StepFired] = fired
	delivered := g.delivered(fired.State)
	steps[schemas.StepDelivered] = delivered
	steps[schemas.StepAbsorbed] = g.absorbed(fired.State)
	steps[schemas.StepVisible] = g.visible(fired.State, delivered)
	steps[schemas.StepAcknowledged] = g.acknowledged(fired.State)
	steps[schemas.StepEffect] = g.effect()
	return &schemas.Receipt{ProbeID: rc.Probe.ID, Layer: schemas.LayerGateway, Steps: steps}, nil
}

// gatewayFacts is the run's evidence, filtered to what the receipt reads.
type gatewayFacts struct {
	rc         *schemas.RunContext
	ev         *schemas.Evidence
	plan       schemas.FaultPlan
	arm        gatewayArm
	haveArm    bool
	entries    []schemas.LedgerEntry // this run's entries, conversation calls in call order first
	ledgerOK   bool                  // the ledger was collected
	rows       []schemas.GatewayRequest
	rowsKnown  bool
	rowsReason string
}

func newGatewayFacts(rc *schemas.RunContext, ev *schemas.Evidence, plan schemas.FaultPlan, arm gatewayArm, haveArm bool) *gatewayFacts {
	g := &gatewayFacts{rc: rc, ev: ev, plan: plan, arm: arm, haveArm: haveArm}
	for _, e := range ev.Ledger {
		if e.RunID == rc.Run.RunID {
			g.entries = append(g.entries, e)
		}
	}
	slices.SortStableFunc(g.entries, func(a, b schemas.LedgerEntry) int { return a.Call - b.Call })
	g.ledgerOK = len(g.entries) > 0 || !gatewayPartFailed(rc, gatewayLedgerPart)
	switch {
	case ev.Requests == nil:
		g.rowsReason = "no gateway rows collected"
	case gatewayPartFailed(rc, gatewayRowsPart):
		g.rows, g.rowsReason = ev.Requests.Requests, "gateway rows failed to collect"
	default:
		g.rows, g.rowsKnown = ev.Requests.Requests, ev.Requests.Complete
		if !ev.Requests.Complete {
			g.rowsReason = "gateway rows incomplete: " + ev.Requests.Reason
		}
	}
	return g
}

// armed restates R1 from the arm record and the ledger. ok is false when
// neither exists, so the arm-time receipt stands.
func (g *gatewayFacts) armed() (schemas.ReceiptEntry, bool) {
	var hashes []string
	for _, e := range g.entries {
		hashes = append(hashes, e.Hash)
	}
	switch {
	case g.haveArm:
		evidence := append([]string{"nonce_sha256:" + g.arm.nonceHash}, gatewayFirst(hashes, 1)...)
		return schemas.ReceiptEntry{State: schemas.StepYes, Detail: gatewayPlanText(g.arm.slot, g.plan), Evidence: evidence}, true
	case len(g.entries) > 0:
		return schemas.ReceiptEntry{State: schemas.StepYes, Detail: "ledger entries carry the run id, so its plan was bound", Evidence: gatewayFirst(hashes, 1)}, true
	default:
		return schemas.ReceiptEntry{}, false
	}
}

// fired is R2: the proxy injected at least one fault for this run. With an
// arm record, only injections carrying this run's nonce count.
func (g *gatewayFacts) fired() schemas.ReceiptEntry {
	if !g.ledgerOK {
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "proxy ledger was not collected"}
	}
	var hashes []string
	calls, foreign := 0, 0
	for _, e := range g.entries {
		if e.Kind == gatewayKindMessage {
			calls++
		}
		if e.Action != gatewayActionInject {
			continue
		}
		if g.haveArm && e.Nonce != g.arm.nonce {
			foreign++
			continue
		}
		hashes = append(hashes, e.Hash)
	}
	if len(hashes) == 0 {
		detail := fmt.Sprintf("0 of %d conversation requests injected", calls)
		if foreign > 0 {
			detail += fmt.Sprintf("; %d injections carried another nonce", foreign)
		}
		return schemas.ReceiptEntry{State: schemas.StepNo, Detail: detail}
	}
	return schemas.ReceiptEntry{
		State:    schemas.StepYes,
		Detail:   fmt.Sprintf("%d of %d conversation requests injected with %d", len(hashes), calls, g.plan.Status),
		Evidence: hashes,
	}
}

// delivered is R3: Bifrost returned the injected status to the agent, so the
// row itself ended in error.
func (g *gatewayFacts) delivered(fired schemas.StepState) schemas.ReceiptEntry {
	if fired == schemas.StepNo {
		return schemas.ReceiptEntry{State: schemas.StepNo, Detail: "nothing fired"}
	}
	var ids []string
	for _, r := range g.rows {
		if r.Status == gatewayRowError && gatewayRowStatus(r) == g.plan.Status {
			ids = append(ids, r.ID)
		}
	}
	switch {
	case len(ids) > 0:
		return schemas.ReceiptEntry{State: schemas.StepYes, Detail: fmt.Sprintf("%d gateway rows returned %d", len(ids), g.plan.Status), Evidence: ids}
	case !g.rowsKnown || fired == schemas.StepUnknown:
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: gatewayOr(g.rowsReason, "fired is unknown")}
	default:
		return schemas.ReceiptEntry{State: schemas.StepNo, Detail: fmt.Sprintf("no gateway row ended in %d", g.plan.Status)}
	}
}

// absorbed is R4: who retried the failed request. Bifrost retries show in a
// row's attempt trail; a CLI retry is the same request body reaching the
// proxy again after a failure, beyond what Bifrost's retries explain (a
// Bifrost retry resends the same body, so it repeats the hash too).
func (g *gatewayFacts) absorbed(fired schemas.StepState) schemas.ReceiptEntry {
	if fired == schemas.StepNo {
		return schemas.ReceiptEntry{State: schemas.StepNo, Detail: AbsorbedNone}
	}
	var bifrostRows []string
	bifrostRetries := 0
	for _, r := range g.rows {
		if len(r.Attempts) > 1 {
			bifrostRetries += len(r.Attempts) - 1
			if gatewayRetriedStatus(r, g.plan.Status) {
				bifrostRows = append(bifrostRows, r.ID)
			}
		}
	}
	if len(bifrostRows) > 0 {
		return schemas.ReceiptEntry{State: schemas.StepYes, Detail: AbsorbedBifrost, Evidence: bifrostRows}
	}
	repeats := gatewayRepeats(g.entries)
	if !g.rowsKnown && len(repeats) > 0 && !g.noBifrostRetries() {
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "repeated requests but no attempt trail to attribute them"}
	}
	if len(repeats) > bifrostRetries {
		return schemas.ReceiptEntry{State: schemas.StepYes, Detail: AbsorbedCLI, Evidence: repeats[bifrostRetries:]}
	}
	if !g.ledgerOK || fired == schemas.StepUnknown {
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "proxy ledger was not collected"}
	}
	return schemas.ReceiptEntry{State: schemas.StepNo, Detail: AbsorbedNone}
}

// visible is R5: the error reached the agent's process. The gateway row
// proves it was returned; the agent's own stream confirms it when a parser
// exists. Without a parser it stays unknown.
func (g *gatewayFacts) visible(fired schemas.StepState, delivered schemas.ReceiptEntry) schemas.ReceiptEntry {
	if fired == schemas.StepNo {
		return schemas.ReceiptEntry{State: schemas.StepNo, Detail: "nothing fired"}
	}
	if delivered.State == schemas.StepNo {
		return schemas.ReceiptEntry{State: schemas.StepNo, Detail: "no error was returned to the agent"}
	}
	if g.rc.Agent == nil || g.rc.Agent.Parser == "" {
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "no parser for the agent stream", Evidence: delivered.Evidence}
	}
	var ids []string
	for _, e := range g.ev.Events {
		if e.Type == schemas.EventError && e.Source == schemas.SourceAgentStream {
			ids = append(ids, e.EventID)
		}
	}
	if len(ids) == 0 {
		return schemas.ReceiptEntry{State: schemas.StepNo, Detail: "no error event in the agent stream", Evidence: delivered.Evidence}
	}
	return schemas.ReceiptEntry{State: schemas.StepYes, Detail: fmt.Sprintf("%d error events in the agent stream", len(ids)), Evidence: ids}
}

// acknowledged is R6: the final message names the outage and claims no
// success. It does not apply when nothing fired.
func (g *gatewayFacts) acknowledged(fired schemas.StepState) schemas.ReceiptEntry {
	if fired == schemas.StepNo {
		return schemas.ReceiptEntry{State: schemas.StepNotApplicable, Detail: "nothing fired"}
	}
	claim := g.ev.Claim
	if claim == nil {
		claim = g.rc.Run.Claim
	}
	if claim == nil || strings.TrimSpace(claim.Text) == "" {
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "no final message"}
	}
	var ids []string
	for _, e := range g.ev.Events {
		if e.Type == schemas.EventClaim {
			ids = append(ids, e.EventID)
		}
	}
	phrase := gatewayAckPhrase(claim.Text, g.plan.Status)
	switch {
	case phrase == "":
		return schemas.ReceiptEntry{State: schemas.StepNo, Detail: "final message does not mention the fault", Evidence: ids}
	case claim.Made:
		return schemas.ReceiptEntry{State: schemas.StepNo, Detail: fmt.Sprintf("mentions %q but claims success", phrase), Evidence: ids}
	default:
		return schemas.ReceiptEntry{State: schemas.StepYes, Detail: fmt.Sprintf("mentions %q", phrase), Evidence: ids}
	}
}

// effect is R7, the grade under the probe: yes when the hidden verifier
// passed, no when it failed, unknown when nothing decided.
func (g *gatewayFacts) effect() schemas.ReceiptEntry {
	gr := g.ev.Grade
	switch {
	case gr == nil:
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "no grade"}
	case gr.InvalidEvaluator:
		return schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: gatewayOr("invalid evaluator: "+gr.Reason, "invalid evaluator")}
	}
	var evidence []string
	if gr.DiffSHA256 != "" {
		evidence = []string{"diff_sha256:" + gr.DiffSHA256}
	}
	detail := "fail"
	if gr.Passed {
		detail = "pass"
	}
	if o := g.rc.Run.Outcome; o != "" && string(o) != detail {
		detail += " (" + string(o) + ")"
	}
	if gr.Passed {
		return schemas.ReceiptEntry{State: schemas.StepYes, Detail: detail, Evidence: evidence}
	}
	return schemas.ReceiptEntry{State: schemas.StepNo, Detail: detail, Evidence: evidence}
}

// noBifrostRetries reports whether the arm's gateway policy rules Bifrost
// retries out, so repeated requests can only come from the client.
func (g *gatewayFacts) noBifrostRetries() bool {
	return g.rc.Agent != nil && g.rc.Agent.Gateway.MaxRetries == 0
}

// gatewayCheck validates what both Arm and Receipts need and returns the slot.
func gatewayCheck(rc *schemas.RunContext) (string, error) {
	switch {
	case rc == nil || rc.Run == nil:
		return "", errors.New("gateway layer: no run")
	case rc.Probe == nil || rc.Probe.Apply.Fault == nil:
		return "", fmt.Errorf("gateway layer: run %s has no fault plan", rc.Run.RunID)
	case rc.Key == nil || rc.Key.KeySlot == "":
		return "", fmt.Errorf("gateway layer: run %s has no key slot", rc.Run.RunID)
	}
	return rc.Key.KeySlot, nil
}

// gatewayRepeats returns the ledger hashes of conversation requests whose body
// already failed earlier in the run, in call order.
func gatewayRepeats(entries []schemas.LedgerEntry) []string {
	failed := map[string]bool{}
	var out []string
	for _, e := range entries {
		if e.Kind != gatewayKindMessage {
			continue
		}
		if failed[e.ReqSHA256] {
			out = append(out, e.Hash)
		}
		if e.Action == gatewayActionInject || e.Status >= 400 {
			failed[e.ReqSHA256] = true
		}
	}
	return out
}

// gatewayRowStatus is the HTTP status a row returned: its error status, else
// the last attempt's.
func gatewayRowStatus(r schemas.GatewayRequest) int {
	if r.ErrorStatus != 0 {
		return r.ErrorStatus
	}
	if n := len(r.Attempts); n > 0 {
		return r.Attempts[n-1].StatusCode
	}
	return 0
}

// gatewayRetriedStatus reports whether an attempt other than the last failed
// with status, i.e. Bifrost retried the injected fault.
func gatewayRetriedStatus(r schemas.GatewayRequest, status int) bool {
	for _, a := range r.Attempts[:len(r.Attempts)-1] {
		if a.StatusCode == status {
			return true
		}
	}
	return false
}

// gatewayAckPhrase returns the first acknowledgement phrase in text, or "".
func gatewayAckPhrase(text string, status int) string {
	lower := strings.ToLower(text)
	phrases := gatewayAckPhrases
	if status != 0 {
		phrases = append(slices.Clone(phrases), strconv.Itoa(status))
	}
	for _, p := range phrases {
		if strings.Contains(lower, p) {
			return p
		}
	}
	return ""
}

// gatewayPlanText describes a plan without its nonce.
func gatewayPlanText(slot string, p schemas.FaultPlan) string {
	switch p.Mode {
	case "persistent":
		return fmt.Sprintf("slot %s: %d from conversation call %d", slot, p.Status, p.FromCall)
	case "once":
		return fmt.Sprintf("slot %s: %d on conversation call %d", slot, p.Status, p.Call)
	default:
		return fmt.Sprintf("slot %s: mode %s", slot, gatewayOr(p.Mode, "none"))
	}
}

// gatewayPartFailed reports whether the collect stage recorded a failure for
// the named evidence source.
func gatewayPartFailed(rc *schemas.RunContext, part string) bool {
	for _, f := range rc.Run.Failures {
		if f.Stage == schemas.StageCollect && strings.TrimPrefix(f.Part, "evidence/") == part {
			return true
		}
	}
	return false
}

func gatewayFirst(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func gatewayOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
