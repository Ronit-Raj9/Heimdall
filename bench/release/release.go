// Package release keeps the release log: an append-only, hash-chained record
// of which agent config was released, by whom, on which analysis and which
// sealed runs, with an evidence pack per release.
//
// The chain makes an edit to a past record detectable: each record carries the
// hash of the one before it and a hash of itself. It is not a cryptographic
// signature and not a compliance certification. It shows the log has not
// changed since it was written, not who wrote it.
package release

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// File names under the releases root.
const (
	LogFile      = "releases.jsonl"
	EvidenceFile = "evidence.zip"
	// RootEnv overrides the releases root, which defaults to <bench root>/releases.
	RootEnv = "BENCH_RELEASES_ROOT"
)

// Input limits on a request. They keep a log line readable, not secret.
const (
	maxApprover = 200
	maxNote     = 2000
	maxOverride = 2000
)

// Run store layout the release reads (DESIGN.md 16.1).
const (
	manifestFile = "manifest.json"
	sealsLogFile = "seals.log"
	analysisDir  = "analysis"
	verdictFile  = "verdict.json"
	reportFile   = "report.md"
	policyFile   = "policy.json"
)

var idPattern = regexp.MustCompile(`^[a-z0-9_]{1,128}$`)

// Store is the part of the run store a release reads. schemas.Store
// satisfies it.
type Store interface {
	Root() string
	LoadManifest(ctx context.Context, expID string) (*schemas.Manifest, error)
	SealedRuns(ctx context.Context, expID, gradeSource string) ([]schemas.SealedRun, error)
	Verify(ctx context.Context, expID, runID string) error
}

// ChainVerifier is optionally implemented by a Store: VerifyChain checks an
// experiment's seals.log as one linear chain over its sealed runs.
// store.FSStore implements it.
type ChainVerifier interface {
	VerifyChain(expID string) error
}

// Deps is what a release log is built from.
type Deps struct {
	Store  Store  // the run store; nil allows reads only
	Root   string // releases root, never inside the runs root
	Logger schemas.Logger
	Now    func() time.Time
}

// ErrChainBroken wraps every chain verification failure.
var ErrChainBroken = errors.New("release chain broken")

// ErrEvidenceMismatch means an evidence pack no longer hashes to the value its
// record carries.
var ErrEvidenceMismatch = errors.New("evidence pack does not match its record")

// RefusedError is a release refused on its evidence: the verdict, the policy,
// a seal or a missing field. It is the caller's to fix, not a failure.
type RefusedError struct {
	Reason string
	Err    error // an underlying sentinel, e.g. schemas.ErrSealMismatch
}

// Error implements error.
func (e *RefusedError) Error() string { return "release refused: " + e.Reason }

// Unwrap returns the underlying sentinel, if any.
func (e *RefusedError) Unwrap() error { return e.Err }

// ReleaseRefused lets a consumer that cannot import this package recognize a
// refusal with errors.As on an interface.
func (e *RefusedError) ReleaseRefused() bool { return true }

// Log is the release log under one releases root.
type Log struct {
	d  Deps
	mu sync.Mutex // one append at a time in this process; the flock covers other processes
}

// DefaultRoot returns the releases root: BENCH_RELEASES_ROOT, else
// <benchRoot>/releases. It is never the runs root, where every folder is read
// as an experiment.
func DefaultRoot(benchRoot string) string {
	if v := strings.TrimSpace(os.Getenv(RootEnv)); v != "" {
		return v
	}
	return filepath.Join(benchRoot, "releases")
}

// New returns the release log at d.Root. It refuses a root inside the runs
// root.
func New(d Deps) (*Log, error) {
	if strings.TrimSpace(d.Root) == "" {
		return nil, errors.New("new release log: empty releases root")
	}
	abs, err := filepath.Abs(d.Root)
	if err != nil {
		return nil, fmt.Errorf("new release log: resolve %s: %w", d.Root, err)
	}
	d.Root = abs
	if d.Store != nil {
		runs, err := filepath.Abs(d.Store.Root())
		if err != nil {
			return nil, fmt.Errorf("new release log: resolve runs root: %w", err)
		}
		if within(runs, abs) {
			return nil, fmt.Errorf("new release log: releases root %s is inside the runs root %s", abs, runs)
		}
	}
	if d.Logger == nil {
		d.Logger = schemas.NoopLogger{}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Log{d: d}, nil
}

// Root returns the absolute releases root.
func (l *Log) Root() string { return l.d.Root }

// Create records a release of an experiment's analysis and writes its
// evidence pack. It refuses unless the analysis action is ship and the policy
// decision, when a policy was evaluated, is not block; req.Override, a reason,
// releases against either and is recorded. Every counted run's seal is
// verified first, and so is the experiment's seals.log chain when the store
// can check it (ChainVerifier); a mismatch, or a run the store loaded with an
// evidence problem (a slot with more than one counted run), refuses whatever
// the override says.
func (l *Log) Create(ctx context.Context, req *schemas.ReleaseRequest) (*schemas.ReleaseRecord, error) {
	if l.d.Store == nil {
		return nil, errors.New("create release: no run store")
	}
	if req == nil {
		return nil, errors.New("create release: nil request")
	}
	r, err := normalize(req)
	if err != nil {
		return nil, err
	}
	m, err := l.d.Store.LoadManifest(ctx, r.ExpID)
	if err != nil {
		return nil, fmt.Errorf("create release: %w", err)
	}
	ev, err := l.loadEvidence(ctx, m, r.AnalysisID)
	if err != nil {
		return nil, err
	}
	if cv, ok := l.d.Store.(ChainVerifier); ok {
		if err := cv.VerifyChain(m.ExpID); err != nil {
			if errors.Is(err, schemas.ErrSealMismatch) {
				return nil, &RefusedError{Reason: fmt.Sprintf("seals.log chain of %s does not verify: %v", m.ExpID, err), Err: schemas.ErrSealMismatch}
			}
			return nil, fmt.Errorf("create release: verify seal chain of %s: %w", m.ExpID, err)
		}
	}
	for _, run := range ev.runs {
		if len(run.Problems) > 0 {
			return nil, &RefusedError{Reason: fmt.Sprintf("run %s: %s", run.Run.RunID, strings.Join(run.Problems, "; ")), Err: schemas.ErrSealMismatch}
		}
		if err := l.d.Store.Verify(ctx, m.ExpID, run.Run.RunID); err != nil {
			if errors.Is(err, schemas.ErrSealMismatch) {
				return nil, &RefusedError{Reason: fmt.Sprintf("seal of run %s does not verify: %v", run.Run.RunID, err), Err: schemas.ErrSealMismatch}
			}
			return nil, fmt.Errorf("create release: verify run %s: %w", run.Run.RunID, err)
		}
	}
	// The release rule runs after the integrity checks, so a damaged run
	// is refused as such and never reported as a changed snapshot.
	if err := checkRelease(m, ev, r.Override); err != nil {
		return nil, err
	}
	rec, err := newRecord(m, ev, r, l.d.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := l.appendRelease(rec, ev); err != nil {
		return nil, err
	}
	l.d.Logger.Info("release recorded", "release_id", rec.ReleaseID, "exp_id", rec.ExpID, "approver", rec.Approver, "override", rec.Override != "")
	return rec, nil
}

// List returns every record in log order, oldest first, and whether the
// chain verifies. A line that cannot be read is left out and breaks the chain.
func (l *Log) List(ctx context.Context) (*schemas.ReleaseList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, err := l.readLog()
	if err != nil {
		return nil, err
	}
	recs, chainErr := parseChain(b)
	if chainErr != nil {
		l.d.Logger.Warn("release chain does not verify", "error", chainErr.Error())
	}
	if recs == nil {
		recs = []schemas.ReleaseRecord{}
	}
	return &schemas.ReleaseList{Releases: recs, ChainOK: chainErr == nil}, nil
}

// Get returns one record.
func (l *Log) Get(ctx context.Context, releaseID string) (*schemas.ReleaseRecord, error) {
	list, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list.Releases {
		if list.Releases[i].ReleaseID == releaseID {
			rec := list.Releases[i]
			return &rec, nil
		}
	}
	return nil, fmt.Errorf("release %s: %w", releaseID, schemas.ErrNotFound)
}

// VerifyChain checks the log: every line is a record in canonical form, its
// prev_hash is the hash of the line before and its hash recomputes. A failure
// wraps ErrChainBroken and names the first bad line.
func (l *Log) VerifyChain() error {
	b, err := l.readLog()
	if err != nil {
		return err
	}
	_, chainErr := parseChain(b)
	return chainErr
}

// Verify is VerifyChain plus every record's evidence pack: present and
// hashing to the record's evidence_sha256. It returns the number of records.
func (l *Log) Verify(ctx context.Context) (int, error) {
	b, err := l.readLog()
	if err != nil {
		return 0, err
	}
	recs, err := parseChain(b)
	if err != nil {
		return len(recs), err
	}
	for i := range recs {
		if err := ctx.Err(); err != nil {
			return i, err
		}
		if _, err := l.readEvidence(&recs[i]); err != nil {
			return i, err
		}
	}
	return len(recs), nil
}

// Export returns the path of a release's evidence pack after checking it
// still hashes to the record's evidence_sha256.
func (l *Log) Export(ctx context.Context, releaseID string) (string, error) {
	rec, err := l.Get(ctx, releaseID)
	if err != nil {
		return "", err
	}
	if _, err := l.readEvidence(rec); err != nil {
		return "", err
	}
	return l.evidencePath(rec.ReleaseID), nil
}

// Evidence returns a release's evidence pack bytes, checked against the
// record's evidence_sha256.
func (l *Log) Evidence(ctx context.Context, releaseID string) ([]byte, error) {
	rec, err := l.Get(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	return l.readEvidence(rec)
}

// SealsDigest is the sha256 over seal hashes: sorted, each followed by a
// newline. It binds a release to exactly the sealed runs it counted.
func SealsDigest(sealHashes []string) string {
	hs := append([]string(nil), sealHashes...)
	sort.Strings(hs)
	var buf bytes.Buffer
	for _, h := range hs {
		buf.WriteString(h)
		buf.WriteByte('\n')
	}
	return utils.SHA256Bytes(buf.Bytes())
}

// evidence is everything a release reads from the run store.
type evidence struct {
	analysis    *schemas.Analysis
	analysisDir string
	policy      *schemas.PolicyResult
	policyJSON  []byte // the policy result as packed; nil when none was evaluated
	runs        []schemas.SealedRun
}

// normalize trims a request and checks its fields.
func normalize(req *schemas.ReleaseRequest) (schemas.ReleaseRequest, error) {
	r := schemas.ReleaseRequest{
		ExpID:      strings.TrimSpace(req.ExpID),
		AnalysisID: strings.TrimSpace(req.AnalysisID),
		Approver:   strings.TrimSpace(req.Approver),
		Note:       strings.TrimSpace(req.Note),
		Override:   strings.TrimSpace(req.Override),
	}
	if !idPattern.MatchString(r.ExpID) {
		return r, fmt.Errorf("create release: invalid exp_id %q", r.ExpID)
	}
	if r.AnalysisID != "" && !idPattern.MatchString(r.AnalysisID) {
		return r, fmt.Errorf("create release: invalid analysis_id %q", r.AnalysisID)
	}
	switch {
	case r.Approver == "":
		return r, &RefusedError{Reason: "an approver is required"}
	case len(r.Approver) > maxApprover:
		return r, &RefusedError{Reason: fmt.Sprintf("approver is longer than %d bytes", maxApprover)}
	case len(r.Note) > maxNote:
		return r, &RefusedError{Reason: fmt.Sprintf("note is longer than %d bytes", maxNote)}
	case len(r.Override) > maxOverride:
		return r, &RefusedError{Reason: fmt.Sprintf("override reason is longer than %d bytes", maxOverride)}
	}
	return r, nil
}

// loadEvidence reads the analysis (the named one or the newest), its policy
// result and the counted sealed runs with the analysis's grade source.
func (l *Log) loadEvidence(ctx context.Context, m *schemas.Manifest, analysisID string) (*evidence, error) {
	base := filepath.Join(l.d.Store.Root(), m.ExpID, analysisDir)
	if analysisID == "" {
		id, err := l.newestAnalysis(base)
		if err != nil {
			return nil, fmt.Errorf("create release for %s: %w", m.ExpID, err)
		}
		analysisID = id
	}
	ev := &evidence{analysisDir: filepath.Join(base, analysisID)}
	var a schemas.Analysis
	if err := readJSON(filepath.Join(ev.analysisDir, verdictFile), &a); err != nil {
		return nil, fmt.Errorf("create release: analysis %s of %s: %w", analysisID, m.ExpID, err)
	}
	if a.AnalysisID == "" {
		a.AnalysisID = analysisID
	}
	if a.AnalysisID != analysisID || (a.ExpID != "" && a.ExpID != m.ExpID) {
		return nil, &RefusedError{Reason: fmt.Sprintf("analysis folder %s holds analysis %s of %s", analysisID, a.AnalysisID, a.ExpID)}
	}
	ev.analysis = &a
	if err := ev.loadPolicy(); err != nil {
		return nil, err
	}
	runs, err := l.d.Store.SealedRuns(ctx, m.ExpID, a.GradeSource)
	if err != nil {
		if errors.Is(err, schemas.ErrSealMismatch) {
			return nil, &RefusedError{Reason: "sealed runs do not verify: " + err.Error(), Err: schemas.ErrSealMismatch}
		}
		return nil, fmt.Errorf("create release: sealed runs of %s: %w", m.ExpID, err)
	}
	ev.runs = runs
	return ev, nil
}

// loadPolicy takes the policy result from the analysis, else from a
// policy.json beside it. Neither means no policy was evaluated.
func (ev *evidence) loadPolicy() error {
	if ev.analysis.Policy != nil {
		b, err := json.MarshalIndent(ev.analysis.Policy, "", "  ")
		if err != nil {
			return fmt.Errorf("create release: encode policy result: %w", err)
		}
		ev.policy, ev.policyJSON = ev.analysis.Policy, append(b, '\n')
		return nil
	}
	b, err := os.ReadFile(filepath.Join(ev.analysisDir, policyFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create release: read policy result: %w", err)
	}
	var p schemas.PolicyResult
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("create release: decode policy result: %w", err)
	}
	ev.policy, ev.policyJSON = &p, b
	return nil
}

// newestAnalysis returns the analysis id with the latest created_at, ties to
// the larger id, among the analyses of the original grades. Folders without
// verdict.json (an incomplete write) are skipped; a verdict.json that exists
// but does not decode refuses, since the newest analysis might be the one
// that said INVALID (audit 1-D-07). An analysis of another grade source (a
// regrade or judge) is used only when the request names it: reading such a
// verdict writes an analysis, and that must not change which evidence a
// later release takes by default (audit 4-B-07).
func (l *Log) newestAnalysis(base string) (string, error) {
	entries, err := os.ReadDir(base)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("list analyses: %w", err)
	}
	var best string
	var bestAt time.Time
	for _, e := range entries {
		if !e.IsDir() || !idPattern.MatchString(e.Name()) {
			continue
		}
		var a schemas.Analysis
		if err := readJSON(filepath.Join(base, e.Name(), verdictFile), &a); err != nil {
			if errors.Is(err, schemas.ErrNotFound) {
				l.d.Logger.Warn("analysis skipped", "analysis_id", e.Name(), "error", err.Error())
				continue
			}
			return "", fmt.Errorf("analysis %s: %w", e.Name(), err)
		}
		if a.GradeSource != "" && a.GradeSource != schemas.GradeSourceOriginal {
			continue
		}
		if best == "" || a.CreatedAt.After(bestAt) || (a.CreatedAt.Equal(bestAt) && e.Name() > best) {
			best, bestAt = e.Name(), a.CreatedAt
		}
	}
	if best == "" {
		return "", fmt.Errorf("no analysis written yet: %w", schemas.ErrNotFound)
	}
	return best, nil
}

// frozenPolicySkipped reports why the analysis's policy result is not an
// evaluation of the policy the experiment froze, or "". `check -policy none`
// and `check -policy <other file>` write ordinary analyses; releasing one of
// them without an override reason would let a policy the experiment never
// agreed to (or no policy) stand in for the frozen one (QC2-22-01).
func frozenPolicySkipped(m *schemas.Manifest, p *schemas.PolicyResult) string {
	if m.Experiment.Policy == "" {
		return ""
	}
	if p == nil {
		return fmt.Sprintf("the analysis did not evaluate the frozen policy %s", m.Experiment.Policy)
	}
	if m.PolicyHash != "" && p.PolicyHash != m.PolicyHash {
		return fmt.Sprintf("the analysis evaluated policy %s (%s), not the frozen policy %s (%s)", p.PolicyName, p.PolicyHash, m.Experiment.Policy, m.PolicyHash)
	}
	return ""
}

// checkRelease applies the release rule: ship and not blocked, or an
// override reason. An override with nothing to override is refused so a red
// override marker always means something. Integrity problems are never
// overridable.
func checkRelease(m *schemas.Manifest, ev *evidence, override string) error {
	a := ev.analysis
	if err := utils.VerifyManifestHash(m); err != nil {
		return &RefusedError{Reason: err.Error(), Err: schemas.ErrSealMismatch}
	}
	if a.Verdict.ManifestHash != "" && a.Verdict.ManifestHash != m.Hash {
		return &RefusedError{Reason: fmt.Sprintf("analysis %s was computed on manifest %s, the experiment's is %s", a.AnalysisID, a.Verdict.ManifestHash, m.Hash)}
	}
	if len(ev.runs) == 0 {
		return &RefusedError{Reason: "the experiment has no counted sealed runs"}
	}
	// The saved verdict is only released with the evidence it was computed
	// from: a review, regrade or run added since makes the inputs differ, and
	// the release needs a fresh analysis (audit 1-A-03). An analysis written
	// before inputs_digest existed has no snapshot to compare, so its saved
	// action cannot be tied to today's reviews and runs: it stays readable
	// but is never released (audit qc2-01-03).
	if a.InputsDigest == "" {
		return &RefusedError{Reason: fmt.Sprintf("analysis %s predates evidence snapshots (no inputs_digest), so its decision cannot be matched to the current runs, grades and reviews; run a fresh analysis and release that", a.AnalysisID)}
	}
	d, err := utils.AnalysisInputsDigest(ev.runs)
	if err != nil {
		return fmt.Errorf("create release: digest inputs: %w", err)
	}
	if d != a.InputsDigest {
		return &RefusedError{Reason: fmt.Sprintf("the sealed runs, grades or reviews of %s changed after analysis %s (inputs %s, now %s); run a fresh analysis and release that", m.ExpID, a.AnalysisID, a.InputsDigest, d)}
	}
	var blocked []string
	if a.Verdict.Action != schemas.ActionShip {
		blocked = append(blocked, fmt.Sprintf("verdict action is %s, not ship", a.Verdict.Action))
	}
	if ev.policy != nil && ev.policy.Decision == schemas.PolicyBlock {
		blocked = append(blocked, fmt.Sprintf("policy %s decision is block", ev.policy.PolicyName))
	}
	if reason := frozenPolicySkipped(m, ev.policy); reason != "" {
		blocked = append(blocked, reason)
	}
	switch {
	case len(blocked) > 0 && override == "":
		return &RefusedError{Reason: strings.Join(blocked, "; ") + "; give an override reason to release anyway"}
	case len(blocked) == 0 && override != "":
		return &RefusedError{Reason: "override given but nothing to override: action is ship and the policy does not block"}
	}
	return nil
}

// newRecord fills a record from the manifest, the analysis and the runs.
// Chain fields and the evidence pack are filled at append time.
func newRecord(m *schemas.Manifest, ev *evidence, r schemas.ReleaseRequest, now time.Time) (*schemas.ReleaseRecord, error) {
	a := ev.analysis
	arm := "B"
	if a.Verdict.Effect != nil && a.Verdict.Effect.ArmB != "" {
		arm = a.Verdict.Effect.ArmB
	}
	am, ok := m.Arms[arm]
	if !ok {
		return nil, &RefusedError{Reason: fmt.Sprintf("manifest of %s has no arm %s", m.ExpID, arm)}
	}
	hashes := make([]string, 0, len(ev.runs))
	for _, run := range ev.runs {
		hashes = append(hashes, run.SealHash)
	}
	rec := &schemas.ReleaseRecord{
		ExpID:         m.ExpID,
		AnalysisID:    a.AnalysisID,
		Arm:           arm,
		AgentConfigID: am.Config.ID,
		ConfigHash:    am.ConfigHash,
		Decision:      a.Verdict.Decision,
		Action:        a.Verdict.Action,
		ManifestHash:  m.Hash,
		SealsDigest:   SealsDigest(hashes),
		Approver:      r.Approver,
		Note:          r.Note,
		Override:      r.Override,
		CreatedAt:     now,
	}
	if ev.policy != nil {
		rec.PolicyName, rec.PolicyHash, rec.PolicyDecision = ev.policy.PolicyName, ev.policy.PolicyHash, ev.policy.Decision
	}
	return rec, nil
}

// readEvidence reads a record's pack and checks its hash.
func (l *Log) readEvidence(rec *schemas.ReleaseRecord) ([]byte, error) {
	if !idPattern.MatchString(rec.ReleaseID) {
		return nil, fmt.Errorf("evidence of release %q: invalid release id", rec.ReleaseID)
	}
	b, err := os.ReadFile(l.evidencePath(rec.ReleaseID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("evidence of release %s: %w", rec.ReleaseID, schemas.ErrNotFound)
		}
		return nil, fmt.Errorf("evidence of release %s: %w", rec.ReleaseID, err)
	}
	if got := utils.SHA256Bytes(b); got != rec.EvidenceSHA256 {
		return nil, fmt.Errorf("evidence of release %s hashes to %s, record says %s: %w", rec.ReleaseID, got, rec.EvidenceSHA256, ErrEvidenceMismatch)
	}
	return b, nil
}

// within reports whether p is parent or below it.
func within(parent, p string) bool {
	rel, err := filepath.Rel(parent, p)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

func (l *Log) evidencePath(releaseID string) string {
	return filepath.Join(l.d.Root, releaseID, EvidenceFile)
}

// readJSON decodes a file, mapping a missing file to schemas.ErrNotFound.
func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s: %w", filepath.Base(p), schemas.ErrNotFound)
		}
		return fmt.Errorf("read %s: %w", filepath.Base(p), err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(p), err)
	}
	return nil
}
