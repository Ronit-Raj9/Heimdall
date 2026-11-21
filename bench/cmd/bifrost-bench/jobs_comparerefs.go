package main

// Gap lane G07: experiment.compare-refs, the web form of
// `compare --baseline <ref> --candidate <ref> --base agents/<agent>.yaml`.
// It reads the agent's files (notes, skills, the committed agent config) at
// both refs, either from a GitHub repository the org connected (hosted, no
// git needed on the server) or from the project's own git repository
// (local), writes experiments/<name>.yaml and its fixtures, then plans,
// freezes and starts the experiment through the org's Launcher, so hosted
// runs go to the org's runners like any other experiment.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/quickstart"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
)

const kindExperimentCompareRefs = "experiment.compare-refs"

// compareRefsParams are experiment.compare-refs' params.
type compareRefsParams struct {
	Agent     string   `json:"agent"`           // agents/<id>.yaml: the base both arms start from
	RefA      string   `json:"ref_a"`           // baseline branch, tag or commit
	RefB      string   `json:"ref_b"`           // candidate
	Suite     string   `json:"suite"`           // id@version
	Repo      string   `json:"repo,omitempty"`  // owner/name on GitHub; "" = the project's git repository (local only)
	Tasks     []string `json:"tasks,omitempty"` // task ids; default every task of the suite
	Trials    int      `json:"trials,omitempty"`
	BudgetUSD float64  `json:"budget_usd,omitempty"` // required unless both arms are scripted
	Paths     []string `json:"paths,omitempty"`      // agent files and folders; default scaffold.DefaultAgentPaths
	Name      string   `json:"name,omitempty"`
	Policy    string   `json:"policy,omitempty"` // default policies/default.yaml when it exists, else none
	NoStart   bool     `json:"no_start,omitempty"`
}

// compareRefsResult is experiment.compare-refs' result.
type compareRefsResult struct {
	followUpResult
	Subject string `json:"subject"`
	Repo    string `json:"repo,omitempty"`
}

// githubTreeReader is what compare-refs needs from GitHub; tests fake it.
type githubTreeReader interface {
	Tree(ctx context.Context, token, owner, repo, ref string) (string, []auth.GitHubTreeEntry, error)
	Blob(ctx context.Context, token, owner, repo, sha string) ([]byte, error)
}

// compareRefsGitHub builds the GitHub reader and token of an org; a var so
// tests can swap it.
var compareRefsGitHub = func(ctx context.Context, env jobEnv) (githubTreeReader, string, error) {
	cfg, err := auth.ConfigFrom(env.App.cfg, "")
	if err != nil {
		return nil, "", err
	}
	conn := auth.NewGitHubRepoConnector(cfg)
	if !conn.Configured() {
		return nil, "", errors.New("GitHub is not configured on this server (no OAuth app); type the refs of the project's own repository with the CLI, or ask the server owner to set the GitHub app")
	}
	tok, _, err := auth.FindRepoToken(ctx, env.Services.Keys, env.OrgID)
	if errors.Is(err, keystore.ErrNotFound) {
		return nil, "", errors.New("GitHub is not connected for this organization: connect it in Settings > GitHub, then pick the repository")
	}
	if err != nil {
		return nil, "", fmt.Errorf("read the GitHub token: %w", err)
	}
	return conn, tok, nil
}

func init() {
	registerJobKind(jobKindSpec{
		Kind: kindExperimentCompareRefs, Local: true, Hosted: true,
		Opts:     []jobs.KindOption{jobs.WithMaxAttempts(1)},
		Validate: validateCompareRefsJob,
		Run:      runCompareRefsJob,
	})
}

func validateCompareRefsJob(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p compareRefsParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if p.Trials == 0 {
		p.Trials = quickstart.DefaultTrials
	}
	if p.Trials < 1 || p.Trials > maxFollowUpTrials {
		return nil, "", invalid(fmt.Errorf("trials must be between 1 and %d", maxFollowUpTrials))
	}
	if p.Agent == "" || p.RefA == "" || p.RefB == "" || p.Suite == "" {
		return nil, "", invalid(errors.New("agent, ref_a, ref_b and suite are required"))
	}
	if !strings.HasSuffix(p.Agent, ".yaml") && !strings.HasSuffix(p.Agent, ".yml") {
		return nil, "", invalid(errors.New("agent must be an agent config file such as agents/<id>.yaml"))
	}
	if _, err := ops.Confine(env.App.cfg.Root, "agent", p.Agent); err != nil {
		return nil, "", invalid(err)
	}
	for _, r := range []string{p.RefA, p.RefB} {
		if !auth.ValidGitRef(r) {
			return nil, "", invalid(fmt.Errorf("%q is not a branch, tag or commit name", r))
		}
	}
	if p.RefA == p.RefB {
		return nil, "", invalid(errors.New("ref_a and ref_b are the same ref; pick two different refs"))
	}
	if !modelRefRE.MatchString(p.Suite) {
		return nil, "", invalid(fmt.Errorf("suite %q is not a suite id", p.Suite))
	}
	if p.Repo != "" {
		owner, name, ok := strings.Cut(p.Repo, "/")
		if !ok || !auth.ValidRepo(owner, name) {
			return nil, "", invalid(errors.New("repo must be owner/name of a GitHub repository"))
		}
	} else if env.Hosted {
		return nil, "", invalid(errors.New("pick a GitHub repository (connect GitHub in Settings > GitHub): a hosted project has no git history of its own"))
	}
	for _, id := range p.Tasks {
		if err := ops.ValidID("task", id); err != nil {
			return nil, "", invalid(err)
		}
	}
	if len(p.Tasks) > maxFollowUpTasks {
		return nil, "", invalid(fmt.Errorf("at most %d tasks", maxFollowUpTasks))
	}
	for _, pth := range p.Paths {
		if pth == "" || strings.HasPrefix(pth, "/") || strings.Contains(pth, "..") {
			return nil, "", invalid(fmt.Errorf("path %q must be relative to the repository root", pth))
		}
	}
	if p.BudgetUSD < 0 {
		return nil, "", invalid(errors.New("budget_usd cannot be negative"))
	}
	if p.Policy != "" && p.Policy != "none" {
		if _, err := ops.Confine(env.App.cfg.Root, "policy", p.Policy); err != nil {
			return nil, "", invalid(err)
		}
	}
	if p.Name != "" && slug(p.Name) == "" {
		return nil, "", invalid(errors.New("name needs letters or digits"))
	}
	return p, "compare-refs:" + slug(p.Agent+"-"+p.RefA+"-"+p.RefB), nil
}

func runCompareRefsJob(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p compareRefsParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	a := env.App
	if env.Services.Launcher == nil {
		return nil, errors.New("this server has no experiment launcher; start it with a database (serve) to run experiments from the web")
	}
	name := p.Name
	if name == "" {
		name = "compare-refs-" + time.Now().UTC().Format("20060102-150405")
	}
	paths := p.Paths
	if len(paths) == 0 {
		paths = scaffold.DefaultAgentPaths
	}
	policy := p.Policy
	if policy == "" {
		policy = "none"
		if _, err := os.Stat(a.cfg.Path(quickstart.DefaultPolicy)); err == nil {
			policy = quickstart.DefaultPolicy
		}
	}
	f := compareFlags{
		baseline: p.RefA, candidate: p.RefB, base: p.Agent, suite: p.Suite, trials: p.Trials,
		budget: p.BudgetUSD, name: name, policy: policy, concurrency: 2, paths: strings.Join(paths, ","),
		repo:    a.cfg.Root,
		command: fmt.Sprintf("web job %s (%s vs %s of %s)", kindExperimentCompareRefs, p.RefA, p.RefB, p.Agent),
	}
	if p.Repo != "" {
		owner, repo, _ := strings.Cut(p.Repo, "/")
		gh, tok, err := compareRefsGitHub(ctx, env)
		if err != nil {
			return nil, err
		}
		f.export = func(ctx context.Context, ref string, paths []string, dst string) (*scaffold.ExportedRef, error) {
			log.Printf("reading agent files of %s at %s from GitHub", p.Repo, ref)
			commit, tree, err := gh.Tree(ctx, tok, owner, repo, ref)
			if err != nil {
				return nil, err
			}
			entries := make([]scaffold.RemoteEntry, 0, len(tree))
			for _, e := range tree {
				entries = append(entries, scaffold.RemoteEntry{Path: e.Path, Mode: e.Mode, Type: e.Type, SHA: e.SHA})
			}
			return scaffold.ExportRemoteTree(ctx, ref, commit, entries, paths, dst, func(ctx context.Context, sha string) ([]byte, error) {
				return gh.Blob(ctx, tok, owner, repo, sha)
			})
		}
	} else {
		log.Printf("reading agent files at %s and %s from the project's git repository", p.RefA, p.RefB)
	}
	progress(map[string]any{"phase": "exporting"})
	setup, err := buildCompare(ctx, f, a.cfg)
	if err != nil {
		if errors.Is(err, errNoAgentChange) {
			return nil, fmt.Errorf("%v (pick refs whose agent files differ)", err)
		}
		return nil, err
	}
	fixtures := filepath.Join(a.cfg.Root, "experiments", "fixtures", quickstart.ExperimentName(name))
	cleanup := func() {
		_ = os.Remove(setup.path)
		_ = os.RemoveAll(fixtures)
	}
	log.Printf("comparing %s", setup.subject)
	e, err := loadExperiment(setup.path)
	if err != nil {
		cleanup()
		return nil, err
	}
	if len(p.Tasks) > 0 {
		for i := range e.Conditions {
			e.Conditions[i].Tasks = schemas.TaskSelector{IDs: append([]string(nil), p.Tasks...)}
		}
		raw, err := experimentYAML(e)
		if err == nil {
			err = os.WriteFile(setup.path, raw, 0o644)
		}
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("write %s: %w", setup.rel, err)
		}
	}
	res, err := startWritten(ctx, env, e, setup.rel, p.NoStart, log, progress)
	if err != nil {
		if res == nil { // nothing was frozen: free the name for a retry
			cleanup()
		}
		return nil, err
	}
	return compareRefsResult{followUpResult: *res, Subject: setup.subject, Repo: p.Repo}, nil
}

// startWritten plans, freezes and starts an experiment whose file is
// already written at rel. A non-nil result with an error means the
// experiment was frozen but not started.
func startWritten(ctx context.Context, env jobEnv, e *schemas.Experiment, rel string, noStart bool, log jobs.Logger, progress func(any)) (*followUpResult, error) {
	a, l := env.App, env.Services.Launcher
	if err := a.validatePolicy(e); err != nil {
		return nil, fmt.Errorf("policy %s: %w", e.Policy, err)
	}
	progress(map[string]any{"phase": "planning"})
	m, err := l.Plan(ctx, e)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	slots := countedSlots(m)
	log.Printf("planned %s: %d counted runs over %d arm(s)", m.ExpID, slots, len(m.Experiment.Arms))
	if err := l.Create(ctx, m); err != nil {
		if errors.Is(err, schemas.ErrExists) {
			return nil, fmt.Errorf("experiment %s already exists: open it from the experiments list, or pick another name", m.ExpID)
		}
		return nil, fmt.Errorf("freeze %s: %w", m.ExpID, err)
	}
	res := &followUpResult{ExpID: m.ExpID, File: rel, Slots: slots}
	if !noStart {
		if err := l.Start(ctx, m.ExpID); err != nil {
			return res, fmt.Errorf("start %s: %w (the experiment is frozen; start it from its page)", m.ExpID, err)
		}
		res.Started = true
		log.Printf("started %s", m.ExpID)
	}
	progress(map[string]any{"phase": "done", "exp_id": m.ExpID})
	return res, nil
}
