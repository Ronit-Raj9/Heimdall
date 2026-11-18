package graders

// X4 (docs/code-scan.md): a suite's external code scanners run under the
// verifier's clean room. This file only adds an entry point; the clean room
// itself is cleanroom.go's.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/codescan"
	"github.com/maximhq/bifrost/bench/schemas"
)

// ScanInCleanRoom runs one external code scanner (codescan.Runner) under the
// same isolation as a verifier: a fresh workspace volume filled from the
// pristine repository (or the task image's workspace folder) with the run's
// diff applied by the trusted helper, then the scanner in image with no
// network, as uid 1000, the workspace read-only at the suite workdir, the
// hidden tests read-only at /grader/tests and an empty /grader/out. It reads
// the SARIF file the scanner wrote there, up to codescan.MaxSARIFBytes, and
// removes every container and volume before it returns. A snapshot-graded
// suite is refused (codescan.CheckSuite refuses it at plan time too).
func ScanInCleanRoom(cfg HiddenTestsConfig) codescan.Runner {
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	room := &cleanRoom{docker: cfg.Docker, logger: cfg.Logger}
	return func(ctx context.Context, rc *schemas.RunContext, diff *schemas.Diff, image string, sc schemas.CodeScanner, timeout time.Duration) (*codescan.ScanRun, error) {
		if rc == nil || rc.Suite == nil {
			return nil, errors.New("code scan needs a run context with a suite")
		}
		if rc.Suite.Verifier.Mode == schemas.VerifierSnapshot {
			return nil, fmt.Errorf("suite %s is snapshot-graded: external scanners need the clean-room tree", rc.Suite.ID)
		}
		expID, runID, err := runIDs(rc)
		if err != nil {
			return nil, err
		}
		in, err := gatherInputs(rc, diff, cfg.Image)
		if err != nil {
			return nil, fmt.Errorf("code scan %s: %w", sc.ID, err)
		}
		if in.workdir, err = verifierWorkdir(rc.Suite.Workdir); err != nil {
			return nil, err
		}
		if err := applyScenario(rc, in); err != nil {
			return nil, err
		}
		in.image, in.command, in.timeout, in.root = image, sc.Command, timeout, false
		var out codescan.ScanRun
		res, err := room.run(ctx, newGradeJob(expID, runID, in), func(ctx context.Context, container string) error {
			data, err := room.readFile(ctx, container, sc.SARIF, codescan.MaxSARIFBytes, "sarif", "SARIF report")
			var tl *tooLargeError
			if errors.As(err, &tl) {
				out.Problem = fmt.Sprintf("SARIF report is %v", tl)
				return nil
			}
			out.SARIF = data
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("code scan %s: %w", sc.ID, err)
		}
		out.Applied, out.ExitCode, out.TimedOut = res.applied, res.exitCode, res.timedOut
		return &out, nil
	}
}
