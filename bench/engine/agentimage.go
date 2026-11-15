package engine

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// AgentImageBuilder builds one agent image (an AgentImage with a Dockerfile)
// and returns its local image id.
type AgentImageBuilder interface {
	BuildAgentImage(ctx context.Context, img AgentImage) (string, error)
}

// DockerAgentImages builds agent images with the docker CLI
// (environments.BuildImage), labelled as bench images. Build output goes to
// Out; nil discards it.
type DockerAgentImages struct {
	Docker utils.Docker
	Out    io.Writer
}

// BuildAgentImage implements AgentImageBuilder.
func (d DockerAgentImages) BuildAgentImage(ctx context.Context, img AgentImage) (string, error) {
	if img.Dockerfile == "" {
		return "", fmt.Errorf("agent image %s has no Dockerfile to build", img.Tag)
	}
	out := d.Out
	if out == nil {
		out = io.Discard
	}
	info, err := environments.BuildImage(ctx, d.Docker, environments.ImageBuild{Dockerfile: img.Dockerfile, Context: img.Context,
		Tag: img.Tag, Args: img.BuildArgs, Labels: map[string]string{utils.LabelBench: "1"}}, out)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

// WithAgentImageBuilder sets how the planner builds the agent images of tasks
// with their own image. Without it the planner uses the suite environment's
// own builder when it has one, else docker when the environment inspects
// images (a docker environment), else builds nothing and warns.
func WithAgentImageBuilder(b AgentImageBuilder) PlannerOption {
	return func(p *Planner) { p.agentImages = b }
}

// agentImageBuilder picks the builder for suite (see WithAgentImageBuilder).
func (p *Planner) agentImageBuilder(suite *schemas.Suite) AgentImageBuilder {
	if p.agentImages != nil {
		return p.agentImages
	}
	env, ok := findPart(p.reg.Environments, suite.Environment)
	if !ok {
		return nil
	}
	if b, ok := any(env).(AgentImageBuilder); ok {
		return b
	}
	if _, ok := any(env).(ImageInspector); ok {
		return DockerAgentImages{}
	}
	return nil
}

// taskAgentImages builds, for every arm whose agent build declares
// build.base_arg and every used task with its own image, the agent image on
// that task image (ResolveTaskAgentImage), and records its tag and id in the
// arm (ArmManifest.TaskImages, TaskImageIDs). A scripted control has no image
// of its own to keep: it stands in the agent's place, so it runs in the task
// image itself, as it does at admission. Any other agent without base_arg
// keeps its own image and records nothing. A build that fails refuses the
// plan: none of that arm's runs of the task could start.
func (p *Planner) taskAgentImages(ctx context.Context, suite *schemas.Suite, conds, compat []condPlan, m *schemas.Manifest) error {
	if !anyBaseArg(m) && !anyScripted(m) {
		return nil
	}
	tags := map[string]string{}
	for _, id := range usedTasks(append(append([]condPlan(nil), conds...), compat...)) {
		t := suite.TaskByID(id)
		if t == nil || t.Environment == nil {
			continue
		}
		img, ok, err := utils.TaskImage(suite.ID, t)
		if err != nil {
			return err
		}
		if ok {
			tags[id] = img.Tag
		}
	}
	if len(tags) == 0 {
		return nil
	}
	var errs []error
	for _, arm := range sortedKeys(m.Arms) {
		am := m.Arms[arm]
		if err := p.buildTaskAgents(ctx, suite, conds, tags, m.TaskImages, &am); err != nil {
			errs = append(errs, fmt.Errorf("arm %s: %w", arm, err))
		}
		m.Arms[arm] = am
	}
	if m.Compatibility != nil {
		if err := p.buildTaskAgents(ctx, suite, compat, tags, m.TaskImages, m.Compatibility); err != nil {
			errs = append(errs, fmt.Errorf("compatibility agent: %w", err))
		}
	}
	return errors.Join(errs...)
}

// scriptedAdapter is the adapter name of the scripted control agents.
const scriptedAdapter = "scripted"

// anyScripted reports whether an arm or the compatibility agent is a scripted
// control.
func anyScripted(m *schemas.Manifest) bool {
	for _, am := range m.Arms {
		if am.Config.Adapter == scriptedAdapter {
			return true
		}
	}
	return m.Compatibility != nil && m.Compatibility.Config.Adapter == scriptedAdapter
}

// anyBaseArg reports whether an arm or the compatibility agent declares
// build.base_arg, the only agents built per task.
func anyBaseArg(m *schemas.Manifest) bool {
	has := func(cfg *schemas.AgentConfig) bool { return cfg.Build != nil && cfg.Build.BaseArg != "" }
	for _, am := range m.Arms {
		if has(&am.Config) {
			return true
		}
	}
	return m.Compatibility != nil && has(&m.Compatibility.Config)
}

// buildTaskAgents builds one arm's per-task agent images for the tasks of
// conds that have their own image (tags: task id -> task image tag;
// taskImageIDs: task id -> the task image's id at plan time).
func (p *Planner) buildTaskAgents(ctx context.Context, suite *schemas.Suite, conds []condPlan, tags, taskImageIDs map[string]string, am *schemas.ArmManifest) error {
	cfg := am.Config
	if cfg.Adapter == scriptedAdapter {
		for _, id := range usedTasks(conds) {
			taskTag, ok := tags[id]
			if !ok {
				continue
			}
			if am.TaskImages == nil {
				am.TaskImages = map[string]string{}
			}
			am.TaskImages[id] = taskTag
			// The task image's id was recorded when the planner built it.
			if imageID := taskImageIDs[id]; imageID != "" {
				if am.TaskImageIDs == nil {
					am.TaskImageIDs = map[string]string{}
				}
				am.TaskImageIDs[id] = imageID
			}
		}
		return nil
	}
	if cfg.Build == nil || cfg.Build.BaseArg == "" {
		return nil
	}
	cfg.Image = cfg.Build.Tag
	b := p.agentImageBuilder(suite)
	for _, id := range usedTasks(conds) {
		taskTag, ok := tags[id]
		if !ok {
			continue
		}
		img, err := ResolveTaskAgentImage(&cfg, suite, suite.TaskByID(id), taskTag, p.benchRoot)
		if err != nil {
			return fmt.Errorf("task %s: %w", id, err)
		}
		if !img.PerTask {
			continue
		}
		if am.TaskImages == nil {
			am.TaskImages = map[string]string{}
		}
		am.TaskImages[id] = img.Tag
		if b == nil {
			p.logger.Warn("no agent image builder; the per-task agent image is not built", "agent", cfg.ID, "task", id, "image", img.Tag)
			continue
		}
		imageID, err := b.BuildAgentImage(ctx, img)
		if err != nil {
			return fmt.Errorf("task %s: build agent image %s: %w", id, img.Tag, err)
		}
		if imageID != "" {
			if am.TaskImageIDs == nil {
				am.TaskImageIDs = map[string]string{}
			}
			am.TaskImageIDs[id] = imageID
		}
	}
	return nil
}

// taskAgentImage is the agent image a slot's run starts, and the id it was
// planned with: the per-task image when the arm recorded one for the task,
// else the arm's own (empty strings).
func taskAgentImage(am *schemas.ArmManifest, task string) (string, string) {
	if am == nil || am.TaskImages[task] == "" {
		return "", ""
	}
	return am.TaskImages[task], am.TaskImageIDs[task]
}

// checkTaskAgentImage refuses a run whose per-task agent image tag no longer
// names the image the manifest recorded, when the environment can inspect
// images. The environment checks the arm's own image itself.
func (p *Pipeline) checkTaskAgentImage(ctx context.Context, st *runState) error {
	want := st.taskImageID
	if want == "" || st.rc.Agent == nil {
		return nil
	}
	ins, ok := any(st.env).(ImageInspector)
	if !ok {
		return nil
	}
	got, err := ins.ImageID(ctx, st.rc.Agent.Image)
	if err != nil {
		return wrapPart("environment", st.env, fmt.Errorf("agent image %s: %w", st.rc.Agent.Image, err))
	}
	if got != want {
		return wrapPart("environment", st.env, fmt.Errorf("agent image %s is now %s but experiment %s was planned with %s; restore that image or plan a new experiment",
			st.rc.Agent.Image, got, st.rc.Manifest.ExpID, want))
	}
	return nil
}
