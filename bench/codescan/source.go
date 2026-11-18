package codescan

import (
	"context"

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
	if scan.Scanned && scan.AddedLines > 0 && rc != nil && rc.Suite != nil && rc.Suite.CodeScan != nil && len(rc.Suite.CodeScan.External) > 0 {
		runExternal(ctx, s.Run, rc, ev.Diff, scan)
	}
	ev.CodeScan = scan
	return nil
}
