package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

// The printed recorded-mode step, run as printed in a fresh project, puts
// the experiment in its own folder beneath recorded/ (audit 5-B-07).
func TestRecordedCopyNoteNestsTheExperiment(t *testing.T) {
	project := filepath.Join(t.TempDir(), ".heimdall")
	if err := os.MkdirAll(filepath.Join(project, "runs", "e_x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "runs", "e_x", "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(project, "recorded")
	note := recordedCopyNote(rec, project)
	start, end := strings.Index(note, "("), strings.LastIndex(note, ")")
	cmd := strings.ReplaceAll(note[start+1:end], "<exp_id>", "e_x")
	if out, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v %s", cmd, err, out)
	}
	if _, err := os.Stat(filepath.Join(rec, "e_x", "manifest.json")); err != nil {
		t.Fatalf("experiment not nested under recorded/: %v", err)
	}
}

// A misspelled -fail-on verdict is a usage error, not a gate that never
// fails (audit 5-B-10).
func TestParseDecisionsRefusesUnknown(t *testing.T) {
	got, err := parseDecisions(" regressed, inconclusive,,")
	if err != nil || len(got) != 2 || got[1] != "inconclusive" {
		t.Fatalf("valid list: %v %v", got, err)
	}
	if _, err := parseDecisions("regressed,inconclusve"); err == nil || !strings.Contains(err.Error(), "inconclusve") {
		t.Fatalf("typo accepted: %v", err)
	}
	if code := runCheck([]string{"-exp", "e_x", "-fail-on", "inconclusve"}); code != exitUsage {
		t.Fatalf("check with a typo: exit %d", code)
	}
}

// Check fails when it can not write the JSON report beside the markdown
// (audit 5-B-09).
func TestCheckFailsWithoutJSONReport(t *testing.T) {
	_, config := evidenceSandbox(t, "")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "check.json"), 0o755); err != nil { // a folder where the JSON goes
		t.Fatal(err)
	}
	if code := runCheck([]string{"-config", config, "-exp", "e_smoke_sdoj", "-out", filepath.Join(dir, "check.md")}); code != exitInfra {
		t.Fatalf("check with an unwritable JSON report: exit %d", code)
	}
	if code := runCheck([]string{"-config", config, "-exp", "e_smoke_sdoj", "-out", filepath.Join(dir, "ok.md")}); code != exitOK {
		t.Fatalf("normal check: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "ok.json")); err != nil {
		t.Fatal(err)
	}
}

// QC2-21-05, QC2-21-08: check refuses a -out that is also its JSON sidecar,
// and a saved experiment id together with an experiment file; run refuses
// -resume with a file. All before anything runs.
func TestCheckRefusesAmbiguousOutputAndTargets(t *testing.T) {
	_, config := evidenceSandbox(t, "")
	dir := t.TempDir()
	out := filepath.Join(dir, "claimed-markdown.json")
	if code := runCheck([]string{"-config", config, "-exp", "e_smoke_sdoj", "-out", out}); code != exitUsage {
		t.Fatalf("check -out x.json: exit %d", code)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("check wrote the colliding report")
	}
	if code := runCheck([]string{"-config", config, "-exp", "e_smoke_sdoj", "absent-experiment.yaml", "-out", filepath.Join(dir, "r.md")}); code != exitUsage {
		t.Fatalf("check with -exp and a file: exit %d", code)
	}
	if code := runRun([]string{"-config", config, "-resume", "e_smoke_sdoj", "absent-experiment.yaml"}); code != exitUsage {
		t.Fatalf("run with -resume and a file: exit %d", code)
	}
}

// plan, run and check read the experiment file before they set the gateway
// up: a missing file changes nothing on the gateway (audit 5-B-08).
func TestPlanReadsExperimentBeforeGatewaySetup(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("AUDIT_BIFROST_TOKEN", "x")
	dir := t.TempDir()
	config := filepath.Join(dir, "bench.yaml")
	yaml := "runs_root: runs\ngateway:\n  url: " + srv.URL + "\n  admin_token_env: AUDIT_BIFROST_TOKEN\n  provider: anthropic\n  key_slots: [fp-slot-1]\n"
	if err := os.WriteFile(config, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "does-not-exist.yaml")
	for _, args := range [][]string{{"plan", "-config", config, missing}, {"run", "-config", config, missing}, {"check", "-config", config, missing}} {
		var code int
		switch args[0] {
		case "plan":
			code = runPlan(args[1:])
		case "run":
			code = runRun(args[1:])
		case "check":
			code = runCheck(args[1:])
		}
		if code == exitOK {
			t.Fatalf("%s of a missing file: exit 0", args[0])
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d gateway calls before the experiment was read", n)
	}
	// QC2-21-10: a schema-valid experiment naming a missing suite also
	// fails before the gateway is set up.
	raw, err := os.ReadFile("../../experiments/smoke.yaml")
	if err != nil {
		t.Skip("no experiments/smoke.yaml")
	}
	noSuite := filepath.Join(dir, "no-suite.yaml")
	if err := os.WriteFile(noSuite, []byte(regexp.MustCompile(`(?m)^suite: .*$`).ReplaceAllString(string(raw), "suite: no-such-suite@1.0.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExperiment(noSuite); err != nil {
		t.Fatalf("fixture not schema-valid: %v", err)
	}
	for _, run := range []func([]string) int{runPlan, runRun, runCheck} {
		if code := run([]string{"-config", config, noSuite}); code == exitOK {
			t.Fatal("missing suite: exit 0")
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d gateway calls for an experiment with a missing suite", n)
	}
}

// init's next steps follow what it wrote: no build of a base/ folder the
// scaffold did not make for a chosen image (audit 5-B-13).
func TestBaseImageStepFollowsScaffold(t *testing.T) {
	project := t.TempDir()
	if _, cmd, ok := baseImageStep(project, "go-demo", "audit-toolchain:fixture"); !ok || !strings.Contains(cmd, "docker image inspect") ||
		!strings.Contains(cmd, "audit-toolchain:fixture") || strings.Contains(cmd, "docker build") {
		t.Fatalf("custom image step %q", cmd)
	}
	if _, _, ok := baseImageStep(project, "go-demo", ""); ok {
		t.Fatal("step for a suite with neither base/ nor -image")
	}
	base := filepath.Join(project, "benchmarks", "go-demo", "base")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, cmd, ok := baseImageStep(project, "go-demo", ""); !ok || !strings.Contains(cmd, "docker build -t bench/go-demo-base:1") {
		t.Fatalf("base build step %q", cmd)
	}
}
