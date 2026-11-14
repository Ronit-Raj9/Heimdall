package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Check plan defaults.
const (
	// CheckArm is the one arm of an agent check manifest.
	CheckArm = "A"
	// checkMetric is the primary metric a check manifest names. A check has no
	// verdict; the field is required by the manifest shape only.
	checkMetric = "verified_success"
	checkPrefix = "check-"
)

// CheckSpec is one agent conformance run (`bifrost-bench agent check`): one
// agent config, one task, clean condition, one trial.
type CheckSpec struct {
	Agent string // agent config path, relative to the bench root or absolute
	Suite string // suite ref, "<id>" or "<id>@<version>"
	Task  string // task id; empty picks the suite's first task
}

// PlanCheck plans an agent check: a one-arm, one-slot manifest through the
// same arm resolution, image resolution, file hashing and gateway preflight
// as Plan. It differs from an experiment in three ways only: one arm is
// enough, suite lock problems are warnings (a company checks its agent while
// its suite is still a draft), and the upstream check is skipped, because a
// check runs on the fault proxy's scripted model and never reaches a
// provider.
func (p *Planner) PlanCheck(ctx context.Context, spec CheckSpec) (*schemas.Manifest, error) {
	if spec.Agent == "" || spec.Suite == "" {
		return nil, errors.New("plan check: agent and suite are required")
	}
	task := spec.Task
	if task == "" {
		s, err := LoadSuite(ctx, p.reg, spec.Suite)
		if err != nil {
			return nil, fmt.Errorf("plan check: %w", err)
		}
		if len(s.TaskIDs) == 0 {
			return nil, fmt.Errorf("plan check: suite %s has no tasks", s.ID)
		}
		task = s.TaskIDs[0]
	}
	e := &schemas.Experiment{
		Name:  checkPrefix + checkName(spec.Agent),
		Suite: spec.Suite,
		Arms:  map[string]schemas.ArmSpec{CheckArm: {Label: "check", Agent: spec.Agent}},
		Conditions: []schemas.ConditionSpec{{
			ID: schemas.ConditionClean, Tasks: schemas.TaskSelector{IDs: []string{task}}, Trials: 1,
		}},
		Primary:     schemas.Endpoint{Condition: schemas.ConditionClean, Metric: checkMetric},
		Seed:        1,
		Concurrency: 1,
	}
	return p.plan(ctx, e, planMode{minArms: 1, lockWarnOnly: true, skipUpstreams: true})
}

// checkName is the agent config's file name without folder and extension.
func checkName(agentPath string) string {
	base := agentPath[strings.LastIndexAny(agentPath, `/\`)+1:]
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	if base == "" {
		return "agent"
	}
	return base
}
