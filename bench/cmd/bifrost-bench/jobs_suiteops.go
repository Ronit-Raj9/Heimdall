package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/taskhealth"
)

// Suite ops job kinds (web parity lane L11):
//
//   - task.from-commit: cut one task from a base and a reference commit of a
//     local repository (local mode) or a connected GitHub repository.
//   - tasks.mine: rank fix commits of a repository and, outside dry-run, cut
//     and admit proven tasks. Dry-run is in-process and runs in both modes;
//     a full mine proves tasks in containers: locally through the CLI, on a
//     hosted server as a bridge experiment on runners (jobs_hostedadmit.go).
//   - suite.quarantine-flaky: the `suite health -auto-quarantine` action:
//     quarantine every task with verifier flakiness evidence.
//
// Admission, lint and lock already exist as suite.admit, suite.lint and
// suite.lock (servejobs.go). Quarantine and restore of one task are plain
// routes (handlers/suiteops.go).

const (
	kindTaskFromCommit       = "task.from-commit"
	kindTasksMine            = "tasks.mine"
	kindSuiteQuarantineFlaky = "suite.quarantine-flaky"
)

// suiteOpsCloneTimeout bounds one git clone of a connected repository.
const suiteOpsCloneTimeout = 5 * time.Minute

// suiteOpsGitHubProvider is the keystore provider of the GitHub token the
// GitHub connect lane (L07) stores as a Kind secret.
const suiteOpsGitHubProvider = "github"

var (
	suiteOpsGitHubRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
	suiteOpsGitRef     = regexp.MustCompile(`^[A-Za-z0-9_./-]{1,200}$`)
)

// suiteOpsRepo names where a task is cut from: exactly one of a local
// repository path (local mode only) or a GitHub "owner/name".
type suiteOpsRepo struct {
	Repo   string `json:"repo,omitempty"`   // local mode: git work tree, absolute or relative to the project folder
	GitHub string `json:"github,omitempty"` // owner/name of a GitHub repository (connected in Settings, or public)
	Branch string `json:"branch,omitempty"` // GitHub: branch to clone; default the repository's default branch
}

type taskFromCommitParams struct {
	Suite        string   `json:"suite"`
	ID           string   `json:"id"`
	Base         string   `json:"base"`
	Reference    string   `json:"reference"`
	Tests        []string `json:"tests,omitempty"`
	License      string   `json:"license,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Difficulty   string   `json:"difficulty,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	Force        bool     `json:"force,omitempty"`
	suiteOpsRepo
}

type tasksMineParams struct {
	Suite    string   `json:"suite"`
	Since    string   `json:"since,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	MinTests int      `json:"min_tests,omitempty"`
	License  string   `json:"license,omitempty"`
	DryRun   bool     `json:"dry_run,omitempty"`
	suiteOpsRepo
}

type quarantineFlakyParams struct {
	Suite string `json:"suite"`
}

func init() {
	registerJobKind(jobKindSpec{Kind: kindTaskFromCommit, Local: true, Hosted: true,
		Validate: validateTaskFromCommit, Run: runTaskFromCommit})
	suiteOpsRegisterReplacing(jobKindSpec{Kind: kindTasksMine, Local: true, Hosted: true,
		Validate: validateTasksMine, Run: runTasksMineJob})
	registerJobKind(jobKindSpec{Kind: kindSuiteQuarantineFlaky, Local: true, Hosted: true,
		Validate: validateQuarantineFlaky, Run: runQuarantineFlaky})
}

// suiteOpsRegisterReplacing registers s, replacing a plain kind of the same
// name another file registered first (L02's generic tasks.mine arg builder):
// this lane's tasks.mine adds connected GitHub repositories and a hosted
// dry-run.
func suiteOpsRegisterReplacing(s jobKindSpec) {
	jobKindsMu.Lock()
	for i, k := range jobKinds {
		if k.Kind == s.Kind {
			jobKinds = append(jobKinds[:i], jobKinds[i+1:]...)
			break
		}
	}
	jobKindsMu.Unlock()
	registerJobKind(s)
}

// suiteOpsBenchRoot is the org's benchmarks folder.
func suiteOpsBenchRoot(env jobEnv) (string, error) {
	if env.App == nil || env.App.cfg == nil {
		return "", errors.New("no project config on this server")
	}
	return env.App.cfg.Path(env.App.cfg.BenchmarksRoot), nil
}

// suiteOpsCheckSuite checks id names a suite folder with a suite.yaml.
func suiteOpsCheckSuite(env jobEnv, id string) (string, error) {
	if err := ops.ValidID("suite", id); err != nil {
		return "", invalid(err)
	}
	root, err := suiteOpsBenchRoot(env)
	if err != nil {
		return "", invalid(err)
	}
	if suite.IsLibrarySuite(root, id) {
		return "", invalid(fmt.Errorf("suite %s: %w", id, schemas.ErrReadOnly)) // built-in library: no org copy
	}
	dir, err := ops.Confine(root, "suite", id)
	if err != nil {
		return "", invalid(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "suite.yaml")); err != nil {
		return "", fmt.Errorf("%w: suite %s has no suite.yaml; create the suite first (Suites, New suite)", jobs.ErrNotFound, id)
	}
	return dir, nil
}

// check validates the repository source for this server mode.
func (r *suiteOpsRepo) check(env jobEnv) error {
	r.Repo, r.GitHub, r.Branch = strings.TrimSpace(r.Repo), strings.TrimSpace(r.GitHub), strings.TrimSpace(r.Branch)
	r.GitHub = strings.TrimSuffix(strings.TrimPrefix(r.GitHub, "https://github.com/"), ".git")
	switch {
	case r.Repo != "" && r.GitHub != "":
		return invalid(errors.New("give either repo (a local folder) or github (owner/name), not both"))
	case r.GitHub != "":
		if !suiteOpsGitHubRepo.MatchString(r.GitHub) || strings.Contains(r.GitHub, "..") {
			return invalid(fmt.Errorf("github %q is not owner/name", r.GitHub))
		}
		if r.Branch != "" && (!suiteOpsGitRef.MatchString(r.Branch) || strings.HasPrefix(r.Branch, "-") || strings.Contains(r.Branch, "..")) {
			return invalid(fmt.Errorf("branch %q is not a valid branch name", r.Branch))
		}
	case r.Branch != "":
		return invalid(errors.New("branch is only for a github repository"))
	case env.Hosted:
		return invalid(errors.New("hosted servers cut tasks from GitHub only: connect GitHub in Settings, then pick a repository"))
	case r.Repo == "":
		r.Repo = "." // the project's own repository
	}
	return nil
}

// checkout returns a local work tree of the source and its cleanup. A
// GitHub repository is cloned into a temporary folder with the org's
// connected token, passed through git's environment config only: it is
// never on a command line, in a log line or in the job params.
func (r suiteOpsRepo) checkout(ctx context.Context, env jobEnv, depth int, log jobs.Logger) (string, func(), error) {
	if r.GitHub == "" {
		dir := r.Repo
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(env.ProjectRoot, dir)
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return "", nil, fmt.Errorf("repo %s is not a folder on this server", r.Repo)
		}
		return dir, func() {}, nil
	}
	token, err := suiteOpsGitHubToken(ctx, env)
	if err != nil {
		return "", nil, err
	}
	tmp, err := os.MkdirTemp("", "heimdall-clone-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	dir := filepath.Join(tmp, "repo")
	args := []string{"clone", "--quiet"}
	if depth > 0 {
		args = append(args, "--depth", strconv.Itoa(depth))
	}
	if r.Branch != "" {
		args = append(args, "--branch", r.Branch)
	}
	args = append(args, "--", "https://github.com/"+r.GitHub+".git", dir)
	cctx, cancel := context.WithTimeout(ctx, suiteOpsCloneTimeout)
	defer cancel()
	base := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	clone := func(env []string) error {
		err := suiteOpsGitClone(cctx, args, env)
		if err != nil {
			for _, line := range strings.Split(strings.TrimSpace(suiteOpsRedact(err.Error(), token)), "\n") {
				if line != "" {
					log.Printf("git: %s", line)
				}
			}
		}
		return err
	}
	if token == "" {
		log.Printf("cloning github.com/%s without a token (GitHub is not connected; only public repositories work)", r.GitHub)
		if err := clone(base); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("clone github.com/%s failed: connect GitHub in Settings for a private repository, or check the name", r.GitHub)
		}
		return dir, cleanup, nil
	}
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	withToken := append(slices.Clone(base), "GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=AUTHORIZATION: basic "+basic)
	log.Printf("cloning github.com/%s with the connected GitHub token", r.GitHub)
	if err := clone(withToken); err != nil {
		// A refused stored token must not block a public repository (p11-2).
		log.Printf("the connected GitHub token was refused; trying without it (public repositories only)")
		_ = os.RemoveAll(dir)
		if err := clone(base); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("Could not copy github.com/%s: the connected GitHub account was refused and the repository is not public. Reconnect GitHub in Settings > GitHub.", r.GitHub)
		}
	}
	return dir, cleanup, nil
}

// suiteOpsGitClone runs git with args and env; its error carries git's
// output. Tests replace it.
var suiteOpsGitClone = func(ctx context.Context, args []string, env []string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// suiteOpsGitHubToken is the org's connected GitHub token, "" when none.
func suiteOpsGitHubToken(ctx context.Context, env jobEnv) (string, error) {
	ks := env.Services.Keys
	if ks == nil {
		return "", nil
	}
	keys, err := ks.List(ctx, env.OrgID, keystore.KindSecret)
	if err != nil {
		return "", nil // no keystore configured: public repositories still work
	}
	for _, k := range keys {
		if strings.EqualFold(k.Provider, suiteOpsGitHubProvider) {
			v, err := ks.Reveal(ctx, env.OrgID, k.ID)
			if err != nil {
				return "", fmt.Errorf("read the connected GitHub token: reconnect GitHub in Settings")
			}
			return v, nil
		}
	}
	return "", nil
}

// suiteOpsRedact removes a secret from text.
func suiteOpsRedact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

func suiteOpsCheckList(what string, vs []string, max int) error {
	if len(vs) > max {
		return invalid(fmt.Errorf("%s: at most %d entries", what, max))
	}
	for _, v := range vs {
		if len(v) > 200 || strings.ContainsAny(v, "\x00\n") {
			return invalid(fmt.Errorf("%s: entry %q is not valid", what, v))
		}
	}
	return nil
}

func validateTaskFromCommit(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p taskFromCommitParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if _, err := suiteOpsCheckSuite(env, p.Suite); err != nil {
		return nil, "", err
	}
	if !scaffold.ValidID(p.ID) {
		return nil, "", invalid(fmt.Errorf("task id %q is not valid: use letters, digits, '-' and '_'", p.ID))
	}
	p.Base, p.Reference = strings.TrimSpace(p.Base), strings.TrimSpace(p.Reference)
	for what, v := range map[string]string{"base": p.Base, "reference": p.Reference} {
		if v == "" {
			return nil, "", invalid(fmt.Errorf("%s commit is required", what))
		}
		if !suiteOpsGitRef.MatchString(v) || strings.HasPrefix(v, "-") {
			return nil, "", invalid(fmt.Errorf("%s %q is not a commit, tag or branch", what, v))
		}
	}
	if err := suiteOpsCheckList("tests", p.Tests, 50); err != nil {
		return nil, "", err
	}
	if err := suiteOpsCheckList("capabilities", p.Capabilities, 50); err != nil {
		return nil, "", err
	}
	switch p.Difficulty {
	case "", "easy", "medium", "hard":
	default:
		return nil, "", invalid(errors.New("difficulty must be easy, medium or hard"))
	}
	switch p.Kind {
	case "", "bugfix", "feature", "refactor", "performance":
	default:
		return nil, "", invalid(errors.New("kind must be bugfix, feature, refactor or performance"))
	}
	if len(p.License) > 100 {
		return nil, "", invalid(errors.New("license is too long"))
	}
	if err := p.suiteOpsRepo.check(env); err != nil {
		return nil, "", err
	}
	return p, "suite:" + p.Suite, nil
}

func runTaskFromCommit(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p taskFromCommitParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	suiteDir, err := suiteOpsCheckSuite(env, p.Suite)
	if err != nil {
		return nil, err
	}
	repo, cleanup, err := p.suiteOpsRepo.checkout(ctx, env, 0, log)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	license := p.License
	if license == "" {
		license = "proprietary"
	}
	kind := p.Kind
	if kind == "" {
		kind = "bugfix"
	}
	opts := scaffold.TaskInitOptions{SuiteDir: suiteDir, ID: p.ID, Repo: repo, Base: p.Base, Reference: p.Reference,
		Tests: p.Tests, License: license, Capabilities: p.Capabilities, Difficulty: p.Difficulty, Kind: kind, Force: p.Force}
	if p.GitHub == "" {
		opts.ExcludeDir = env.ProjectRoot
	}
	log.Printf("cutting %s/%s: base %s, reference %s", p.Suite, p.ID, p.Base, p.Reference)
	res, err := scaffold.InitTask(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("task init: %v", err)
	}
	log.Printf("wrote %s (template %s)", res.Dir, orUnknown(res.Template))
	log.Printf("hidden tests (verifier/tests): %s", strings.Join(res.Hidden, ", "))
	for _, w := range res.Warnings {
		log.Warnf("%s", w)
	}
	findings, _ := suite.ScanSecrets(res.Dir)
	for _, f := range findings {
		log.Warnf("%s; remove it before admitting", f)
	}
	log.Printf("next: rewrite instruction.md as the request a teammate would send (Edit tab), then Admit %s", p.ID)
	return map[string]any{"suite": p.Suite, "task": p.ID, "template": res.Template, "hidden": res.Hidden,
		"warnings": len(res.Warnings) + len(findings), "suite_edited": res.SuiteEdited}, nil
}

func validateTasksMine(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p tasksMineParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if _, err := suiteOpsCheckSuite(env, p.Suite); err != nil {
		return nil, "", err
	}
	if p.Limit == 0 {
		p.Limit = 5
	}
	if p.Limit < 1 || p.Limit > 50 {
		return nil, "", invalid(errors.New("limit must be 1 to 50"))
	}
	if p.MinTests == 0 {
		p.MinTests = 1
	}
	if p.MinTests < 1 || p.MinTests > 20 {
		return nil, "", invalid(errors.New("min_tests must be 1 to 20"))
	}
	p.Since = strings.TrimSpace(p.Since)
	if p.Since != "" && (!suiteOpsGitRef.MatchString(p.Since) || strings.HasPrefix(p.Since, "-")) {
		return nil, "", invalid(fmt.Errorf("since %q is not a commit, tag, branch or YYYY-MM-DD date", p.Since))
	}
	if err := suiteOpsCheckList("paths", p.Paths, 20); err != nil {
		return nil, "", err
	}
	if len(p.License) > 100 {
		return nil, "", invalid(errors.New("license is too long"))
	}
	if env.Hosted && !p.DryRun {
		// G04: the hosted full mine proves the cuts on a runner with the
		// oracle and noop controls of the suite.admit bridge.
		for _, a := range [][2]string{{"oracle", bridgeOracleAgent}, {"noop", bridgeNoopAgent}} {
			if err := ensureControlAgent(env.ProjectRoot, a[1]); err != nil {
				return nil, "", err
			}
			if err := validAgentFile(env.ProjectRoot, a[0], a[1]); err != nil {
				return nil, "", invalid(fmt.Errorf("%v (hosted mining proves tasks with it)", err))
			}
		}
	}
	if err := p.suiteOpsRepo.check(env); err != nil {
		return nil, "", err
	}
	target := "suite:" + p.Suite
	if p.DryRun {
		target = "" // writes nothing
	}
	return p, target, nil
}

func runTasksMineJob(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p tasksMineParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	suiteDir, err := suiteOpsCheckSuite(env, p.Suite)
	if err != nil {
		return nil, err
	}
	repo, cleanup, err := p.suiteOpsRepo.checkout(ctx, env, scaffold.MineDefaultScan+1, log)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if p.DryRun {
		return suiteOpsMinePreview(ctx, env, p, suiteDir, repo, log)
	}
	if env.Hosted {
		return runHostedTasksMine(ctx, env, j, p, suiteDir, repo, log)
	}
	// The server's resolved config (a redacted copy, runCLI), never the
	// project's deploy/bench.yaml, which may be absent or another server's.
	argv := []string{"tasks", "mine", "-config", cliConfig, "-suite", p.Suite, "-repo", repo,
		"-limit", strconv.Itoa(p.Limit), "-min-tests", strconv.Itoa(p.MinTests)}
	if p.Since != "" {
		argv = append(argv, "-since", p.Since)
	}
	if len(p.Paths) > 0 {
		argv = append(argv, "-paths", strings.Join(p.Paths, ","))
	}
	if p.License != "" {
		argv = append(argv, "-license", p.License)
	}
	if _, err := runCLI(ctx, env, argv, log); err != nil {
		return nil, err
	}
	return map[string]any{"suite": p.Suite, "dry_run": false}, nil
}

// suiteOpsMineCandidate is one ranked commit of a dry run.
type suiteOpsMineCandidate struct {
	Commit  string `json:"commit"`
	Subject string `json:"subject"`
	Area    string `json:"area"`
	Lines   int    `json:"lines"`
	Files   int    `json:"files"`
}

// suiteOpsMinePreview ranks candidates in process; it writes nothing.
func suiteOpsMinePreview(ctx context.Context, env jobEnv, p tasksMineParams, suiteDir, repo string, log jobs.Logger) (any, error) {
	suiteYAML, err := os.ReadFile(filepath.Join(suiteDir, "suite.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read suite %s: %v", p.Suite, err)
	}
	tmpl := scaffold.DetectTemplate(suiteYAML)
	globs := scaffold.DefaultTestGlobs(tmpl)
	if len(globs) == 0 {
		return nil, fmt.Errorf("suite %s: template %q has no test globs; tasks mine needs python-pytest, node-test, go-test or generic-reward", p.Suite, tmpl)
	}
	cands, skipped, err := scaffold.MineCandidates(ctx, scaffold.MineOptions{
		Repo: repo, Since: p.Since, Paths: p.Paths, MinTests: p.MinTests, TestGlobs: globs,
		NeedBaseTests: suiteGrader(suiteYAML) == "hidden_tests",
	})
	if err != nil {
		return nil, fmt.Errorf("tasks mine: %v", err)
	}
	log.Printf("looked at %d commits: %d candidates, %d skipped (suite %s, template %s)", len(cands)+len(skipped), len(cands), len(skipped), p.Suite, tmpl)
	out := []suiteOpsMineCandidate{}
	for i, c := range cands {
		if i >= p.Limit*4 {
			break
		}
		log.Printf("%2d. %s %-12s %3d lines %d files  %s", i+1, c.ShortCommit(), c.Area, c.Lines, len(c.Source)+len(c.Tests), c.Subject)
		out = append(out, suiteOpsMineCandidate{Commit: c.Commit, Subject: c.Subject, Area: c.Area, Lines: c.Lines, Files: len(c.Source) + len(c.Tests)})
	}
	if len(cands) == 0 {
		log.Printf("no candidates: widen since or paths, or lower min tests")
	}
	return map[string]any{"suite": p.Suite, "dry_run": true, "candidates": out, "skipped": len(skipped)}, nil
}

func validateQuarantineFlaky(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p quarantineFlakyParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if _, err := suiteOpsCheckSuite(env, p.Suite); err != nil {
		return nil, "", err
	}
	return p, "suite:" + p.Suite, nil
}

func runQuarantineFlaky(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p quarantineFlakyParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	root, err := suiteOpsBenchRoot(env)
	if err != nil {
		return nil, err
	}
	s, err := loadSuite(ctx, root, p.Suite)
	if err != nil {
		return nil, fmt.Errorf("load suite %s: %v", p.Suite, err)
	}
	src := serveHealthSource(env.App.cfg)
	if src == nil {
		return nil, errors.New("no run store yet: run an experiment on this suite first, then check its health")
	}
	f, err := suite.ReadHealthFile(s.Root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %v", schemas.SuiteHealthFileName, err)
	}
	already := f.Quarantined
	h, err := taskhealth.Compute(ctx, src, s, already, taskhealth.Options{})
	if err != nil {
		return nil, fmt.Errorf("suite health %s: %v", s.ID, err)
	}
	var buf bytes.Buffer
	if err := autoQuarantine(s, h, already, &buf); err != nil {
		return nil, err
	}
	var added []string
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		log.Printf("%s", sc.Text())
		added = append(added, sc.Text())
	}
	flaky := taskhealth.FlakyTasks(h)
	if len(flaky) == 0 {
		log.Printf("no task of %s has verifier flakiness evidence; nothing quarantined (difficulty and agent variance never quarantine)", s.ID)
	}
	return map[string]any{"suite": s.ID, "flaky": len(flaky), "quarantined": len(added)}, nil
}
