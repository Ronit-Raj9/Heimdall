package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
)

// Hosted site stages (W07). The server runs no Docker: building and testing
// one issue is one issuebench.work job on a worker (the org's own or the
// shared one). A build-mode job also tests its own issue in the environment
// it built; that validation is cached here so Validate does not queue a
// second job for it. Model checks still run on the server.

// ibValidationCache is the file a build-mode job's validation is kept in
// until Validate reads it.
type ibValidationCache struct {
	DockerfileSHA256 string                `json:"dockerfile_sha256"`
	Validation       issuebench.Validation `json:"validation"`
}

func ibSHA256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (s *ibStages) validationPath(c issuebench.Candidate) string {
	return s.dir("work", "validations", strings.ReplaceAll(c.Repo, "/", "__")+"#"+strconv.Itoa(c.Issue)+".json")
}

// buildOnWorker queues a build-mode work job and waits for it.
func (s *ibStages) buildOnWorker(ctx context.Context, c issuebench.Candidate, version string, mc pipeline.ModelChoice, maxUSD float64, logf func(string, ...any)) (issuebench.BuildResult, error) {
	req := issuebench.WorkRequest{
		Mode: issuebench.WorkModeBuild, OrgID: s.orgID, Repo: c.Repo, Version: version, Candidate: c,
		Provider: mc.Provider, Model: mc.Model, MaxUSD: maxUSD, EgressHosts: ibEgressHosts(),
	}
	if prior, ok := s.loadEnv(c.Repo, version); ok {
		req.Env = prior
	}
	params, _ := json.Marshal(issuebench.WorkParams{Request: req, KeyID: mc.KeyID, Consent: true})
	var res issuebench.WorkResult
	if err := s.runJob(ctx, issuebench.WorkJobKind, params, &res, func(id string) {
		issuebench.NoteJob(ctx, issuebench.StageBuild, id)
		logf("issue #%d: build and test job %s on a worker, %s/%s (cap $%.2f)", c.Issue, id, mc.Provider, mc.Model, maxUSD)
	}); err != nil {
		return issuebench.BuildResult{}, err
	}
	if res.Build == nil {
		if res.Stop != "" {
			return issuebench.BuildResult{}, errors.New(res.Stop)
		}
		return issuebench.BuildResult{}, errors.New("the worker returned no build result")
	}
	res.Build.CostUSD = res.CostUSD
	if res.Build.Env != nil {
		res.Build.Env.Repo, res.Build.Env.Version = c.Repo, version
		s.saveEnv(*res.Build.Env)
		if res.Validation != nil {
			s.saveValidation(c, res.Build.Env.Dockerfile, *res.Validation)
		}
	}
	return *res.Build, nil
}

func (s *ibStages) saveValidation(c issuebench.Candidate, dockerfile string, v issuebench.Validation) {
	p := s.validationPath(c)
	b, err := json.Marshal(ibValidationCache{DockerfileSHA256: ibSHA256(dockerfile), Validation: v})
	if err == nil && os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		_ = os.WriteFile(p, b, 0o644)
	}
}

// cachedValidation returns the build job's validation of c when it was made
// in this Dockerfile, and removes the file either way once read.
func (s *ibStages) cachedValidation(c issuebench.Candidate, dockerfile string) (issuebench.Validation, bool) {
	p := s.validationPath(c)
	b, err := os.ReadFile(p)
	if err != nil {
		return issuebench.Validation{}, false
	}
	_ = os.Remove(p)
	var vc ibValidationCache
	if json.Unmarshal(b, &vc) != nil || vc.DockerfileSHA256 != ibSHA256(dockerfile) {
		return issuebench.Validation{}, false
	}
	return vc.Validation, true
}

// validateOnWorker tests c in env: the build job's result when it matches,
// else a validate-mode work job. Then the model checks run on the server.
func (s *ibStages) validateOnWorker(ctx context.Context, c issuebench.Candidate, env issuebench.EnvSpec, logf func(string, ...any)) (issuebench.Validation, error) {
	if env.Repo == "" {
		env.Repo = c.Repo
	}
	if env.Version == "" {
		env.Version = pipeline.DefaultVersion(c)
	}
	v, ok := s.cachedValidation(c, env.Dockerfile)
	if ok {
		logf("issue #%d: tests already ran on the worker in the build job", c.Issue)
	} else {
		if s.jobs == nil {
			return issuebench.Validation{}, errors.New("this server has no job runner to test issues")
		}
		if strings.TrimSpace(env.Dockerfile) == "" {
			return issuebench.Validation{}, errors.New("the environment has no Dockerfile")
		}
		mc := ibModel(pipeline.ModelFrom(ctx))
		e := env
		req := issuebench.WorkRequest{
			Mode: issuebench.WorkModeValidate, OrgID: s.orgID, Repo: c.Repo, Version: env.Version, Candidate: c,
			Env: &e, EgressHosts: ibEgressHosts(),
		}
		params, _ := json.Marshal(issuebench.WorkParams{Request: req, KeyID: mc.KeyID, Consent: true})
		var res issuebench.WorkResult
		if err := s.runJob(ctx, issuebench.WorkJobKind, params, &res, func(id string) {
			issuebench.NoteJob(ctx, issuebench.StageValidate, id)
			logf("issue #%d: test job %s on a worker", c.Issue, id)
		}); err != nil {
			return issuebench.Validation{}, err
		}
		if res.Validation == nil {
			if res.Stop != "" {
				return issuebench.Validation{}, errors.New(res.Stop)
			}
			return issuebench.Validation{}, errors.New("the worker returned no test result")
		}
		v = *res.Validation
	}
	if v.Stop != "" {
		return v, nil // the tests already stopped it
	}
	if err := s.modelChecks(ctx, &v, logf); err != nil {
		return issuebench.Validation{}, err
	}
	return v, nil
}

// ibEgressHosts is the build sandbox's allowlist: the defaults plus the
// operator's BENCH_ISSUEBENCH_EGRESS_ALLOW (comma separated).
func ibEgressHosts() []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, h := range issuebench.DefaultEgressHosts {
		add(h)
	}
	for _, h := range strings.Split(os.Getenv(issuebench.EgressAllowEnv), ",") {
		add(h)
	}
	return out
}
