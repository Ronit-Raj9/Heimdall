package main

// Add benchmark jobs (lane Z20): onboard.ready runs the readiness checks on
// a draft set and onboard.publish copies its ready tasks into the benchmark.
//
// Readiness on this server (local mode) admits the draft suite folder in
// place: the admission loads it from the set's own benchmarks root, so the
// draft never enters benchmarks/. A hosted server proves controls on a
// worker through the suite.admit bridge, which reads suites from the org's
// benchmarks area: the draft is copied there as a staging benchmark
// draft-<set> for the run and removed after, its records read back.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/onboard"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

func init() {
	registerJobKind(jobKindSpec{Kind: handlers.OnboardReadyKind, Local: true, Hosted: true,
		Validate: validateOnboardReady, Run: runOnboardReady})
	registerJobKind(jobKindSpec{Kind: handlers.OnboardPublishKind, Local: true, Hosted: true,
		Validate: validateOnboardPublish, Run: runOnboardPublish})
}

func onboardRoot(env jobEnv) (string, error) {
	if env.App == nil || env.App.cfg == nil || env.App.cfg.Root == "" {
		return "", errors.New("no project folder on this server")
	}
	return env.App.cfg.Root, nil
}

func validateOnboardReady(_ context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p handlers.OnboardReadyParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	root, err := onboardRoot(env)
	if err != nil {
		return nil, "", invalid(err)
	}
	set, err := onboard.LoadSet(root, p.Set)
	if err != nil {
		return nil, "", invalid(fmt.Errorf("draft %s: %w", p.Set, err))
	}
	if p.Task != "" && set.Task(p.Task) == nil {
		return nil, "", invalid(fmt.Errorf("draft %s has no task %s", p.Set, p.Task))
	}
	return p, "onboard:" + p.Set, nil
}

func validateOnboardPublish(_ context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p handlers.OnboardPublishParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	root, err := onboardRoot(env)
	if err != nil {
		return nil, "", invalid(err)
	}
	set, err := onboard.LoadSet(root, p.Set)
	if err != nil {
		return nil, "", invalid(fmt.Errorf("draft %s: %w", p.Set, err))
	}
	return p, "suite:" + set.Suite, nil
}

// updateSet reloads the set, applies f and saves it.
func updateSet(root, id string, f func(*onboard.DraftSet)) (*onboard.DraftSet, error) {
	set, err := onboard.LoadSet(root, id)
	if err != nil {
		return nil, err
	}
	unlock := set.Lock()
	defer unlock()
	if set, err = onboard.LoadSet(root, id); err != nil {
		return nil, err
	}
	f(set)
	return set, set.Save()
}

// onboardReadyResult is onboard.ready's result: the local admission shape
// the website's readiness view reads ({tasks: {id: record}, order,
// problems}), plus the set.
type onboardReadyResult struct {
	Set      string                              `json:"set"`
	Suite    string                              `json:"suite"`
	Where    string                              `json:"where"`
	Tasks    map[string]*schemas.AdmissionRecord `json:"tasks"`
	Order    []string                            `json:"order"`
	Problems []suite.Problem                     `json:"problems"`
	Admitted int                                 `json:"admitted"`
	Failed   int                                 `json:"failed"`
	ExpID    string                              `json:"exp_id,omitempty"`
}

func runOnboardReady(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p handlers.OnboardReadyParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	root, err := onboardRoot(env)
	if err != nil {
		return nil, err
	}
	where := "this server"
	if env.Hosted {
		where = "a worker"
	}
	// Stamp the canary into a draft with held-out tasks before the checks,
	// so the records match the files publish copies and carry over.
	oenv := &onboard.Env{Root: root, BenchmarksDir: env.App.cfg.Path(env.App.cfg.BenchmarksRoot), Hosted: env.Hosted, Actor: jobActor(j)}
	var stampErr error
	set, err := updateSet(root, p.Set, func(s *onboard.DraftSet) {
		guid, changed, err := s.StampCanary(ctx, oenv)
		if err != nil {
			stampErr = err
			return
		}
		if len(changed) > 0 {
			log.Printf("stamped canary %s into %d draft tasks (held-out tasks in the draft)", guid, len(changed))
		}
		total := len(s.Tasks)
		if p.Task != "" {
			total = 1
		}
		s.Readiness = &onboard.Readiness{JobID: j.ID, Status: onboard.StatusRunning, Where: where, Total: total, At: time.Now().UTC()}
	})
	if err != nil {
		return nil, err
	}
	if stampErr != nil {
		return nil, fmt.Errorf("stamp the canary in the draft: %w", stampErr)
	}
	res := &onboardReadyResult{Set: set.ID, Suite: set.Suite, Where: where, Tasks: map[string]*schemas.AdmissionRecord{}, Problems: []suite.Problem{}}
	reasons := map[string]string{}
	var runErr error
	if env.Hosted {
		runErr = onboardReadyHosted(ctx, env, j, set, p.Task, log, progress, res, reasons)
	} else {
		runErr = onboardReadyLocal(ctx, env, set, p.Task, log, progress, res)
	}
	for _, rec := range res.Tasks {
		if rec != nil && rec.Admitted {
			res.Admitted++
		} else {
			res.Failed++
		}
	}
	_, saveErr := updateSet(root, p.Set, func(s *onboard.DraftSet) {
		s.ApplyReadiness(res.Tasks, reasons)
		r := s.Readiness
		if r == nil || r.JobID != j.ID {
			r = &onboard.Readiness{JobID: j.ID, Where: where}
			s.Readiness = r
		}
		r.Status, r.Done, r.Admitted, r.Refused, r.ExpID, r.At = onboard.StatusDone, len(res.Tasks), res.Admitted, res.Failed, res.ExpID, time.Now().UTC()
		r.Problems = nil
		for _, pr := range res.Problems {
			if pr.Severity == suite.SeverityError {
				r.Problems = append(r.Problems, onboard.Problem{Ref: pr.Path, Message: pr.Message})
			}
		}
		if runErr != nil {
			r.Status, r.Error = onboard.StatusFailed, runErr.Error()
		}
	})
	if runErr != nil {
		return res, runErr
	}
	if saveErr != nil {
		return res, saveErr
	}
	log.Printf("readiness on draft %s: %d ready, %d not ready", set.ID, res.Admitted, res.Failed)
	return res, nil
}

// onboardReadyLocal admits the draft suite in its own benchmarks root.
func onboardReadyLocal(ctx context.Context, env jobEnv, set *onboard.DraftSet, task string, log jobs.Logger, progress func(any), res *onboardReadyResult) error {
	a := env.App
	cfg := *a.cfg
	cfg.BenchmarksRoot = set.BenchRoot()
	report := admitProgress(progress)
	aenv := ops.AdmitEnv{Config: &cfg, Docker: a.docker, Logger: a.logger,
		Registry: func(context.Context, bool) (*schemas.Registry, error) { return a.reg, nil },
		Progress: func(done, total int, id string, rec *schemas.AdmissionRecord) {
			if report != nil {
				report(done, total, id, rec)
			}
			_, _ = updateSet(a.cfg.Root, set.ID, func(s *onboard.DraftSet) {
				if s.Readiness != nil {
					s.Readiness.Done, s.Readiness.Total = done, total
					if rec != nil {
						if rec.Admitted {
							s.Readiness.Admitted++
						} else {
							s.Readiness.Refused++
						}
					}
				}
				if rec != nil {
					s.ApplyReadiness(map[string]*schemas.AdmissionRecord{id: rec}, nil)
				}
			})
		}}
	rep, err := ops.SuiteAdmit(ctx, aenv, set.Suite, task, log)
	if rep != nil {
		res.Tasks, res.Order = rep.Tasks, rep.Order
		if rep.Problems != nil {
			res.Problems = rep.Problems
		}
	}
	if res.Tasks == nil {
		res.Tasks = map[string]*schemas.AdmissionRecord{}
	}
	return err
}

// stagingSuiteID is the benchmark id a hosted readiness run stages a draft as.
func stagingSuiteID(set *onboard.DraftSet) string { return "draft-" + set.ID }

// onboardReadyHosted stages the draft as draft-<set> in the org's benchmarks,
// runs the hosted suite.admit bridge on it and reads the records back.
func onboardReadyHosted(ctx context.Context, env jobEnv, j *jobs.Job, set *onboard.DraftSet, task string, log jobs.Logger, progress func(any),
	res *onboardReadyResult, reasons map[string]string) error {
	a := env.App
	bench := a.cfg.Path(a.cfg.BenchmarksRoot)
	id := stagingSuiteID(set)
	dst := filepath.Join(bench, id)
	if err := stageDraft(set.SuiteDir(), dst, id); err != nil {
		return fmt.Errorf("stage the draft: %w", err)
	}
	defer os.RemoveAll(dst)
	log.Printf("staged draft %s as benchmark %s for the readiness run (removed after)", set.ID, id)
	params, _ := json.Marshal(hostedSuiteAdmitParams{Suite: id, Task: task})
	bj := *j
	bj.Params = params
	out, err := runHostedSuiteAdmit(ctx, env, &bj, log, progress)
	refusedOnly := false
	if rep, ok := out.(*hostedAdmitReport); ok && rep != nil {
		refusedOnly = rep.Failed > 0 && len(rep.Tasks) > 0
		res.ExpID = rep.ExpID
		for _, t := range rep.Tasks {
			res.Order = append(res.Order, t.Task)
			if t.Reason != "" {
				reasons[t.Task] = t.Reason
			}
		}
	}
	if s, lerr := loadSuite(ctx, bench, id); lerr == nil && s.Lock != nil {
		for tid, tl := range s.Lock.Tasks {
			if tl.Admission != nil && (task == "" || tid == task) {
				res.Tasks[tid] = tl.Admission
			}
		}
	}
	if err != nil && refusedOnly && len(res.Tasks) > 0 {
		return nil // refused tasks are results, not a failed run
	}
	return err
}

// stageDraft copies a draft suite folder to dst with suite.yaml's id set to id.
func stageDraft(src, dst, id string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if !info.Mode().IsRegular() || rel == "suite.lock" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if rel == suite.SuiteFile {
			if data, err = setYAMLID(data, id); err != nil {
				return err
			}
		}
		return os.WriteFile(out, data, info.Mode().Perm()|0o044)
	})
	if err != nil {
		os.RemoveAll(dst)
	}
	return err
}

func setYAMLID(src []byte, id string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("suite.yaml is not a mapping")
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "id" {
			m.Content[i+1].Value = id
		}
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// onboardPublishResult is onboard.publish's result.
type onboardPublishResult struct {
	*onboard.PublishResult
	Carried []string `json:"carried"`
	Recheck []string `json:"recheck"`
}

func runOnboardPublish(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p handlers.OnboardPublishParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	root, err := onboardRoot(env)
	if err != nil {
		return nil, err
	}
	a := env.App
	bench := a.cfg.Path(a.cfg.BenchmarksRoot)
	set, err := onboard.LoadSet(root, p.Set)
	if err != nil {
		return nil, err
	}
	if suite.IsLibrarySuite(bench, set.Suite) {
		return nil, fmt.Errorf("benchmark %s: %w", set.Suite, schemas.ErrReadOnly)
	}
	unlock := set.Lock()
	defer unlock()
	if set, err = onboard.LoadSet(root, p.Set); err != nil {
		return nil, err
	}
	var store projectfs.Store = env.Services.Projects
	oenv := &onboard.Env{Root: root, BenchmarksDir: bench, Hosted: env.Hosted, Actor: jobActor(j), Store: store}
	pres, err := onboard.Publish(ctx, oenv, set, onboard.PublishOptions{Tasks: p.Tasks, IncludeUnready: p.IncludeUnready, Actor: jobActor(j)})
	if err != nil {
		return pres, err
	}
	out := &onboardPublishResult{PublishResult: pres, Carried: []string{}, Recheck: []string{}}
	log.Printf("published %d tasks into %s", len(pres.Tasks), set.Suite)
	for _, w := range pres.Warnings {
		log.Warnf("%s", w)
	}
	carried, recheck, cerr := carryReadiness(ctx, env, set.Suite, pres, jobActor(j))
	out.Carried, out.Recheck = carried, recheck
	if cerr != nil {
		log.Warnf("readiness records were not carried into the lock: %v", cerr)
	}
	for _, id := range carried {
		log.Printf("READY   %s (readiness record carried: same files and settings)", id)
	}
	for _, id := range recheck {
		log.Printf("CHECK   %s: run the readiness checks on the benchmark", id)
	}
	if pres.HeldOut > 0 {
		log.Printf("%d held out (the plan holds out %d%%)", pres.HeldOut, set.Plan.Hold.Pct)
	}
	if len(recheck) > 0 {
		out.Next = fmt.Sprintf("Published. %d tasks need the readiness checks on the benchmark's Build tab before evaluations use them.", len(recheck))
	}
	// The set's lock is still held here, so reload and save directly
	// (updateSet would take the same lock again and deadlock).
	if s, err := onboard.LoadSet(root, set.ID); err != nil {
		log.Warnf("record the publish on the draft: %v", err)
	} else if s.Published != nil {
		s.Published.Carried, s.Published.Recheck, s.Published.JobID = carried, recheck, j.ID
		if err := s.Save(); err != nil {
			log.Warnf("record the publish on the draft: %v", err)
		}
	}
	return out, nil
}

// carriesInto reports whether the draft's admission record of tid still
// holds in the published benchmark with fingerprints f. A record with a
// carry fingerprint is compared by what grades the task (fix wave 3, N252):
// the staged draft and the published benchmark differ in suite.yaml's id,
// description and canary, which the full admission fingerprint binds, so
// nothing carried before. An older record keeps the full comparison.
func carriesInto(rec *schemas.AdmissionRecord, f *schemas.LockFingerprints, tid string) bool {
	if rec == nil || !rec.Admitted || rec.Fingerprint == "" {
		return false
	}
	if rec.CarryFingerprint != "" {
		return suite.CarryFingerprint(f, tid) == rec.CarryFingerprint
	}
	return suite.AdmissionFingerprint(f, tid) == rec.Fingerprint
}

// carryReadiness writes the draft's admission records of the published
// tasks into the benchmark's lock when each record's fingerprint (suite and
// task settings and files) matches the published task exactly; any other
// task is listed for a readiness run on the benchmark.
func carryReadiness(ctx context.Context, env jobEnv, id string, pres *onboard.PublishResult, actor string) ([]string, []string, error) {
	carried, recheck := []string{}, []string{}
	a := env.App
	s, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), id)
	if err != nil {
		return carried, pres.Tasks, err
	}
	lock, err := suite.LockWith(s, time.Now(), lockFingerprintOptions(ctx, a.cfg, a.docker, suite.LockPinsLocal(s.Lock)))
	if err != nil {
		return carried, pres.Tasks, err
	}
	for _, tid := range pres.Tasks {
		rec := pres.Records[tid]
		if !carriesInto(rec, lock.Fingerprints, tid) {
			recheck = append(recheck, tid)
			continue
		}
		cp := *rec
		if err := suite.RecordAdmission(lock, tid, &cp); err != nil {
			recheck = append(recheck, tid)
			continue
		}
		carried = append(carried, tid)
	}
	if len(carried) == 0 {
		return carried, recheck, nil
	}
	if _, err := writeHostedLock(ctx, env, s, lock, actor, "readiness carried from an Add benchmark draft"); err != nil {
		return []string{}, pres.Tasks, err
	}
	return carried, recheck, nil
}
