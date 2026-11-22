package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/builder"
	"github.com/maximhq/bifrost/bench/issuebench/emit"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/issuebench/scan"
	"github.com/maximhq/bifrost/bench/issuebench/validate"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
)

// Issues to benchmark: the real stages behind the G06 pipeline. Scan is G01
// on the org's GitHub connection, build is the G02 issuebench.build job (so
// it gets the job's gateway, per-job key and spend accounting), validate is
// G04 with a docker runner, emit is G05 with readiness jobs queued. A missing
// dependency (no GitHub connection, no docker, no model set, no job runner)
// is returned as an error in plain words; the pipeline shows it on the item.
//
// The builder's model comes from BENCH_ISSUEBENCH_PROVIDER and
// BENCH_ISSUEBENCH_MODEL; BENCH_ISSUEBENCH_MAX_USD is the per-build dollar
// cap (default 5) unless a retry raised it.

func init() {
	handlers.IssueBenchStages = issuebenchStagesFor
}

func issuebenchStagesFor(env handlers.OrgEnv) pipeline.Stages {
	root := ""
	if env.Cfg != nil {
		root = env.Cfg.Root
	}
	ib := &ibStages{root: root, orgID: env.Services.OrgID, keys: env.Services.Keys, jobs: env.Services.Jobs}
	return pipeline.Stages{Scanner: ibScanner{ib}, Builder: ibBuilder{ib}, Validator: ibValidator{ib}, Emitter: ibEmitter{ib}}
}

// ibStages is what the four adapters share for one org.
type ibStages struct {
	root  string
	orgID string
	keys  keystore.Store
	jobs  handlers.JobsAPI
	mu    sync.Mutex // one checkout or image build at a time per org
}

func (s *ibStages) dir(parts ...string) string {
	return filepath.Join(append([]string{s.root, handlers.IssueBenchDir}, parts...)...)
}

// Seams the tests replace.
var (
	ibGitHubAPI = ""
	ibCommand   = func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		err := cmd.Run()
		return buf.Bytes(), err
	}
	ibPoll = 2 * time.Second
)

// token is the org's GitHub token: the stored repo connection, else
// GITHUB_TOKEN on a local server.
func (s *ibStages) token(ctx context.Context) (string, error) {
	if s.keys != nil {
		t, _, err := auth.FindRepoToken(ctx, s.keys, s.orgID)
		if err == nil && t != "" {
			return t, nil
		}
		if err != nil && !errors.Is(err, keystore.ErrNotFound) {
			return "", errors.New("the stored GitHub token can not be read")
		}
	}
	if t := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); t != "" {
		return t, nil
	}
	return "", errors.New("GitHub is not connected for this organization: an admin can connect it in Settings > GitHub")
}

type ibScanner struct{ s *ibStages }

func (a ibScanner) Scan(ctx context.Context, repo string, opts pipeline.ScanOptions) ([]issuebench.Candidate, issuebench.Funnel, error) {
	tok, err := a.s.token(ctx)
	if err != nil {
		return nil, issuebench.Funnel{}, err
	}
	o := scan.Options{Repo: repo, MaxPRs: opts.Limit}
	if opts.Since != "" {
		t, err := time.Parse("2006-01-02", opts.Since)
		if err != nil {
			return nil, issuebench.Funnel{}, fmt.Errorf("%w: since must be YYYY-MM-DD", pipeline.ErrBadRequest)
		}
		o.Since = t
	}
	res, err := scan.Scan(ctx, &scan.Client{APIURL: ibGitHubAPI, Token: tok}, o)
	if err != nil {
		return nil, issuebench.Funnel{}, err
	}
	return res.Candidates, res.Funnel, nil
}

// checkout puts repo at sha in dir from a bare mirror kept per repo. The
// token is passed per command and never written into a git config.
func (s *ibStages) checkout(ctx context.Context, repo, sha, dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := ibCommand(ctx, nil, "git", "--version"); err != nil {
		return errors.New("git is not installed on this server")
	}
	tok, err := s.token(ctx)
	if err != nil {
		return err
	}
	hdr := "http.extraheader=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+tok))
	mirror := s.dir("src", strings.ReplaceAll(repo, "/", "__")+".git")
	if _, err := os.Stat(mirror); err != nil {
		if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
			return err
		}
		if out, err := ibCommand(ctx, nil, "git", "-c", hdr, "clone", "--quiet", "--bare", "https://github.com/"+repo+".git", mirror); err != nil {
			return fmt.Errorf("clone %s failed: %s", repo, ibTail(out))
		}
	} else if _, err := ibCommand(ctx, nil, "git", "-C", mirror, "cat-file", "-e", sha+"^{commit}"); err != nil {
		if out, err := ibCommand(ctx, nil, "git", "-C", mirror, "-c", hdr, "fetch", "--quiet", "https://github.com/"+repo+".git", "+refs/heads/*:refs/heads/*", "+refs/pull/*/head:refs/pull/*/head"); err != nil {
			return fmt.Errorf("fetch %s failed: %s", repo, ibTail(out))
		}
	}
	if st, err := os.Stat(filepath.Join(dir, ".git")); err == nil && st.IsDir() {
		if out, err := ibCommand(ctx, nil, "git", "-C", dir, "checkout", "--quiet", "--force", sha); err != nil {
			return fmt.Errorf("check out %s at %s failed: %s", repo, sha, ibTail(out))
		}
		return nil
	}
	_ = os.RemoveAll(dir)
	if out, err := ibCommand(ctx, nil, "git", "clone", "--quiet", "--no-hardlinks", "--no-checkout", mirror, dir); err != nil {
		return fmt.Errorf("copy %s failed: %s", repo, ibTail(out))
	}
	if out, err := ibCommand(ctx, nil, "git", "-C", dir, "checkout", "--quiet", "--force", sha); err != nil {
		return fmt.Errorf("check out %s at %s failed: %s", repo, sha, ibTail(out))
	}
	_, _ = ibCommand(ctx, nil, "git", "-C", dir, "remote", "remove", "origin")
	return nil
}

// Checkout implements pipeline.Checkout.
func (s *ibStages) Checkout(ctx context.Context, repo, sha, dir string) error {
	return s.checkout(ctx, repo, sha, dir)
}

func (s *ibStages) envPath(repo, version string) string {
	return s.dir("envs", strings.ReplaceAll(repo, "/", "__")+"@"+version+".json")
}

func (s *ibStages) saveEnv(env issuebench.EnvSpec) {
	p := s.envPath(env.Repo, env.Version)
	if b, err := json.Marshal(env); err == nil && os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		_ = os.WriteFile(p, b, 0o644)
	}
}

func (s *ibStages) loadEnv(repo, version string) (*issuebench.EnvSpec, bool) {
	b, err := os.ReadFile(s.envPath(repo, version))
	if err != nil {
		return nil, false
	}
	var e issuebench.EnvSpec
	if json.Unmarshal(b, &e) != nil || e.Dockerfile == "" {
		return nil, false
	}
	return &e, true
}

type ibBuilder struct{ s *ibStages }

func (a ibBuilder) Build(ctx context.Context, c issuebench.Candidate, opts pipeline.BuildOptions, logf func(string, ...any)) (issuebench.BuildResult, error) {
	s := a.s
	if s.jobs == nil {
		return issuebench.BuildResult{}, errors.New("this server has no job runner to build environments")
	}
	provider, model := strings.TrimSpace(os.Getenv("BENCH_ISSUEBENCH_PROVIDER")), strings.TrimSpace(os.Getenv("BENCH_ISSUEBENCH_MODEL"))
	if provider == "" || model == "" {
		return issuebench.BuildResult{}, errors.New("no builder model is set: the operator sets BENCH_ISSUEBENCH_PROVIDER and BENCH_ISSUEBENCH_MODEL")
	}
	maxUSD := 5.0
	if v, err := strconv.ParseFloat(os.Getenv("BENCH_ISSUEBENCH_MAX_USD"), 64); err == nil && v > 0 {
		maxUSD = v
	}
	if opts.BudgetUSD > 0 {
		maxUSD = opts.BudgetUSD
	}
	version := pipeline.DefaultVersion(c)
	dir := s.dir("src", strings.ReplaceAll(c.Repo, "/", "__")+"@"+version)
	if err := s.checkout(ctx, c.Repo, c.BaseSHA, dir); err != nil {
		return issuebench.BuildResult{}, err
	}
	params, _ := json.Marshal(issuebenchBuildParams{Repo: c.Repo, Version: version, RepoDir: dir, Candidates: []issuebench.Candidate{c},
		Provider: provider, Model: model, MaxUSD: maxUSD, Consent: true})
	j, err := s.jobs.Create(ctx, jobs.NewJob{OrgID: s.orgID, Kind: kindIssuebenchBuild, Params: params, CreatedBy: "system"})
	if err != nil {
		return issuebench.BuildResult{}, fmt.Errorf("queue the build: %w", err)
	}
	logf("issue #%d: build job %s on %s/%s (cap $%.2f)", c.Issue, j.ID, provider, model, maxUSD)
	for {
		switch j.Status {
		case jobs.StatusSucceeded:
			var res issuebench.BuildResult
			if err := json.Unmarshal(j.Result, &res); err != nil {
				return res, fmt.Errorf("read the build result: %w", err)
			}
			if res.Env != nil {
				res.Env.Repo, res.Env.Version = c.Repo, version
				s.saveEnv(*res.Env)
			}
			return res, nil
		case jobs.StatusFailed, jobs.StatusCancelled:
			msg := j.Error
			if msg == "" {
				msg = string(j.Status)
			}
			return issuebench.BuildResult{}, fmt.Errorf("build job %s: %s", j.ID, msg)
		}
		select {
		case <-ctx.Done():
			_, _ = s.jobs.Cancel(context.WithoutCancel(ctx), s.orgID, j.ID)
			return issuebench.BuildResult{}, ctx.Err()
		case <-time.After(ibPoll):
		}
		if j, err = s.jobs.Get(ctx, s.orgID, j.ID); err != nil {
			return issuebench.BuildResult{}, fmt.Errorf("read build job: %w", err)
		}
	}
}

type ibValidator struct{ s *ibStages }

func (a ibValidator) Validate(ctx context.Context, c issuebench.Candidate, env issuebench.EnvSpec, logf func(string, ...any)) (issuebench.Validation, error) {
	if _, err := ibCommand(ctx, nil, "docker", "version", "--format", "{{.Server.Version}}"); err != nil {
		return issuebench.Validation{}, errors.New("docker is not available on this server, so tests can not run")
	}
	if env.Repo == "" {
		env.Repo = c.Repo
	}
	if env.Version == "" {
		env.Version = pipeline.DefaultVersion(c)
	}
	img, err := a.s.image(ctx, c, env)
	if err != nil {
		return issuebench.Validation{}, err
	}
	logf("issue #%d: running tests in %s", c.Issue, img)
	v, err := (&validate.Validator{Runner: ibRunner{image: img}}).Validate(ctx, &env, c, validate.Options{})
	if err != nil {
		return issuebench.Validation{}, err
	}
	return *v, nil
}

// image builds the environment image once per repo version from the
// Dockerfile the builder wrote, with the base checkout as the context.
func (s *ibStages) image(ctx context.Context, c issuebench.Candidate, env issuebench.EnvSpec) (string, error) {
	if env.ImageRef != "" {
		return env.ImageRef, nil
	}
	if env.Dockerfile == "" {
		return "", errors.New("the environment has no Dockerfile")
	}
	tag := "heimdall-issuebench/" + issuebenchKeyRE.ReplaceAllString(strings.ToLower(c.Repo), "-") + ":" + issuebenchKeyRE.ReplaceAllString(strings.ToLower(env.Version), "-")
	if _, err := ibCommand(ctx, nil, "docker", "image", "inspect", tag); err == nil {
		return tag, nil
	}
	dir := s.dir("src", strings.ReplaceAll(c.Repo, "/", "__")+"@"+env.Version)
	if err := s.checkout(ctx, c.Repo, c.BaseSHA, dir); err != nil {
		return "", err
	}
	if out, err := ibCommand(ctx, []byte(env.Dockerfile), "docker", "build", "--quiet", "-t", tag, "-f", "-", dir); err != nil {
		return "", fmt.Errorf("build the environment image failed: %s", ibTail(out))
	}
	return tag, nil
}

// ibRunner runs one test command in a fresh offline container.
type ibRunner struct{ image string }

const ibPatchFailed = "HEIMDALL_PATCH_FAILED"

func (r ibRunner) Run(ctx context.Context, env *issuebench.EnvSpec, c *issuebench.Candidate, run validate.Run) ([]byte, error) {
	script := ibScript(c, run)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	out, err := ibCommand(cctx, []byte(script), "docker", "run", "--rm", "-i", "--network", "none", "--memory", "4g", "--cpus", "2",
		"--pids-limit", "2048", "--security-opt", "no-new-privileges", "-w", builder.Workdir, "--label", "heimdall.issuebench=validate", r.image, "bash", "-s")
	switch {
	case bytes.Contains(out, []byte(ibPatchFailed)):
		return out, fmt.Errorf("%w: %s", validate.ErrPatchApply, ibTail(out))
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		return out, validate.ErrTimeout
	case err != nil && len(out) == 0:
		return out, fmt.Errorf("docker run: %w", err)
	}
	return out, nil // a failing test command is a result, not an error
}

// ibScript applies the patches the state needs, then runs the command.
func ibScript(c *issuebench.Candidate, run validate.Run) string {
	var b strings.Builder
	apply := func(name, patch string) {
		if strings.TrimSpace(patch) == "" {
			return
		}
		fmt.Fprintf(&b, "echo %s | base64 -d > /tmp/%s.patch\n", base64.StdEncoding.EncodeToString([]byte(patch)), name)
		fmt.Fprintf(&b, "git apply --whitespace=nowarn /tmp/%s.patch || { echo %s %s; exit 97; }\n", name, ibPatchFailed, name)
	}
	switch run.State {
	case validate.StateTests:
		apply("tests", c.TestPatch)
	case validate.StateGold:
		apply("tests", c.TestPatch)
		apply("fix", c.FixPatch)
	}
	b.WriteString(run.Command + "\n")
	return b.String()
}

type ibEmitter struct{ s *ibStages }

func (a ibEmitter) Emit(ctx context.Context, repo string, tasks []issuebench.Validation) (string, error) {
	s := a.s
	if len(tasks) == 0 {
		return "", errors.New("no accepted tasks to add")
	}
	work := s.dir("work", fmt.Sprintf("emit-%d", time.Now().UnixNano()))
	var in []emit.Input
	for _, v := range tasks {
		c := v.Candidate
		env, ok := s.loadEnv(repo, pipeline.DefaultVersion(c))
		if !ok {
			return "", fmt.Errorf("issue #%d: its environment is not on this server any more; retry the issue to build it again", c.Issue)
		}
		dir := filepath.Join(work, "repos", strconv.Itoa(c.Issue))
		if err := s.checkout(ctx, repo, c.BaseSHA, dir); err != nil {
			return "", err
		}
		in = append(in, emit.Input{Validation: v, Env: *env, RepoDir: dir})
	}
	opts := emit.Options{BenchRoot: s.root, WorkDir: work}
	if s.jobs != nil {
		opts.Readiness = pipeline.JobQueuer{Jobs: s.jobs, OrgID: s.orgID}
	}
	rep, err := emit.Emit(ctx, in, opts)
	if err != nil {
		return "", err
	}
	if len(rep.Suites) == 0 {
		return "", errors.New("no benchmark was written")
	}
	return rep.Suites[0].Suite, nil
}

func ibTail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		s = s[len(s)-400:]
	}
	return s
}
