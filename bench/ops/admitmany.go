package ops

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/taskguard"
)

// AdmitManyOptions choose the tasks SuiteAdmitMany admits and how many run
// at once.
type AdmitManyOptions struct {
	Tasks    []string                   // empty admits every task of the suite
	Parallel int                        // concurrent admissions; < 1 means 1
	problems map[string][]suite.Problem // precomputed static validation per task
	// admit replaces suite.Admit in tests.
	admit func(ctx context.Context, deps suite.AdmitDeps, s *schemas.Suite, t *schemas.Task) (*schemas.AdmissionRecord, error)
}

// AdmitManyReport is what SuiteAdmitMany did, per task.
type AdmitManyReport struct {
	Suite    string                              `json:"suite"`
	Path     string                              `json:"path"`
	FullLock bool                                `json:"full_lock"` // every task was re-hashed (no usable previous lock)
	Problems []suite.Problem                     `json:"problems"`
	Tasks    map[string]*schemas.AdmissionRecord `json:"tasks"`
	Reasons  map[string]string                   `json:"reasons"` // why a task was refused or failed
	Order    []string                            `json:"order"`   // as given
	Admitted int                                 `json:"admitted"`
	Refused  int                                 `json:"refused"`
}

// SuiteAdmitMany loads suite ref once and admits the chosen tasks with up to
// Parallel workers. Only the chosen tasks are validated, hashed and
// fingerprinted (suite.LockTasks); every other lock entry stays as it was.
// A task that static validation refuses, or whose admission run errors, is
// recorded as not admitted (draft) with the reason, and the others go on.
// suite.lock is rewritten (atomically, under a mutex) after each task, so
// progress survives a crash. Shared images (base, services, helper) are
// built once before the fan-out. Errors are only what stops the whole call:
// the suite does not load, an unknown task, a suite-level validation error,
// an image build, or a lock write.
func SuiteAdmitMany(ctx context.Context, env AdmitEnv, ref string, o AdmitManyOptions, w io.Writer) (*AdmitManyReport, error) {
	w = out(w)
	reg, err := env.Registry(ctx, false)
	if err != nil {
		return nil, err
	}
	s, err := LoadSuite(ctx, env.Config.Path(env.Config.BenchmarksRoot), ref)
	if err != nil {
		return nil, fmt.Errorf("load suite %s: %w", ref, err)
	}
	ids, err := admitIDs(s, o.Tasks)
	if err != nil {
		return nil, err
	}
	if s.Grading == schemas.GradingJudged {
		if reg, err = env.Registry(ctx, true); err != nil {
			return nil, err
		}
	}
	deps, err := admitDeps(s, reg, env)
	if err != nil {
		return nil, err
	}
	rep := &AdmitManyReport{Suite: s.ID, Tasks: map[string]*schemas.AdmissionRecord{}, Reasons: map[string]string{}, Order: ids}
	rep.Problems = suite.SuiteProblems(s)
	if rep.Problems == nil {
		rep.Problems = []suite.Problem{}
	}
	for _, p := range rep.Problems {
		fmt.Fprintf(w, "%-7s %s: %s\n", p.Severity, p.Path, p.Message)
	}
	if n := suite.Errors(rep.Problems); n > 0 {
		return rep, fmt.Errorf("%w: suite-level validation found %d error(s); fix suite.yaml and admit again", ErrAdmitRefused, n)
	}
	// Static validation of every chosen task first, before any docker call:
	// when it refuses them all, nothing runs and nothing is written.
	o.problems = validateTasks(ctx, s, ids, o.Parallel)
	valid := 0
	for _, id := range ids {
		if suite.Errors(o.problems[id]) == 0 {
			valid++
		}
		for _, p := range o.problems[id] {
			fmt.Fprintf(w, "%-7s %s: %s\n", p.Severity, p.Path, p.Message)
		}
	}
	if valid == 0 {
		return rep, fmt.Errorf("%w: static validation refused every chosen task; fix them (bifrost-bench task validate <suite>/<task> shows the same list) and admit again", ErrAdmitRefused)
	}
	if err := EnsureImages(ctx, env.Config, env.Docker, s, w); err != nil {
		return rep, err
	}
	lock, full, err := suite.LockTasks(s, ids, time.Now().UTC(), LockFingerprintOptions(ctx, env.Config, env.Docker, suite.LockPinsLocal(s.Lock)))
	if err != nil {
		return rep, fmt.Errorf("lock: %w", err)
	}
	rep.FullLock = full
	rep.Path = filepath.Join(s.Root, "suite.lock")
	writeErr := runAdmissions(ctx, deps, s, lock, ids, o, rep, w)
	PrintAdmitSummary(w, rep)
	if writeErr != nil {
		return rep, writeErr
	}
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	return rep, nil
}

// admitOne validates and admits one task; a refusal or run error becomes a
// not-admitted record and its reason.
func admitOne(ctx context.Context, deps suite.AdmitDeps, s *schemas.Suite, t *schemas.Task,
	admit func(context.Context, suite.AdmitDeps, *schemas.Suite, *schemas.Task) (*schemas.AdmissionRecord, error),
	pre map[string][]suite.Problem) (*schemas.AdmissionRecord, string) {
	problems, ok := pre[t.ID]
	if !ok {
		problems = suite.ValidateTask(ctx, s, t)
	}
	if n := suite.Errors(problems); n > 0 {
		var msgs []string
		for _, p := range problems {
			if p.Severity == suite.SeverityError {
				msgs = append(msgs, p.Path+": "+p.Message)
			}
		}
		reason := fmt.Sprintf("refused: static validation found %d error(s)", n)
		return refusedRecord("static validation", "0 errors", fmt.Sprintf("%d errors", n), strings.Join(msgs, "; ")), reason
	}
	rec, err := admit(ctx, deps, s, t)
	if err != nil {
		return refusedRecord("admission run", "completes", "error", err.Error()), "error: " + err.Error()
	}
	rec = taskguard.Apply(s, t, rec)
	if rec == nil {
		return refusedRecord("admission run", "record", "none", "admission returned no record"), "error: no record"
	}
	return rec, ""
}

func refusedRecord(name, want, got, detail string) *schemas.AdmissionRecord {
	return &schemas.AdmissionRecord{Admitted: false, At: time.Now().UTC(),
		Checks: []schemas.AdmissionCheck{{Name: name, Want: want, Got: got, Passed: false, Detail: detail}}}
}

func failedChecks(rec *schemas.AdmissionRecord) string {
	var names []string
	for _, c := range rec.Checks {
		if !c.Passed {
			names = append(names, c.Name)
		}
	}
	if len(names) == 0 {
		return "not admitted"
	}
	return "failed: " + strings.Join(names, ", ")
}

// admitIDs is the chosen task ids in order, deduplicated; every one must be
// in the suite's tasks list. Empty chooses every task.
func admitIDs(s *schemas.Suite, want []string) ([]string, error) {
	if len(want) == 0 {
		ids := make([]string, 0, len(s.Tasks))
		for _, t := range s.Tasks {
			ids = append(ids, t.ID)
		}
		return ids, nil
	}
	seen := map[string]bool{}
	var ids, missing []string
	for _, id := range want {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if s.TaskByID(id) == nil {
			missing = append(missing, id)
			continue
		}
		ids = append(ids, id)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("suite %s has no task(s) %s in its tasks list", s.ID, strings.Join(missing, ", "))
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no tasks chosen")
	}
	return ids, nil
}

// admitDeps finds the suite's environment and grader in reg, first
// registered wins, as SuiteAdmit does.
func admitDeps(s *schemas.Suite, reg *schemas.Registry, env AdmitEnv) (suite.AdmitDeps, error) {
	deps := suite.AdmitDeps{Logger: env.Logger, GPUs: reg.GPUs}
	for _, e := range reg.Environments {
		if deps.Env == nil && e.GetName() == s.Environment {
			deps.Env = e
		}
	}
	for _, g := range reg.Graders {
		if deps.Grader == nil && g.GetName() == s.Grader {
			deps.Grader = g
		}
	}
	if deps.Env == nil {
		return deps, fmt.Errorf("suite %s: environment %q is not registered", s.ID, s.Environment)
	}
	if deps.Grader == nil {
		return deps, fmt.Errorf("suite %s: grader %q is not registered", s.ID, s.Grader)
	}
	return deps, nil
}

// PrintAdmitSummary prints one row per chosen task and the totals.
func PrintAdmitSummary(w io.Writer, rep *AdmitManyReport) {
	fmt.Fprintf(w, "\n%-24s %-12s %s\n", "TASK", "STATUS", "REASON")
	for _, id := range rep.Order {
		rec, ok := rep.Tasks[id]
		status := "not run"
		switch {
		case ok && rec.Admitted:
			status = "admitted"
		case ok:
			status = "draft"
		}
		fmt.Fprintf(w, "%-24s %-12s %s\n", id, status, rep.Reasons[id])
	}
	fmt.Fprintf(w, "%s: %d chosen, %d admitted, %d not admitted, %d not run; wrote %s\n",
		rep.Suite, len(rep.Order), rep.Admitted, rep.Refused, len(rep.Order)-rep.Admitted-rep.Refused, rep.Path)
}

// runAdmissions admits ids with o.Parallel workers, recording each result in
// lock and rewriting rep.Path after every task. It returns the first lock
// record or write error, after which no new task starts.
func runAdmissions(ctx context.Context, deps suite.AdmitDeps, s *schemas.Suite, lock *schemas.SuiteLock, ids []string, o AdmitManyOptions, rep *AdmitManyReport, w io.Writer) error {
	admit := o.admit
	if admit == nil {
		admit = suite.Admit
	}
	par := o.Parallel
	if par < 1 {
		par = 1
	}
	if par > len(ids) {
		par = len(ids)
	}
	var mu sync.Mutex // guards lock, rep, w and the lock file
	var writeErr error
	finish := func(id string, rec *schemas.AdmissionRecord, reason string) {
		mu.Lock()
		defer mu.Unlock()
		if err := suite.RecordAdmission(lock, id, rec); err != nil && writeErr == nil {
			writeErr = err
		}
		rep.Tasks[id] = rec
		if rec.Admitted {
			rep.Admitted++
		} else {
			rep.Refused++
			if reason == "" {
				reason = failedChecks(rec)
			}
			rep.Reasons[id] = reason
		}
		PrintAdmission(w, id, rec)
		if _, err := suite.WriteLock(rep.Path, lock); err != nil && writeErr == nil {
			writeErr = fmt.Errorf("write %s: %w", rep.Path, err)
		}
	}
	jobs := make(chan *schemas.Task)
	var wg sync.WaitGroup
	for i := 0; i < par; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				rec, reason := admitOne(ctx, deps, s, t, admit, o.problems)
				finish(t.ID, rec, reason)
			}
		}()
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		mu.Lock()
		stop := writeErr != nil
		mu.Unlock()
		if stop {
			break
		}
		jobs <- s.TaskByID(id)
	}
	close(jobs)
	wg.Wait()
	return writeErr
}

// validateTasks runs suite.ValidateTask on ids with up to par workers.
func validateTasks(ctx context.Context, s *schemas.Suite, ids []string, par int) map[string][]suite.Problem {
	if par < 1 {
		par = 1
	}
	out := make(map[string][]suite.Problem, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	ch := make(chan string)
	for i := 0; i < par; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ch {
				ps := suite.ValidateTask(ctx, s, s.TaskByID(id))
				mu.Lock()
				out[id] = ps
				mu.Unlock()
			}
		}()
	}
	for _, id := range ids {
		ch <- id
	}
	close(ch)
	wg.Wait()
	return out
}
