package probes

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func processRC(rule *schemas.ProcessFault) *schemas.RunContext {
	return &schemas.RunContext{
		Probe: &schemas.ProbeSpec{ID: "crash-resume", Layer: schemas.LayerProcess, Apply: schemas.ProbeApply{Process: rule}},
		Task:  &schemas.Task{ID: "t1", Sessions: []schemas.SessionSpec{{ID: "s1"}, {ID: "s2"}}},
	}
}

func TestProcessLayerArm(t *testing.T) {
	l := newProcessLayer()
	if _, err := l.Arm(context.Background(), processRC(nil)); err == nil {
		t.Fatal("no rule must be refused")
	}
	if _, err := l.Arm(context.Background(), processRC(&schemas.ProcessFault{})); err == nil {
		t.Fatal("rule without trigger must be refused")
	}
	if _, err := l.Arm(context.Background(), processRC(&schemas.ProcessFault{AtModelCall: 2, Session: "s9"})); err == nil {
		t.Fatal("unknown session must be refused")
	}
	rec, err := l.Arm(context.Background(), processRC(&schemas.ProcessFault{AtModelCall: 2, Session: "s2"}))
	if err != nil || rec.Steps[schemas.StepArmed].State != schemas.StepYes {
		t.Fatalf("arm: %+v %v", rec, err)
	}
}

func TestProcessLayerReceipts(t *testing.T) {
	l := newProcessLayer()
	rc := processRC(&schemas.ProcessFault{AfterS: 30})
	rec, err := l.Receipts(context.Background(), rc, &schemas.Evidence{})
	if err != nil || rec.Steps[schemas.StepFired].State != schemas.StepNo {
		t.Fatalf("not fired: %+v %v", rec, err)
	}
	code := 0
	ev := &schemas.Evidence{ProcessKill: &schemas.ProcessKill{KilledAt: time.Now(), Trigger: "after_s", Resumed: true, ResumeExit: &code}}
	rec, _ = l.Receipts(context.Background(), rc, ev)
	if rec.Steps[schemas.StepFired].State != schemas.StepYes || rec.Steps[schemas.StepDelivered].State != schemas.StepYes {
		t.Fatalf("fired and resumed: %+v", rec)
	}
	ev.ProcessKill.Resumed = false
	rec, _ = l.Receipts(context.Background(), rc, ev)
	if rec.Steps[schemas.StepDelivered].State != schemas.StepNo {
		t.Fatalf("not resumed: %+v", rec)
	}
}
