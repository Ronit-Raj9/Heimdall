package codescan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// The recorded fixture: bandit 1.9.4 (PyPI, bandit[sarif]) run on 18 Nov
// 2026 as `bandit -q -r app -f sarif -o bandit.sarif` on a two-commit
// sample repository; testdata/bandit-1.9.4.diff is `git diff` between the
// commits. Of its 7 results, B602 (line 11) and B404 (line 3) sit on lines
// that were already there, so 5 fall on added lines.
func recordedFixture(t *testing.T) ([]byte, []byte) {
	t.Helper()
	sarif, err := os.ReadFile("testdata/bandit-1.9.4.sarif")
	if err != nil {
		t.Fatal(err)
	}
	diff, err := os.ReadFile("testdata/bandit-1.9.4.diff")
	if err != nil {
		t.Fatal(err)
	}
	return sarif, diff
}

func TestParseRecordedSARIF(t *testing.T) {
	sarif, diff := recordedFixture(t)
	added, err := AddedLines(diff)
	if err != nil {
		t.Fatal(err)
	}
	fs, total, err := ParseSARIF("bandit", sarif, added, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 7 || len(fs) != 5 {
		t.Fatalf("total %d, kept %d: %+v", total, len(fs), fs)
	}
	want := map[string]struct {
		line     int
		cwe, sev string
	}{
		"bandit:B403": {2, "CWE-502", "low"},
		"bandit:B301": {19, "CWE-502", "medium"},
		"bandit:B506": {23, "CWE-20", "medium"},
		"bandit:B501": {27, "CWE-295", "high"},
		"bandit:B324": {31, "CWE-327", "high"},
	}
	for _, f := range fs {
		w, ok := want[f.Rule]
		if !ok || f.Line != w.line || f.CWE != w.cwe || f.Severity != w.sev || f.Source != "bandit" || f.File != "app/server.py" || f.PatchLine == 0 || f.Snippet == "" {
			t.Errorf("finding %+v, want %+v", f, w)
		}
	}
	for _, f := range fs {
		if f.Rule == "bandit:B602" || f.Rule == "bandit:B404" {
			t.Errorf("pre-existing line reported: %+v", f)
		}
	}
}

func TestParseSARIFShapes(t *testing.T) {
	added := map[string]map[int]addedLine{"cmd/x.go": {10: {Patch: 7, Text: `exec.Command("sh", "-c", c)`}}}
	// gosec-style: CWE through rule relationships, absolute file URI.
	gosec := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"gosec","rules":[{"id":"G204","shortDescription":{"text":"Subprocess launched with variable"},
		"relationships":[{"target":{"id":"78","toolComponent":{"name":"CWE"}}}],"properties":{"tags":["security","MEDIUM"]}}]}},
		"results":[{"ruleId":"G204","level":"warning","message":{"text":"x"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"file:///src/cmd/x.go"},"region":{"startLine":9,"endLine":11}}}]}]}]}`
	fs, total, err := ParseSARIF("gosec", []byte(gosec), added, "/src")
	if err != nil || total != 1 || len(fs) != 1 || fs[0].CWE != "CWE-78" || fs[0].Line != 10 || fs[0].Severity != "medium" || fs[0].Title != "Subprocess launched with variable" {
		t.Fatalf("gosec: %+v %d %v", fs, total, err)
	}
	// semgrep-style: CWE in a tag, numeric security-severity.
	semgrep := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"semgrep","rules":[{"id":"python.lang.security.audit.x","name":"x",
		"properties":{"tags":["CWE-78: Improper Neutralization of Special Elements used in an OS Command"],"security-severity":"9.1"}}]}},
		"results":[{"ruleId":"python.lang.security.audit.x","message":{"text":"y"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"cmd/x.go"},"region":{"startLine":10}}}]}]}]}`
	fs, _, err = ParseSARIF("semgrep", []byte(semgrep), added, "")
	if err != nil || len(fs) != 1 || fs[0].CWE != "CWE-78" || fs[0].Severity != "critical" {
		t.Fatalf("semgrep: %+v %v", fs, err)
	}
	for _, bad := range []string{`not json`, `{"version":"2.0.0","runs":[]}`} {
		if _, _, err := ParseSARIF("x", []byte(bad), added, ""); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestCheckSuite(t *testing.T) {
	base := func(sc ...schemas.CodeScanner) *schemas.Suite {
		return &schemas.Suite{ID: "s", Verifier: schemas.SuiteVerifier{Command: []string{"pytest"}}, CodeScan: &schemas.SuiteCodeScan{External: sc}}
	}
	ok := schemas.CodeScanner{ID: "bandit", Image: "base", Command: []string{"bandit", "-r", "/workspace", "-f", "sarif", "-o", "/grader/out/b.sarif"}, SARIF: "/grader/out/b.sarif"}
	if err := CheckSuite(base(ok)); err != nil {
		t.Fatal(err)
	}
	pinned := ok
	pinned.Image = "ghcr.io/x/semgrep@sha256:" + strings.Repeat("a", 64)
	if err := CheckSuite(base(pinned)); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*schemas.Suite){
		"unpinned image":  func(s *schemas.Suite) { s.CodeScan.External[0].Image = "semgrep/semgrep:latest" },
		"sarif outside":   func(s *schemas.Suite) { s.CodeScan.External[0].SARIF = "/workspace/out.sarif" },
		"empty command":   func(s *schemas.Suite) { s.CodeScan.External[0].Command = nil },
		"builtin id":      func(s *schemas.Suite) { s.CodeScan.External[0].ID = "builtin" },
		"duplicate id":    func(s *schemas.Suite) { s.CodeScan.External = append(s.CodeScan.External, s.CodeScan.External[0]) },
		"long timeout":    func(s *schemas.Suite) { s.CodeScan.External[0].TimeoutS = 99999 },
		"snapshot suite":  func(s *schemas.Suite) { s.Verifier.Mode = schemas.VerifierSnapshot },
		"no clean room":   func(s *schemas.Suite) { s.Verifier.Command = nil },
		"bad id":          func(s *schemas.Suite) { s.CodeScan.External[0].ID = "Bandit Scanner" },
		"sarif subfolder": func(s *schemas.Suite) { s.CodeScan.External[0].SARIF = "/grader/out/x/b.sarif" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			s := base(ok)
			mutate(s)
			if err := CheckSuite(s); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestCheckPolicy(t *testing.T) {
	for _, p := range []*schemas.CodeScanPolicy{nil, {}, {VetoAt: "high"}, {Ignore: []string{"code-eval", "bandit:B101"}}} {
		if err := CheckPolicy(p); err != nil {
			t.Errorf("%+v: %v", p, err)
		}
	}
	for _, p := range []*schemas.CodeScanPolicy{{VetoAt: "severe"}, {Ignore: []string{"no-such-rule"}}, {Ignore: []string{":x"}}} {
		if err := CheckPolicy(p); err == nil {
			t.Errorf("%+v accepted", p)
		}
	}
}

// TestSourceWithRecordedScanner runs the evidence source with a runner that
// returns the recorded SARIF, as the clean room would.
func TestSourceWithRecordedScanner(t *testing.T) {
	sarif, diff := recordedFixture(t)
	dir := t.TempDir()
	dp := filepath.Join(dir, "workspace.diff")
	if err := os.WriteFile(dp, diff, 0o600); err != nil {
		t.Fatal(err)
	}
	suite := &schemas.Suite{ID: "s", BaseImage: schemas.ImageSpec{Tag: "bench/s-base:1"}, Verifier: schemas.SuiteVerifier{Command: []string{"pytest"}},
		CodeScan: &schemas.SuiteCodeScan{External: []schemas.CodeScanner{{ID: "bandit", Image: "base", Command: []string{"bandit"}, SARIF: "/grader/out/b.sarif"}}}}
	var gotImage string
	run := func(_ context.Context, _ *schemas.RunContext, _ *schemas.Diff, image string, _ schemas.CodeScanner, timeout time.Duration) (*ScanRun, error) {
		gotImage = image
		if timeout != defaultScanTimeoutS*time.Second {
			t.Errorf("timeout %v", timeout)
		}
		return &ScanRun{Applied: true, ExitCode: 1, SARIF: sarif}, nil
	}
	ev := &schemas.Evidence{Diff: &schemas.Diff{Path: dp, SHA256: "sha256:x"}}
	if err := NewSource(run).Collect(context.Background(), &schemas.RunContext{Suite: suite}, ev); err != nil {
		t.Fatal(err)
	}
	cs := ev.CodeScan
	if gotImage != "bench/s-base:1" || cs == nil || !cs.Scanned || len(cs.External) != 1 || cs.External[0].Error != "" || cs.External[0].Results != 7 || cs.External[0].OnAdded != 5 || cs.External[0].ExitCode != 1 || !strings.HasPrefix(cs.External[0].SARIFSHA256, "sha256:") {
		t.Fatalf("code scan %+v", cs)
	}
	var builtin, external []string
	for _, f := range cs.Findings {
		if f.Source == SourceBuiltin {
			builtin = append(builtin, f.Rule)
		} else {
			external = append(external, f.Rule)
		}
	}
	// The builtin rules see the same four problems; the pre-existing
	// shell=True stays unreported by both.
	for _, r := range []string{"insecure-deserialization", "tls-verify-disabled", "weak-password-hash"} {
		if !slices.Contains(builtin, r) {
			t.Errorf("builtin %v lacks %s", builtin, r)
		}
	}
	if slices.Contains(builtin, "shell-injection-python") || len(external) != 5 {
		t.Errorf("builtin %v, external %v", builtin, external)
	}
	if got, sealed := ForRun(ev); !sealed || got != cs {
		t.Error("ForRun did not use the sealed scan")
	}
	b, _ := json.Marshal(cs)
	if utils.HasUnredactedSecrets(string(b)) {
		t.Fatalf("evidence would not seal: %s", b)
	}

	// A failing scanner is recorded, and the counts become incomplete.
	ev = &schemas.Evidence{Diff: &schemas.Diff{Path: dp}}
	fail := func(context.Context, *schemas.RunContext, *schemas.Diff, string, schemas.CodeScanner, time.Duration) (*ScanRun, error) {
		return nil, errors.New("image not found")
	}
	_ = NewSource(fail).Collect(context.Background(), &schemas.RunContext{Suite: suite}, ev)
	if ev.CodeScan.External[0].Error != "did not run: image not found" || !strings.Contains(Incomplete(ev.CodeScan), "bandit") {
		t.Fatalf("failure not recorded: %+v", ev.CodeScan.External)
	}
	for res, want := range map[*ScanRun]string{
		{Applied: false}:                    "does not apply",
		{Applied: true, TimedOut: true}:     "timed out",
		{Applied: true, ExitCode: 2}:        "no SARIF file at /grader/out/b.sarif (exit 2)",
		{Applied: true, Problem: "big"}:     "big",
		{Applied: true, SARIF: []byte("x")}: "SARIF does not parse",
	} {
		rec := schemas.CodeScanExternal{}
		recordRun(&rec, res, nil, suite.CodeScan.External[0], nil, "", &schemas.CodeScanEvidence{})
		if !strings.Contains(rec.Error, want) {
			t.Errorf("%+v: error %q, want %q", res, rec.Error, want)
		}
	}
	// No runner configured.
	ev = &schemas.Evidence{Diff: &schemas.Diff{Path: dp}}
	_ = NewSource(nil).Collect(context.Background(), &schemas.RunContext{Suite: suite}, ev)
	if ev.CodeScan.External[0].Error == "" {
		t.Fatal("missing runner not recorded")
	}
	// No diff.
	ev = &schemas.Evidence{}
	_ = NewSource(nil).Collect(context.Background(), nil, ev)
	if ev.CodeScan == nil || ev.CodeScan.Scanned {
		t.Fatalf("no diff: %+v", ev.CodeScan)
	}
}
