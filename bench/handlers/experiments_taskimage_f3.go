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
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/matrixarms"
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
// A matrix experiment (the New comparison form's two agents) is expanded
// into its arms first, the way the planner expands it, so each arm gets its
// own agent's variant; the matrix stays on exp, and the planner accepts arms
// that differ from the expansion only by these overrides. An arm whose agent
// cannot run on the tasks' images is refused with the evaluation's sentence
// (taskImageSkip). Any problem loading leaves exp as it is: the planner then
// says what is wrong.
func (h *ExperimentsHandler) deriveTaskImageArms(ctx context.Context, exp *schemas.Experiment) error {
	d := h.taskImage
	if d == nil || d.suites == nil || d.agents == nil || exp == nil || (len(exp.Arms) == 0 && exp.Matrix == nil) {
		return nil
	}
	suiteID, _, _ := strings.Cut(exp.Suite, "@")
	st, err := d.suites.LoadSuite(ctx, suiteID)
	if err != nil || st == nil {
		return nil
	}
	files, err := d.agents.AgentConfigs(ctx)
	if err != nil {
		return nil
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
	arms := exp.Arms
	if len(arms) == 0 { // a matrix: the planner refuses a bad one with its own words
		expanded, err := matrixarms.Expand(exp)
		if err != nil || expanded == nil {
			return nil
		}
		arms = expanded.Arms
	}
	out := make(map[string]schemas.ArmSpec, len(arms))
	changed := false
	for label, arm := range arms {
		out[label] = arm
		cfg := agents[agentKey(firstNonEmpty(arm.Agent, exp.Base))]
		set, skip := TaskImageArmSet(cfg, st, taskIDs, agents, root)
		if skip != "" {
			return errors.New(sentence(skip))
		}
		extra := map[string]any{}
		for k, v := range set {
			if _, mine := arm.Set[k]; !mine { // the user's own override wins
				extra[k] = v
			}
		}
		if len(extra) > 0 {
			out[label] = withSet(arm, extra)
			changed = true
		}
	}
	if changed {
		exp.Arms = out
	}
	return nil
}

// agentKey is the agents map key of an arm's agent: a config path as sent,
// or a bare agent id.
func agentKey(ref string) string {
	if ref == "" || strings.Contains(ref, "/") || strings.HasSuffix(ref, ".yaml") {
		return ref
	}
	return "agents/" + ref + ".yaml"
}

// sentence starts s with a capital letter and ends it with a full stop.
func sentence(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[n:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}
