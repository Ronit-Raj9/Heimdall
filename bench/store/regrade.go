package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// RegradeInfo is the content of regrades/<regrade_id>/regrade.json. Every
// grade of one regrade carries the same suite and grader version.
type RegradeInfo struct {
	RegradeID     string    `json:"regrade_id"`
	ExpID         string    `json:"exp_id"`
	SuiteVersion  string    `json:"suite_version"`
	GraderVersion string    `json:"grader_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// WriteRegrade writes regrades/<regrade_id>/runs/<run_id>/grade.json with
// parent_seal set to the run's seal hash, so the regrade references the
// sealed run without writing into it. The run must be sealed. regrade.json is
// written by the first grade of a regrade; later grades must carry the same
// suite and grader version. A run's regrade grade is written once
// (schemas.ErrExists after). g itself is not modified; a ParentSeal already
// set on it must equal the run's seal hash.
func (s *FSStore) WriteRegrade(ctx context.Context, expID, regradeID, runID string, g *schemas.Grade) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("write regrade: nil grade")
	}
	if err := validateID("regrade_id", regradeID); err != nil {
		return fmt.Errorf("write regrade: %w", err)
	}
	runDir, err := s.findRunDir(expID, runID)
	if err != nil {
		return fmt.Errorf("write regrade %s: %w", regradeID, err)
	}
	seal, err := readSeal(runDir)
	if err != nil {
		return fmt.Errorf("write regrade %s for run %s: %w", regradeID, runID, err)
	}
	if g.ParentSeal != "" && g.ParentSeal != seal.Hash {
		return fmt.Errorf("write regrade %s for run %s: parent_seal %s is not the run seal %s: %w",
			regradeID, runID, g.ParentSeal, seal.Hash, schemas.ErrSealMismatch)
	}
	c := *g
	c.ParentSeal = seal.Hash
	base := filepath.Join(s.expDir(expID), regradesDir, regradeID)
	if err := s.ensureRegradeInfo(base, expID, regradeID, &c); err != nil {
		return fmt.Errorf("write regrade %s: %w", regradeID, err)
	}
	dir := filepath.Join(base, runsDir, runID)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("write regrade %s for run %s: %w", regradeID, runID, err)
	}
	b, err := marshalIndent(&c)
	if err != nil {
		return fmt.Errorf("write regrade %s for run %s: %w", regradeID, runID, err)
	}
	if err := writeFileExclusive(filepath.Join(dir, gradeFile), b); err != nil {
		return fmt.Errorf("write regrade %s for run %s: %w", regradeID, runID, err)
	}
	return nil
}

// WriteAnalysis writes analysis/<analysis_id>/verdict.json (the Analysis) and
// the extra files, e.g. report.md. The analysis folder is created with mkdir,
// so an existing analysis is never overwritten (schemas.ErrExists).
func (s *FSStore) WriteAnalysis(ctx context.Context, a *schemas.Analysis, files map[string][]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil {
		return fmt.Errorf("write analysis: nil analysis")
	}
	if err := validateID("exp_id", a.ExpID); err != nil {
		return fmt.Errorf("write analysis: %w", err)
	}
	if err := validateID("analysis_id", a.AnalysisID); err != nil {
		return fmt.Errorf("write analysis: %w", err)
	}
	for name := range files {
		if err := validateName(name); err != nil {
			return fmt.Errorf("write analysis %s: %w", a.AnalysisID, err)
		}
		if name == verdictFile {
			return fmt.Errorf("write analysis %s: %s is reserved", a.AnalysisID, verdictFile)
		}
	}
	if _, err := os.Stat(filepath.Join(s.expDir(a.ExpID), manifestFile)); err != nil {
		return fmt.Errorf("write analysis %s: experiment %s: %w", a.AnalysisID, a.ExpID, schemas.ErrNotFound)
	}
	parent := filepath.Join(s.expDir(a.ExpID), analysisDir)
	if err := os.MkdirAll(parent, dirPerm); err != nil {
		return fmt.Errorf("write analysis %s: %w", a.AnalysisID, err)
	}
	dir := filepath.Join(parent, a.AnalysisID)
	if err := os.Mkdir(dir, dirPerm); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("write analysis %s: %w", a.AnalysisID, schemas.ErrExists)
		}
		return fmt.Errorf("write analysis %s: %w", a.AnalysisID, err)
	}
	if err := writeJSONAtomic(filepath.Join(dir, verdictFile), a); err != nil {
		return fmt.Errorf("write analysis %s: %w", a.AnalysisID, err)
	}
	for name, data := range files {
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
			return fmt.Errorf("write analysis %s: %w", a.AnalysisID, err)
		}
		if err := writeFileExclusive(target, data); err != nil {
			return fmt.Errorf("write analysis %s file %s: %w", a.AnalysisID, name, err)
		}
	}
	return nil
}

// ensureRegradeInfo writes regrade.json on the first grade of a regrade and
// checks later grades against it.
func (s *FSStore) ensureRegradeInfo(base, expID, regradeID string, g *schemas.Grade) error {
	if err := os.MkdirAll(base, dirPerm); err != nil {
		return err
	}
	info := RegradeInfo{
		RegradeID:     regradeID,
		ExpID:         expID,
		SuiteVersion:  g.SuiteVersion,
		GraderVersion: g.GraderVersion,
		CreatedAt:     s.now().UTC(),
	}
	b, err := marshalIndent(&info)
	if err != nil {
		return err
	}
	p := filepath.Join(base, regradeFile)
	err = writeFileExclusive(p, b)
	if err == nil || !errors.Is(err, schemas.ErrExists) {
		return err
	}
	var have RegradeInfo
	if err := readJSON(p, &have); err != nil {
		return err
	}
	if have.SuiteVersion != g.SuiteVersion || have.GraderVersion != g.GraderVersion {
		return fmt.Errorf("grade is suite %s grader %s, regrade is suite %s grader %s",
			g.SuiteVersion, g.GraderVersion, have.SuiteVersion, have.GraderVersion)
	}
	return nil
}
