package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// planBridgeLauncher plans one slot per task and arm, and records the
// experiments it was asked to plan.
type planBridgeLauncher struct {
	fakeBridgeLauncher
	planned []*schemas.Experiment
}

func (f *planBridgeLauncher) Plan(_ context.Context, e *schemas.Experiment) (*schemas.Manifest, error) {
	f.planned = append(f.planned, e)
	m := &schemas.Manifest{ExpID: "e_hostedtest"}
	for _, id := range e.Conditions[0].Tasks.IDs {
		for _, arm := range []string{admitArmOracle, admitArmNoop} {
			m.Slots = append(m.Slots, schemas.Slot{Task: id, Arm: arm, Counted: true})
		}
	}
	return m, nil
}

// hostedBridgeEnv builds an org-like job env over the project initQuickRepo
// wrote, with the runner check and sealed-run read stubbed: every oracle run
// ends oracle and every noop run ends noop.
func hostedBridgeEnv(t *testing.T, oracle, noop schemas.Outcome) (jobEnv, *planBridgeLauncher) {
	t.Helper()
	a, err := newApp(context.Background(), defaultConfig, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{bridgeOracleAgent, bridgeNoopAgent} {
		if err := os.WriteFile(filepath.Join(a.cfg.Root, f), []byte("id: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := projectfs.Open(a.cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	l := &planBridgeLauncher{}
	oldReq, oldRuns, oldPoll := bridgeRequireRunner, bridgeSealedRuns, bridgePollEvery
	t.Cleanup(func() { bridgeRequireRunner, bridgeSealedRuns, bridgePollEvery = oldReq, oldRuns, oldPoll })
	bridgePollEvery = 5 * time.Millisecond
	bridgeRequireRunner = func(handlers.Launcher) error { return nil }
	bridgeSealedRuns = func(_ context.Context, _ jobEnv, expID string) ([]bridgeRun, error) {
		var out []bridgeRun
		for _, e := range l.planned {
			for _, id := range e.Conditions[0].Tasks.IDs {
				out = append(out, bridgeRun{Task: id, Arm: admitArmOracle, Outcome: string(oracle), RunID: expID + "-a"},
					bridgeRun{Task: id, Arm: admitArmNoop, Outcome: string(noop), RunID: expID + "-b"})
			}
		}
		return out, nil
	}
	return jobEnv{App: a, ProjectRoot: a.cfg.Root, Hosted: true, Services: handlers.Services{Launcher: l, Projects: ps}}, l
}

// cutMinedTask cuts the quick repo's one fix commit into suite lib as a
// mined task (instruction drafted, so static validation passes).
func cutMinedTask(t *testing.T, dir, base, ref string) string {
	t.Helper()
	suiteDir := filepath.Join(dir, ".heimdall", "benchmarks", "lib")
	res, err := scaffold.InitTask(context.Background(), scaffold.TaskInitOptions{SuiteDir: suiteDir, ID: "m-1", Repo: dir, Base: base, Reference: ref,
		ExcludeDir: filepath.Join(dir, ".heimdall")})
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffold.MarkMined(res.Dir, scaffold.MinedInstruction("fix the thing", "", nil), "python -m pytest"); err != nil {
		t.Fatal(err)
	}
	return "m-1"
}

// Hosted suite.admit writes suite.lock as local admission does: the task is
// admitted in the lock, its record names the runner experiment, and a fresh
// LockWith of the unchanged suite keeps the admission (same format and
// fingerprints).
func TestHostedSuiteAdmitWritesLock(t *testing.T) {
	dir, base, ref := initQuickRepo(t)
	id := cutMinedTask(t, dir, base, ref)
	env, l := hostedBridgeEnv(t, schemas.OutcomePass, schemas.OutcomeFail)
	raw, _ := json.Marshal(hostedSuiteAdmitParams{Suite: "lib", Task: id})
	out, err := runHostedSuiteAdmit(context.Background(), env, &jobs.Job{Params: raw, CreatedBy: "u1"}, &bridgeTestLog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rep := out.(*hostedAdmitReport)
	if rep.Admitted != 1 || rep.Failed != 0 || rep.LockPath == "" || rep.ExpID != "e_hostedtest" {
		t.Fatalf("report %+v", rep)
	}
	if len(l.planned) != 1 || !l.planned[0].NoPaidTraffic() {
		t.Fatalf("planned %+v", l.planned)
	}
	s, err := loadSuite(context.Background(), env.App.cfg.Path(env.App.cfg.BenchmarksRoot), "lib")
	if err != nil {
		t.Fatal(err)
	}
	if !admittedIn(s.Lock, id) {
		t.Fatalf("lock does not admit %s: %+v", id, s.Lock)
	}
	rec := s.Lock.Tasks[id].Admission
	if rec.Fingerprint == "" || len(rec.Checks) != 3 || !strings.Contains(rec.Checks[1].Detail, "e_hostedtest") {
		t.Fatalf("record %+v", rec)
	}
	if ok, err := admissionCurrent(context.Background(), env.App, "lib", id); err != nil || !ok {
		t.Fatalf("admission not current after a re-lock: %v %v", ok, err)
	}
	data, _ := os.ReadFile(filepath.Join(s.Root, "suite.lock"))
	want, _ := suite.EncodeLock(s.Lock)
	if string(data) != string(want) {
		t.Fatal("suite.lock bytes differ from suite.WriteLock's encoding")
	}
}

// A refused task is recorded as a draft with the failing control named.
func TestHostedSuiteAdmitRecordsRefusal(t *testing.T) {
	dir, base, ref := initQuickRepo(t)
	id := cutMinedTask(t, dir, base, ref)
	env, _ := hostedBridgeEnv(t, schemas.OutcomePass, schemas.OutcomePass)
	raw, _ := json.Marshal(hostedSuiteAdmitParams{Suite: "lib", Task: id})
	out, err := runHostedSuiteAdmit(context.Background(), env, &jobs.Job{Params: raw}, &bridgeTestLog{}, nil)
	if err == nil {
		t.Fatal("a refused task must fail the job")
	}
	if rep := out.(*hostedAdmitReport); rep.Failed != 1 || rep.LockPath == "" {
		t.Fatalf("report %+v", rep)
	}
	s, err := loadSuite(context.Background(), env.App.cfg.Path(env.App.cfg.BenchmarksRoot), "lib")
	if err != nil {
		t.Fatal(err)
	}
	tl := s.Lock.Tasks[id]
	if tl.Status != schemas.TaskDraft || tl.Admission == nil || tl.Admission.Admitted || tl.Admission.Checks[2].Passed {
		t.Fatalf("lock entry %+v", tl)
	}
}

// Hosted tasks.mine cuts the candidate, proves it on a runner through the
// launcher and records the admission; an unproven cut is removed again.
func TestHostedTasksMineProvesThroughRunner(t *testing.T) {
	dir, _, _ := initQuickRepo(t)
	env, l := hostedBridgeEnv(t, schemas.OutcomePass, schemas.OutcomeFail)
	suiteDir := filepath.Join(dir, ".heimdall", "benchmarks", "lib")
	p := tasksMineParams{Suite: "lib", Limit: 1, MinTests: 1}
	out, err := runHostedTasksMine(context.Background(), env, &jobs.Job{}, p, suiteDir, dir, &bridgeTestLog{})
	if err != nil {
		t.Fatal(err)
	}
	rep := out.(*hostedMineReport)
	if len(rep.Proved) != 1 || rep.ExpID != "e_hostedtest" || rep.LockPath == "" {
		t.Fatalf("report %+v", rep)
	}
	if len(l.planned) != 1 || len(l.planned[0].Conditions[0].Tasks.IDs) != 1 || !l.planned[0].NoPaidTraffic() {
		t.Fatalf("planned %+v", l.planned)
	}
	s, err := loadSuite(context.Background(), env.App.cfg.Path(env.App.cfg.BenchmarksRoot), "lib")
	if err != nil {
		t.Fatal(err)
	}
	if s.TaskByID(rep.Proved[0]) == nil || !admittedIn(s.Lock, rep.Proved[0]) {
		t.Fatalf("mined task not in suite or lock: %+v", s.Lock)
	}
}

func TestHostedTasksMineDropsUnproven(t *testing.T) {
	dir, _, _ := initQuickRepo(t)
	env, _ := hostedBridgeEnv(t, schemas.OutcomeFail, schemas.OutcomeFail)
	suiteDir := filepath.Join(dir, ".heimdall", "benchmarks", "lib")
	before, _ := os.ReadFile(filepath.Join(suiteDir, "suite.yaml"))
	out, err := runHostedTasksMine(context.Background(), env, &jobs.Job{}, tasksMineParams{Suite: "lib", Limit: 1, MinTests: 1}, suiteDir, dir, &bridgeTestLog{})
	if err == nil {
		t.Fatal("no proven task must fail the job")
	}
	rep := out.(*hostedMineReport)
	if len(rep.Proved) != 0 || len(rep.Dropped) != 1 {
		t.Fatalf("report %+v", rep)
	}
	after, _ := os.ReadFile(filepath.Join(suiteDir, "suite.yaml"))
	if string(after) != string(before) {
		t.Fatalf("suite.yaml not restored:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(suiteDir, "tasks", rep.Dropped[0].Task)); err == nil {
		t.Fatal("unproven task folder kept")
	}
}

// Hosted full mine is accepted only when the oracle and noop agents exist.
func TestHostedTasksMineValidation(t *testing.T) {
	initQuickRepo(t)
	a, err := newApp(context.Background(), defaultConfig, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env := jobEnv{App: a, ProjectRoot: a.cfg.Root, Hosted: true}
	_ = os.Remove(filepath.Join(a.cfg.Root, bridgeOracleAgent))
	raw := json.RawMessage(`{"suite":"lib","github":"acme/app"}`)
	if _, _, err := validateTasksMine(context.Background(), env, raw); err == nil || !strings.Contains(err.Error(), "oracle") {
		t.Fatalf("missing oracle accepted: %v", err)
	}
	for _, f := range []string{bridgeOracleAgent, bridgeNoopAgent} {
		if err := os.WriteFile(filepath.Join(a.cfg.Root, f), []byte("id: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, target, err := validateTasksMine(context.Background(), env, raw); err != nil || target != "suite:lib" {
		t.Fatalf("hosted full mine refused: %v %q", err, target)
	}
}
