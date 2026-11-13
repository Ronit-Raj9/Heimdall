package gates

import (
	"context"
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// NullAgents is the name of gate G6.
const NullAgents = "g6_null_agents"

var _ schemas.Gate = (*nullAgentsGate)(nil)

// nullKinds are the scripted agents that do nothing useful: noop exits
// without touching the workspace and refuse declines the task.
var nullKinds = map[string]bool{"noop": true, "refuse": true}

// nullAgentsGate is G6. A task that a null agent passes measures nothing, so
// the gate fires at admission and the task is not admitted. It never fires for
// real agents.
type nullAgentsGate struct{}

// GetName implements schemas.Part.
func (g *nullAgentsGate) GetName() string { return NullAgents }

// GetVersion implements schemas.Part.
func (g *nullAgentsGate) GetVersion() string { return version }

// Effect implements schemas.Gate.
func (g *nullAgentsGate) Effect() schemas.GateEffect { return schemas.GateAdmission }

// Baseline implements schemas.Gate.
func (g *nullAgentsGate) Baseline() bool { return true }

// Check implements schemas.Gate. An undecided grade does not fire it; the
// admission check reports the grader problem instead.
func (g *nullAgentsGate) Check(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.GateResult, error) {
	if rc == nil || rc.Agent == nil {
		return nil, errors.New("g6: run context has no agent")
	}
	a := rc.Agent
	if a.Adapter != "scripted" || a.Scripted == nil || !nullKinds[a.Scripted.Kind] {
		return newResult(g, false, "not a null agent", nil), nil
	}
	if ev == nil || ev.Grade == nil || ev.Grade.InvalidEvaluator {
		return newResult(g, false, "null agent "+a.Scripted.Kind+": grade undecided", nil), nil
	}
	if !ev.Grade.Passed {
		return newResult(g, false, "", nil), nil
	}
	detail := fmt.Sprintf("scripted %s agent passed task %s", a.Scripted.Kind, taskID(rc))
	return newResult(g, true, detail, []string{"agent:" + a.ID, "diff_sha256:" + ev.Grade.DiffSHA256}), nil
}

// newNullAgentsGate returns gate G6.
func newNullAgentsGate() *nullAgentsGate {
	return &nullAgentsGate{}
}

// taskID returns the run's task id for messages, or "?".
func taskID(rc *schemas.RunContext) string {
	if rc == nil || rc.Task == nil {
		return "?"
	}
	return rc.Task.ID
}
