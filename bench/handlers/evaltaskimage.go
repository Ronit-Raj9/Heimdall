package handlers

// Task-image agents for evaluations. A benchmark whose tasks bring their own
// image and whose verifier grades a snapshot of the agent's container
// (Terminal-Bench, SkillsBench, every imported Harbor suite) only works with
// an agent built on each task's image (build.base_arg); the planner refuses
// any other container agent there, since every run would grade 0. A user
// picks "codex", not "codex-task", so the evaluation derives the task-image
// variant itself: the same agent config with build.base_arg set through arm
// overrides. The arm keeps the agent id the user picked; the variant is
// recorded in the child experiment. An agent that cannot be rebuilt on a
// task image (a remote service, a prebuilt image) skips the benchmark.

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// taskImageBaseArg is the Dockerfile ARG the agent templates and the
// library's task-image agents (codex-task) build FROM.
const taskImageBaseArg = "BASE_IMAGE"

// taskImageSkipReason is the plain reason a benchmark is skipped for an agent
// that cannot run inside a task's own image.
const taskImageSkipReason = "this benchmark runs inside each task's own environment; register a container agent"

// remoteAdapters run outside any container the bench builds.
var remoteAdapters = map[string]bool{"remote_http": true, "a2a": true, "openai_agent": true, "pull": true}

// taskImageVariant is how one arm's agent runs on a benchmark whose tasks
// bring their own image.
type taskImageVariant struct {
	Set  map[string]any // arm overrides that rebuild the agent on each task image; nil when none are needed
	Note string         // one line for the child experiment, empty when nothing changed
	Skip string         // the benchmark cannot run with this agent
}

// needsTaskImageAgent reports whether st grades a snapshot of the agent's
// container on a task (of taskIDs, or any task when empty) that brings its
// own image: the planner's snapshotOwnImageAgents rule.
func needsTaskImageAgent(st *schemas.Suite, taskIDs []string) bool {
	if st == nil || st.Verifier.Mode != schemas.VerifierSnapshot {
		return false
	}
	own := func(t *schemas.Task) bool {
		if t == nil || t.Environment == nil {
			return false
		}
		_, ok, err := utils.TaskImage(st.ID, t)
		return err == nil && ok
	}
	if len(taskIDs) == 0 {
		for _, t := range st.Tasks {
			if own(t) {
				return true
			}
		}
		return false
	}
	for _, id := range taskIDs {
		if own(st.TaskByID(id)) {
			return true
		}
	}
	return false
}

// deriveTaskImageAgent returns the variant of cfg for st. agents holds the
// org's agent configs by path (the library's agents/<id>-task.yaml, when
// present, gives the build section to copy); root resolves the Dockerfile to
// check that it builds FROM the base argument. A nil cfg (not known here) is
// left to the planner.
func deriveTaskImageAgent(cfg *schemas.AgentConfig, st *schemas.Suite, taskIDs []string, agents map[string]*schemas.AgentConfig, root string) taskImageVariant {
	if cfg == nil || !needsTaskImageAgent(st, taskIDs) {
		return taskImageVariant{}
	}
	if cfg.Adapter == scriptedAdapterName || (cfg.Build != nil && cfg.Build.BaseArg != "") {
		return taskImageVariant{}
	}
	if remoteAdapters[cfg.Adapter] || cfg.Image == "" || cfg.Build == nil || cfg.Build.Dockerfile == "" {
		return taskImageVariant{Skip: taskImageSkipReason}
	}
	set := map[string]any{"build.base_arg": taskImageBaseArg}
	b := *cfg.Build
	b.BaseArg = taskImageBaseArg
	variant := "agents/" + cfg.ID + ".yaml with build.base_arg " + taskImageBaseArg
	if lib := agents["agents/"+cfg.ID+"-task.yaml"]; lib != nil && lib.Build != nil && lib.Build.BaseArg != "" && lib.Build.Dockerfile != "" {
		b = *lib.Build
		set = map[string]any{"build.base_arg": b.BaseArg, "build.dockerfile": b.Dockerfile, "build.tag": b.Tag}
		if b.Context != "" {
			set["build.context"] = b.Context
		}
		if lib.Image != "" {
			set["image"] = lib.Image
		}
		variant = "the build of agents/" + lib.ID + ".yaml"
	} else if cfg.Image != b.Tag {
		set["image"] = b.Tag
	}
	if root != "" && !dockerfileUsesArg(filepath.Join(root, b.Dockerfile), b.BaseArg) {
		return taskImageVariant{Skip: taskImageSkipReason + fmt.Sprintf(" (agent %s's Dockerfile %s does not start FROM ${%s})", cfg.ID, b.Dockerfile, b.BaseArg)}
	}
	return taskImageVariant{Set: set, Note: fmt.Sprintf("agent %s is built on each task's own image here (%s)", cfg.ID, variant)}
}

// scriptedAdapterName runs in the task image already.
const scriptedAdapterName = "scripted"

// dockerfileUsesArg reports whether the Dockerfile at path declares ARG name
// before a FROM that uses it. A Dockerfile that cannot be read (hosted
// library, other worker) is given the benefit of the doubt.
func dockerfileUsesArg(path, name string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	declared := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		switch strings.ToUpper(f[0]) {
		case "ARG":
			if n, _, _ := strings.Cut(f[1], "="); n == name {
				declared = true
			}
		case "FROM":
			if declared && (strings.Contains(f[1], "${"+name+"}") || strings.Contains(f[1], "$"+name)) {
				return true
			}
		}
	}
	return false
}

// withSet returns arm with extra overrides merged in.
func withSet(arm schemas.ArmSpec, extra map[string]any) schemas.ArmSpec {
	if len(extra) == 0 {
		return arm
	}
	set := make(map[string]any, len(arm.Set)+len(extra))
	for k, v := range arm.Set {
		set[k] = v
	}
	for k, v := range extra {
		set[k] = v
	}
	arm.Set = set
	return arm
}
