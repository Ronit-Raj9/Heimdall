package probes

import (
	"context"
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// B1: the a2a probe layer (docs/a2a-agents.md). It perturbs the client side
// of an A2A stream through the agent gateway: the bench's own launcher drops
// the stream after a number of events, or stops reading it for a while.
// Bifrost documents that it never replays a stream and that a disconnected
// client does not cancel the task, so the task must be recovered through the
// gateway with GetTask or SubscribeToTask. The layer starts nothing; the
// adapter hands the fault to the launcher (BENCH_A2A_FAULT).
//
//   - fired: the launcher reports it applied the fault (bench code);
//   - delivered: the agent log holds the stream operation of the task;
//   - absorbed_by: gateway when the agent log shows the recovery (a GetTask
//     or SubscribeToTask of the same task) and a completed final state;
//   - effect: the grade.

var _ schemas.ProbeLayer = (*a2aLayer)(nil)

const a2aLayerVersion = "1.0.0"

var a2aSteps = []schemas.ReceiptStep{schemas.StepAssigned, schemas.StepArmed, schemas.StepFired, schemas.StepDelivered, schemas.StepAbsorbed, schemas.StepEffect}

type a2aLayer struct{}

func newA2ALayer() *a2aLayer { return &a2aLayer{} }

// GetName implements schemas.Part.
func (l *a2aLayer) GetName() string { return string(schemas.LayerA2A) }

// GetVersion implements schemas.Part.
func (l *a2aLayer) GetVersion() string { return a2aLayerVersion }

// Steps implements schemas.ProbeLayer.
func (l *a2aLayer) Steps() []schemas.ReceiptStep {
	return append([]schemas.ReceiptStep(nil), a2aSteps...)
}

// Arm implements schemas.ProbeLayer. The planner has already refused a
// non-a2a or non-streaming arm; Arm checks again.
func (l *a2aLayer) Arm(_ context.Context, rc *schemas.RunContext) (*schemas.Receipt, error) {
	f := utils.A2AFaultOf(rc)
	switch {
	case f == nil:
		return nil, errors.New("a2a arm: the probe has no apply.a2a")
	case !utils.IsA2A(rc.Agent) || rc.Agent.A2A == nil:
		return nil, errors.New("a2a arm: the run's agent is not an a2a agent")
	case !rc.Agent.A2A.Streaming:
		return nil, errors.New("a2a arm: the run's agent does not stream")
	}
	rec := startReceipt(rc, schemas.LayerA2A, a2aSteps)
	rec.Steps[schemas.StepAssigned] = schemas.ReceiptEntry{State: schemas.StepYes}
	rec.Steps[schemas.StepArmed] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: fmt.Sprintf("launcher told to %s the stream", f.Stream)}
	return rec, nil
}

// Disarm implements schemas.ProbeLayer. Nothing outlives the launcher.
func (l *a2aLayer) Disarm(context.Context, *schemas.RunContext) error { return nil }

// Receipts implements schemas.ProbeLayer.
func (l *a2aLayer) Receipts(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.Receipt, error) {
	rec := startReceipt(rc, schemas.LayerA2A, a2aSteps)
	rec.Steps[schemas.StepAssigned] = schemas.ReceiptEntry{State: schemas.StepYes}
	if utils.A2AFaultOf(rc) != nil {
		rec.Steps[schemas.StepArmed] = schemas.ReceiptEntry{State: schemas.StepYes}
	}
	rec.Steps[schemas.StepEffect] = gradeEffect(ev)
	if ev == nil || ev.A2A == nil {
		return rec, nil
	}
	a := ev.A2A
	if a.Launcher != nil && a.Launcher.Fault != nil && a.Launcher.Fault.Fired {
		rec.Steps[schemas.StepFired] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: "the launcher applied the stream fault", Evidence: []string{"a2a_report"}}
	} else {
		rec.Steps[schemas.StepFired] = schemas.ReceiptEntry{State: schemas.StepNo, Detail: "the launcher did not apply the fault (the stream may have ended first)"}
	}
	task := ""
	if a.Launcher != nil {
		task = a.Launcher.TaskID
	}
	var stream []string
	for _, op := range a.Operations {
		if op.Operation == "SendStreamingMessage" && op.TaskID == task && task != "" {
			stream = append(stream, op.ID)
		}
	}
	switch {
	case len(stream) > 0:
		rec.Steps[schemas.StepDelivered] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: "the agent log holds the stream operation", Evidence: stream}
	case a.Complete:
		rec.Steps[schemas.StepDelivered] = schemas.ReceiptEntry{State: schemas.StepNo, Detail: "no stream operation of the task in a complete agent log"}
	default:
		rec.Steps[schemas.StepDelivered] = schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: "agent log incomplete: " + a.Reason}
	}
	if f := a.Fault; f != nil {
		state := schemas.StepNo
		if f.Recovered {
			state = schemas.StepYes
		}
		rec.Steps[schemas.StepAbsorbed] = schemas.ReceiptEntry{State: state, Detail: f.Detail, Evidence: []string{"gateway"}}
	}
	return rec, nil
}
