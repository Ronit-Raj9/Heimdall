package handlers

// Bare-model baseline and image prewarm for evaluations.
//
// The bare model (G4-21) is the same model with no agent scaffolding: the
// shipped agents/bare-model.yaml, one model call with the task text and no
// tools. "Compare with the bare model" in the wizard is an opponent
// evaluation whose arm B is that agent on arm A's model; it runs only where
// the benchmark grades an answer (compat.TaskOK), and is left out with the
// reason elsewhere. An org seeded before the baseline existed gets its
// files written on first use, from the copies built into the server.
//
// Prewarm (G4-04): right after an evaluation's experiment is created, a
// launcher that can (Prewarmer) pulls or builds the images its runs need in
// the background, so the first runs do not wait for them one after another.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	shipped "github.com/maximhq/bifrost/bench/agents"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Prewarmer is optionally implemented by a Launcher that can pull or build
// a planned experiment's images ahead of its runs. Prewarm must return at
// once and do its work in the background; a failure only means the run
// pulls the image itself, as before.
type Prewarmer interface {
	Prewarm(m *schemas.Manifest)
}

// prewarmImages asks l to prewarm m's images when it can.
func prewarmImages(l Launcher, m *schemas.Manifest) {
	if p, ok := l.(Prewarmer); ok && m != nil {
		p.Prewarm(m)
	}
}

// bareModelFiles are the project files of the bare-model baseline, as
// shipped in package agents (paths relative to agents/).
var bareModelFiles = []string{
	utils.BareModelAgent + ".yaml",
	"images/bare-model/Dockerfile",
	"images/bare-model/agent.py",
}

// ensureBareModel writes the bare-model baseline's files into the org's
// project when an arm uses it and the project does not have them yet. A
// file the org already has is never overwritten.
func (h *evaluationsHandler) ensureBareModel(ctx context.Context, spec *schemas.EvaluationSpec) error {
	uses := false
	for _, a := range spec.Arms {
		uses = uses || strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(a.Agent), "agents/"), ".yaml") == utils.BareModelAgent
	}
	store := h.env.Services.Projects
	if !uses || store == nil {
		return nil
	}
	for _, rel := range bareModelFiles {
		if _, _, err := store.Read(ctx, projectfs.AreaAgents, rel); err == nil {
			continue
		} else if !errors.Is(err, projectfs.ErrNotFound) {
			return fmt.Errorf("read agents/%s: %w", rel, err)
		}
		data, err := fs.ReadFile(shipped.Files, path.Clean(rel))
		if err != nil {
			return fmt.Errorf("the bare-model baseline is not built into this server (%s): %w", rel, err)
		}
		if _, err := store.Write(ctx, projectfs.AreaAgents, rel, data, projectfs.WriteOpts{Note: "bare-model baseline"}); err != nil {
			return fmt.Errorf("add agents/%s: %w", rel, err)
		}
	}
	return nil
}

// bareModelArm points the bare-model baseline at the arm's model: Bifrost
// routes "<provider>/<model>", and a model with no provider goes to openai.
func bareModelArm(arm schemas.ArmSpec, agent, provider, model string) schemas.ArmSpec {
	if agent != utils.BareModelAgent || model == "" {
		return arm
	}
	p, m := schemas.SplitProviderModel(model)
	if p == "" {
		p, m = strings.TrimSpace(provider), model
	}
	if p == "" || p == "openai-compatible" {
		p = "openai"
	}
	return withSet(arm, map[string]any{"env.BENCH_MODEL": p + "/" + m})
}
