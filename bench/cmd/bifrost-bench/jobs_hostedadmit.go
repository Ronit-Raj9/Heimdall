package main

// Hosted admission records and hosted mining (gap lane G04). The hosted
// suite.admit bridge (jobs_hostedops.go) proves the oracle and noop controls
// on a runner; this file turns those outcomes into admission records and
// writes suite.lock exactly as local `suite admit` does: suite.LockWith,
// suite.RecordAdmission and the same encoding (suite.EncodeLock), stored
// through the org's project store. The hosted tasks.mine full run cuts the
// candidates in process (git only), proves them all in one bridge experiment
// on runners, keeps the proven tasks in the suite and records their
// admissions the same way.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/quickstart"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// Admission check names of a hosted admission record.
const (
	hostedCheckOracle = "oracle_control"
	hostedCheckNoop   = "noop_control"
)

// Test seams: the runner check and the sealed-run read of a bridge job.
var (
	bridgeRequireRunner = requireRunner
	bridgeSealedRuns    = sealedRuns
)

// admitExperimentIDs is admitExperiment over an explicit task list (empty:
// every task of the suite).
func admitExperimentIDs(suiteRef, oracle, noop string, ids []string) *schemas.Experiment {
	e := admitExperiment(hostedSuiteAdmitParams{Suite: suiteRef, Oracle: oracle, Noop: noop})
	if len(ids) > 0 {
		e.Conditions[0].Tasks = schemas.TaskSelector{IDs: append([]string(nil), ids...)}
	}
	return e
}

// hostedAdmissionRecord is the admission record of one task proved on a
// runner: static validation (run in process before the experiment), then the
// oracle control (want pass) and the noop control (want not pass).
func hostedAdmissionRecord(s *schemas.Suite, t hostedAdmitTask, expID string) *schemas.AdmissionRecord {
	where := "proved on a runner in experiment " + expID
	oracle := schemas.AdmissionCheck{Name: hostedCheckOracle, Want: suite.GotPass, Got: t.Oracle,
		Passed: t.Oracle == string(schemas.OutcomePass), Detail: where}
	noop := schemas.AdmissionCheck{Name: hostedCheckNoop, Want: suite.GotFail, Got: t.Noop, Detail: where}
	switch schemas.Outcome(t.Noop) {
	case "", schemas.OutcomePass, schemas.OutcomeInfraError, schemas.OutcomeAbortedInfra, schemas.OutcomeInvalidEvaluator:
	default:
		noop.Passed = true
	}
	if !t.Admitted && t.Reason != "" {
		// Name the failing control in its check, as local admission does.
		if !oracle.Passed {
			oracle.Detail = t.Reason + "; " + where
		} else {
			noop.Passed, noop.Detail = false, t.Reason+"; "+where
		}
	}
	rec := &schemas.AdmissionRecord{
		Admitted: t.Admitted && oracle.Passed && noop.Passed,
		At:       time.Now().UTC(),
		Grader:   s.Grader,
		Checks: []schemas.AdmissionCheck{
			{Name: suite.StaticValidationCheck, Want: "clean", Got: "clean", Passed: true},
			oracle, noop,
		},
		ReferenceRepeats: 1,
	}
	if t.Oracle != "" {
		rec.ReferenceResults = []string{t.Oracle}
	}
	return rec
}

// hostedSuiteState is a suite as a hosted admission found it before its
// experiment: suite.yaml bytes and the fingerprints the proofs ran against.
type hostedSuiteState struct {
	suite  *schemas.Suite
	before []byte
	fps    *schemas.LockFingerprints
}

// snapshotSuite loads suite id and fingerprints it, as proveTask does.
func snapshotSuite(ctx context.Context, a *app, id string) (*hostedSuiteState, error) {
	if suite.IsLibrarySuite(a.cfg.Path(a.cfg.BenchmarksRoot), id) {
		return nil, fmt.Errorf("suite %s: %w", id, schemas.ErrReadOnly) // built-in library: admitted on the server, never from an org
	}
	s, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), id)
	if err != nil {
		return nil, fmt.Errorf("load suite %s: %w", id, err)
	}
	before, err := os.ReadFile(filepath.Join(s.Root, "suite.yaml"))
	if err != nil {
		return nil, err
	}
	fps, err := suite.Fingerprints(s, lockFingerprintOptions(ctx, a.cfg, a.docker, suite.LockPinsLocal(s.Lock)))
	if err != nil {
		return nil, fmt.Errorf("fingerprint %s: %w", id, err)
	}
	return &hostedSuiteState{suite: s, before: before, fps: fps}, nil
}

// staticErrors runs the static validation of `task validate` on the chosen
// tasks (all when only is empty) and returns the error lines.
func staticErrors(ctx context.Context, s *schemas.Suite, only string) []string {
	var out []string
	for _, p := range suite.StaticValidation(ctx, s, only) {
		if p.Severity == suite.SeverityError {
			out = append(out, fmt.Sprintf("%s: %s", p.Path, p.Message))
		}
	}
	return out
}

// recordHostedAdmissions writes suite.lock with the records of tasks, carried
// like recordProofs: a task whose files or the suite's settings changed while
// the experiment ran is dropped (returned) and keeps no record.
func recordHostedAdmissions(ctx context.Context, env jobEnv, st *hostedSuiteState, tasks []hostedAdmitTask, expID string, actor string) (string, []string, error) {
	proofs := map[string]proof{}
	for _, t := range tasks {
		if _, ok := st.fps.Tasks[t.Task]; !ok {
			continue
		}
		proofs[t.Task] = proof{rec: hostedAdmissionRecord(st.suite, t, expID), fp: st.fps.Tasks[t.Task], suite: st.fps.Suite}
	}
	s, lock, dropped, err := buildProofLock(ctx, env.App, st.suite.ID, st.before, proofs)
	if err != nil {
		return "", nil, err
	}
	path, err := writeHostedLock(ctx, env, s, lock, actor, "admission proved on a runner in "+expID)
	return path, dropped, err
}

// writeHostedLock stores lock as <suite>/suite.lock through the org's
// project store (versioned, atomic), in WriteLock's exact bytes. Without a
// store, or for a suite outside the store's benchmarks area, it writes the
// file with suite.WriteLock.
func writeHostedLock(ctx context.Context, env jobEnv, s *schemas.Suite, lock *schemas.SuiteLock, actor, note string) (string, error) {
	path := filepath.Join(s.Root, "suite.lock")
	if ps := env.Services.Projects; ps != nil {
		if base, err := ps.Resolve(projectfs.AreaBenchmarks, "."); err == nil {
			if rel, err := filepath.Rel(base, path); err == nil && !strings.HasPrefix(rel, "..") {
				data, err := suite.EncodeLock(lock)
				if err != nil {
					return "", err
				}
				if _, err := ps.Write(ctx, projectfs.AreaBenchmarks, filepath.ToSlash(rel), data, projectfs.WriteOpts{Actor: actor, Note: note}); err == nil {
					return path, nil
				} else if !errors.Is(err, projectfs.ErrTooLarge) {
					return "", fmt.Errorf("write %s: %w", rel, err)
				}
			}
		}
	}
	if _, err := suite.WriteLock(path, lock); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// ---- tasks.mine (hosted, full run) ----

// hostedMineDrop is one candidate the hosted mine did not keep.
type hostedMineDrop struct {
	Task   string `json:"task"`
	Commit string `json:"commit"`
	Reason string `json:"reason"`
}

// hostedMineReport is the hosted tasks.mine result.
type hostedMineReport struct {
	Suite    string            `json:"suite"`
	DryRun   bool              `json:"dry_run"`
	ExpID    string            `json:"exp_id,omitempty"`
	Proved   []string          `json:"proved"`
	Dropped  []hostedMineDrop  `json:"dropped"`
	Tasks    []hostedAdmitTask `json:"tasks,omitempty"`
	LockPath string            `json:"lock,omitempty"`
	Note     string            `json:"note"`
}

// runHostedTasksMine cuts up to p.Limit candidates of repo into the suite,
// proves them in one bridge experiment on the org's runners (oracle passes,
// noop fails), keeps the proven ones and records their admissions in
// suite.lock. Every unproven cut is removed again and suite.yaml lists only
// the proven additions.
func runHostedTasksMine(ctx context.Context, env jobEnv, j *jobs.Job, p tasksMineParams, suiteDir, repo string, log jobs.Logger) (any, error) {
	if env.App == nil || env.App.cfg == nil {
		return nil, errors.New("no organization app")
	}
	timeout := bridgeDefaultTimeout
	l, err := bridgeLauncher(env)
	if err != nil {
		return nil, err
	}
	if err := bridgeRequireRunner(l); err != nil {
		return nil, err
	}
	suitePath := filepath.Join(suiteDir, "suite.yaml")
	before, err := os.ReadFile(suitePath)
	if err != nil {
		return nil, fmt.Errorf("read suite %s: %v", p.Suite, err)
	}
	tmpl := scaffold.DetectTemplate(before)
	globs := scaffold.DefaultTestGlobs(tmpl)
	if len(globs) == 0 {
		return nil, fmt.Errorf("suite %s: template %q has no test globs; tasks mine needs python-pytest, node-test, go-test or generic-reward", p.Suite, tmpl)
	}
	cands, skipped, err := scaffold.MineCandidates(ctx, scaffold.MineOptions{
		Repo: repo, Since: p.Since, Paths: p.Paths, MinTests: p.MinTests, TestGlobs: globs,
		NeedBaseTests: suiteGrader(before) == "hidden_tests",
	})
	if err != nil {
		return nil, fmt.Errorf("tasks mine: %v", err)
	}
	log.Printf("looked at %d commits: %d candidates, %d skipped (suite %s, template %s)", len(cands)+len(skipped), len(cands), len(skipped), p.Suite, tmpl)
	rep := &hostedMineReport{Suite: p.Suite, Proved: []string{}, Dropped: []hostedMineDrop{},
		Note: "hosted mining proves the oracle and noop controls on a runner; the full local admission (mutants, exploit, 3 reference runs) still runs with bifrost-bench suite admit"}
	testCmd := ""
	if pf, _ := quickstart.LoadProjectFile(env.App.cfg.Root); pf != nil {
		testCmd = pf.Tests.Command
	}

	// Cut the candidates (git only, no containers).
	var cut []string
	commitOf := map[string]string{}
	for _, c := range cands {
		if len(cut) >= p.Limit {
			break
		}
		id := c.TaskID()
		if _, err := os.Stat(filepath.Join(suiteDir, "tasks", id)); err == nil {
			log.Printf("%s %s is already in suite %s; skipped", id, c.ShortCommit(), p.Suite)
			continue
		}
		pre, err := os.ReadFile(suitePath)
		if err != nil {
			return rep, err
		}
		dir := filepath.Join(suiteDir, "tasks", id)
		drop := func(reason string) error {
			rep.Dropped = append(rep.Dropped, hostedMineDrop{Task: id, Commit: c.ShortCommit(), Reason: reason})
			log.Warnf("DROP    %s %s: %s", id, c.ShortCommit(), reason)
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			return os.WriteFile(suitePath, pre, 0o644)
		}
		kind := "bugfix"
		if low := strings.ToLower(c.Subject); strings.HasPrefix(low, "add ") || strings.HasPrefix(low, "feat") {
			kind = "feature"
		}
		license := p.License
		if license == "" {
			license = "proprietary"
		}
		res, err := scaffold.InitTask(ctx, scaffold.TaskInitOptions{SuiteDir: suiteDir, ID: id, Repo: repo, Base: c.Parent, Reference: c.Commit,
			License: license, Kind: kind, ExcludeDir: env.App.cfg.Root})
		if err != nil {
			rep.Dropped = append(rep.Dropped, hostedMineDrop{Task: id, Commit: c.ShortCommit(), Reason: "task init: " + err.Error()})
			continue
		}
		if err := scaffold.MarkMined(res.Dir, scaffold.MinedInstruction(c.Subject, c.Body, c.InstructionTests()), testCmd); err != nil {
			if derr := drop("draft: " + err.Error()); derr != nil {
				return rep, derr
			}
			continue
		}
		if findings, err := suite.ScanSecrets(res.Dir); err != nil || len(findings) > 0 {
			reason := "secret scan failed"
			if err != nil {
				reason += ": " + err.Error()
			} else {
				reason += ": " + fmt.Sprint(findings[0])
			}
			if derr := drop(reason); derr != nil {
				return rep, derr
			}
			continue
		}
		log.Printf("cut %s from %s %s", id, c.ShortCommit(), c.Subject)
		cut = append(cut, id)
		commitOf[id] = c.ShortCommit()
	}
	// restore puts suite.yaml back to before plus the kept ids and removes
	// every other cut.
	restore := func(keep map[string]bool) error {
		out := before
		for _, id := range cut {
			if keep[id] {
				if edited, changed, err := scaffold.AddTaskID(out, id); err != nil {
					return err
				} else if changed {
					out = edited
				}
				continue
			}
			if err := os.RemoveAll(filepath.Join(suiteDir, "tasks", id)); err != nil {
				return err
			}
		}
		return os.WriteFile(suitePath, out, 0o644)
	}
	if len(cut) == 0 {
		log.Printf("no task was cut: widen since or paths, or lower min tests (a preview lists the candidates)")
		return rep, nil
	}

	// Static validation in process, before any runner time.
	st, err := snapshotSuite(ctx, env.App, p.Suite)
	if err != nil {
		return rep, errors.Join(err, restore(nil))
	}
	var prove []string
	for _, id := range cut {
		if errs := staticErrors(ctx, st.suite, id); len(errs) > 0 {
			rep.Dropped = append(rep.Dropped, hostedMineDrop{Task: id, Commit: commitOf[id], Reason: "static validation: " + errs[0]})
			log.Warnf("DROP    %s: static validation: %s", id, errs[0])
			continue
		}
		prove = append(prove, id)
	}
	if len(prove) == 0 {
		return rep, restore(nil)
	}

	m, err := l.Plan(ctx, admitExperimentIDs(p.Suite, bridgeOracleAgent, bridgeNoopAgent, prove))
	if err != nil {
		return rep, errors.Join(fmt.Errorf("plan the proving run: %w", err), restore(nil))
	}
	rep.ExpID = m.ExpID
	log.Printf("proving %d tasks on a runner: experiment %s (open it on the Experiments page)", len(prove), m.ExpID)
	if err := runExperiment(ctx, l, m, timeout, log); err != nil {
		return rep, errors.Join(err, restore(nil))
	}
	runs, err := bridgeSealedRuns(ctx, env, m.ExpID)
	if err != nil {
		return rep, errors.Join(err, restore(nil))
	}
	rep.Tasks = admitVerdict(runs, prove)
	keep := map[string]bool{}
	var proved []hostedAdmitTask
	for _, t := range rep.Tasks {
		if t.Admitted {
			keep[t.Task] = true
			proved = append(proved, t)
			log.Printf("PROVED  %s (oracle %s, noop %s)", t.Task, t.Oracle, t.Noop)
		} else {
			rep.Dropped = append(rep.Dropped, hostedMineDrop{Task: t.Task, Commit: commitOf[t.Task], Reason: "not proven: " + t.Reason})
			log.Warnf("DROP    %s: %s", t.Task, t.Reason)
		}
	}
	if err := restore(keep); err != nil {
		return rep, err
	}
	if len(proved) == 0 {
		return rep, fmt.Errorf("tasks mine: no candidate was proven (see experiment %s)", m.ExpID)
	}
	path, dropped, err := recordHostedAdmissions(ctx, env, st, proved, m.ExpID, jobActor(j))
	if err != nil {
		return rep, err
	}
	rep.LockPath = path
	for _, id := range dropped {
		log.Warnf("the admission of %s could not be recorded (the task or suite changed during the run); admit it again", id)
	}
	for _, t := range proved {
		rep.Proved = append(rep.Proved, t.Task)
	}
	log.Printf("proved %d new tasks; admission recorded in suite.lock. Next: review each drafted instruction (Edit tab), then run the suite", len(rep.Proved))
	return rep, nil
}

func jobActor(j *jobs.Job) string {
	if j == nil {
		return ""
	}
	return j.CreatedBy
}
