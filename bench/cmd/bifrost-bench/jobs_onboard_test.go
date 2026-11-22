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
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/onboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// A draft's readiness records carry into the published benchmark's lock
// when the published files and settings are the ones that were checked.
func TestOnboardPublishCarriesReadiness(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "benchmarks"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &lib.Config{Root: root, BenchmarksRoot: "benchmarks"}
	env := jobEnv{App: &app{cfg: cfg, logger: schemas.NoopLogger{}}, ProjectRoot: root}
	oenv := &onboard.Env{Root: root}
	csv := "question,answer\nWhat is 6 x 7?,42\nCapital of France?,Paris\nLargest planet?,Jupiter\n"
	set, err := onboard.Draft(ctx, oenv, onboard.Input{Data: csv, Name: "quiz.csv"}, onboard.Plan{Suite: "quiz"})
	if err != nil {
		t.Fatal(err)
	}
	ds, err := loadSuite(ctx, set.BenchRoot(), "quiz")
	if err != nil {
		t.Fatal(err)
	}
	dlock, err := suite.LockWith(ds, time.Now(), lockFingerprintOptions(ctx, cfg, env.App.docker, false))
	if err != nil {
		t.Fatal(err)
	}
	recs := map[string]*schemas.AdmissionRecord{}
	for _, tk := range set.Tasks[:2] {
		recs[tk.ID] = &schemas.AdmissionRecord{Admitted: true, At: time.Now(), Fingerprint: suite.AdmissionFingerprint(dlock.Fingerprints, tk.ID),
			Checks: []schemas.AdmissionCheck{{Name: "reference_1", Want: "pass", Got: "pass", Passed: true}}}
	}
	recs[set.Tasks[2].ID] = &schemas.AdmissionRecord{Admitted: true, At: time.Now(), Fingerprint: "stale"}
	set.ApplyReadiness(recs, nil)
	if err := set.Save(); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(handlers.OnboardPublishParams{Set: set.ID})
	if _, _, err := validateOnboardPublish(ctx, env, params); err != nil {
		t.Fatal(err)
	}
	out, err := runOnboardPublish(ctx, env, &jobs.Job{ID: "job_p", Params: params, CreatedBy: "tester"}, discardJobLog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(*onboardPublishResult)
	if len(res.Tasks) != 3 || len(res.Carried) != 2 || len(res.Recheck) != 1 || res.Recheck[0] != set.Tasks[2].ID {
		t.Fatalf("publish %+v carried %v recheck %v", res.PublishResult, res.Carried, res.Recheck)
	}
	s, err := loadSuite(ctx, filepath.Join(root, "benchmarks"), "quiz")
	if err != nil {
		t.Fatal(err)
	}
	if s.Lock == nil || s.Lock.Tasks[res.Carried[0]].Status != schemas.TaskAdmitted || s.Lock.Tasks[res.Recheck[0]].Status == schemas.TaskAdmitted {
		t.Fatalf("lock %+v", s.Lock)
	}
	got, _ := onboard.LoadSet(root, set.ID)
	if got.Published == nil || got.Published.JobID != "job_p" || len(got.Published.Carried) != 2 {
		t.Fatalf("set published %+v", got.Published)
	}
	// No held-out tasks here: stamping the canary on a held-out publish
	// changes the suite and task files, so those tasks are rechecked.
	if res.HeldOut != 0 || res.Canary != "" {
		t.Errorf("held out %d canary %q", res.HeldOut, res.Canary)
	}
}

// A held-out draft gets the canary stamped before readiness (lane H07), so
// publish leaves the files alone and every record carries.
func TestOnboardPublishHeldOutCarriesReadiness(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "benchmarks"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &lib.Config{Root: root, BenchmarksRoot: "benchmarks"}
	env := jobEnv{App: &app{cfg: cfg, logger: schemas.NoopLogger{}}, ProjectRoot: root}
	oenv := &onboard.Env{Root: root}
	csv := "question,answer\nWhat is 6 x 7?,42\nCapital of France?,Paris\nLargest planet?,Jupiter\n"
	set, err := onboard.Draft(ctx, oenv, onboard.Input{Data: csv, Name: "quiz.csv"}, onboard.Plan{Suite: "quiz", Hold: onboard.SplitPlan{Pct: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if !set.HasHeldOut() {
		t.Fatal("no held-out task")
	}
	guid, changed, err := set.StampCanary(ctx, oenv)
	if err != nil || guid == "" || len(changed) != len(set.Tasks) {
		t.Fatalf("stamp %q %v %v", guid, changed, err)
	}
	if g2, again, err := set.StampCanary(ctx, oenv); err != nil || g2 != guid || len(again) != 0 {
		t.Fatalf("second stamp %q %v %v", g2, again, err)
	}
	if !strings.Contains(set.Tasks[0].Instruction, guid) {
		t.Errorf("instruction preview lacks the canary: %q", set.Tasks[0].Instruction)
	}
	ds, err := loadSuite(ctx, set.BenchRoot(), "quiz")
	if err != nil {
		t.Fatal(err)
	}
	dlock, err := suite.LockWith(ds, time.Now(), lockFingerprintOptions(ctx, cfg, env.App.docker, false))
	if err != nil {
		t.Fatal(err)
	}
	recs := map[string]*schemas.AdmissionRecord{}
	for _, tk := range set.Tasks {
		recs[tk.ID] = &schemas.AdmissionRecord{Admitted: true, At: time.Now(), Fingerprint: suite.AdmissionFingerprint(dlock.Fingerprints, tk.ID),
			Checks: []schemas.AdmissionCheck{{Name: "reference_1", Want: "pass", Got: "pass", Passed: true}}}
	}
	set.ApplyReadiness(recs, nil)
	if err := set.Save(); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(handlers.OnboardPublishParams{Set: set.ID})
	out, err := runOnboardPublish(ctx, env, &jobs.Job{ID: "job_p", Params: params, CreatedBy: "tester"}, discardJobLog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(*onboardPublishResult)
	if res.Canary != guid || res.HeldOut != 3 || len(res.Carried) != 3 || len(res.Recheck) != 0 {
		t.Fatalf("publish canary %q held %d carried %v recheck %v", res.Canary, res.HeldOut, res.Carried, res.Recheck)
	}
}

func TestStageDraftRenamesSuite(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "draft-x")
	os.WriteFile(filepath.Join(src, "suite.yaml"), []byte("id: quiz\nversion: 1.0.0\ntasks: [a]\n"), 0o644)
	os.WriteFile(filepath.Join(src, "suite.lock"), []byte("{}"), 0o644)
	if err := stageDraft(src, dst, "draft-x"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "suite.yaml"))
	if !strings.Contains(string(b), "id: draft-x") {
		t.Errorf("suite.yaml %s", b)
	}
	if _, err := os.Stat(filepath.Join(dst, "suite.lock")); err == nil {
		t.Error("lock copied")
	}
}
