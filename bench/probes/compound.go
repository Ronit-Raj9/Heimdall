package probes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// compoundLayerVersion is the version of the compound layer. Bump it with
// any change to how it schedules steps or merges their receipts.
const compoundLayerVersion = "1.0.0"

// compoundLayer arms a fault schedule (ProbeApply.Faults, lane X22): one
// step per layer, each armed with the run or AtS seconds into it, through
// that layer's own implementation. Call windows stay in each step's own
// fault (from_call, calls), so a schedule can say "the model browns out at
// call 3, and 20 seconds in the tool starts rate limiting". Its receipt is
// the union of the steps' receipts, with each step's receipt under Parts.
type compoundLayer struct {
	layers map[schemas.ProbeLayerName]schemas.ProbeLayer
	logger schemas.Logger
	after  func(time.Duration) <-chan time.Time // tests replace it

	mu   sync.Mutex
	runs map[string]*compoundRun
}

// compoundRun is one run's schedule in flight.
type compoundRun struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	armed  map[int]bool   // step index -> armed
	errs   map[int]string // step index -> why it did not arm
}

func newCompoundLayer(logger schemas.Logger, layers ...schemas.ProbeLayer) *compoundLayer {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	m := map[schemas.ProbeLayerName]schemas.ProbeLayer{}
	for _, l := range layers {
		m[schemas.ProbeLayerName(l.GetName())] = l
	}
	return &compoundLayer{layers: m, logger: logger, after: time.After, runs: map[string]*compoundRun{}}
}

// GetName implements schemas.Part.
func (l *compoundLayer) GetName() string { return string(schemas.LayerCompound) }

// GetVersion implements schemas.Part.
func (l *compoundLayer) GetVersion() string { return compoundLayerVersion }

// Steps implements schemas.ProbeLayer: every step any part may fill.
func (l *compoundLayer) Steps() []schemas.ReceiptStep {
	return slices.Clone(schemas.AllReceiptSteps)
}

// CheckSchedule checks a fault schedule: at least one step, every layer
// one the compound layer drives, at most one step per layer, and each step
// carrying its layer's fault block. It is pure.
func CheckSchedule(steps []schemas.FaultStep) error {
	if len(steps) == 0 {
		return errors.New("compound probe has no fault steps")
	}
	seen := map[schemas.ProbeLayerName]bool{}
	for i, s := range steps {
		switch s.Layer {
		case schemas.LayerGateway:
			if s.Fault == nil {
				return fmt.Errorf("fault step %d: gateway step has no fault", i+1)
			}
		case schemas.LayerTool:
			if s.Tool == nil {
				return fmt.Errorf("fault step %d: tool step has no tool fault", i+1)
			}
		default:
			return fmt.Errorf("fault step %d: layer %q is not gateway or tool", i+1, s.Layer)
		}
		if s.AtS < 0 {
			return fmt.Errorf("fault step %d: at_s must not be negative", i+1)
		}
		if seen[s.Layer] {
			return fmt.Errorf("fault step %d: a second %s step; a schedule has at most one step per layer", i+1, s.Layer)
		}
		seen[s.Layer] = true
	}
	return nil
}

// stepContext is rc with the probe narrowed to one step: its layer and its
// fault block, the parent's target agent kept.
func stepContext(rc *schemas.RunContext, s schemas.FaultStep) *schemas.RunContext {
	sub := *rc
	p := *rc.Probe
	p.Layer = s.Layer
	p.Apply = schemas.ProbeApply{Fault: s.Fault, Tool: s.Tool, Agent: rc.Probe.Apply.Agent}
	sub.Probe = &p
	return &sub
}

// Arm implements schemas.ProbeLayer. Steps at 0 seconds arm now, and a
// failure there fails the arm; later steps arm on a timer until Disarm.
func (l *compoundLayer) Arm(ctx context.Context, rc *schemas.RunContext) (*schemas.Receipt, error) {
	runID := runIDOf(rc)
	if runID == "" || rc.Probe == nil {
		return nil, errors.New("compound arm: run context needs a run and a probe")
	}
	steps := rc.Probe.Apply.Faults
	if err := CheckSchedule(steps); err != nil {
		return nil, fmt.Errorf("compound arm: probe %s: %w", rc.Probe.ID, err)
	}
	for _, s := range steps {
		if l.layers[s.Layer] == nil {
			return nil, fmt.Errorf("compound arm: layer %s is not available", s.Layer)
		}
	}
	bg, cancel := context.WithCancel(context.WithoutCancel(ctx))
	run := &compoundRun{cancel: cancel, armed: map[int]bool{}, errs: map[int]string{}}
	l.mu.Lock()
	l.runs[runID] = run
	l.mu.Unlock()
	rec := startReceipt(rc, schemas.LayerCompound, nil)
	for i, s := range steps {
		if s.AtS > 0 {
			continue
		}
		part, err := l.layers[s.Layer].Arm(ctx, stepContext(rc, s))
		if err != nil {
			_ = l.disarmArmed(ctx, rc, run)
			cancel()
			l.mu.Lock()
			delete(l.runs, runID)
			l.mu.Unlock()
			return nil, fmt.Errorf("compound arm: step %d (%s): %w", i+1, s.Layer, err)
		}
		run.armed[i] = true
		mergeSteps(rec, part)
	}
	for i, s := range steps {
		if s.AtS <= 0 {
			continue
		}
		run.wg.Add(1)
		go func() {
			defer run.wg.Done()
			select {
			case <-bg.Done():
				run.mu.Lock()
				run.errs[i] = "the run ended before the step's time"
				run.mu.Unlock()
				return
			case <-l.after(time.Duration(s.AtS) * time.Second):
			}
			_, err := l.layers[s.Layer].Arm(bg, stepContext(rc, s))
			run.mu.Lock()
			defer run.mu.Unlock()
			if err != nil {
				run.errs[i] = err.Error()
				l.logger.Warn("compound step did not arm", "run", runID, "step", i+1, "layer", string(s.Layer), "error", err.Error())
				return
			}
			run.armed[i] = true
		}()
	}
	return rec, nil
}

// disarmArmed disarms every armed step of run, in reverse step order.
func (l *compoundLayer) disarmArmed(ctx context.Context, rc *schemas.RunContext, run *compoundRun) error {
	run.mu.Lock()
	defer run.mu.Unlock()
	steps := rc.Probe.Apply.Faults
	var errs []error
	for i := len(steps) - 1; i >= 0; i-- {
		if !run.armed[i] {
			continue
		}
		if err := l.layers[steps[i].Layer].Disarm(ctx, stepContext(rc, steps[i])); err != nil {
			errs = append(errs, fmt.Errorf("step %d (%s): %w", i+1, steps[i].Layer, err))
		}
	}
	return errors.Join(errs...)
}

// Disarm implements schemas.ProbeLayer: pending steps are cancelled and
// armed ones disarmed. A gateway step is disarmed even when it never armed,
// since the slot must not keep another run's plan.
func (l *compoundLayer) Disarm(ctx context.Context, rc *schemas.RunContext) error {
	runID := runIDOf(rc)
	if runID == "" || rc.Probe == nil {
		return errors.New("compound disarm: run context needs a run and a probe")
	}
	l.mu.Lock()
	run := l.runs[runID]
	l.mu.Unlock()
	if run == nil {
		return nil
	}
	run.cancel()
	run.wg.Wait()
	return l.disarmArmed(ctx, rc, run)
}

// Receipts implements schemas.ProbeLayer: each armed step's layer completes
// its own receipt (one part per step); a step that never armed is a part
// whose armed step says why. The parent's steps are the union: yes when any
// part says yes, else no when any says no, else unknown.
func (l *compoundLayer) Receipts(ctx context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.Receipt, error) {
	runID := runIDOf(rc)
	if runID == "" || rc.Probe == nil {
		return nil, errors.New("compound receipts: run context needs a run and a probe")
	}
	l.mu.Lock()
	run := l.runs[runID]
	delete(l.runs, runID)
	l.mu.Unlock()
	rec := startReceipt(rc, schemas.LayerCompound, nil)
	var errs []error
	for i, s := range rc.Probe.Apply.Faults {
		sub := stepContext(rc, s)
		armed, why := false, "arm record not in memory"
		if run != nil {
			run.mu.Lock()
			armed, why = run.armed[i], run.errs[i]
			run.mu.Unlock()
		}
		if !armed {
			part := startReceipt(sub, s.Layer, nil)
			part.Steps[schemas.StepArmed] = schemas.ReceiptEntry{State: schemas.StepNo, Detail: why}
			rec.Parts = append(rec.Parts, *part)
			continue
		}
		part, err := l.layers[s.Layer].Receipts(ctx, sub, ev)
		if err != nil {
			errs = append(errs, fmt.Errorf("step %d (%s): %w", i+1, s.Layer, err))
			continue
		}
		if part != nil {
			if part.Steps[schemas.StepArmed].State == "" {
				part.Steps[schemas.StepArmed] = schemas.ReceiptEntry{State: schemas.StepYes}
			}
			rec.Parts = append(rec.Parts, *part)
		}
	}
	for i := range rec.Parts {
		mergeSteps(rec, &rec.Parts[i])
	}
	return rec, errors.Join(errs...)
}

// stepRank orders states for the union: yes over no over unknown over not
// applicable.
func stepRank(s schemas.StepState) int {
	switch s {
	case schemas.StepYes:
		return 3
	case schemas.StepNo:
		return 2
	case schemas.StepUnknown:
		return 1
	}
	return 0
}

// mergeSteps folds part's steps into rec, keeping the stronger state.
func mergeSteps(rec, part *schemas.Receipt) {
	if part == nil {
		return
	}
	for k, v := range part.Steps {
		cur, ok := rec.Steps[k]
		if !ok || stepRank(v.State) > stepRank(cur.State) {
			if v.Detail != "" {
				v.Detail = string(part.Layer) + ": " + v.Detail
			}
			rec.Steps[k] = v
		}
	}
}
