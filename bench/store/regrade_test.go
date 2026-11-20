package store

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestWriteRegrade(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	r1, seal1 := sealedRun(t, s, m, "s_a1", "r_1")
	r2, _ := sealedRun(t, s, m, "s_b1", "r_2")
	open := mustNewRun(t, s, m, "s_c1", "r_open", 1, schemas.OutcomePass)
	g := func(grader string) *schemas.Grade {
		return &schemas.Grade{Passed: true, SuiteVersion: "1.1.0", GraderVersion: grader}
	}
	tests := []struct {
		name    string
		regrade string
		runID   string
		grade   *schemas.Grade
		wantErr error
		anyErr  bool
	}{
		{name: "first grade", regrade: "rg_1", runID: r1.RunID, grade: g("2.0.0")},
		{name: "same run twice", regrade: "rg_1", runID: r1.RunID, grade: g("2.0.0"), wantErr: schemas.ErrExists},
		{name: "other grader version", regrade: "rg_1", runID: r2.RunID, grade: g("3.0.0"), anyErr: true},
		{name: "unsealed run", regrade: "rg_1", runID: open.RunID, grade: g("2.0.0"), wantErr: schemas.ErrNotFound},
		{name: "unknown run", regrade: "rg_1", runID: "r_none", grade: g("2.0.0"), wantErr: schemas.ErrNotFound},
		{name: "wrong parent seal", regrade: "rg_2", runID: r2.RunID,
			grade: &schemas.Grade{ParentSeal: seal1.Hash, GraderVersion: "2.0.0"}, wantErr: schemas.ErrSealMismatch},
		{name: "bad regrade id", regrade: "../rg", runID: r2.RunID, grade: g("2.0.0"), anyErr: true},
		{name: "nil grade", regrade: "rg_1", runID: r2.RunID, anyErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := s.WriteRegrade(ctx, "e_test", tc.regrade, tc.runID, tc.grade)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			case tc.anyErr:
				if err == nil {
					t.Fatal("want error")
				}
				return
			case err != nil:
				t.Fatalf("WriteRegrade: %v", err)
			}
			if tc.grade.ParentSeal != "" {
				t.Fatal("WriteRegrade modified the caller's grade")
			}
		})
	}
	var got schemas.Grade
	if err := readJSON(filepath.Join(s.Root(), "e_test", "regrades", "rg_1", "runs", "r_1", "grade.json"), &got); err != nil {
		t.Fatal(err)
	}
	if got.ParentSeal != seal1.Hash || got.GraderVersion != "2.0.0" {
		t.Fatalf("regrade grade = %+v", got)
	}
	var info RegradeInfo
	if err := readJSON(filepath.Join(s.Root(), "e_test", "regrades", "rg_1", "regrade.json"), &info); err != nil {
		t.Fatal(err)
	}
	if info.SuiteVersion != "1.1.0" || info.GraderVersion != "2.0.0" || info.CreatedAt.IsZero() || info.RegradeID != "rg_1" {
		t.Fatalf("regrade.json = %+v", info)
	}
	if err := s.Verify(ctx, "e_test", r1.RunID); err != nil {
		t.Fatalf("regrade touched the sealed run: %v", err)
	}
}

func TestWriteGateRegrades(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	r1, seal1 := sealedRun(t, s, m, "s_a1", "r_1")
	r2, _ := sealedRun(t, s, m, "s_b1", "r_2")
	open := mustNewRun(t, s, m, "s_c1", "r_open", 1, schemas.OutcomePass)
	if err := s.WriteRegrade(ctx, "e_test", "rg_grades", r1.RunID, &schemas.Grade{SuiteVersion: "1.0.0", GraderVersion: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	v11 := map[string]string{"g1_protected_paths": "1.1.0"}
	info := func(v map[string]string) RegradeInfo {
		return RegradeInfo{SuiteVersion: "1.0.0", GateVersions: v, Note: "rule 1.1.0"}
	}
	res := func() *GateRegrade {
		return &GateRegrade{Outcome: schemas.OutcomePass, SealedOutcome: schemas.OutcomeIntegrityViolation,
			Y: schemas.Observed(1, schemas.EvidenceOutcome), Gates: []schemas.GateResult{{Gate: "g1_protected_paths", Version: "1.1.0"}}}
	}
	first := res()
	tests := []struct {
		name    string
		regrade string
		info    RegradeInfo
		results map[string]*GateRegrade
		wantErr error
		anyErr  bool
	}{
		{name: "first regate", regrade: "rg_gates", info: info(v11), results: map[string]*GateRegrade{r1.RunID: first}},
		{name: "same regrade id twice", regrade: "rg_gates", info: info(v11), results: map[string]*GateRegrade{r2.RunID: res()}, wantErr: schemas.ErrExists},
		{name: "regrade id that holds grades", regrade: "rg_grades", info: info(v11), results: map[string]*GateRegrade{r2.RunID: res()}, wantErr: schemas.ErrExists},
		{name: "no gate versions", regrade: "rg_other", info: info(nil), results: map[string]*GateRegrade{r2.RunID: res()}, anyErr: true},
		{name: "no runs", regrade: "rg_other", info: info(v11), anyErr: true},
		{name: "unsealed run", regrade: "rg_open", info: info(v11), results: map[string]*GateRegrade{r2.RunID: res(), open.RunID: res()}, wantErr: schemas.ErrNotFound},
		{name: "wrong parent seal", regrade: "rg_seal", info: info(v11), results: map[string]*GateRegrade{r2.RunID: {ParentSeal: seal1.Hash}}, wantErr: schemas.ErrSealMismatch},
		{name: "nil result", regrade: "rg_nil", info: info(v11), results: map[string]*GateRegrade{r2.RunID: nil}, anyErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := s.WriteGateRegrades(ctx, "e_test", tc.regrade, tc.info, tc.results)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			case tc.anyErr:
				if err == nil {
					t.Fatal("want error")
				}
			case err != nil:
				t.Fatalf("WriteGateRegrades: %v", err)
			}
			if err != nil && tc.regrade != "rg_gates" && tc.regrade != "rg_grades" {
				if _, statErr := os.Stat(filepath.Join(s.Root(), "e_test", "regrades", tc.regrade)); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("a failed batch left regrade %s behind: %v", tc.regrade, statErr)
				}
			}
		})
	}
	if first.ParentSeal != "" || first.Hash != "" {
		t.Fatal("WriteGateRegrades modified the caller's result")
	}
	if left, _ := filepath.Glob(filepath.Join(s.Root(), "e_test", ".regate-*")); len(left) != 0 {
		t.Fatalf("staging folders left behind: %v", left)
	}
	var got GateRegrade
	if err := readJSON(filepath.Join(s.Root(), "e_test", "regrades", "rg_gates", "runs", "r_1", "regate.json"), &got); err != nil {
		t.Fatal(err)
	}
	if err := verifyGateRegrade(&got, seal1.Hash); err != nil || got.Outcome != schemas.OutcomePass {
		t.Fatalf("regate.json = %+v, verify: %v", got, err)
	}
	got.Outcome = schemas.OutcomeFail
	if err := verifyGateRegrade(&got, seal1.Hash); !errors.Is(err, schemas.ErrSealMismatch) {
		t.Fatalf("edited regate verified: %v", err)
	}
	info2, err := s.RegradeInfo(ctx, "e_test", "rg_gates")
	if err != nil || info2.GateVersions["g1_protected_paths"] != "1.1.0" || info2.GraderVersion != "" || info2.Note != "rule 1.1.0" || info2.CreatedAt.IsZero() ||
		len(info2.Runs) != 1 || info2.Runs[0] != r1.RunID {
		t.Fatalf("regrade.json = %+v, %v", info2, err)
	}
	if _, err := s.RegradeInfo(ctx, "e_test", "rg_none"); !errors.Is(err, schemas.ErrNotFound) {
		t.Fatalf("missing regrade: %v", err)
	}
	if err := s.WriteRegrade(ctx, "e_test", "rg_gates", r2.RunID, &schemas.Grade{SuiteVersion: "1.0.0"}); err == nil {
		t.Fatal("a grade was written into a regrade that holds gates")
	}
	if err := s.Verify(ctx, "e_test", r1.RunID); err != nil {
		t.Fatalf("regate touched the sealed run: %v", err)
	}
}

func TestWriteAnalysis(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	mustCreateExp(t, s, "e_test")
	a := func(id, exp string) *schemas.Analysis {
		return &schemas.Analysis{AnalysisID: id, ExpID: exp, GradeSource: "original", Mode: "synthetic",
			CreatedAt: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	}
	report := map[string][]byte{"report.md": []byte("# Verdict\n"), "diagnostics.json": []byte("[]")}
	tests := []struct {
		name    string
		a       *schemas.Analysis
		files   map[string][]byte
		wantErr error
		anyErr  bool
	}{
		{name: "ok", a: a("an_1", "e_test"), files: report},
		{name: "refuse overwrite", a: a("an_1", "e_test"), files: report, wantErr: schemas.ErrExists},
		{name: "unknown experiment", a: a("an_2", "e_none"), wantErr: schemas.ErrNotFound},
		{name: "traversal file", a: a("an_3", "e_test"), files: map[string][]byte{"../x.md": nil}, anyErr: true},
		{name: "verdict reserved", a: a("an_4", "e_test"), files: map[string][]byte{"verdict.json": nil}, anyErr: true},
		{name: "bad analysis id", a: a("an/5", "e_test"), anyErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := s.WriteAnalysis(ctx, tc.a, tc.files)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			case tc.anyErr:
				if err == nil {
					t.Fatal("want error")
				}
				return
			case err != nil:
				t.Fatalf("WriteAnalysis: %v", err)
			}
		})
	}
	dir := filepath.Join(s.Root(), "e_test", "analysis", "an_1")
	var got schemas.Analysis
	if err := readJSON(filepath.Join(dir, "verdict.json"), &got); err != nil {
		t.Fatal(err)
	}
	if got.AnalysisID != "an_1" || got.Mode != "synthetic" {
		t.Fatalf("verdict.json = %+v", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil || string(b) != "# Verdict\n" {
		t.Fatalf("report.md = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "e_test", "analysis", "an_3")); err != nil {
		// Validation runs before mkdir, so a rejected analysis leaves no folder.
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	} else {
		t.Fatal("rejected analysis left a folder")
	}
}

// TestPartialRegateIsNotAGradeSource is the regression test for audit
// finding 8-D-03: a regate folder left by an interrupted batch (regrade.json
// with gate versions and one run's result, no coverage record) is neither
// listed nor accepted as a grade source, and a committed regate that lost a
// covered run's result makes that run an evidence problem instead of
// silently keeping its sealed gates.
func TestPartialRegateIsNotAGradeSource(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	r1, _ := sealedRun(t, s, m, "s_a1", "r_1")
	r2, _ := sealedRun(t, s, m, "s_b1", "r_2")
	res := func() *GateRegrade {
		return &GateRegrade{Outcome: schemas.OutcomePass, SealedOutcome: schemas.OutcomeIntegrityViolation,
			Y: schemas.Observed(1, schemas.EvidenceOutcome), Gates: []schemas.GateResult{{Gate: "g1_protected_paths", Version: "1.1.0"}}}
	}
	info := RegradeInfo{SuiteVersion: "1.0.0", GateVersions: map[string]string{"g1_protected_paths": "1.1.0"}}
	if err := s.WriteGateRegrades(ctx, "e_test", "rg_full", info, map[string]*GateRegrade{r1.RunID: res(), r2.RunID: res()}); err != nil {
		t.Fatal(err)
	}
	// What the old per-run writer left after its first run: copy the
	// committed folder, drop the coverage record and the second result.
	full := filepath.Join(s.Root(), "e_test", "regrades", "rg_full")
	partial := filepath.Join(s.Root(), "e_test", "regrades", "rg_partial")
	if err := os.CopyFS(partial, os.DirFS(full)); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := readJSON(filepath.Join(partial, regradeFile), &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "runs")
	b, _ := json.Marshal(raw)
	if err := os.WriteFile(filepath.Join(partial, regradeFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(partial, runsDir, r2.RunID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegradeInfo(ctx, "e_test", "rg_partial"); !errors.Is(err, schemas.ErrNotFound) {
		t.Fatalf("partial regate listed: %v", err)
	}
	if _, err := s.SealedRuns(ctx, "e_test", "rg_partial"); !errors.Is(err, schemas.ErrNotFound) || !strings.Contains(err.Error(), "no coverage record") {
		t.Fatalf("partial regate accepted as grade source: %v", err)
	}
	got, err := s.SealedRuns(ctx, "e_test", "rg_full")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.GradeSource != "rg_full" || len(r.Problems) != 0 {
			t.Fatalf("committed regate run %s: source %s problems %v", r.Run.RunID, r.GradeSource, r.Problems)
		}
	}
	// A covered run whose result disappeared is a problem, not a fallback.
	if err := os.RemoveAll(filepath.Join(full, runsDir, r2.RunID)); err != nil {
		t.Fatal(err)
	}
	got, err = s.SealedRuns(ctx, "e_test", "rg_full")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Run.RunID == r2.RunID && (len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "holds no result")) {
			t.Fatalf("missing covered result: problems %v", r.Problems)
		}
	}
}

// Audit 1-D-07: WriteAnalysis publishes a complete folder or nothing, and
// LatestAnalysis picks by created_at, skips incomplete folders and fails on
// an unreadable verdict.json.
func TestWriteAnalysisStagedAndLatest(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	mustCreateExp(t, s, "e_test")
	if a, err := s.LatestAnalysis(ctx, "e_test"); err != nil || a != nil {
		t.Fatalf("no analyses: %+v, %v", a, err)
	}
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	invalid := &schemas.Analysis{AnalysisID: "an_b", ExpID: "e_test", CreatedAt: at, Verdict: schemas.Verdict{Decision: schemas.DecisionInvalid}}
	older := &schemas.Analysis{AnalysisID: "an_c", ExpID: "e_test", CreatedAt: at.Add(-time.Hour)}
	for _, a := range []*schemas.Analysis{invalid, older} {
		if err := s.WriteAnalysis(ctx, a, nil); err != nil {
			t.Fatal(err)
		}
	}
	// A failing extra file must not leave a published folder.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_ = s.WriteAnalysis(cancelled, &schemas.Analysis{AnalysisID: "an_x", ExpID: "e_test", CreatedAt: at.Add(time.Hour)}, nil)
	err := s.WriteAnalysis(ctx, &schemas.Analysis{AnalysisID: "an_y", ExpID: "e_test", CreatedAt: at.Add(time.Hour)},
		map[string][]byte{"a/b": nil, "a": nil})
	if err == nil {
		t.Fatal("conflicting extra files: want error")
	}
	base := filepath.Join(s.Root(), "e_test", "analysis")
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "an_b,an_c" {
		t.Fatalf("analysis folders %v, want only an_b,an_c", names)
	}
	// A legacy interrupted folder without verdict.json is skipped.
	if err := os.Mkdir(filepath.Join(base, "an_z"), dirPerm); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestAnalysis(ctx, "e_test")
	if err != nil || got == nil || got.AnalysisID != "an_b" || got.Verdict.Decision != schemas.DecisionInvalid {
		t.Fatalf("latest = %+v, %v, want an_b invalid", got, err)
	}
	if err := os.WriteFile(filepath.Join(base, "an_z", "verdict.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestAnalysis(ctx, "e_test"); err == nil {
		t.Fatal("unreadable verdict.json: want error")
	}
}
