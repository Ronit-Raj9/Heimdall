package main

// agent.build on the shared worker (fix wave 3, N181). An org with only the
// shared worker could not prepare an agent's container from the website:
// the shared runner took only issuebench.work jobs. It now also takes
// agent.build, which it runs in process with the org's policy docker, so an
// org-written Dockerfile builds only on the isolated builder.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// sharedJobExecutor sends each runner job kind of the shared worker to its
// executor.
type sharedJobExecutor struct {
	issuebench engine.JobExecutor
	agentBuild engine.JobExecutor
}

// newSharedJobExecutor is the shared worker's job executor: issuebench.work
// to ib, agent.build to build; every other kind is refused.
func newSharedJobExecutor(ib, build engine.JobExecutor) engine.JobExecutor {
	return &sharedJobExecutor{issuebench: ib, agentBuild: build}
}

// RunJob implements engine.JobExecutor.
func (x *sharedJobExecutor) RunJob(ctx context.Context, job *schemas.JobLease, log func(string)) (*engine.JobRun, error) {
	if job != nil {
		switch {
		case job.JobKind == kindAgentBuild && x.agentBuild != nil:
			return x.agentBuild.RunJob(ctx, job, log)
		case job.JobKind == issuebench.WorkJobKind && x.issuebench != nil:
			return x.issuebench.RunJob(ctx, job, log)
		}
	}
	return nil, errors.New("the shared worker refuses the job: it runs only issuebench.work and agent.build jobs; other jobs need your own worker (Settings > Workers)")
}

// sharedAgentBuildExecutor runs agent.build jobs for one org on the shared
// worker, against the org's project copy (root), with docker, the org's
// policy docker.
type sharedAgentBuildExecutor struct {
	docker    utils.Docker
	root      string
	refresh   func(context.Context) error // brings the project copy up to date; nil skips it
	loadSuite func(ctx context.Context, ref string) (*schemas.Suite, error)
}

// newSharedAgentBuildExecutor builds agent images for an org on the shared
// worker: reg loads its benchmarks, root is its project copy.
func newSharedAgentBuildExecutor(docker utils.Docker, reg *schemas.Registry, root string, refresh func(context.Context) error) *sharedAgentBuildExecutor {
	return &sharedAgentBuildExecutor{docker: docker, root: root, refresh: refresh,
		loadSuite: func(ctx context.Context, ref string) (*schemas.Suite, error) { return engine.LoadSuite(ctx, reg, ref) }}
}

// RunJob implements engine.JobExecutor for agent.build: `agent build
// [-config X] [-suite s] agents/<id>.yaml`, the argv agentBuildArgs makes.
func (x *sharedAgentBuildExecutor) RunJob(ctx context.Context, job *schemas.JobLease, log func(string)) (*engine.JobRun, error) {
	if job == nil || job.JobKind != kindAgentBuild {
		return nil, errors.New("this executor runs only agent.build jobs")
	}
	rel, suiteRef, err := parseSharedAgentBuild(job.Argv)
	if err != nil {
		return nil, err
	}
	if x.refresh != nil {
		if err := x.refresh(ctx); err != nil {
			log("the project copy was not refreshed; building from the last copy: " + err.Error())
		}
	}
	agent, err := loadAgentConfig(filepath.Join(x.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	var s *schemas.Suite
	if suiteRef != "" {
		if s, err = x.loadSuite(ctx, suiteRef); err != nil {
			return nil, fmt.Errorf("load benchmark %s: %w", suiteRef, err)
		}
	} else if agent.Build != nil && agent.Build.BaseArg != "" {
		return nil, fmt.Errorf("agent %s builds one container per benchmark; pick the benchmark first", agent.ID)
	}
	lw := &jobLineWriter{log: log}
	tail := utils.NewTailWriter(utils.DefaultTailLines)
	log("preparing the agent container on the shared worker, under your organization's isolated image builder")
	img, info, err := buildAgentImage(ctx, x.docker, x.root, agent, s, io.MultiWriter(lw, tail))
	lw.flush()
	if err != nil {
		if last := tail.String(); last != "" {
			return nil, fmt.Errorf("%w; last build output:\n%s", err, last)
		}
		return nil, err
	}
	// agentBuildResult reads these two lines, as from `agent build`.
	log("tag    " + img.Tag)
	log("image  " + info.ID)
	res, _ := json.Marshal(map[string]string{"tag": img.Tag, "image_id": info.ID, "built_on": "shared worker"})
	return &engine.JobRun{Result: res}, nil
}

// parseSharedAgentBuild reads an agent.build argv: the agent config (a
// .yaml under agents/, inside the project) and the benchmark, if named.
func parseSharedAgentBuild(argv []string) (agent, suiteRef string, err error) {
	if len(argv) < 3 || argv[0] != "agent" || argv[1] != "build" {
		return "", "", errors.New("the shared worker refuses the job: it is not an agent build")
	}
	fs := flag.NewFlagSet("agent build", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("config", "", "")
	suiteFlag := fs.String("suite", "", "")
	pos, err := parseInterspersed(fs, argv[2:])
	if err != nil || len(pos) != 1 {
		return "", "", errors.New("the shared worker refuses the job: want agent build [-suite s] agents/<id>.yaml")
	}
	rel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(pos[0])))
	ext := strings.ToLower(filepath.Ext(rel))
	if !filepath.IsLocal(filepath.FromSlash(rel)) || !strings.HasPrefix(rel, "agents/") || (ext != ".yaml" && ext != ".yml") {
		return "", "", errors.New("the shared worker refuses the job: the agent must be a .yaml agent config under agents/")
	}
	return rel, strings.TrimSpace(*suiteFlag), nil
}

// jobLineWriter sends build output to a job log one line at a time.
type jobLineWriter struct {
	log  func(string)
	mu   sync.Mutex
	part []byte
}

func (w *jobLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.part = append(w.part, p...)
	for {
		i := bytes.IndexByte(w.part, '\n')
		if i < 0 {
			break
		}
		w.log(strings.TrimRight(string(w.part[:i]), "\r"))
		w.part = w.part[i+1:]
	}
	return len(p), nil
}

func (w *jobLineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.part) > 0 {
		w.log(string(w.part))
		w.part = nil
	}
}
