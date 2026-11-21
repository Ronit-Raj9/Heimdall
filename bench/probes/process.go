package probes

import (
	"context"
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

var _ schemas.ProbeLayer = (*processLayer)(nil)

const processLayerVersion = "1.0.0"

// processSteps are the receipt steps of the process layer: the engine kills
// the agent (fired) and starts it again (delivered: the resumed agent ran);
// effect is the grade.
var processSteps = []schemas.ReceiptStep{
	schemas.StepAssigned, schemas.StepArmed, schemas.StepFired, schemas.StepDelivered, schemas.StepEffect,
}

// processLayer is the crash-resume probe (stream.go ProcessFault). Arming
// only checks the rule: the engine watches the run and sends SIGKILL itself
// (engine/processrun.go), then resumes the agent with BENCH_RESUME=1 on the
// same workspace and memory. The receipt reads the kill record the engine
// put on the evidence, never the agent's own account.
type processLayer struct{}

func newProcessLayer() *processLayer { return &processLayer{} }

// GetName implements schemas.Part.
func (l *processLayer) GetName() string { return string(schemas.LayerProcess) }

// GetVersion implements schemas.Part.
func (l *processLayer) GetVersion() string { return processLayerVersion }

// Steps implements schemas.ProbeLayer.
func (l *processLayer) Steps() []schemas.ReceiptStep {
	return append([]schemas.ReceiptStep(nil), processSteps...)
}

// Arm implements schemas.ProbeLayer.
func (l *processLayer) Arm(_ context.Context, rc *schemas.RunContext) (*schemas.Receipt, error) {
	if rc == nil || rc.Probe == nil {
		return nil, errors.New("process arm: run context needs a probe")
	}
	if err := rc.Probe.Apply.Process.Validate(); err != nil {
		return nil, fmt.Errorf("process arm: probe %s: %w", rc.Probe.ID, err)
	}
	if s := rc.Probe.Apply.Process.Session; s != "" && rc.Task != nil && !hasSession(rc.Task, s) {
		return nil, fmt.Errorf("process arm: probe %s: task %s has no session %q", rc.Probe.ID, rc.Task.ID, s)
	}
	rec := startReceipt(rc, schemas.LayerProcess, processSteps)
	rec.Steps[schemas.StepAssigned] = schemas.ReceiptEntry{State: schemas.StepYes}
	rec.Steps[schemas.StepArmed] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: "kill rule checked; the engine watches the run"}
	return rec, nil
}

// Disarm implements schemas.ProbeLayer.
func (l *processLayer) Disarm(context.Context, *schemas.RunContext) error { return nil }

// Receipts implements schemas.ProbeLayer.
func (l *processLayer) Receipts(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.Receipt, error) {
	if rc == nil || rc.Probe == nil {
		return nil, errors.New("process receipts: run context needs a probe")
	}
	rec := startReceipt(rc, schemas.LayerProcess, processSteps)
	rec.Steps[schemas.StepAssigned] = schemas.ReceiptEntry{State: schemas.StepYes}
	if err := rc.Probe.Apply.Process.Validate(); err != nil {
		rec.Steps[schemas.StepArmed] = schemas.ReceiptEntry{State: schemas.StepNo, Detail: err.Error()}
	} else {
		rec.Steps[schemas.StepArmed] = schemas.ReceiptEntry{State: schemas.StepYes}
	}
	var kill *schemas.ProcessKill
	if ev != nil {
		kill = ev.ProcessKill
	}
	switch {
	case kill == nil:
		rec.Steps[schemas.StepFired] = schemas.ReceiptEntry{State: schemas.StepNo, Detail: "the agent exited before the kill trigger"}
		rec.Steps[schemas.StepDelivered] = schemas.ReceiptEntry{State: schemas.StepNo}
	default:
		rec.Steps[schemas.StepFired] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: fmt.Sprintf("SIGKILL on %s after %d model call(s)", kill.Trigger, kill.ModelCalls), Evidence: []string{schemas.ProcessKillFile}}
		if kill.Resumed {
			rec.Steps[schemas.StepDelivered] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: "the agent ran again with BENCH_RESUME=1"}
		} else {
			rec.Steps[schemas.StepDelivered] = schemas.ReceiptEntry{State: schemas.StepNo, Detail: kill.ResumeError}
		}
	}
	rec.Steps[schemas.StepEffect] = gradeEffect(ev)
	return rec, nil
}

func hasSession(t *schemas.Task, id string) bool {
	for _, s := range t.Sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}
