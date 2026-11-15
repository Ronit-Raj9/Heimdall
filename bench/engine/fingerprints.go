package engine

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// ImageRoler is implemented by an environment that knows the images it
// starts beside the suite's own: role (suite.ImageHelper,
// suite.ImageToolProxy) to tag. Lock fingerprints bind them, and a plan
// records their ids (docs/lock-fingerprints.md).
type ImageRoler interface {
	ImageRoles() map[string]string
}

// ImageDigester is implemented by an environment that can list the
// registry digests (repo@sha256:...) of a local image; empty for an image
// built locally. Lock fingerprints record a pulled image by that digest.
type ImageDigester interface {
	ImageRepoDigests(ctx context.Context, ref string) ([]string, error)
}

// FingerprintOptions is what the engine binds when it recomputes a suite's
// lock fingerprints: the registry's part versions and, when the suite's
// environment can inspect images, the image roles and their local ids.
func FingerprintOptions(ctx context.Context, reg *schemas.Registry, s *schemas.Suite) suite.FingerprintOptions {
	opts := suite.FingerprintOptions{Parts: PartVersions(reg)}
	if reg == nil || s == nil {
		return opts
	}
	env, ok := findPart(reg.Environments, s.Environment)
	if !ok {
		return opts
	}
	if r, ok := any(env).(ImageRoler); ok {
		opts.Images = r.ImageRoles()
	}
	if ins, ok := any(env).(ImageInspector); ok {
		opts.ImageID = func(tag string) (string, bool) {
			id, err := ins.ImageID(ctx, tag)
			return id, err == nil && id != ""
		}
	}
	if dg, ok := any(env).(ImageDigester); ok {
		opts.RepoDigests = func(tag string) []string {
			digests, err := dg.ImageRepoDigests(ctx, tag)
			if err != nil {
				return nil
			}
			return digests
		}
	}
	return opts
}

// checkFingerprints recomputes the lock fingerprints of s and refuses drift
// (docs/lock-fingerprints.md), naming the suite, task or probe and the part
// that drifted. warnOnly (a smoke experiment or an agent check) turns the
// refusal into warnings. A legacy lock, and an image that cannot be
// inspected, only warn. what names the step for the message: plan, run.
func checkFingerprints(ctx context.Context, reg *schemas.Registry, s *schemas.Suite, logger schemas.Logger, warnOnly bool, what, expID string) error {
	if s == nil || s.Lock == nil {
		return nil
	}
	drift, warns, err := suite.CheckFingerprints(s, FingerprintOptions(ctx, reg, s))
	if err != nil {
		if warnOnly {
			logger.Warn("smoke experiment or agent check ignores a lock fingerprint error", "experiment", expID, "error", err.Error())
			return nil
		}
		return fmt.Errorf("suite lock fingerprints: %w", err)
	}
	for _, w := range warns {
		logger.Warn(w, "suite", s.ID, "experiment", expID)
	}
	if len(drift) == 0 {
		return nil
	}
	text := suite.DriftText(drift)
	if warnOnly {
		logger.Warn("smoke experiment or agent check ignores lock fingerprint drift", "experiment", expID, "drift", text)
		return nil
	}
	return fmt.Errorf("suite lock fingerprints drifted, refusing to %s: %s; the evaluator is no longer the one suite.lock admitted: re-lock with `bifrost-bench suite lock %s` and re-admit the changed tasks",
		what, text, s.ID)
}

// lockFingerprintHash is the manifest's fingerprint_hash: the sha256 of the
// lock's fingerprints section, empty for a legacy lock.
func lockFingerprintHash(s *schemas.Suite) (string, error) {
	if s == nil || s.Lock == nil {
		return "", nil
	}
	return suite.FingerprintHash(s.Lock.Fingerprints)
}
