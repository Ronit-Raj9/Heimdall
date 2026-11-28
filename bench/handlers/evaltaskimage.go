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
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"

	benchagents "github.com/maximhq/bifrost/bench/agents"
	agenttemplates "github.com/maximhq/bifrost/bench/agents/templates"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// taskImageBaseArg is the Dockerfile ARG the agent templates and the
// library's task-image agents (codex-task) build FROM.
const taskImageBaseArg = "BASE_IMAGE"

// taskImageSkipReason is the start of the plain reason a benchmark is
// skipped for an agent that cannot run inside a task's own image
// (taskImageSkip gives the whole sentence).
const taskImageSkipReason = "this benchmark runs the agent inside each task's own image"

// taskImageSkip is the one sentence a user reads when agent id cannot run on
// a benchmark whose tasks bring their own image: what is wrong and the two
// ways out (a template that supports it, or base_arg on its build). why, when
// set, is the technical cause, put in brackets.
func taskImageSkip(id, why string) string {
	if why != "" {
		why = " (" + why + ")"
	}
	return fmt.Sprintf("%s and agent %s's image cannot be built on top of it%s: create the agent from a template that supports this (%s), or add base_arg: %s to its build with a Dockerfile that starts FROM ${%s}",
		taskImageSkipReason, id, why, strings.Join(taskImageTemplates(), ", "), taskImageBaseArg, taskImageBaseArg)
}

// taskImageTemplates names the agent builder templates whose agents run on
// a task's own image: a repo template whose shipped agent has a task-image
// variant (<stem>-task.yaml), or a starter whose rendered build sets
// base_arg.
var taskImageTemplates = sync.OnceValue(func() []string {
	var out []string
	for _, t := range agentTemplates {
		switch {
		case strings.HasPrefix(t.source, "repo:"):
			if v := taskVariantConfig(t.origin(), nil); v != nil && v.Build != nil && v.Build.BaseArg != "" {
				out = append(out, t.Name)
			}
		case strings.HasPrefix(t.source, "skeleton:"):
			files, err := agenttemplates.Render(strings.TrimPrefix(t.source, "skeleton:"), "probe", "bench/agent-probe:1")
			if err != nil || len(files) == 0 {
				continue
			}
			var cfg schemas.AgentConfig
			if yaml.Unmarshal(files[0].Content, &cfg) == nil && cfg.Build != nil && cfg.Build.BaseArg != "" {
				out = append(out, t.Name)
			}
		}
	}
	return out
})

// remoteAdapters run outside any container the bench builds.
var remoteAdapters = map[string]bool{"remote_http": true, "a2a": true, "openai_agent": true, "pull": true, "voice_platform": true}

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
		return taskImageVariant{Skip: taskImageSkip(cfg.ID, "")}
	}
	set := map[string]any{"build.base_arg": taskImageBaseArg}
	b := *cfg.Build
	b.BaseArg = taskImageBaseArg
	if lib := taskVariantFor(cfg, agents); lib != nil && lib.Build != nil && lib.Build.BaseArg != "" && lib.Build.Dockerfile != "" {
		b = *lib.Build
		set = map[string]any{"build.base_arg": b.BaseArg, "build.dockerfile": b.Dockerfile, "build.tag": b.Tag}
		if b.Context != "" {
			set["build.context"] = b.Context
		}
		if lib.Image != "" {
			set["image"] = lib.Image
		}
	} else if cfg.Image != b.Tag {
		set["image"] = b.Tag
	}
	if root != "" {
		// The worker resolves the Dockerfile the same way (engine
		// ResolveAgentImageIn): the project, then the library, then the
		// build files shipped with the binary. Missing from all of them, the
		// worker would refuse every slot, so the benchmark is skipped here.
		path, found := utils.AgentBuildPath(root, suite.LibraryDir(), b.Dockerfile)
		if !found {
			return taskImageVariant{Skip: fmt.Sprintf("agent %s cannot run here: its task-image build file %s is not in the organization's project, the library or the built-in agents", cfg.ID, b.Dockerfile)}
		}
		if !dockerfileUsesArg(path, b.BaseArg) {
			return taskImageVariant{Skip: taskImageSkip(cfg.ID, fmt.Sprintf("its Dockerfile does not start FROM ${%s}", b.BaseArg))}
		}
	}
	return taskImageVariant{Set: set, Note: fmt.Sprintf("agent %s is built on each task's own image here", cfg.ID)}
}

// taskVariantFor is the task-image variant cfg runs with: the one of its own
// id, else the one of the agent it was made from (originOf), but only while
// cfg still runs the same tool as that variant: the same adapter and the
// same launch program (the base name of argv[0]). An agent made from the
// Codex template whose command was changed to another program gets none.
func taskVariantFor(cfg *schemas.AgentConfig, agents map[string]*schemas.AgentConfig) *schemas.AgentConfig {
	if v := taskVariantConfig(cfg.ID, agents); v != nil {
		return v
	}
	o := originOf(cfg)
	if o == "" || o == cfg.ID {
		return nil
	}
	if v := taskVariantConfig(o, agents); v != nil && sameTool(cfg, v) {
		return v
	}
	return nil
}

// sameTool reports whether a and b run the same adapter and launch program.
func sameTool(a, b *schemas.AgentConfig) bool {
	return a.Adapter == b.Adapter && len(a.Argv) > 0 && len(b.Argv) > 0 && path.Base(a.Argv[0]) == path.Base(b.Argv[0])
}

// originOf is the agent cfg was made from: its origin field (written by the
// agent builder at creation), else, for an agent created before that field
// existed, a shipped agent with a task-image variant that cfg matches on
// image or build tag, or on adapter, launch program and parser together
// (a copy of the Codex template keeps argv "codex ..." and parser "codex").
func originOf(cfg *schemas.AgentConfig) string {
	if cfg.Origin != "" {
		return cfg.Origin
	}
	entries, err := fs.ReadDir(benchagents.Files, ".")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), "-task.yaml")
		if !ok {
			continue
		}
		b, err := fs.ReadFile(benchagents.Files, id+".yaml")
		if err != nil {
			continue
		}
		var base schemas.AgentConfig
		if yaml.Unmarshal(b, &base) != nil {
			continue
		}
		if base.Image != "" && cfg.Image == base.Image {
			return id
		}
		if cfg.Build != nil && base.Build != nil && base.Build.Tag != "" && cfg.Build.Tag == base.Build.Tag {
			return id
		}
		if cfg.Parser != "" && cfg.Parser == base.Parser && sameTool(cfg, &base) {
			return id
		}
	}
	return ""
}

// taskVariantConfig is the task-image variant agents/<id>-task.yaml of an
// agent: the organization's or the library's (agents holds both), else the
// one shipped with the binary (bench/agents/codex-task.yaml). The shipped
// variants are not listed as agents of their own; a user picks the base id.
func taskVariantConfig(id string, agents map[string]*schemas.AgentConfig) *schemas.AgentConfig {
	if cfg := agents["agents/"+id+"-task.yaml"]; cfg != nil {
		return cfg
	}
	b, err := fs.ReadFile(benchagents.Files, id+"-task.yaml")
	if err != nil {
		return nil
	}
	var cfg schemas.AgentConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil
	}
	return &cfg
}

// scriptedAdapterName runs in the task image already.
const scriptedAdapterName = "scripted"

// dockerfileUsesArg reports whether the Dockerfile at path declares ARG name
// before a FROM that uses it. The caller checks first that the file exists
// (utils.AgentBuildPath); one that exists but cannot be read from here is
// given the benefit of the doubt.
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
