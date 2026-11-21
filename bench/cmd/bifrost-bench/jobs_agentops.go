package main

// Agent ops job kinds (web parity lane L09): agent.build builds an agent's
// image (local only: it runs docker), agent.diff compares two versions of an
// agent config over the sealed runs. Both call the same code the CLI does,
// in process. agent.check already exists (servejobs.go); its hosted variant
// comes from lane L27. scan.skill and scan.mcp are lane L02's cliJob kinds.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/agentdiff"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/store"
)

const (
	kindAgentBuild = "agent.build"
	kindAgentDiff  = "agent.diff"
)

// agentBuildParams are agent.build's params.
type agentBuildParams struct {
	Agent string `json:"agent"`           // agents/<id>.yaml, relative to the project
	Suite string `json:"suite,omitempty"` // required when the agent builds per suite
}

// agentDiffParams are agent.diff's params (agentdiff.Query).
type agentDiffParams struct {
	Agent     string `json:"agent,omitempty"` // agent id: its two newest config hashes
	A         string `json:"a,omitempty"`
	B         string `json:"b,omitempty"`
	Suite     string `json:"suite,omitempty"`
	Condition string `json:"condition,omitempty"`
	Synthetic bool   `json:"synthetic,omitempty"`
}

// validAgentPath checks an agent config path relative to the project.
func validAgentPath(root, p string) error {
	ext := strings.ToLower(filepath.Ext(p))
	if ext != ".yaml" && ext != ".yml" {
		return errors.New("agent must be a .yaml agent config relative to the project folder, for example agents/codex.yaml")
	}
	if !strings.HasPrefix(filepath.ToSlash(filepath.Clean(p)), "agents/") {
		return errors.New("agent must be under agents/")
	}
	_, err := ops.Confine(root, "agent", p)
	return err
}

func init() {
	registerJobKind(jobKindSpec{
		Kind: kindAgentBuild, Local: true, Hosted: false, // docker on the server: never for hosted orgs
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			var p agentBuildParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			if err := validAgentPath(env.ProjectRoot, p.Agent); err != nil {
				return nil, "", invalid(err)
			}
			if p.Suite != "" {
				if err := ops.ValidID("suite", strings.SplitN(p.Suite, "@", 2)[0]); err != nil {
					return nil, "", invalid(err)
				}
			}
			return p, "agent:" + filepath.ToSlash(filepath.Clean(p.Agent)), nil // one build of an agent at a time
		},
		Run: runAgentBuildJob,
	})
	registerJobKind(jobKindSpec{
		Kind: kindAgentDiff, Local: true, Hosted: false, // hosted orgs use GET /agents/diff
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			var p agentDiffParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			if _, err := p.query().Normalize(); err != nil {
				return nil, "", invalid(err)
			}
			return p, "", nil // read-only
		},
		Run: runAgentDiffJob,
	})
}

func (p agentDiffParams) query() agentdiff.Query {
	return agentdiff.Query{A: p.A, B: p.B, Agent: p.Agent, Suite: p.Suite, Condition: p.Condition, Synthetic: p.Synthetic}
}

func runAgentBuildJob(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p agentBuildParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	path, err := ops.Confine(env.ProjectRoot, "agent", p.Agent) // resolved again right before use
	if err != nil {
		return nil, err
	}
	cfg := env.App.cfg
	agent, err := loadAgentConfig(path)
	if err != nil {
		return nil, err
	}
	s, err := agentSuite(ctx, cfg, agent, p.Suite)
	if err != nil {
		return nil, err
	}
	log.Printf("building agent %s image", agent.ID)
	img, info, err := buildAgentImage(ctx, env.App.docker, cfg.Root, agent, s, log)
	if err != nil {
		return nil, err
	}
	log.Printf("tag    %s", img.Tag)
	log.Printf("image  %s", info.ID)
	for _, d := range info.RepoDigests {
		log.Printf("digest %s", d)
	}
	return map[string]any{"agent": agent.ID, "tag": img.Tag, "image_id": info.ID, "digests": info.RepoDigests}, nil
}

func runAgentDiffJob(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p agentDiffParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	cfg := env.App.cfg
	st, err := store.NewFSStore(cfg.Path(cfg.RunsRoot), nil)
	if err != nil {
		return nil, fmt.Errorf("open run store: %w", err)
	}
	ix := store.NewIndex(st)
	log.Printf("indexing sealed runs")
	if err := ix.Rebuild(ctx); err != nil {
		return nil, fmt.Errorf("index runs: %w", err)
	}
	src, ok := handlers.AgentDiffSource(indexAdapter{ix: ix, root: st.Root()})
	if !ok {
		return nil, errors.New("the run index cannot verify seals")
	}
	report, err := agentdiff.Diff(ctx, src, agentdiff.RootContents(cfg.Root), p.query())
	if err != nil {
		return nil, fmt.Errorf("agent diff: %w", err)
	}
	if _, err := log.Write([]byte(agentDiffText(report))); err != nil {
		return nil, err
	}
	return report, nil
}
