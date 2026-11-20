package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

type rawReceiptLayer struct {
	sawRaw bool
	fail   bool
}

func (l *rawReceiptLayer) GetName() string    { return string(schemas.LayerFixture) }
func (l *rawReceiptLayer) GetVersion() string { return "test" }
func (l *rawReceiptLayer) Steps() []schemas.ReceiptStep {
	return []schemas.ReceiptStep{schemas.StepVisible}
}
func (l *rawReceiptLayer) Arm(context.Context, *schemas.RunContext) (*schemas.Receipt, error) {
	return nil, nil
}
func (l *rawReceiptLayer) Disarm(context.Context, *schemas.RunContext) error { return nil }
func (l *rawReceiptLayer) Receipts(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (*schemas.Receipt, error) {
	if ev != nil && ev.Requests != nil && len(ev.Requests.Requests) > 0 {
		l.sawRaw = len(ev.Requests.Requests[0].Raw) > 0
	}
	if l.fail {
		return nil, errors.New("receipt failed")
	}
	return &schemas.Receipt{Steps: map[schemas.ReceiptStep]schemas.ReceiptEntry{
		schemas.StepVisible: {State: schemas.StepYes},
	}}, nil
}

func TestReceiptUsesTransientRawAndClearsIt(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "error"}[fail], func(t *testing.T) {
			b := newBench(t)
			layer := &rawReceiptLayer{fail: fail}
			run := &schemas.Run{RunID: "r_1"}
			slot := &schemas.Slot{ExpID: "e_1", SlotID: "s_1"}
			dir, err := b.store.NewRun(context.Background(), slot, run)
			if err != nil {
				t.Fatal(err)
			}
			st := &runState{
				rc: &schemas.RunContext{
					Manifest: &schemas.Manifest{Hash: "manifest"},
					Run:      run,
					Probe:    &schemas.ProbeSpec{ID: "p_1", Layer: schemas.LayerFixture},
					Dir:      dir,
				},
				layer: layer,
				ev: &schemas.Evidence{Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{{
					Raw: []byte(`{"text":"nonce"}`),
				}}}},
			}
			p := NewPipeline(b.reg, b.root, nil)
			err = p.receipt(context.Background(), st)
			if (err != nil) != fail {
				t.Fatalf("err = %v, want failure %v", err, fail)
			}
			if !layer.sawRaw {
				t.Fatal("probe receipt did not see transient raw request body")
			}
			if got := st.ev.Requests.Requests[0].Raw; got != nil {
				t.Fatalf("raw request body remained after receipt: %q", got)
			}
		})
	}
}

func TestMergeReceipt(t *testing.T) {
	b, m, slot := pipelineFixture(t, "stale-notes")
	st := &runState{
		rc:    &schemas.RunContext{Manifest: m, Slot: slot, Probe: b.suite.ProbeByID("stale-notes")},
		layer: fakeLayer{part{"fixture"}, b.w},
		armReceipt: &schemas.Receipt{Steps: map[schemas.ReceiptStep]schemas.ReceiptEntry{
			schemas.StepArmed:   {State: schemas.StepYes},
			schemas.StepVisible: {State: schemas.StepNo},
		}},
	}
	later := &schemas.Receipt{Steps: map[schemas.ReceiptStep]schemas.ReceiptEntry{
		schemas.StepVisible: {State: schemas.StepYes, Evidence: []string{"row1"}},
	}}
	tests := []struct {
		name  string
		later *schemas.Receipt
		step  schemas.ReceiptStep
		want  schemas.StepState
	}{
		{"arm-time step kept", later, schemas.StepArmed, schemas.StepYes},
		{"later receipt wins", later, schemas.StepVisible, schemas.StepYes},
		{"assigned from manifest", later, schemas.StepAssigned, schemas.StepYes},
		{"declared step unknown", later, schemas.StepEffect, schemas.StepUnknown},
		{"undeclared step not applicable", later, schemas.StepFired, schemas.StepNotApplicable},
		{"no later receipt", nil, schemas.StepVisible, schemas.StepNo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := mergeReceipt(st, tt.later)
			if len(r.Steps) != len(schemas.AllReceiptSteps) {
				t.Fatalf("%d steps, want every step", len(r.Steps))
			}
			if got := r.Steps[tt.step].State; got != tt.want {
				t.Fatalf("%s = %s, want %s", tt.step, got, tt.want)
			}
			if r.ProbeID != "stale-notes" || r.Layer != schemas.LayerFixture {
				t.Fatalf("receipt header %s %s", r.ProbeID, r.Layer)
			}
		})
	}
}

func TestStageDispatch(t *testing.T) {
	p := NewPipeline(&schemas.Registry{}, "", nil)
	err := p.do(context.Background(), &runState{}, schemas.Stage("teleport"))
	if err == nil || !strings.Contains(err.Error(), "no handler") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolvePartsMissing(t *testing.T) {
	b, m, slot := pipelineFixture(t, "stale-notes")
	reg := *b.reg
	reg.ProbeLayers, reg.Gateway, reg.Graders = nil, nil, nil
	st := &runState{rc: &schemas.RunContext{Manifest: m, Suite: b.suite, Slot: slot, Task: b.suite.TaskByID(slot.Task),
		Probe: b.suite.ProbeByID(slot.Condition), Agent: &schemas.AgentConfig{Adapter: "command"}}}
	err := NewPipeline(&reg, b.root, nil).resolveParts(st)
	for _, want := range []string{`probe layer "fixture"`, "no gateway", `grader "hidden_tests"`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

// fakeDerived is a metric that also yields run-time named values.
type fakeDerived struct {
	fakeMetric
	vals map[string]schemas.Value
	err  error
}

func (m fakeDerived) ComputeDerived(context.Context, *schemas.RunContext, *schemas.Evidence) (map[string]schemas.Value, error) {
	return m.vals, m.err
}

func TestMeasureDerived(t *testing.T) {
	b := newBench(t)
	obs := func(v float64) schemas.Value { return schemas.Observed(v, schemas.EvidenceOutcome) }
	derived := fakeDerived{fakeMetric: fakeMetric{part{"reward"}, b.w}, vals: map[string]schemas.Value{
		"reward_safety":    obs(0.5),
		"reward_style":     obs(1),
		"verified_success": obs(9), // a selected registered metric
		costMetric:         obs(7), // a registered metric the experiment did not select
	}}
	tests := []struct {
		name    string
		metrics []schemas.Metric
		want    map[string]float64
		wantErr string
	}{
		{name: "no derived metric adds nothing", metrics: b.reg.Metrics, want: map[string]float64{"verified_success": 1}},
		{name: "derived values beside registered ones, never over them",
			metrics: append(append([]schemas.Metric{}, b.reg.Metrics...), derived),
			want:    map[string]float64{"verified_success": 1, "reward_safety": 0.5, "reward_style": 1}},
		{name: "a failing derived metric is a part error",
			metrics: append(append([]schemas.Metric{}, b.reg.Metrics...), fakeDerived{fakeMetric: fakeMetric{part{"reward"}, b.w}, err: errors.New("boom")}),
			want:    map[string]float64{"verified_success": 1}, wantErr: "compute derived: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := *b.reg
			reg.Metrics = tt.metrics
			p := NewPipeline(&reg, b.root, nil)
			st := &runState{rc: &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}}, ev: &schemas.Evidence{},
				metrics: map[string]schemas.Value{"verified_success": obs(1)}}
			var selected []schemas.Metric
			for _, m := range tt.metrics {
				if m.GetName() != costMetric {
					selected = append(selected, m)
				}
			}
			errs := p.measureDerived(context.Background(), st, selected)
			if err := errors.Join(errs...); (err != nil) != (tt.wantErr != "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if len(st.metrics) != len(tt.want) {
				t.Fatalf("metrics = %+v, want %v", st.metrics, tt.want)
			}
			for k, want := range tt.want {
				if got, ok := st.metrics[k].Float(); !ok || got != want {
					t.Errorf("%s = %+v, want %v", k, st.metrics[k], want)
				}
			}
		})
	}
}

func TestBindFaultPlanModes(t *testing.T) {
	gatewayProbe := &schemas.ProbeSpec{ID: "model-outage", Layer: schemas.LayerGateway}
	tests := []struct {
		name     string
		mode     string
		noFault  bool
		probe    *schemas.ProbeSpec
		exp      *schemas.Experiment // 3-C-01: a smoke or scripted_upstream experiment
		direct   bool                // the run key is on a direct slot (no SlotAuth)
		wantMode string              // mode of the one plan bound; empty: none bound
		wantErr  string
	}{
		{name: "default binds none", wantMode: faultModeNone},
		{name: "scripted binds scripted", mode: faultModeScripted, wantMode: faultModeScripted},
		{name: "a gateway probe binds its own plan", probe: gatewayProbe},
		{name: "scripted refuses a gateway probe", mode: faultModeScripted, probe: gatewayProbe, wantErr: "cannot run gateway-layer probe"},
		{name: "no fault proxy binds nothing", noFault: true},
		{name: "scripted refuses to run without the fault proxy", mode: faultModeScripted, noFault: true, wantErr: "needs the fault proxy"},

		// 3-C-01, 7-C-04: a smoke or scripted_upstream run on the default pipeline is scripted.
		{name: "smoke binds scripted", exp: &schemas.Experiment{Smoke: true}, wantMode: faultModeScripted},
		{name: "scripted_upstream binds scripted", exp: &schemas.Experiment{ScriptedUpstream: true}, wantMode: faultModeScripted},
		{name: "smoke refuses to run without the fault proxy", exp: &schemas.Experiment{Smoke: true}, noFault: true, wantErr: "needs the fault proxy"},
		{name: "smoke refuses a direct slot", exp: &schemas.Experiment{Smoke: true}, direct: true, wantErr: "is direct, not proxied"},
		{name: "smoke lets a gateway probe bind its scripted plan", exp: &schemas.Experiment{Smoke: true}, probe: gatewayProbe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			reg := *b.reg
			if tt.noFault {
				reg.Fault = nil
			}
			key := &schemas.RunKey{KeySlot: "fp-slot-1", SlotAuth: "slot-auth-1"}
			if tt.direct {
				key.SlotAuth = ""
			}
			st := &runState{rc: &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}, Probe: tt.probe, Experiment: tt.exp, Key: key}}
			err := NewPipeline(&reg, b.root, nil, WithFaultMode(tt.mode)).bindFaultPlan(context.Background(), st)
			if (err != nil) != (tt.wantErr != "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			switch {
			case tt.wantMode == "" && len(b.w.plans) != 0:
				t.Fatalf("plans bound: %+v", b.w.plans)
			case tt.wantMode != "" && (len(b.w.plans) != 1 || b.w.plans[0].Mode != tt.wantMode || b.w.plans[0].RunID != "r_1"):
				t.Fatalf("plans = %+v, want one %s plan", b.w.plans, tt.wantMode)
			}
		})
	}
}
