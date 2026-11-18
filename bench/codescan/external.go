package codescan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// External scanner limits.
const (
	defaultScanTimeoutS = 300
	maxScanTimeoutS     = 1800
	// MaxSARIFBytes is the largest SARIF file read from a scanner.
	MaxSARIFBytes = 16 << 20
	// ImageBase is CodeScanner.Image for the suite's own base image.
	ImageBase = "base"
)

var (
	scannerIDRE   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	pinnedImageRE = regexp.MustCompile(`^[^\s@]+@sha256:[0-9a-f]{64}$`)
	sarifPathRE   = regexp.MustCompile(`^/grader/out/[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
)

// ScanRun is what one external scanner left behind in the clean room.
type ScanRun struct {
	Applied  bool   // the diff applied to the pristine repository
	ExitCode int    // the scanner's exit code; many exit non-zero when they find something
	TimedOut bool   // killed at its timeout
	SARIF    []byte // nil when it wrote no file at the SARIF path
	Problem  string // why the SARIF file cannot be used (over the size cap)
}

// Runner runs one external scanner on the run's post-run tree and returns
// what it left. The pipeline's runner is the clean room of the graders
// package (graders.ScanInCleanRoom); tests pass a recorded result.
type Runner func(ctx context.Context, rc *schemas.RunContext, diff *schemas.Diff, image string, sc schemas.CodeScanner, timeout time.Duration) (*ScanRun, error)

// CheckSuite validates a suite's external scanners. The planner refuses an
// experiment on a suite that fails it, so a bad scanner never runs half way.
func CheckSuite(s *schemas.Suite) error {
	if s == nil || s.CodeScan == nil || len(s.CodeScan.External) == 0 {
		return nil
	}
	if utils.VerifierMode(s) == schemas.VerifierSnapshot {
		return fmt.Errorf("suite %s: codescan.external scanners run in the clean room on the pristine repository plus the diff; a snapshot-graded suite has no such tree", s.ID)
	}
	if len(s.Verifier.Command) == 0 {
		return fmt.Errorf("suite %s: codescan.external needs a clean-room verifier (hidden_tests or verifier_command): the scanner runs under the same isolation", s.ID)
	}
	seen := map[string]bool{}
	for i, sc := range s.CodeScan.External {
		where := fmt.Sprintf("suite %s: codescan.external[%d]", s.ID, i)
		switch {
		case !scannerIDRE.MatchString(sc.ID) || sc.ID == SourceBuiltin:
			return fmt.Errorf("%s: id %q must match %s and not be %q", where, sc.ID, scannerIDRE, SourceBuiltin)
		case seen[sc.ID]:
			return fmt.Errorf("%s: id %q repeats", where, sc.ID)
		case sc.Image != ImageBase && !pinnedImageRE.MatchString(sc.Image):
			return fmt.Errorf("%s (%s): image %q must be %q (the suite's locked base image) or pinned by digest (name@sha256:<64 hex>), like a verifier", where, sc.ID, sc.Image, ImageBase)
		case len(sc.Command) == 0:
			return fmt.Errorf("%s (%s): command is empty", where, sc.ID)
		case !sarifPathRE.MatchString(sc.SARIF):
			return fmt.Errorf("%s (%s): sarif %q must name a file directly in /grader/out, the folder created empty for the scan", where, sc.ID, sc.SARIF)
		case sc.TimeoutS < 0 || sc.TimeoutS > maxScanTimeoutS:
			return fmt.Errorf("%s (%s): timeout_s %d is outside 0..%d", where, sc.ID, sc.TimeoutS, maxScanTimeoutS)
		}
		seen[sc.ID] = true
	}
	return nil
}

// runExternal runs the suite's scanners and adds their findings on added
// lines, and one record per scanner, to scan. A scanner that fails is
// recorded with its error, so the counts read unknown, never zero.
func runExternal(ctx context.Context, run Runner, rc *schemas.RunContext, diff *schemas.Diff, scan *schemas.CodeScanEvidence) {
	text, err := os.ReadFile(diff.Path)
	var added map[string]map[int]addedLine
	if err == nil {
		added, err = AddedLines(text)
	}
	for _, sc := range rc.Suite.CodeScan.External {
		image := sc.Image
		if image == ImageBase {
			image = rc.Suite.BaseImage.Tag
		}
		rec := schemas.CodeScanExternal{Scanner: sc.ID, Image: image, ExitCode: -1}
		switch {
		case err != nil:
			rec.Error = "diff not readable: " + err.Error()
		case run == nil:
			rec.Error = "no scanner runner is configured"
		default:
			timeout := time.Duration(sc.TimeoutS) * time.Second
			if timeout <= 0 {
				timeout = defaultScanTimeoutS * time.Second
			}
			res, rerr := run(ctx, rc, diff, image, sc, timeout)
			recordRun(&rec, res, rerr, sc, added, rc.Suite.Workdir, scan)
		}
		rec.Error = SafeText(rec.Error)
		scan.External = append(scan.External, rec)
	}
	sortFindings(scan.Findings)
}

// recordRun fills rec from one scanner run and appends its findings.
func recordRun(rec *schemas.CodeScanExternal, res *ScanRun, err error, sc schemas.CodeScanner, added map[string]map[int]addedLine, workdir string, scan *schemas.CodeScanEvidence) {
	switch {
	case err != nil:
		rec.Error = "did not run: " + err.Error()
		return
	case res == nil:
		rec.Error = "did not run: no result"
		return
	}
	rec.ExitCode = res.ExitCode
	switch {
	case !res.Applied:
		rec.Error = "the diff does not apply to the pristine repository"
		return
	case res.TimedOut:
		rec.Error = "timed out"
		return
	case res.Problem != "":
		rec.Error = res.Problem
		return
	case res.SARIF == nil:
		rec.Error = fmt.Sprintf("no SARIF file at %s (exit %d)", sc.SARIF, res.ExitCode)
		return
	}
	sum := sha256.Sum256(res.SARIF)
	rec.SARIFSHA256 = "sha256:" + hex.EncodeToString(sum[:])
	fs, total, perr := ParseSARIF(sc.ID, res.SARIF, added, workdir)
	if perr != nil {
		rec.Error = perr.Error()
		return
	}
	rec.Results, rec.OnAdded = total, len(fs)
	scan.Findings = append(scan.Findings, fs...)
}
