package codescan

import (
	"context"
	"path"

	"github.com/maximhq/bifrost/bench/schemas"
)

// SourceName is the evidence source's part name.
const SourceName = "codescan"

var _ schemas.EvidenceSource = (*Source)(nil)

// Source is the evidence source that records a run's code scan in
// Evidence.CodeScan, sealed in evidence.json with the rest of the evidence:
// the builtin rules over the exported diff, then the suite's external
// scanners, if it declares any, through Run. It runs in the collect stage,
// after the diff is exported and before grading. It never fails a run: a
// diff it cannot read or a scanner that breaks is recorded in the evidence
// with the reason.
type Source struct {
	Run Runner
}

// NewSource returns the code scan evidence source. run may be nil when no
// suite declares an external scanner; such a scanner is then recorded as
// failed.
func NewSource(run Runner) *Source { return &Source{Run: run} }

// GetName implements schemas.Part.
func (s *Source) GetName() string { return SourceName }

// GetVersion implements schemas.Part.
func (s *Source) GetVersion() string { return RulesVersion }

// Collect implements schemas.EvidenceSource. External scanners run only
// when the agent added lines: with none, no finding could be the agent's.
func (s *Source) Collect(ctx context.Context, rc *schemas.RunContext, ev *schemas.Evidence) error {
	if ev == nil {
		return nil
	}
	if ev.Diff == nil || ev.Diff.Path == "" {
		ev.CodeScan = notScanned("no exported diff")
		return nil
	}
	scan := ScanFile(ev.Diff.Path)
	scan.DiffSHA256 = ev.Diff.SHA256
	skipPlanted(scan, rc)
	if scan.Scanned && scan.AddedLines > 0 && rc != nil && rc.Suite != nil && rc.Suite.CodeScan != nil && len(rc.Suite.CodeScan.External) > 0 {
		runExternal(ctx, s.Run, rc, ev.Diff, scan)
	}
	ev.CodeScan = scan
	return nil
}

// skipPlanted moves findings in files the bench laid down itself
// (rc.Handle.FileHashes: notes, arm files and probe fixtures such as the
// secret canary's .env.local) out of the findings and records each such file
// as skipped. Those lines are the bench's, so they are never the agent's
// insecure code; a planted secret the agent copies elsewhere is still found
// in the file it lands in, and secret_exposed reports the leak itself.
func skipPlanted(scan *schemas.CodeScanEvidence, rc *schemas.RunContext) {
	if scan == nil || rc == nil || rc.Handle == nil || len(rc.Handle.FileHashes) == 0 {
		return
	}
	planted := map[string]bool{}
	for p := range rc.Handle.FileHashes {
		planted[path.Clean(p)] = true
	}
	kept := scan.Findings[:0]
	skipped := map[string]bool{}
	for _, f := range scan.Findings {
		if !planted[path.Clean(f.File)] {
			kept = append(kept, f)
			continue
		}
		if !skipped[f.File] {
			skipped[f.File] = true
			scan.Skipped = append(scan.Skipped, schemas.CodeScanSkip{File: f.File, Reason: "planted by the bench"})
		}
	}
	scan.Findings = kept
}
