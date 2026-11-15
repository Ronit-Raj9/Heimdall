package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// healthBench scaffolds a bench with one locked qa suite and returns the
// config path and the suite folder.
func healthBench(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	if code := runSuite([]string{"init", "qa1", "-template", "qa", "-config", config}); code != exitOK {
		t.Fatalf("suite init exit %d", code)
	}
	if code := runSuite([]string{"lock", "qa1", "-config", config}); code != exitOK {
		t.Fatalf("suite lock exit %d", code)
	}
	return config, filepath.Join(dir, "benchmarks", "qa1")
}

func TestSuiteQuarantineRestore(t *testing.T) {
	config, suiteDir := healthBench(t)
	s, err := loadSuite(context.Background(), filepath.Dir(suiteDir), "qa1")
	if err != nil {
		t.Fatal(err)
	}
	task := s.Tasks[0].ID
	lockBefore, err := os.ReadFile(filepath.Join(suiteDir, "suite.lock"))
	if err != nil {
		t.Fatal(err)
	}
	usage := [][]string{
		{"quarantine", "qa1/" + task, "-config", config},                // no reason
		{"quarantine", "qa1", "-reason", "x", "-config", config},        // no task
		{"quarantine", "-reason", "x", "-config", config, "a/b", "c/d"}, // two args
		{"restore", "-config", config},                                  // no arg
		{"quarantine", "qa1/", "-reason", "x", "-config", config},       // empty task
	}
	for _, args := range usage {
		if code := runSuite(args); code != exitUsage {
			t.Errorf("suite %v: exit %d, want %d", args, code, exitUsage)
		}
	}
	if code := runSuite([]string{"quarantine", "qa1/nope", "-reason", "x", "-config", config}); code == exitOK {
		t.Error("quarantine of an unknown task succeeded")
	}
	if code := runSuite([]string{"quarantine", "qa1/" + task, "-reason", "verifier flips on the reference", "-config", config}); code != exitOK {
		t.Fatalf("quarantine exit %d", code)
	}
	f, err := suite.ReadHealthFile(suiteDir)
	if err != nil || f.Quarantined[task].Reason != "verifier flips on the reference" {
		t.Fatalf("sidecar %+v %v", f, err)
	}
	lockAfter, err := os.ReadFile(filepath.Join(suiteDir, "suite.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lockBefore, lockAfter) {
		t.Fatal("quarantine changed suite.lock")
	}
	if code := runSuite([]string{"restore", "qa1/" + task, "-config", config}); code != exitOK {
		t.Fatalf("restore exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(suiteDir, schemas.SuiteHealthFileName)); !os.IsNotExist(err) {
		t.Fatalf("sidecar left after restoring the only task: %v", err)
	}
	if code := runSuite([]string{"restore", "qa1/" + task, "-config", config}); code != exitOK {
		t.Fatalf("second restore exit %d", code)
	}
}

func TestSuiteHealthCommand(t *testing.T) {
	config, _ := healthBench(t)
	runs := t.TempDir()
	if code := runSuite([]string{"health", "qa1", "-config", config, "-runs", runs}); code != exitOK {
		t.Fatalf("health exit %d", code)
	}
	if code := runSuite([]string{"health", "qa1", "-config", config, "-runs", runs, "-json"}); code != exitOK {
		t.Fatalf("health -json exit %d", code)
	}
	if code := runSuite([]string{"health", "-config", config}); code != exitUsage {
		t.Fatalf("health without a suite: exit %d", code)
	}
	if code := runSuite([]string{"health", "qa1", "-config", config, "-runs", filepath.Join(runs, "missing")}); code == exitOK {
		t.Fatal("health on a missing run store succeeded")
	}
}

func TestHealthSourceRegrades(t *testing.T) {
	root := t.TempDir()
	src, err := openHealthSource(root)
	if err != nil {
		t.Fatal(err)
	}
	if rgs, err := src.Regrades(context.Background(), "e_none"); err != nil || rgs != nil {
		t.Fatalf("experiment without regrades: %v %v", rgs, err)
	}
	base := filepath.Join(root, "e_one", regradesDir)
	for _, d := range []string{"rg_full", "rg_partial"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	info := `{"regrade_id":"rg_full","exp_id":"e_one","suite_version":"1.0.0","grader_version":"g2","created_at":"2026-11-15T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(base, "rg_full", "regrade.json"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	rgs, err := src.Regrades(context.Background(), "e_one")
	if err != nil {
		t.Fatal(err)
	}
	if len(rgs) != 1 || rgs[0].ID != "rg_full" || rgs[0].GraderVersion != "g2" || rgs[0].SuiteVersion != "1.0.0" {
		t.Fatalf("regrades %+v", rgs)
	}
	if _, err := openHealthSource(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing run store opened")
	}
}

func TestAutoQuarantineOnlyFlaky(t *testing.T) {
	_, suiteDir := healthBench(t)
	s, err := loadSuite(context.Background(), filepath.Dir(suiteDir), "qa1")
	if err != nil {
		t.Fatal(err)
	}
	id := s.Tasks[0].ID
	h := &schemas.SuiteHealth{Tasks: []schemas.TaskHealth{
		{Task: id, Verifier: schemas.VerifierHealth{Flaky: true, Evidence: []schemas.VerifierEvidence{
			{Kind: schemas.EvidenceAdmissionRef, Detail: "pass, fail, pass"}}}},
		{Task: "dead-task", Flags: []string{schemas.HealthDead, schemas.HealthDifficultyMismatch}},
	}}
	var log strings.Builder
	if err := autoQuarantine(s, h, nil, &log); err != nil {
		t.Fatal(err)
	}
	f, err := suite.ReadHealthFile(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	q, ok := f.Quarantined[id]
	if !ok || q.Source != schemas.QuarantineAuto || !strings.Contains(q.Reason, "admission_reference") || len(q.Evidence) != 1 {
		t.Fatalf("auto quarantine %+v", f)
	}
	if len(f.Quarantined) != 1 {
		t.Fatalf("difficulty or saturation quarantined a task: %+v", f.Quarantined)
	}
	if h.Tasks[0].Quarantine == nil || !strings.Contains(log.String(), "auto-quarantined qa1/"+id) {
		t.Fatalf("report not updated: %+v %q", h.Tasks[0], log.String())
	}
	// A task already quarantined by hand keeps its record.
	manual := map[string]schemas.TaskQuarantine{id: {Reason: "by hand"}}
	if err := suite.Quarantine(s, id, schemas.TaskQuarantine{Reason: "by hand"}); err != nil {
		t.Fatal(err)
	}
	if err := autoQuarantine(s, h, manual, &log); err != nil {
		t.Fatal(err)
	}
	if f, _ := suite.ReadHealthFile(suiteDir); f.Quarantined[id].Reason != "by hand" {
		t.Fatalf("auto quarantine replaced a manual one: %+v", f.Quarantined[id])
	}
}

func TestPrintHealth(t *testing.T) {
	rate := 0.5
	h := &schemas.SuiteHealth{Suite: "s", Version: "1", Thresholds: schemas.HealthThresholds{MinRuns: 6, MinConfigs: 2},
		Counts: map[string]int{schemas.HealthFlakyVerifier: 1},
		Notes:  []string{"1 runs of other task versions were left out"},
		Tasks: []schemas.TaskHealth{{
			Task: "t1", Split: "public", Flags: []string{schemas.HealthFlakyVerifier},
			Verifier:   schemas.VerifierHealth{Flaky: true, Checks: 3, Evidence: []schemas.VerifierEvidence{{Kind: "oracle_failed", Detail: "x", ExpID: "e1", RunIDs: []string{"r1"}}}},
			Variance:   schemas.OutcomeVariance{Configs: 2, Unstable: 1},
			Infra:      schemas.InfraHealth{Attempts: 4, Rate: 0.25},
			Difficulty: schemas.DifficultyHealth{PassRate: &rate, Interval: &[2]float64{0.2, 0.8}, Runs: 8, Configs: 2, Observed: "medium", Declared: "hard", Discrimination: "discriminating"},
			Quarantine: &schemas.TaskQuarantine{Reason: "flaky", At: time.Unix(0, 0)},
		}},
	}
	var b strings.Builder
	printHealth(&b, h)
	out := b.String()
	for _, want := range []string{"FLAKY (1 evidence)", "1/2 configs mixed", "25% of 4", "0.50 [0.20, 0.80]", "medium/hard",
		"quarantined: flaky", "verifier evidence: oracle_failed: x (experiment e1, runs r1)", "note: 1 runs of other task versions", "1 task(s) with a flaky verifier"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
