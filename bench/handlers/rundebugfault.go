package handlers

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Debug of fault runs (fix wave 2, row N133). A replay answers every model
// call from the source run's recording, and the calls on which the fault
// proxy injected a model fault are in that recording as fault entries,
// replayed as recorded. So a replay of a run under a model (gateway layer)
// fault reproduces the run without arming any fault. Fork and Debug send
// live calls and always plan the clean condition, and tool or environment
// faults are not in the recording, so those stay refused.

// RunDebugAllowed says which debug actions a run allows (RunDetail
// debug_allowed); the type lives in schemas.
type RunDebugAllowed = schemas.RunDebugAllowed

// debugFaultRule decides whether a run under condition cond may be debugged
// in mode (replay, fork or step). It returns nil when allowed, else the
// refusal text. probe is the condition's probe, nil when unknown.
func debugFaultRule(cond string, probe *schemas.ProbeSpec, mode string) error {
	if cond == "" || cond == schemas.ConditionClean {
		return nil
	}
	name := faultConditionName(cond, probe)
	if mode == schemas.DebugReplay {
		if probe != nil && probe.Layer == schemas.LayerGateway {
			return nil
		}
		return fmt.Errorf("Replay needs a run without a fault or with a model fault: this run ran under %s, which a replay cannot repeat.", name)
	}
	return fmt.Errorf("Fork and Debug need a run without a fault: this run ran under %s. Replay works for model faults only.", name)
}

// faultConditionName is the name of a fault condition in a refusal: its id
// without the "heimdall-" prefix of the generic fault probes. The page shows
// its own readable fault name.
func faultConditionName(cond string, _ *schemas.ProbeSpec) string {
	return cmp.Or(strings.TrimPrefix(cond, "heimdall-"), cond)
}

// conditionProbe loads the probe of a run's condition from its suite, or
// the generic fault probe of that id when the suite cannot be loaded. It is
// nil for the clean condition and for an unknown probe.
func conditionProbe(ctx context.Context, suites SuiteSource, rv *RunView, m *schemas.Manifest) *schemas.ProbeSpec {
	cond := rv.Slot.Condition
	if cond == "" || cond == schemas.ConditionClean {
		return nil
	}
	if suites != nil {
		id := rv.Slot.Suite
		if id == "" && m != nil {
			id = m.Experiment.Suite
		}
		if id != "" {
			if s, err := suites.LoadSuite(ctx, id); err == nil && s != nil {
				return s.ProbeByID(cond)
			}
		}
	}
	return schemas.GenericProbe(cond)
}

// runDebugAllowed is the debug_allowed of a run detail: the same rule the
// replay, fork and debug routes apply. A replay of a replay is refused too
// (its source is the run to replay).
func runDebugAllowed(cond string, probe *schemas.ProbeSpec, sealed bool, debug *schemas.RunDebug) *RunDebugAllowed {
	out := &RunDebugAllowed{}
	replayErr := debugFaultRule(cond, probe, schemas.DebugReplay)
	forkErr := debugFaultRule(cond, probe, schemas.DebugFork)
	out.Replay = replayErr == nil && sealed && (debug == nil || debug.Mode != schemas.DebugReplay)
	out.Fork = forkErr == nil
	switch {
	case replayErr != nil:
		out.Reason = replayErr.Error()
	case forkErr != nil:
		out.Reason = forkReasonShort(cond, probe)
	}
	return out
}

// forkReasonShort is the page line for a run that allows Replay but not
// Fork and Debug.
func forkReasonShort(cond string, probe *schemas.ProbeSpec) string {
	return fmt.Sprintf("Fork and Debug need a run without a fault. This run ran under %s.", faultConditionName(cond, probe))
}
