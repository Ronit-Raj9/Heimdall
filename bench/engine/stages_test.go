package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

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
