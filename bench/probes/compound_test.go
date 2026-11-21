package probes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeLayer records arms and disarms and answers a fixed receipt.
type fakeLayer struct {
	name    schemas.ProbeLayerName
	mu      sync.Mutex
	armed   []string // probe layer + fault seen at arm
	disarms int
	failArm bool
}

func (f *fakeLayer) GetName() string              { return string(f.name) }
func (f *fakeLayer) GetVersion() string           { return "t" }
func (f *fakeLayer) Steps() []schemas.ReceiptStep { return schemas.AllReceiptSteps }
func (f *fakeLayer) Disarm(context.Context, *schemas.RunContext) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disarms++
	return nil
}
func (f *fakeLayer) Arm(_ context.Context, rc *schemas.RunContext) (*schemas.Receipt, error) {
	if f.failArm {
		return nil, errors.New("boom")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	what := string(rc.Probe.Layer)
	if rc.Probe.Apply.Fault != nil {
		what += ":" + rc.Probe.Apply.Fault.Mode
	}
	if rc.Probe.Apply.Tool != nil {
		what += ":" + rc.Probe.Apply.Tool.Mode
	}
	f.armed = append(f.armed, what)
	return nil, nil
}
func (f *fakeLayer) Receipts(_ context.Context, rc *schemas.RunContext, _ *schemas.Evidence) (*schemas.Receipt, error) {
	r := startReceipt(rc, f.name, []schemas.ReceiptStep{schemas.StepFired})
	r.Steps[schemas.StepFired] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: "1"}
	return r, nil
}

func compoundRC() *schemas.RunContext {
	return &schemas.RunContext{Run: &schemas.Run{RunID: "r1"}, Probe: &schemas.ProbeSpec{ID: "c", Layer: schemas.LayerCompound, Apply: schemas.ProbeApply{Faults: []schemas.FaultStep{
		{Layer: schemas.LayerGateway, Fault: &schemas.FaultPlan{Mode: "persistent", FromCall: 3, Calls: 6, Status: 503}},
		{Layer: schemas.LayerTool, AtS: 20, Tool: &schemas.ToolFault{Mode: "rate_limit", Limit: 3, WindowS: 20}},
	}}}}
}

func TestCheckSchedule(t *testing.T) {
	gw := schemas.FaultStep{Layer: schemas.LayerGateway, Fault: &schemas.FaultPlan{Mode: "none"}}
	tool := schemas.FaultStep{Layer: schemas.LayerTool, Tool: &schemas.ToolFault{Mode: "error"}}
	cases := []struct {
		name  string
		steps []schemas.FaultStep
		ok    bool
	}{
		{"empty", nil, false},
		{"two layers", []schemas.FaultStep{gw, tool}, true},
		{"same layer twice", []schemas.FaultStep{gw, gw}, false},
		{"gateway without fault", []schemas.FaultStep{{Layer: schemas.LayerGateway}}, false},
		{"unknown layer", []schemas.FaultStep{{Layer: schemas.LayerFixture}}, false},
	}
	for _, tc := range cases {
		if err := CheckSchedule(tc.steps); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
	if err := CheckSchedule(schemas.GenericProbe(schemas.GenericProbeCompound).Apply.Faults); err != nil {
		t.Fatalf("generic compound probe: %v", err)
	}
}

func TestCompoundArmsOnSchedule(t *testing.T) {
	gw, tl := &fakeLayer{name: schemas.LayerGateway}, &fakeLayer{name: schemas.LayerTool}
	l := newCompoundLayer(nil, gw, tl)
	fire := make(chan time.Time)
	var asked time.Duration
	l.after = func(d time.Duration) <-chan time.Time { asked = d; return fire }
	rc := compoundRC()
	if _, err := l.Arm(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if len(gw.armed) != 1 || gw.armed[0] != "gateway:persistent" || len(tl.armed) != 0 {
		t.Fatalf("at start: gateway %v tool %v", gw.armed, tl.armed)
	}
	fire <- time.Now()
	deadline := time.Now().Add(2 * time.Second)
	for {
		tl.mu.Lock()
		n := len(tl.armed)
		tl.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if asked != 20*time.Second || len(tl.armed) != 1 || tl.armed[0] != "tool:rate_limit" {
		t.Fatalf("timed step: asked %v armed %v", asked, tl.armed)
	}
	if err := l.Disarm(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if gw.disarms != 1 || tl.disarms != 1 {
		t.Fatalf("disarms gateway %d tool %d", gw.disarms, tl.disarms)
	}
	rec, err := l.Receipts(context.Background(), rc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Parts) != 2 || rec.Steps[schemas.StepFired].State != schemas.StepYes || rec.Layer != schemas.LayerCompound {
		t.Fatalf("receipt %+v", rec)
	}
}

func TestCompoundPendingStepCancelled(t *testing.T) {
	gw, tl := &fakeLayer{name: schemas.LayerGateway}, &fakeLayer{name: schemas.LayerTool}
	l := newCompoundLayer(nil, gw, tl)
	l.after = func(time.Duration) <-chan time.Time { return make(chan time.Time) }
	rc := compoundRC()
	if _, err := l.Arm(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if err := l.Disarm(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if len(tl.armed) != 0 || tl.disarms != 0 || gw.disarms != 1 {
		t.Fatalf("tool armed %v disarms %d, gateway disarms %d", tl.armed, tl.disarms, gw.disarms)
	}
	rec, _ := l.Receipts(context.Background(), rc, nil)
	if len(rec.Parts) != 2 || rec.Parts[1].Steps[schemas.StepArmed].State != schemas.StepNo {
		t.Fatalf("parts %+v", rec.Parts)
	}
}

func TestCompoundArmFailureDisarms(t *testing.T) {
	gw := &fakeLayer{name: schemas.LayerGateway, failArm: true}
	l := newCompoundLayer(nil, gw, &fakeLayer{name: schemas.LayerTool})
	if _, err := l.Arm(context.Background(), compoundRC()); err == nil {
		t.Fatal("arm failure not reported")
	}
}
