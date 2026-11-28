package handlers

// PAID-8 (round 4): the comparison plan refused codex on hd-harbor-bfcl
// ("every run would score 0") while the evaluation wizard ran it. The wizard
// derives the task-image variant of the agent (deriveTaskImageAgent,
// evaltaskimage.go); the comparison sent the plain agent to the planner. Now
// both use the one rule: before planning, each arm whose agent can be built
// on each task's image gets the same overrides the wizard adds, and an agent
// that cannot is still refused by the planner with its reason.
//
// Exported name for other lanes (drift monitors): TaskImageArmSet.

import (
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// WithTaskImageAgents lets the comparison plan derive task-image agents like
// the evaluation does. suites and agents may be nil (nothing derived); root
// is the bench root the agent Dockerfiles resolve against.
func (h *ExperimentsHandler) WithTaskImageAgents(suites SuiteSource, agents AgentSource, root func() string) *ExperimentsHandler {
	h.taskImage = &taskImageDeps{suites: suites, agents: agents, root: root}
	return h
}

type taskImageDeps struct {
	suites SuiteSource
	agents AgentSource
	root   func() string
}

// TaskImageArmSet returns the arm overrides that build cfg on each task's own
// image of st for taskIDs (any task when empty), or the plain reason it
// cannot run there. Both nil and "" mean nothing to change. It is the one
// compatibility rule of the wizard, the comparison plan and drift monitors.
func TaskImageArmSet(cfg *schemas.AgentConfig, st *schemas.Suite, taskIDs []string, agents map[string]*schemas.AgentConfig, root string) (map[string]any, string) {
	v := deriveTaskImageAgent(cfg, st, taskIDs, agents, root)
	return v.Set, v.Skip
}

// deriveTaskImageArms adds the task-image overrides to exp's arms in place.
// Any problem loading leaves exp as it is: the planner then says what is
// wrong.
func (h *ExperimentsHandler) deriveTaskImageArms(ctx context.Context, exp *schemas.Experiment) {
	d := h.taskImage
	if d == nil || d.suites == nil || d.agents == nil || exp == nil || len(exp.Arms) == 0 {
		return
	}
	suiteID, _, _ := strings.Cut(exp.Suite, "@")
	st, err := d.suites.LoadSuite(ctx, suiteID)
	if err != nil || st == nil {
		return
	}
	files, err := d.agents.AgentConfigs(ctx)
	if err != nil {
		return
	}
	agents := map[string]*schemas.AgentConfig{}
	for _, f := range files {
		if f.Err == nil && f.Config != nil {
			agents[f.Path] = f.Config
		}
	}
	var taskIDs []string
	for _, c := range exp.Conditions {
		if c.Tasks.All {
			taskIDs = nil
			break
		}
		taskIDs = append(taskIDs, c.Tasks.IDs...)
	}
	root := ""
	if d.root != nil {
		root = d.root()
	}
	for label, arm := range exp.Arms {
		path := firstNonEmpty(arm.Agent, exp.Base)
		set, _ := TaskImageArmSet(agents[path], st, taskIDs, agents, root)
		if set == nil {
			continue
		}
		extra := map[string]any{}
		for k, v := range set {
			if _, mine := arm.Set[k]; !mine { // the user's own override wins
				extra[k] = v
			}
		}
		exp.Arms[label] = withSet(arm, extra)
	}
}
