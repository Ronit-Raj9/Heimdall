package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestWriteGateRegrade(t *testing.T) {
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
	tests := []struct {
		name    string
		regrade string
		runID   string
		info    RegradeInfo
		r       *GateRegrade
		wantErr error
		anyErr  bool
	}{
		{name: "first regate", regrade: "rg_gates", runID: r1.RunID, info: info(v11), r: res()},
		{name: "same run twice", regrade: "rg_gates", runID: r1.RunID, info: info(v11), r: res(), wantErr: schemas.ErrExists},
		{name: "other gate versions", regrade: "rg_gates", runID: r2.RunID, info: info(map[string]string{"g1_protected_paths": "1.2.0"}), r: res(), anyErr: true},
		{name: "regrade id that holds grades", regrade: "rg_grades", runID: r2.RunID, info: info(v11), r: res(), anyErr: true},
		{name: "no gate versions", regrade: "rg_other", runID: r2.RunID, info: info(nil), r: res(), anyErr: true},
		{name: "unsealed run", regrade: "rg_gates", runID: open.RunID, info: info(v11), r: res(), wantErr: schemas.ErrNotFound},
		{name: "wrong parent seal", regrade: "rg_gates", runID: r2.RunID, info: info(v11), r: &GateRegrade{ParentSeal: seal1.Hash}, wantErr: schemas.ErrSealMismatch},
		{name: "nil result", regrade: "rg_gates", runID: r2.RunID, info: info(v11), anyErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := s.WriteGateRegrade(ctx, "e_test", tc.regrade, tc.runID, tc.info, tc.r)
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
				t.Fatalf("WriteGateRegrade: %v", err)
			case tc.r.ParentSeal != "" || tc.r.Hash != "":
				t.Fatal("WriteGateRegrade modified the caller's result")
			}
		})
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
	if err != nil || info2.GateVersions["g1_protected_paths"] != "1.1.0" || info2.GraderVersion != "" || info2.Note != "rule 1.1.0" || info2.CreatedAt.IsZero() {
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
