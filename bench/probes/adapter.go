package probes

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

const adapterVersion = "1.0.0"

type adapterLayer struct {
	controller schemas.AdapterFaultController
}

func newAdapterLayer(c schemas.AdapterFaultController) *adapterLayer {
	return &adapterLayer{controller: c}
}
func (l *adapterLayer) GetName() string    { return string(schemas.LayerAdapter) }
func (l *adapterLayer) GetVersion() string { return adapterVersion }
func (l *adapterLayer) Steps() []schemas.ReceiptStep {
	return []schemas.ReceiptStep{schemas.StepAssigned, schemas.StepArmed, schemas.StepFired, schemas.StepDelivered, schemas.StepAbsorbed, schemas.StepAcknowledged, schemas.StepEffect}
}
func (l *adapterLayer) Arm(ctx context.Context, rc *schemas.RunContext) (*schemas.Receipt, error) {
	if l.controller == nil {
		return nil, fmt.Errorf("adapter fault controller is not configured")
	}
	if rc == nil || rc.Probe == nil || rc.Probe.Apply.AdapterFault == nil {
		return nil, fmt.Errorf("adapter fault probe has no rule")
	}
	if err := l.controller.Arm(ctx, rc); err != nil {
		return nil, err
	}
	return startReceipt(rc, schemas.LayerAdapter, l.Steps()), nil
}
func (l *adapterLayer) Disarm(ctx context.Context, rc *schemas.RunContext) error {
	if l.controller == nil {
		return nil
	}
	return l.controller.Freeze(ctx, rc)
}
func (l *adapterLayer) Receipts(ctx context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.Receipt, error) {
	_ = ctx
	rec := startReceipt(rc, schemas.LayerAdapter, l.Steps())
	if ev == nil || ev.AdapterFault == nil {
		return rec, nil
	}
	a := ev.AdapterFault
	rec.Steps[schemas.StepArmed] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: "private controller armed"}
	rec.Steps[schemas.StepFired] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: fmt.Sprintf("%d controller decisions", countPhase(a.Entries, "decision"))}
	rec.Steps[schemas.StepDelivered] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: "decision returned to registered hook"}
	rec.Steps[schemas.StepAcknowledged] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: fmt.Sprintf("%d SDK acknowledgements", countPhase(a.Entries, "applied"))}
	if !a.Complete {
		rec.Steps[schemas.StepAbsorbed] = schemas.ReceiptEntry{State: schemas.StepUnknown, Detail: a.Reason}
	} else {
		rec.Steps[schemas.StepAbsorbed] = schemas.ReceiptEntry{State: schemas.StepYes, Detail: "runtime reported session completion"}
	}
	return rec, nil
}
func countPhase(es []schemas.AdapterFaultEntry, p string) int {
	n := 0
	for _, e := range es {
		if e.Phase == p {
			n++
		}
	}
	return n
}
