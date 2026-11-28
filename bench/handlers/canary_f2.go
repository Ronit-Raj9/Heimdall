package handlers

// Lane F2: a drift monitor is held to the same rules as the Evaluate
// wizard: the agent must fit the benchmark (evalCompat, the wizard's rule)
// and only tasks that passed the readiness check can be watched.

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// canaryFitProblems says why the agent cannot run the benchmark, or why
// the picked tasks cannot run, in the wizard's words. A server without a
// suite source, a path benchmark ref and an agent config that does not load
// are left to the other checks and the planner.
func canaryFitProblems(ctx context.Context, env OrgEnv, d *canary.Definition) []canary.Problem {
	src := env.Services.Suites
	if src == nil || d.Suite == "" || strings.ContainsAny(d.Suite, `/\`) {
		return nil
	}
	id, _, _ := strings.Cut(d.Suite, "@")
	st, err := src.LoadSuite(ctx, id)
	if err != nil || st == nil {
		return nil
	}
	var out []canary.Problem
	agentID := strings.TrimSuffix(strings.TrimSuffix(path.Base(d.Agent), ".yaml"), ".yml")
	cfg, _ := monitorAgentConfig(ctx, env, agentID)
	if cfg == nil && env.Cfg != nil { // no project store here: the agent file itself
		if data, err := os.ReadFile(filepath.Join(env.Cfg.Root, filepath.FromSlash(d.Agent))); err == nil {
			var c schemas.AgentConfig
			if yaml.Unmarshal(data, &c) == nil {
				cfg = &c
			}
		}
	}
	if cfg != nil {
		if ok, missing, reason := evalCompat(cfg, st); !ok {
			if reason == "" {
				reason = "Agent " + agentID + " cannot run benchmark " + id + ": it lacks " + strings.Join(capNames(missing), ", ") + "."
			}
			out = append(out, canary.Problem{Field: "agent", Message: reason})
		}
	}
	ready := admittedTasks(st, schemas.SplitPublic).IDs
	all := suiteTaskIDs(st)
	switch {
	case len(all) == 0: // only the head loaded: the planner screens the tasks
	case len(ready) == 0:
		out = append(out, canary.Problem{Field: "tasks", Message: "No task of benchmark " + id + " has passed the readiness check yet. Run the readiness check on the benchmark page first."})
	case d.Tasks.All && len(ready) < len(all):
		out = append(out, canary.Problem{Field: "tasks", Message: fmt.Sprintf("Only %d of the %d tasks of benchmark %s are ready. Choose \"Only some tasks\" and pick ready ones.", len(ready), len(all), id)})
	case !d.Tasks.All:
		var notReady []string
		for _, t := range d.Tasks.IDs {
			if !slices.Contains(ready, t) {
				notReady = append(notReady, t)
			}
		}
		if len(notReady) > 0 {
			out = append(out, canary.Problem{Field: "tasks", Message: fmt.Sprintf("These tasks have not passed the readiness check: %s. Pick ready tasks only.", planErrorIDList(notReady))})
		}
	}
	return out
}
