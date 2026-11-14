package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// RegradeInfo is the content of regrades/<regrade_id>/regrade.json. Every
// grade of one regrade carries the same suite and grader version. A regrade
// that recomputed gates instead of grades (a regate) has GateVersions set and
// no grader version; one regrade id never holds both kinds.
type RegradeInfo struct {
	RegradeID     string            `json:"regrade_id"`
	ExpID         string            `json:"exp_id"`
	SuiteVersion  string            `json:"suite_version"`
	GraderVersion string            `json:"grader_version"`
	GateVersions  map[string]string `json:"gate_versions,omitempty"`   // gate name -> version the gates were recomputed at
	SuiteLockHash string            `json:"suite_lock_hash,omitempty"` // the suite lock a regate read, when it differs from the manifest's
	Note          string            `json:"note,omitempty"`            // why the gates were recomputed, shown in the report
	CreatedAt     time.Time         `json:"created_at"`
}

// GateRegrade is regrades/<regrade_id>/runs/<run_id>/regate.json: one sealed
// run's gates recomputed after the fact from its sealed evidence, with the
// outcome and the metrics that follow from them. The sealed run itself is
// never touched; analysis reads this file in place of the run's gates.json
// when the regrade is the grade source. Hash is the sha256 of the canonical
// JSON with Hash empty, checked on every read.
type GateRegrade struct {
	ParentSeal    string                   `json:"parent_seal"`
	Hash          string                   `json:"hash"`
	Gates         []schemas.GateResult     `json:"gates"`
	Recomputed    []string                 `json:"recomputed"` // gates checked again; the others were carried from the seal at the same version
	SealedOutcome schemas.Outcome          `json:"sealed_outcome"`
	Outcome       schemas.Outcome          `json:"outcome"`
	Y             schemas.Value            `json:"y"`
	Metrics       map[string]schemas.Value `json:"metrics,omitempty"` // only metrics whose value the recomputed gates changed
	Notes         []string                 `json:"notes,omitempty"`
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

// WriteGateRegrade writes regrades/<regrade_id>/runs/<run_id>/regate.json
// for a sealed run, with parent_seal set to the run's seal hash and hash set
// over the content. info gives the regrade's suite version, gate versions and
// note; regrade.json is written by the first call and later calls must carry
// the same gate versions. A regrade id created by WriteRegrade (grades only)
// is refused, and a run's regate is written once (schemas.ErrExists after).
// r itself is not modified.
func (s *FSStore) WriteGateRegrade(ctx context.Context, expID, regradeID, runID string, info RegradeInfo, r *GateRegrade) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("write regate: nil result")
	}
	if len(info.GateVersions) == 0 {
		return fmt.Errorf("write regate %s: no gate versions", regradeID)
	}
	if err := validateID("regrade_id", regradeID); err != nil {
		return fmt.Errorf("write regate: %w", err)
	}
	runDir, err := s.findRunDir(expID, runID)
	if err != nil {
		return fmt.Errorf("write regate %s: %w", regradeID, err)
	}
	seal, err := readSeal(runDir)
	if err != nil {
		return fmt.Errorf("write regate %s for run %s: %w", regradeID, runID, err)
	}
	if r.ParentSeal != "" && r.ParentSeal != seal.Hash {
		return fmt.Errorf("write regate %s for run %s: parent_seal %s is not the run seal %s: %w",
			regradeID, runID, r.ParentSeal, seal.Hash, schemas.ErrSealMismatch)
	}
	c := *r
	c.ParentSeal, c.Hash = seal.Hash, ""
	if c.Hash, err = utils.HashJSON(&c); err != nil {
		return fmt.Errorf("write regate %s for run %s: %w", regradeID, runID, err)
	}
	base := filepath.Join(s.expDir(expID), regradesDir, regradeID)
	if err := s.ensureGateRegradeInfo(base, expID, regradeID, info); err != nil {
		return fmt.Errorf("write regate %s: %w", regradeID, err)
	}
	dir := filepath.Join(base, runsDir, runID)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("write regate %s for run %s: %w", regradeID, runID, err)
	}
	b, err := marshalIndent(&c)
	if err != nil {
		return fmt.Errorf("write regate %s for run %s: %w", regradeID, runID, err)
	}
	if err := writeFileExclusive(filepath.Join(dir, regateFile), b); err != nil {
		return fmt.Errorf("write regate %s for run %s: %w", regradeID, runID, err)
	}
	return nil
}

// RegradeInfo reads regrades/<regrade_id>/regrade.json.
func (s *FSStore) RegradeInfo(ctx context.Context, expID, regradeID string) (*RegradeInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateID("exp_id", expID); err != nil {
		return nil, fmt.Errorf("regrade info: %w", err)
	}
	if err := validateID("regrade_id", regradeID); err != nil {
		return nil, fmt.Errorf("regrade info: %w", err)
	}
	var info RegradeInfo
	found, err := readOptionalJSON(filepath.Join(s.expDir(expID), regradesDir, regradeID, regradeFile), &info)
	if err != nil {
		return nil, fmt.Errorf("regrade info %s: %w", regradeID, err)
	}
	if !found {
		return nil, fmt.Errorf("regrade %s: %w", regradeID, schemas.ErrNotFound)
	}
	return &info, nil
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
	if len(have.GateVersions) > 0 {
		return fmt.Errorf("regrade %s holds recomputed gates, not grades; use a new regrade id", regradeID)
	}
	if have.SuiteVersion != g.SuiteVersion || have.GraderVersion != g.GraderVersion {
		return fmt.Errorf("grade is suite %s grader %s, regrade is suite %s grader %s",
			g.SuiteVersion, g.GraderVersion, have.SuiteVersion, have.GraderVersion)
	}
	return nil
}

// ensureGateRegradeInfo writes regrade.json on the first regate of a regrade
// and checks later ones against it.
func (s *FSStore) ensureGateRegradeInfo(base, expID, regradeID string, info RegradeInfo) error {
	if err := os.MkdirAll(base, dirPerm); err != nil {
		return err
	}
	info.RegradeID, info.ExpID, info.GraderVersion = regradeID, expID, ""
	info.CreatedAt = s.now().UTC()
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
	if len(have.GateVersions) == 0 {
		return fmt.Errorf("regrade %s holds regraded grades, not gates; use a new regrade id", regradeID)
	}
	if !maps.Equal(have.GateVersions, info.GateVersions) {
		return fmt.Errorf("regate gate versions %v differ from the regrade's %v", info.GateVersions, have.GateVersions)
	}
	return nil
}

// verifyGateRegrade checks a regate read from disk against its run's seal and
// its own hash.
func verifyGateRegrade(r *GateRegrade, sealHash string) error {
	if r.ParentSeal != sealHash {
		return fmt.Errorf("parent_seal %s, run seal %s: %w", r.ParentSeal, sealHash, schemas.ErrSealMismatch)
	}
	c := *r
	c.Hash = ""
	h, err := utils.HashJSON(&c)
	if err != nil {
		return err
	}
	if h != r.Hash {
		return fmt.Errorf("content hash %s, recorded %s: %w", h, r.Hash, schemas.ErrSealMismatch)
	}
	return nil
}
