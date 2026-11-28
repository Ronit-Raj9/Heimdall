package main

// Lane T4 (round7/T_RULES.md): repotask.build cuts one task from a commit
// pair of a public git repository the user typed on the website, then runs
// admission for that one task. No model is used. The address is checked
// before anything runs: https only, no credential in it, no local or
// private host (the modelswitch.CheckGatewayURL rule), and every address the
// host resolves to must be public. The clone runs with git's file, ssh and
// ext protocols off and redirects refused, through suiteOpsGitClone, the
// same clone path task.from-commit and tasks.mine use. Admission decides:
// the job never marks a task good itself.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/modelswitch"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

const kindRepoTaskBuild = "repotask.build"

func init() {
	registerJobKind(jobKindSpec{Kind: kindRepoTaskBuild, Local: true, Hosted: true, Opts: []jobs.KindOption{jobs.WithMaxAttempts(1)},
		Validate: validateRepoTask, Run: runRepoTask})
}

type repoTaskParams struct {
	RepoURL   string  `json:"repo_url"`
	Base      string  `json:"base"`
	Reference string  `json:"reference"`
	Suite     string  `json:"suite"`
	ID        string  `json:"id,omitempty"`
	Template  string  `json:"template,omitempty"`
	Model     string  `json:"model,omitempty"`
	BudgetUSD float64 `json:"budget_usd,omitempty"`
}

// repoTaskResult is the job's result: what was cut and what admission said.
type repoTaskResult struct {
	Suite    string                   `json:"suite"`
	Task     string                   `json:"task"`
	Template string                   `json:"template"`
	Hidden   []string                 `json:"hidden"`
	Admitted bool                     `json:"admitted"`
	Reason   string                   `json:"reason,omitempty"`
	Controls []schemas.AdmissionCheck `json:"controls"`
	ExpID    string                   `json:"exp_id,omitempty"`
	Note     string                   `json:"note"`
}

// repoTaskLookup resolves a host; tests replace it.
var repoTaskLookup = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// repoTaskAdmit runs admission for one task and returns its checks; tests
// replace it at this interface. Hosted: the readiness controls on a worker
// (the tasks.mine bridge). Local: the CLI's task admit.
var repoTaskAdmit = func(ctx context.Context, env jobEnv, j *jobs.Job, suiteID, id string, log jobs.Logger) (hostedAdmitTask, string, error) {
	if env.Hosted {
		return repoTaskAdmitHosted(ctx, env, j, suiteID, id, log)
	}
	_, err := runCLI(ctx, env, []string{"task", "admit", "-config", cliConfig, suiteID + "/" + id}, log)
	t := hostedAdmitTask{Task: id, Admitted: err == nil}
	if err != nil {
		t.Reason = err.Error()
	}
	return t, "", nil
}

// repoTaskMaxBytes caps the cloned working copy.
const repoTaskMaxBytes = 200 << 20

// checkRepoURL refuses anything but a public https git address.
func checkRepoURL(ctx context.Context, raw string) (*url.URL, error) {
	u, _, err := checkRepoURLPin(ctx, raw)
	return u, err
}

// checkRepoURLPin also returns the checked address git is pinned to, so a
// second lookup by git cannot point elsewhere.
func checkRepoURLPin(ctx context.Context, raw string) (*url.URL, string, error) {
	u, err := modelswitch.CheckGatewayURL(raw, true)
	if err != nil {
		msg := strings.TrimPrefix(err.Error(), modelswitch.ErrGatewayRefused.Error()+": ")
		msg = strings.ReplaceAll(strings.ReplaceAll(msg, "gateway's ", "repository's "), "gateway", "repository")
		return nil, "", invalid(errors.New(msg))
	}
	if u.RawQuery != "" || u.Fragment != "" || u.Port() != "" && u.Port() != "443" {
		return nil, "", invalid(errors.New("give the repository's plain https address, without a query or a port"))
	}
	if strings.Contains(u.Path, "..") || strings.HasPrefix(u.Path, "/-") {
		return nil, "", invalid(errors.New("the repository address is not valid"))
	}
	pin := ""
	if repoTaskLookup != nil {
		ips, err := repoTaskLookup(ctx, u.Hostname())
		if err != nil || len(ips) == 0 {
			return nil, "", invalid(fmt.Errorf("the host %s could not be found", u.Hostname()))
		}
		for _, ip := range ips {
			if _, err := modelswitch.CheckGatewayURL("https://"+net.JoinHostPort(ip.String(), "443"), true); err != nil {
				return nil, "", invalid(errors.New("the host points to a private or local address; only public repositories work"))
			}
		}
		pin = ips[0].String()
	}
	return u, pin, nil
}

func validateRepoTask(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p repoTaskParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if p.Model != "" || p.BudgetUSD != 0 {
		return nil, "", invalid(errors.New("no model is used for a task from a commit: leave model and budget empty"))
	}
	dir, err := suiteOpsCheckSuite(env, p.Suite)
	if err != nil {
		return nil, "", err
	}
	if p.Template != "" {
		y, err := os.ReadFile(filepath.Join(dir, "suite.yaml"))
		if err != nil {
			return nil, "", err
		}
		if got := scaffold.DetectTemplate(y); got != p.Template {
			return nil, "", invalid(fmt.Errorf("benchmark %s uses template %q, not %q: pick a benchmark of that language", p.Suite, got, p.Template))
		}
	}
	p.Base, p.Reference = strings.TrimSpace(p.Base), strings.TrimSpace(p.Reference)
	for what, v := range map[string]string{"base": p.Base, "reference": p.Reference} {
		if v == "" || !suiteOpsGitRef.MatchString(v) || strings.HasPrefix(v, "-") || strings.Contains(v, "..") {
			return nil, "", invalid(fmt.Errorf("the %s commit %q is not a commit id, tag or branch", what, v))
		}
	}
	if p.Base == p.Reference {
		return nil, "", invalid(errors.New("the commit before the fix and the fix are the same commit"))
	}
	if p.ID == "" {
		p.ID = "c-" + strings.ToLower(p.Reference[:min(len(p.Reference), 10)])
	}
	if !scaffold.ValidID(p.ID) {
		return nil, "", invalid(fmt.Errorf("task id %q is not valid: use letters, digits, '-' and '_'", p.ID))
	}
	u, err := checkRepoURL(ctx, p.RepoURL)
	if err != nil {
		return nil, "", err
	}
	p.RepoURL = u.String()
	return p, "suite:" + p.Suite, nil
}

// repoTaskClone clones the public repository into a temporary folder.
func repoTaskClone(ctx context.Context, u *url.URL, pin string, log jobs.Logger) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "heimdall-repotask-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	dir := filepath.Join(tmp, "repo")
	args := repoTaskCloneArgs(u, pin, dir)
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_LFS_SKIP_SMUDGE=1", "LC_ALL=C"}
	cctx, cancel := context.WithTimeout(ctx, suiteOpsCloneTimeout)
	defer cancel()
	log.Printf("cloning %s (public repositories only, no credential is sent)", u)
	if err := suiteOpsGitClone(cctx, args, env); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("could not copy %s: check the address; private repositories need GitHub connected (From GitHub)", u)
	}
	if n := repoTaskDirBytes(dir); n > repoTaskMaxBytes {
		cleanup()
		return "", nil, fmt.Errorf("the repository is %d MB, more than the %d MB limit", n>>20, repoTaskMaxBytes>>20)
	}
	return dir, cleanup, nil
}

// repoTaskCloneArgs is the guarded clone command: https only, no
// redirects, hooks, submodules or credential helper, the host pinned to
// the address the check accepted.
func repoTaskCloneArgs(u *url.URL, pin, dir string) []string {
	args := []string{"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "http.followRedirects=false",
		"-c", "credential.helper=", "-c", "core.hooksPath=/dev/null", "-c", "submodule.recurse=false"}
	if pin != "" {
		args = append(args, "-c", "http.curloptResolve="+u.Hostname()+":443:"+pin)
	}
	return append(args, "clone", "--quiet", "--no-recurse-submodules", "--single-branch", "--no-tags", "--", u.String(), dir)
}

// repoTaskDirBytes is the size of the files under dir.
func repoTaskDirBytes(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func runRepoTask(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p repoTaskParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	u, pin, err := checkRepoURLPin(ctx, p.RepoURL) // the host may resolve elsewhere now
	if err != nil {
		return nil, err
	}
	suiteDir, err := suiteOpsCheckSuite(env, p.Suite)
	if err != nil {
		return nil, err
	}
	step := func(s string) {
		if progress != nil {
			progress(map[string]string{"step": s})
		}
	}
	step("cloning")
	repo, cleanup, err := repoTaskClone(ctx, u, pin, log)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	step("cutting the task")
	res, err := scaffold.InitTask(ctx, scaffold.TaskInitOptions{SuiteDir: suiteDir, ID: p.ID, Repo: repo, Base: p.Base, Reference: p.Reference,
		License: "proprietary", Kind: "bugfix"})
	if err != nil {
		return nil, fmt.Errorf("task init: %v", err)
	}
	if err := scaffold.MarkMined(res.Dir, scaffold.MinedInstruction(commitSubject(ctx, repo, p.Reference), "", nil), ""); err != nil {
		return nil, err
	}
	if findings, _ := suite.ScanSecrets(res.Dir); len(findings) > 0 {
		return nil, fmt.Errorf("the commit holds a secret (%v): the task was not admitted", findings[0])
	}
	out := &repoTaskResult{Suite: p.Suite, Task: p.ID, Template: res.Template, Hidden: res.Hidden, Controls: []schemas.AdmissionCheck{},
		Note: "Admission ran the fix three times, the empty patch, a cheat, doing nothing and refusing. A person still reviews the instruction."}
	step("running admission")
	t, expID, err := repoTaskAdmit(ctx, env, j, p.Suite, p.ID, log)
	if err != nil {
		return out, err
	}
	out.Admitted, out.Reason, out.ExpID = t.Admitted, t.Reason, expID
	if t.Checks != nil {
		out.Controls = t.Checks
	}
	if !out.Admitted && out.Reason == "" {
		for _, c := range out.Controls {
			if !c.Passed {
				out.Reason = fmt.Sprintf("%s: want %s, got %s", c.Name, c.Want, c.Got)
				break
			}
		}
	}
	log.Printf("%s/%s admitted=%v %s", p.Suite, p.ID, out.Admitted, out.Reason)
	return out, nil
}

// commitSubject is the first line of a commit message, "" on error.
func commitSubject(ctx context.Context, repo, rev string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", repo, "log", "-1", "--format=%s", rev, "--").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// repoTaskAdmitHosted proves one task on a worker like hosted tasks.mine.
func repoTaskAdmitHosted(ctx context.Context, env jobEnv, j *jobs.Job, suiteID, id string, log jobs.Logger) (hostedAdmitTask, string, error) {
	none := hostedAdmitTask{Task: id}
	l, err := bridgeLauncher(env)
	if err != nil {
		return none, "", err
	}
	if err := bridgeRequireRunner(l); err != nil {
		return none, "", err
	}
	st, err := snapshotSuite(ctx, env.App, suiteID)
	if err != nil {
		return none, "", err
	}
	if errs := staticErrors(ctx, st.suite, id); len(errs) > 0 {
		none.Reason = "static validation: " + errs[0]
		return none, "", nil
	}
	t := st.suite.TaskByID(id)
	if t == nil {
		return none, "", fmt.Errorf("task %s is not in suite %s", id, suiteID)
	}
	tasks := []*schemas.Task{t}
	m, err := l.Plan(ctx, readinessExperiment(suiteID, bridgeOracleAgent, bridgeNoopAgent, st.suite, tasks, schemas.SplitPublic, false))
	if err != nil {
		return none, "", fmt.Errorf("plan the admission run: %w", err)
	}
	if err := runExperiment(ctx, l, m, max(bridgeDefaultTimeout, readinessTimeout(len(m.Slots))), log); err != nil {
		return none, m.ExpID, err
	}
	runs, err := bridgeSealedRuns(ctx, env, m.ExpID)
	if err != nil {
		return none, m.ExpID, err
	}
	v := readinessVerdict(st.suite, tasks, runs, map[string]string{id: m.ExpID}, readinessFileChecks(env.App, st.suite))
	if len(v) != 1 {
		return none, m.ExpID, errors.New("admission gave no verdict")
	}
	if v[0].Admitted {
		if _, _, err := recordHostedAdmissions(ctx, env, st, v, m.ExpID, jobActor(j)); err != nil {
			return v[0], m.ExpID, err
		}
	}
	return v[0], m.ExpID, nil
}
