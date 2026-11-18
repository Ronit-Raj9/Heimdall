package probes

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// gatewayFakeCtrl records control calls and can fail them.
type gatewayFakeCtrl struct {
	plans   map[string]schemas.FaultPlan
	cleared []string
	err     error
}

func (f *gatewayFakeCtrl) SetPlan(_ context.Context, slot string, p schemas.FaultPlan) error {
	if f.err != nil {
		return f.err
	}
	if f.plans == nil {
		f.plans = map[string]schemas.FaultPlan{}
	}
	f.plans[slot] = p
	return nil
}

func (f *gatewayFakeCtrl) ClearPlan(_ context.Context, slot string) error {
	if f.err != nil {
		return f.err
	}
	f.cleared = append(f.cleared, slot)
	delete(f.plans, slot)
	return nil
}

func (f *gatewayFakeCtrl) Ledger(context.Context, string) ([]schemas.LedgerEntry, error) {
	return nil, errors.New("not used")
}

// gatewayRC builds a run context for the gateway layer.
func gatewayRC(runID string, plan schemas.FaultPlan, parser string, maxRetries int) *schemas.RunContext {
	p := plan
	return &schemas.RunContext{
		Run:      &schemas.Run{RunID: runID},
		Probe:    &schemas.ProbeSpec{ID: "model-outage", Version: "1.0.0", Layer: schemas.LayerGateway, Apply: schemas.ProbeApply{Fault: &p}},
		Key:      &schemas.RunKey{KeySlot: "fp-slot-1"},
		Agent:    &schemas.AgentConfig{Parser: parser, Gateway: schemas.GatewayPolicy{Provider: "anthropic", MaxRetries: maxRetries}},
		Manifest: &schemas.Manifest{Hash: "manifest-hash"},
	}
}

var gatewayOutage = schemas.FaultPlan{Mode: "persistent", FromCall: 3, Status: 503, RetryAfterS: 2}

func TestGatewayArmDisarm(t *testing.T) {
	ctrl := &gatewayFakeCtrl{}
	layer := newGatewayLayer(ctrl, nil)
	rc := gatewayRC("r_1", gatewayOutage, "claude-code", 0)
	rec, err := layer.Arm(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := ctrl.plans["fp-slot-1"]
	if !ok {
		t.Fatal("no plan set on the run's slot")
	}
	if got.RunID != "r_1" || !strings.HasPrefix(got.Nonce, "nonce_") || got.Mode != "persistent" ||
		got.FromCall != 3 || got.Status != 503 || got.RetryAfterS != 2 {
		t.Fatalf("plan = %+v", got)
	}
	if rc.Probe.Apply.Fault.Nonce != "" || rc.Probe.Apply.Fault.RunID != "" {
		t.Fatal("Arm mutated the probe spec")
	}
	if rec.Layer != schemas.LayerGateway || rec.ProbeID != "model-outage" {
		t.Errorf("receipt header = %+v", rec)
	}
	armed := rec.Steps[schemas.StepArmed]
	if rec.Steps[schemas.StepAssigned].State != schemas.StepYes || armed.State != schemas.StepYes {
		t.Fatalf("steps = %+v", rec.Steps)
	}
	if want := "nonce_sha256:" + utils.SHA256Bytes([]byte(got.Nonce)); armed.Evidence[0] != want {
		t.Errorf("armed evidence %v, want %s", armed.Evidence, want)
	}
	gatewayNoNonce(t, rec, got.Nonce)

	first := got.Nonce
	if _, err := layer.Arm(context.Background(), gatewayRC("r_2", gatewayOutage, "", 0)); err != nil {
		t.Fatal(err)
	}
	if ctrl.plans["fp-slot-1"].Nonce == first {
		t.Fatal("nonce reused across runs")
	}
	if err := layer.Disarm(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ctrl.cleared, []string{"fp-slot-1"}) {
		t.Fatalf("cleared = %v", ctrl.cleared)
	}
}

func TestGatewayArmErrors(t *testing.T) {
	cases := []struct {
		name   string
		ctrl   schemas.FaultController
		mutate func(rc *schemas.RunContext)
	}{
		{"no probe", &gatewayFakeCtrl{}, func(rc *schemas.RunContext) { rc.Probe = nil }},
		{"no fault block", &gatewayFakeCtrl{}, func(rc *schemas.RunContext) { rc.Probe.Apply.Fault = nil }},
		{"no key", &gatewayFakeCtrl{}, func(rc *schemas.RunContext) { rc.Key = nil }},
		{"empty slot", &gatewayFakeCtrl{}, func(rc *schemas.RunContext) { rc.Key.KeySlot = "" }},
		{"no run", &gatewayFakeCtrl{}, func(rc *schemas.RunContext) { rc.Run = nil }},
		{"controller fails", &gatewayFakeCtrl{err: errors.New("proxy down")}, func(*schemas.RunContext) {}},
		{"no controller", nil, func(*schemas.RunContext) {}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := gatewayRC("r_1", gatewayOutage, "", 0)
			tc.mutate(rc)
			if _, err := newGatewayLayer(tc.ctrl, schemas.NoopLogger{}).Arm(context.Background(), rc); err == nil {
				t.Fatal("Arm succeeded")
			}
		})
	}
}

func TestGatewaySlotHelpers(t *testing.T) {
	ctx := context.Background()
	ctrl := &gatewayFakeCtrl{}
	if err := BindSlot(ctx, ctrl, "fp-slot-2", "r_9"); err != nil {
		t.Fatal(err)
	}
	if p := ctrl.plans["fp-slot-2"]; p.Mode != "none" || p.RunID != "r_9" || p.Nonce != "" || p.Status != 0 {
		t.Fatalf("bound plan = %+v", p)
	}
	if err := ClearSlot(ctx, ctrl, "fp-slot-2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ctrl.plans["fp-slot-2"]; ok || len(ctrl.cleared) != 1 {
		t.Fatal("slot not cleared")
	}
	for name, err := range map[string]error{
		"clear without controller": ClearSlot(ctx, nil, "s"),
		"clear without slot":       ClearSlot(ctx, ctrl, ""),
		"bind without run":         BindSlot(ctx, ctrl, "s", ""),
		"clear failing":            ClearSlot(ctx, &gatewayFakeCtrl{err: errors.New("x")}, "s"),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestGatewaySteps(t *testing.T) {
	layer := newGatewayLayer(&gatewayFakeCtrl{}, nil)
	steps := layer.Steps()
	if !reflect.DeepEqual(steps, schemas.AllReceiptSteps) || layer.GetName() != "gateway" || layer.GetVersion() == "" {
		t.Fatalf("steps %v name %s", steps, layer.GetName())
	}
	steps[0] = "mutated"
	if schemas.AllReceiptSteps[0] != schemas.StepAssigned {
		t.Fatal("Steps returned the shared slice")
	}
}

// gatewayEntry is a ledger line for run r_1.
func gatewayEntry(call int, action string, status int, body, nonce string) schemas.LedgerEntry {
	return schemas.LedgerEntry{
		Slot: "fp-slot-1", RunID: "r_1", Call: call, Kind: "message", Path: "/v1/messages",
		Action: action, Status: status, ReqSHA256: body, Nonce: nonce, Hash: "h" + string(rune('0'+call)) + action,
	}
}

func gatewayRow(id, status string, errStatus int, attempts ...int) schemas.GatewayRequest {
	r := schemas.GatewayRequest{ID: id, Status: status, Kind: "message", ErrorStatus: errStatus}
	for i, c := range attempts {
		r.Attempts = append(r.Attempts, schemas.Attempt{N: i + 1, StatusCode: c})
	}
	return r
}

type gatewayWant struct {
	state    schemas.StepState
	detail   string // substring
	evidence []string
}

func TestGatewayReceipts(t *testing.T) {
	cases := []struct {
		name    string
		plan    schemas.FaultPlan
		parser  string
		retries int
		arm     bool // arm first; "N" in a ledger nonce is replaced by the armed nonce
		ledger  []schemas.LedgerEntry
		rows    *schemas.RequestLog
		events  []schemas.Event
		claim   *schemas.Claim
		grade   *schemas.Grade
		fail    []schemas.PartFailure
		want    map[schemas.ReceiptStep]gatewayWant
	}{
		{
			name: "outage delivered, seen and acknowledged", plan: gatewayOutage, parser: "claude-code", arm: true,
			ledger: []schemas.LedgerEntry{
				gatewayEntry(4, "inject", 503, "b4", "N"), gatewayEntry(1, "pass", 200, "b1", ""),
				gatewayEntry(2, "pass", 200, "b2", ""), gatewayEntry(3, "inject", 503, "b3", "N"),
				{RunID: "r_1", Kind: "count_tokens", Action: "pass", Status: 200, Hash: "hct"},
				{RunID: "r_other", Kind: "message", Call: 9, Action: "inject", Status: 503, Hash: "hx"},
			},
			rows: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
				gatewayRow("row1", "success", 0, 200), gatewayRow("row2", "success", 0, 200),
				gatewayRow("row3", "error", 503, 503), gatewayRow("row4", "error", 0, 503),
			}},
			events: []schemas.Event{
				{EventID: "r_1:gateway:3", Type: schemas.EventError, Source: schemas.SourceGateway},
				{EventID: "r_1:agent_stream:7", Type: schemas.EventError, Source: schemas.SourceAgentStream},
				{EventID: "r_1:agent_stream:8", Type: schemas.EventClaim, Source: schemas.SourceAgentStream},
			},
			claim: &schemas.Claim{Text: "The model API is Overloaded right now, so I stopped.", Made: false},
			grade: &schemas.Grade{Passed: false, DiffSHA256: "d1"},
			want: map[schemas.ReceiptStep]gatewayWant{
				schemas.StepArmed:        {schemas.StepYes, "503 from conversation call 3", nil},
				schemas.StepFired:        {schemas.StepYes, "2 of 4", []string{"h3inject", "h4inject"}},
				schemas.StepDelivered:    {schemas.StepYes, "2 gateway rows", []string{"row3", "row4"}},
				schemas.StepAbsorbed:     {schemas.StepNo, AbsorbedNone, nil},
				schemas.StepVisible:      {schemas.StepYes, "1 error", []string{"r_1:agent_stream:7"}},
				schemas.StepAcknowledged: {schemas.StepYes, "overloaded", []string{"r_1:agent_stream:8"}},
				schemas.StepEffect:       {schemas.StepNo, "fail", []string{"diff_sha256:d1"}},
			},
		},
		{
			name: "bifrost retry absorbs a single 503", plan: schemas.FaultPlan{Mode: "once", Call: 2, Status: 503},
			parser: "claude-code", retries: 1, arm: true,
			ledger: []schemas.LedgerEntry{
				gatewayEntry(1, "pass", 200, "b1", ""), gatewayEntry(2, "inject", 503, "b2", "N"), gatewayEntry(3, "pass", 200, "b2", ""),
			},
			rows: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
				gatewayRow("row1", "success", 0, 200), gatewayRow("row2", "success", 0, 503, 200),
			}},
			claim: &schemas.Claim{Text: "Fixed the bug; all tests pass.", Made: true},
			grade: &schemas.Grade{Passed: true, DiffSHA256: "d2"},
			want: map[schemas.ReceiptStep]gatewayWant{
				schemas.StepFired:        {schemas.StepYes, "1 of 3", []string{"h2inject"}},
				schemas.StepDelivered:    {schemas.StepNo, "no gateway row", nil},
				schemas.StepAbsorbed:     {schemas.StepYes, AbsorbedBifrost, []string{"row2"}},
				schemas.StepVisible:      {schemas.StepNo, "no error was returned", nil},
				schemas.StepAcknowledged: {schemas.StepNo, "does not mention", nil},
				schemas.StepEffect:       {schemas.StepYes, "pass", []string{"diff_sha256:d2"}},
			},
		},
		{
			name: "cli retries the same body", plan: schemas.FaultPlan{Mode: "persistent", FromCall: 1, Status: 503},
			parser: "claude-code", arm: true,
			ledger: []schemas.LedgerEntry{
				gatewayEntry(1, "inject", 503, "same", "N"), gatewayEntry(2, "inject", 503, "same", "N"), gatewayEntry(3, "inject", 503, "same", "N"),
			},
			rows: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
				gatewayRow("row1", "error", 503, 503), gatewayRow("row2", "error", 503, 503), gatewayRow("row3", "error", 503, 503),
			}},
			claim: &schemas.Claim{Text: "I hit an error but everything is fixed now.", Made: true},
			want: map[schemas.ReceiptStep]gatewayWant{
				schemas.StepAbsorbed:     {schemas.StepYes, AbsorbedCLI, []string{"h2inject", "h3inject"}},
				schemas.StepVisible:      {schemas.StepNo, "no error event", []string{"row1", "row2", "row3"}},
				schemas.StepAcknowledged: {schemas.StepNo, "claims success", nil},
				schemas.StepEffect:       {schemas.StepUnknown, "no grade", nil},
			},
		},
		{
			name: "agent finished before the fault", plan: gatewayOutage, parser: "claude-code", arm: true,
			ledger: []schemas.LedgerEntry{gatewayEntry(1, "pass", 200, "b1", ""), gatewayEntry(2, "pass", 200, "b2", "")},
			rows:   &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{gatewayRow("row1", "success", 0, 200)}},
			claim:  &schemas.Claim{Text: "Done.", Made: true},
			grade:  &schemas.Grade{Passed: true},
			want: map[schemas.ReceiptStep]gatewayWant{
				schemas.StepFired:        {schemas.StepNo, "0 of 2", nil},
				schemas.StepDelivered:    {schemas.StepNo, "nothing fired", nil},
				schemas.StepAbsorbed:     {schemas.StepNo, AbsorbedNone, nil},
				schemas.StepVisible:      {schemas.StepNo, "nothing fired", nil},
				schemas.StepAcknowledged: {schemas.StepNotApplicable, "nothing fired", nil},
				schemas.StepEffect:       {schemas.StepYes, "pass", nil},
			},
		},
		{
			name: "nothing collected and no parser", plan: gatewayOutage,
			fail: []schemas.PartFailure{{Stage: schemas.StageCollect, Part: "evidence/proxy_ledger", Error: "proxy down"}},
			want: map[schemas.ReceiptStep]gatewayWant{
				schemas.StepFired:        {schemas.StepUnknown, "not collected", nil},
				schemas.StepDelivered:    {schemas.StepUnknown, "no gateway rows", nil},
				schemas.StepAbsorbed:     {schemas.StepUnknown, "not collected", nil},
				schemas.StepVisible:      {schemas.StepUnknown, "no parser", nil},
				schemas.StepAcknowledged: {schemas.StepUnknown, "no final message", nil},
				schemas.StepEffect:       {schemas.StepUnknown, "no grade", nil},
			},
		},
		{
			name: "delivered without a parser stays unknown", plan: gatewayOutage,
			ledger: []schemas.LedgerEntry{gatewayEntry(3, "inject", 503, "b3", "nonce_any")},
			rows:   &schemas.RequestLog{Complete: false, Reason: "timeout", Requests: []schemas.GatewayRequest{gatewayRow("row3", "error", 503)}},
			want: map[schemas.ReceiptStep]gatewayWant{
				schemas.StepArmed:     {schemas.StepYes, "ledger entries carry the run id", []string{"h3inject"}},
				schemas.StepFired:     {schemas.StepYes, "1 of 1", []string{"h3inject"}},
				schemas.StepDelivered: {schemas.StepYes, "1 gateway rows", []string{"row3"}},
				schemas.StepVisible:   {schemas.StepUnknown, "no parser", []string{"row3"}},
			},
		},
		{
			name: "injection with another run's nonce does not count", plan: gatewayOutage, arm: true,
			ledger: []schemas.LedgerEntry{gatewayEntry(3, "inject", 503, "b3", "nonce_someone_else")},
			rows:   &schemas.RequestLog{Complete: true},
			want: map[schemas.ReceiptStep]gatewayWant{
				schemas.StepFired: {schemas.StepNo, "another nonce", nil},
			},
		},
		{
			name: "incomplete rows hide delivery", plan: gatewayOutage, parser: "claude-code",
			ledger: []schemas.LedgerEntry{gatewayEntry(3, "inject", 503, "b3", "x")},
			rows:   &schemas.RequestLog{Complete: false, Reason: "row still processing"},
			want: map[schemas.ReceiptStep]gatewayWant{
				schemas.StepDelivered: {schemas.StepUnknown, "row still processing", nil},
				schemas.StepVisible:   {schemas.StepNo, "no error event", nil},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &gatewayFakeCtrl{}
			layer := newGatewayLayer(ctrl, nil)
			rc := gatewayRC("r_1", tc.plan, tc.parser, tc.retries)
			rc.Run.Failures = tc.fail
			nonce := ""
			if tc.arm {
				if _, err := layer.Arm(context.Background(), rc); err != nil {
					t.Fatal(err)
				}
				nonce = ctrl.plans["fp-slot-1"].Nonce
			}
			ledger := make([]schemas.LedgerEntry, len(tc.ledger))
			copy(ledger, tc.ledger)
			for i := range ledger {
				if ledger[i].Nonce == "N" {
					ledger[i].Nonce = nonce
				}
			}
			before := slices.Clone(ledger)
			ev := &schemas.Evidence{Ledger: ledger, Requests: tc.rows, Events: tc.events, Claim: tc.claim, Grade: tc.grade}
			rec, err := layer.Receipts(context.Background(), rc, ev)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, ev.Ledger) {
				t.Fatal("Receipts reordered the shared ledger slice")
			}
			if _, ok := rec.Steps[schemas.StepAssigned]; ok {
				t.Error("Receipts must leave assigned to the arm-time receipt")
			}
			for step, w := range tc.want {
				got, ok := rec.Steps[step]
				if !ok {
					t.Errorf("%s: missing", step)
					continue
				}
				if got.State != w.state || !strings.Contains(got.Detail, w.detail) {
					t.Errorf("%s = %s %q, want %s containing %q", step, got.State, got.Detail, w.state, w.detail)
				}
				if w.evidence != nil && !gatewayContainsAll(got.Evidence, w.evidence) {
					t.Errorf("%s evidence %v, want %v", step, got.Evidence, w.evidence)
				}
			}
			if nonce != "" {
				gatewayNoNonce(t, rec, nonce)
			}
		})
	}
}

func TestGatewayReceiptsErrors(t *testing.T) {
	layer := newGatewayLayer(&gatewayFakeCtrl{}, nil)
	rc := gatewayRC("r_1", gatewayOutage, "", 0)
	rc.Probe = nil
	if _, err := layer.Receipts(context.Background(), rc, &schemas.Evidence{}); err == nil {
		t.Fatal("Receipts without a probe succeeded")
	}
	rec, err := layer.Receipts(context.Background(), gatewayRC("r_1", gatewayOutage, "", 0), nil)
	if err != nil || rec.Steps[schemas.StepFired].State != schemas.StepNo {
		t.Fatalf("nil evidence: %+v, %v", rec, err)
	}
}

func gatewayContainsAll(got, want []string) bool {
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// gatewayNoNonce fails when the secret nonce appears anywhere in a receipt.
func gatewayNoNonce(t *testing.T, rec *schemas.Receipt, nonce string) {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), nonce) {
		t.Fatalf("receipt leaks the nonce: %s", data)
	}
}

// TestRoutedFaultModel (W9): a routed run's fault that names no model fails
// the primary target only; an explicit model, "*" and unrouted runs keep the
// plan as written.
func TestRoutedFaultModel(t *testing.T) {
	routed := &schemas.RunContext{Agent: &schemas.AgentConfig{Model: "strong", Gateway: schemas.GatewayPolicy{Routing: &schemas.RoutingPolicy{
		Strategy: schemas.RoutingFallbackChain, Targets: []schemas.RouteTarget{{Model: "strong"}}, Fallbacks: []schemas.RouteTarget{{Model: "cheap"}}}}}}
	plain := &schemas.RunContext{Agent: &schemas.AgentConfig{Model: "strong"}}
	outage := schemas.FaultPlan{Mode: "persistent", FromCall: 1, Status: 503}
	cases := []struct {
		name string
		plan schemas.FaultPlan
		rc   *schemas.RunContext
		want string
	}{
		{"routed run: primary only", outage, routed, "strong"},
		{"unrouted run: unchanged", outage, plain, ""},
		{"explicit model kept", schemas.FaultPlan{Mode: "once", Call: 1, Status: 503, Model: "cheap"}, routed, "cheap"},
		{"star kept", schemas.FaultPlan{Mode: "persistent", FromCall: 1, Status: 503, Model: "*"}, routed, "*"},
		{"none plan untouched", schemas.FaultPlan{Mode: "none"}, routed, ""},
		{"no agent", outage, &schemas.RunContext{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := routedFaultModel(c.plan, c.rc); got != c.want {
				t.Fatalf("model = %q, want %q", got, c.want)
			}
		})
	}
	if got := gatewayPlanText("fp-slot-1", schemas.FaultPlan{Mode: "persistent", FromCall: 1, Status: 503, Model: "strong"}); got != "slot fp-slot-1: 503 from conversation call 1, model strong only" {
		t.Errorf("plan text = %q", got)
	}
}
