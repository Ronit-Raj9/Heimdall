package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// lockFingerprintOptions is what `suite lock`, `suite admit` and the suites
// API bind into lock fingerprints (docs/lock-fingerprints.md): the versions
// of the built-in environments and graders, the same values the planner's
// registry reports, the helper image of cfg (nil when only -root was
// given), and what the local docker says about each image. An image with a
// registry digest is recorded by it; a locally built one by tag only,
// unless pin records its local image id (`suite lock -pin-local-images`).
// Without docker every image is recorded as "tag only".
func lockFingerprintOptions(ctx context.Context, cfg *lib.Config, d utils.Docker, pin bool) suite.FingerprintOptions {
	reg := &schemas.Registry{
		Environments: environments.Builtin(environments.DockerWorkspaceConfig{}),
		Graders:      append([]schemas.Grader{graders.NewJudge(graders.JudgeConfig{})}, graders.Builtin(graders.HiddenTestsConfig{})...),
	}
	opts := suite.FingerprintOptions{Parts: engine.PartVersions(reg)}
	if cfg != nil {
		opts.Images = map[string]string{suite.ImageHelper: cfg.Images.Helper, suite.ImageToolProxy: cfg.Images.ToolProxy}
	}
	seen := map[string]*environments.ImageInfo{}
	inspect := func(tag string) *environments.ImageInfo {
		if info, ok := seen[tag]; ok {
			return info
		}
		var out *environments.ImageInfo
		if info, err := environments.InspectImage(ctx, d, tag); err == nil && info.ID != "" {
			out = &info
		}
		seen[tag] = out
		return out
	}
	opts.ImageID = func(tag string) (string, bool) {
		if info := inspect(tag); info != nil {
			return info.ID, true
		}
		return "", false
	}
	opts.RepoDigests = func(tag string) []string {
		if info := inspect(tag); info != nil {
			return info.RepoDigests
		}
		return nil
	}
	opts.PinLocal = pin
	return opts
}

// printLockChanges prints what writing fresh over the suite's current lock
// changes, one line per suite, task or probe, as `suite lock` reports drift.
// A legacy lock that is only rewritten never prints anything; a lock gaining
// fingerprints prints that once.
func printLockChanges(w io.Writer, s *schemas.Suite, fresh *schemas.SuiteLock) {
	if s.Lock == nil {
		return
	}
	for _, c := range suite.Diff(s.Lock, fresh) {
		fmt.Fprintf(w, "  %-5s %-24s %-5s %-8s %v\n", c.Scope, c.ID, c.Level, c.Action, c.Fields)
	}
}

// lockMatches reports whether the suite's lock is the one manifest froze:
// the same hash, or the same lock with only a fingerprints section added
// since (suite.LockHashMatches).
func lockMatches(s *schemas.Suite, frozen string) bool {
	return suite.LockHashMatches(s, frozen)
}

// fingerprintRow is the doctor row for a locked suite's fingerprints: pass
// when they match the files, warn for a legacy lock (it binds no evaluator
// setting) or an image that could not be checked, fail on drift, which
// refuses every plan of the suite.
func fingerprintRow(s *schemas.Suite, drift []schemas.FingerprintDrift, warns []string, err error) schemas.DoctorCheck {
	row := schemas.DoctorCheck{ID: "suite " + s.ID + " lock fingerprints"}
	switch {
	case err != nil:
		row.Status, row.Detail = schemas.DoctorFail, err.Error()
	case len(drift) > 0:
		row.Status, row.Detail = schemas.DoctorFail, "drifted, plans refuse: "+suite.DriftText(drift)+"; re-lock and re-admit"
	case s.Lock.Fingerprints == nil:
		row.Status, row.Detail = schemas.DoctorWarn, suite.LegacyLockWarning
	case len(warns) > 0:
		row.Status, row.Detail = schemas.DoctorWarn, "fingerprinted, no drift; "+strings.Join(warns, "; ")
	default:
		row.Status, row.Detail = schemas.DoctorPass, "fingerprinted, no drift"
	}
	return row
}
