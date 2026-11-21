package main

// Govern job kinds (web parity lane L16): verify the release log, write a
// release's compliance pack, and certify a monitor from the website.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/release"
)

var (
	govIDRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	govFPRsRe = regexp.MustCompile(`^[0-9.]+(,[0-9.]+){0,7}$`)
)

// releaseLogFor opens the release log of the job's app (the org's in hosted mode).
func releaseLogFor(env jobEnv) (*release.Log, error) {
	a := env.App
	if a == nil || a.store == nil {
		return nil, errors.New("the run store is not open on this server")
	}
	root := a.releasesRoot
	if root == "" {
		root = release.DefaultRoot(a.cfg.Root)
	}
	return release.New(release.Deps{Store: a.store, Root: root, Logger: a.logger})
}

type releaseComplianceParams struct {
	ReleaseID string `json:"release_id"`
}

type monitorCertifyParams struct {
	Monitor     string   `json:"monitor"` // project-relative monitor config (YAML or JSON)
	ExpIDs      []string `json:"exp_ids"` // labelled experiments
	FPR         string   `json:"fpr"`     // comma-separated target false-positive rates
	GradeSource string   `json:"grade_source"`
	Parallel    int      `json:"parallel"`
}

// confinedRel accepts a project-relative path with no absolute prefix and no "..".
func confinedRel(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if p == "" {
		return "", errors.New("path is empty")
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("path %q must be relative to the project folder", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == ".git" || seg == ".heimdall" {
			return "", fmt.Errorf("path %q leaves the project folder", p)
		}
	}
	return filepath.ToSlash(filepath.Clean(p)), nil
}

func monitorCertifyArgs(raw json.RawMessage) ([]string, error) {
	var p monitorCertifyParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("params must be JSON: %v", err)
	}
	mon, err := confinedRel(p.Monitor)
	if err != nil {
		return nil, fmt.Errorf("monitor: %v", err)
	}
	if len(p.ExpIDs) == 0 || len(p.ExpIDs) > 20 {
		return nil, errors.New("exp_ids: pick 1 to 20 labelled experiments")
	}
	argv := []string{"monitor", "certify", "-config", cliConfig, "-monitor", mon}
	for _, id := range p.ExpIDs {
		if !govIDRe.MatchString(id) {
			return nil, fmt.Errorf("exp_ids: %q is not an experiment id", id)
		}
		argv = append(argv, "-exp", id)
	}
	if p.FPR != "" {
		if !govFPRsRe.MatchString(p.FPR) {
			return nil, errors.New("fpr: comma-separated rates such as 0.01,0.001")
		}
		argv = append(argv, "-fpr", p.FPR)
	}
	if p.GradeSource != "" {
		if !govIDRe.MatchString(p.GradeSource) {
			return nil, errors.New("grade_source: original or a regrade id")
		}
		argv = append(argv, "-grade-source", p.GradeSource)
	}
	if p.Parallel != 0 {
		if p.Parallel < 1 || p.Parallel > 16 {
			return nil, errors.New("parallel: 1 to 16")
		}
		argv = append(argv, "-parallel", strconv.Itoa(p.Parallel))
	}
	return argv, nil
}

func init() {
	registerJobKind(jobKindSpec{
		Kind: "release.verify", Local: true, Hosted: true,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			return map[string]any{}, "releases", nil
		},
		Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
			l, err := releaseLogFor(env)
			if err != nil {
				return nil, err
			}
			log.Printf("verifying the release hash chain and every evidence pack")
			n, err := l.Verify(ctx)
			if err != nil {
				log.Printf("release log does not verify after %d records: %v", n, err)
				return nil, fmt.Errorf("release log does not verify: %w", err)
			}
			log.Printf("release log ok: %d records; the chain and every evidence pack verify", n)
			return map[string]any{"ok": true, "records": n}, nil
		},
	})

	registerJobKind(jobKindSpec{
		Kind: "release.compliance", Local: true, Hosted: true,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			var p releaseComplianceParams
			if err := json.Unmarshal(raw, &p); err != nil || !govIDRe.MatchString(p.ReleaseID) {
				return nil, "", fmt.Errorf("%w: release_id is required (e.g. rel_1)", jobs.ErrInvalidParams)
			}
			return p, p.ReleaseID, nil
		},
		Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
			var p releaseComplianceParams
			if err := json.Unmarshal(j.Params, &p); err != nil {
				return nil, err
			}
			l, err := releaseLogFor(env)
			if err != nil {
				return nil, err
			}
			rec, err := l.Get(ctx, p.ReleaseID)
			if err != nil {
				return nil, fmt.Errorf("release %s: %w", p.ReleaseID, err)
			}
			log.Printf("building the compliance pack of %s (experiment %s)", rec.ReleaseID, rec.ExpID)
			pack, err := env.App.compliancePack(ctx, []string{rec.ExpID}, rec.AnalysisID, rec)
			if err != nil {
				return nil, fmt.Errorf("compliance pack: %w", err)
			}
			dir := filepath.Join(env.App.cfg.Root, "exports", "compliance")
			paths, err := writeCompliancePack(pack, dir, "compliance-"+rec.ReleaseID, "both")
			if err != nil {
				return nil, err
			}
			rel := make([]string, 0, len(paths))
			for _, path := range paths {
				r, err := filepath.Rel(env.App.cfg.Root, path)
				if err != nil {
					r = filepath.Base(path)
				}
				log.Printf("wrote %s", r)
				rel = append(rel, filepath.ToSlash(r))
			}
			return map[string]any{"release_id": rec.ReleaseID, "exp_id": rec.ExpID, "paths": rel}, nil
		},
	})

	// monitor.certify scores sealed runs through the monitor, which may start
	// containers, so it runs only on a local server (hosted: not registered).
	registerJobKind(cliJob("monitor.certify", true, false, monitorCertifyArgs))
}
