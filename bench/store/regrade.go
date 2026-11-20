package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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
	// Runs is a regate's coverage record: the id of every run it recomputed,
	// written in the same atomic publish as the results. A regate without it
	// was not committed and is never a grade source (audit 8-D-03); a
	// counted run outside it was sealed after the regate and keeps its
	// sealed gates.
	Runs      []string  `json:"runs,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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

// WriteGateRegrades writes a whole regate batch as regrade regradeID:
// regrades/<regrade_id>/regrade.json with the coverage record Runs (every
// run id of results, sorted) and runs/<run_id>/regate.json for each run, with
// parent_seal set to the run's seal hash and hash set over the content.
// Everything is staged in a folder beside regrades/ and published with one
// rename, so an interrupted or failed batch never leaves a regrade a reader
// can select (audit 8-D-03); the staged folder is removed on failure. An
// existing regrade id is refused (schemas.ErrExists). results is not
// modified.
func (s *FSStore) WriteGateRegrades(ctx context.Context, expID, regradeID string, info RegradeInfo, results map[string]*GateRegrade) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(info.GateVersions) == 0 {
		return fmt.Errorf("write regate %s: no gate versions", regradeID)
	}
	if err := validateID("regrade_id", regradeID); err != nil {
		return fmt.Errorf("write regate: %w", err)
	}
	if len(results) == 0 {
		return fmt.Errorf("write regate %s: no runs", regradeID)
	}
	parent := filepath.Join(s.expDir(expID), regradesDir)
	final := filepath.Join(parent, regradeID)
	if _, err := os.Lstat(final); err == nil {
		return fmt.Errorf("write regate %s: %w", regradeID, schemas.ErrExists)
	}
	files := map[string][]byte{}
	runIDs := slices.Sorted(maps.Keys(results))
	for _, runID := range runIDs {
		r := results[runID]
		if r == nil {
			return fmt.Errorf("write regate %s for run %s: nil result", regradeID, runID)
		}
		if err := validateID("run_id", runID); err != nil {
			return fmt.Errorf("write regate %s: %w", regradeID, err)
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
		b, err := marshalIndent(&c)
		if err != nil {
			return fmt.Errorf("write regate %s for run %s: %w", regradeID, runID, err)
		}
		files[filepath.Join(runsDir, runID, regateFile)] = b
	}
	info.RegradeID, info.ExpID, info.GraderVersion = regradeID, expID, ""
	info.CreatedAt = s.now().UTC()
	info.Runs = runIDs
	b, err := marshalIndent(&info)
	if err != nil {
		return fmt.Errorf("write regate %s: %w", regradeID, err)
	}
	files[regradeFile] = b
	if err := os.MkdirAll(parent, dirPerm); err != nil {
		return fmt.Errorf("write regate %s: %w", regradeID, err)
	}
	// The staging folder sits in the experiment folder, not in regrades/, so
	// no regrade listing ever sees it, and on the same file system, so the
	// publishing rename is atomic.
	stage, err := os.MkdirTemp(s.expDir(expID), ".regate-"+regradeID+"-")
	if err != nil {
		return fmt.Errorf("write regate %s: %w", regradeID, err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := os.Chmod(stage, dirPerm); err != nil {
		return fmt.Errorf("write regate %s: %w", regradeID, err)
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		target := filepath.Join(stage, name)
		if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
			return fmt.Errorf("write regate %s: %w", regradeID, err)
		}
		if err := writeFileExclusive(target, files[name]); err != nil {
			return fmt.Errorf("write regate %s file %s: %w", regradeID, name, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// os.Rename replaces an empty folder, so check again right before it; a
	// non-empty one makes the rename fail.
	if _, err := os.Lstat(final); err == nil {
		return fmt.Errorf("write regate %s: %w", regradeID, schemas.ErrExists)
	}
	if err := os.Rename(stage, final); err != nil {
		if errors.Is(err, fs.ErrExist) || errors.Is(err, syscall.ENOTEMPTY) {
			return fmt.Errorf("write regate %s: %w", regradeID, schemas.ErrExists)
		}
		return fmt.Errorf("write regate %s: publish: %w", regradeID, err)
	}
	published = true
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
	if len(info.GateVersions) > 0 && len(info.Runs) == 0 {
		// A regate without its coverage record was never committed: listings
		// skip it like a regrade without regrade.json (audit 8-D-03).
		return nil, fmt.Errorf("regrade %s is not committed: %w", regradeID, schemas.ErrNotFound)
	}
	return &info, nil
}

// WriteAnalysis writes analysis/<analysis_id>/verdict.json (the Analysis) and
// the extra files, e.g. report.md. The whole analysis is staged in a hidden
// folder and published with one rename, so an interrupted write never leaves
// a final folder without a complete verdict.json (audit 1-D-07). An existing
// analysis is never overwritten (schemas.ErrExists).
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
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("write analysis %s: %w", a.AnalysisID, schemas.ErrExists)
	}
	stage, err := os.MkdirTemp(parent, ".stage-"+a.AnalysisID+"-")
	if err != nil {
		return fmt.Errorf("write analysis %s: %w", a.AnalysisID, err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := os.Chmod(stage, dirPerm); err != nil {
		return fmt.Errorf("write analysis %s: %w", a.AnalysisID, err)
	}
	if err := writeJSONAtomic(filepath.Join(stage, verdictFile), a); err != nil {
		return fmt.Errorf("write analysis %s: %w", a.AnalysisID, err)
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		target := filepath.Join(stage, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
			return fmt.Errorf("write analysis %s: %w", a.AnalysisID, err)
		}
		if err := writeFileExclusive(target, files[name]); err != nil {
			return fmt.Errorf("write analysis %s file %s: %w", a.AnalysisID, name, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// os.Rename replaces an empty folder, so check again right before it; a
	// non-empty one makes the rename fail.
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("write analysis %s: %w", a.AnalysisID, schemas.ErrExists)
	}
	if err := os.Rename(stage, dir); err != nil {
		if errors.Is(err, fs.ErrExist) || errors.Is(err, syscall.ENOTEMPTY) {
			return fmt.Errorf("write analysis %s: %w", a.AnalysisID, schemas.ErrExists)
		}
		return fmt.Errorf("write analysis %s: publish: %w", a.AnalysisID, err)
	}
	published = true
	return nil
}

// LatestAnalysis returns the newest complete analysis of an experiment,
// ordered by its own created_at and then analysis id, never by folder
// modification time, which copying an archive changes. A folder without
// verdict.json is an incomplete write from before analyses were staged and is
// skipped, as is a hidden staging folder. A verdict.json that exists but
// cannot be read or decoded is an error: the caller cannot tell whether that
// newest record said INVALID, so it must fail closed. No analysis returns
// (nil, nil).
func (s *FSStore) LatestAnalysis(ctx context.Context, expID string) (*schemas.Analysis, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateID("exp_id", expID); err != nil {
		return nil, fmt.Errorf("latest analysis: %w", err)
	}
	base := filepath.Join(s.expDir(expID), analysisDir)
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest analysis %s: %w", expID, err)
	}
	var best *schemas.Analysis
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		var a schemas.Analysis
		found, err := readOptionalJSON(filepath.Join(base, e.Name(), verdictFile), &a)
		if err != nil {
			return nil, fmt.Errorf("latest analysis %s: analysis %s: %w", expID, e.Name(), err)
		}
		if !found {
			continue
		}
		if a.AnalysisID == "" {
			a.AnalysisID = e.Name()
		}
		if best == nil || a.CreatedAt.After(best.CreatedAt) || (a.CreatedAt.Equal(best.CreatedAt) && a.AnalysisID > best.AnalysisID) {
			best = &a
		}
	}
	return best, nil
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
